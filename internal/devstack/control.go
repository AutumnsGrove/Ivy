package devstack

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// ControlRequest is one command sent to a running dev stack over its socket.
type ControlRequest struct {
	Op      string    `json:"op"`
	Account string    `json:"account,omitempty"`
	Mailbox string    `json:"mailbox,omitempty"`
	UID     uint32    `json:"uid,omitempty"`
	Dest    string    `json:"dest,omitempty"`
	Flags   []string  `json:"flags,omitempty"`
	Raw     []byte    `json:"raw,omitempty"`
	Fault   FaultSpec `json:"fault,omitempty"`
	Advance string    `json:"advance,omitempty"`
	State   string    `json:"state,omitempty"`
}

// ControlResponse is the result of a command; Error is non-empty on failure.
type ControlResponse struct {
	Error string `json:"error,omitempty"`
	UID   uint32 `json:"uid,omitempty"`
	Info  string `json:"info,omitempty"`
}

// FaultSpec names a mailworld fault for the "fault" op.
type FaultSpec struct {
	Kind    string `json:"kind"`
	After   int    `json:"after,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// fault maps a spec to the mailworld fault it names. Unknown kinds fail rather
// than arming nothing silently, so a typo in a test step is loud.
func (s FaultSpec) fault() (mailworld.Fault, error) {
	switch s.Kind {
	case "drop":
		return mailworld.DropConnection{After: s.After}, nil
	case "auth-fail":
		return mailworld.AuthFail{}, nil
	case "smtp-reject":
		return mailworld.SMTPReject{Code: s.Code, Message: s.Message}, nil
	case "smtp-auth-fail":
		return mailworld.SMTPAuthFail{}, nil
	default:
		return nil, fmt.Errorf("devstack: unknown fault %q", s.Kind)
	}
}

// Client talks to a running stack's control socket. Each request gets a fresh
// connection, so a short-lived CLI invocation has no session state to keep and
// a dead connection never poisons the next command.
type Client struct{ path string }

// Dial prepares a client for the control socket at path.
func Dial(path string) (*Client, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("devstack: dial control %s: %w", path, err)
	}
	_ = conn.Close()
	return &Client{path: path}, nil
}

// Close releases the client; there is nothing to close between requests.
func (c *Client) Close() error { return nil }

// Do sends one request and returns the response.
func (c *Client) Do(req ControlRequest) (ControlResponse, error) {
	conn, err := net.Dial("unix", c.path)
	if err != nil {
		return ControlResponse{}, fmt.Errorf("devstack: dial control: %w", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return ControlResponse{}, fmt.Errorf("devstack: send %s: %w", req.Op, err)
	}
	var resp ControlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return ControlResponse{}, fmt.Errorf("devstack: read reply to %s: %w", req.Op, err)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Deliver appends raw to a mailbox and returns the new UID.
func (c *Client) Deliver(account, mailbox string, raw []byte) (uint32, error) {
	resp, err := c.Do(ControlRequest{Op: "deliver", Account: account, Mailbox: mailbox, Raw: raw})
	return resp.UID, err
}

// Flag adds flags to one message.
func (c *Client) Flag(account, mailbox string, uid uint32, flags ...string) error {
	_, err := c.Do(ControlRequest{Op: "flag", Account: account, Mailbox: mailbox, UID: uid, Flags: flags})
	return err
}

// Move moves one message to another mailbox.
func (c *Client) Move(account, mailbox string, uid uint32, dest string) error {
	_, err := c.Do(ControlRequest{Op: "move", Account: account, Mailbox: mailbox, UID: uid, Dest: dest})
	return err
}

// Expunge removes one message.
func (c *Client) Expunge(account, mailbox string, uid uint32) error {
	_, err := c.Do(ControlRequest{Op: "expunge", Account: account, Mailbox: mailbox, UID: uid})
	return err
}

// Fault arms a fault for future connections.
func (c *Client) Fault(spec FaultSpec) error {
	_, err := c.Do(ControlRequest{Op: "fault", Fault: spec})
	return err
}

// State applies a named dev condition (DEV.md section 4).
func (c *Client) State(name string) error {
	_, err := c.Do(ControlRequest{Op: "state", State: name})
	return err
}

// AdvanceClock moves the fake clock forward.
func (c *Client) AdvanceClock(d time.Duration) error {
	_, err := c.Do(ControlRequest{Op: "advance-clock", Advance: d.String()})
	return err
}

// ControlServer serves the control protocol over a unix socket. One request is
// handled per connection: the CLI dials, asks, reads the reply and exits, so
// there is no session state to keep.
type ControlServer struct {
	world *mailworld.World
	ln    *net.UnixListener
	wg    sync.WaitGroup
}

// NewControlServer starts serving at path. Close stops it and removes the
// socket. The socket is created 0600 so only the dev user can drive the stack.
func NewControlServer(w *mailworld.World, path string) (*ControlServer, error) {
	if w == nil {
		return nil, errors.New("devstack: control server needs a world")
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("devstack: listen control %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	s := &ControlServer{world: w, ln: ln}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func (s *ControlServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *ControlServer) handle(conn *net.UnixConn) {
	defer conn.Close()
	var req ControlRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(ControlResponse{Error: "bad request: " + err.Error()})
		return
	}
	resp := s.dispatch(req)
	_ = json.NewEncoder(conn).Encode(resp)
}

func (s *ControlServer) dispatch(req ControlRequest) ControlResponse {
	acc := func() *mailworld.Account {
		return s.world.Account(req.Account, mailworld.SeedPassword)
	}
	switch req.Op {
	case "deliver":
		return ControlResponse{UID: acc().Deliver(req.Mailbox, req.Raw)}
	case "flag":
		flags := make([]imap.Flag, len(req.Flags))
		for i, f := range req.Flags {
			flags[i] = imapFlag(f)
		}
		if err := acc().Flag(req.Mailbox, req.UID, flags...); err != nil {
			return ControlResponse{Error: err.Error()}
		}
		return ControlResponse{}
	case "move":
		if err := acc().Move(req.Mailbox, req.UID, req.Dest); err != nil {
			return ControlResponse{Error: err.Error()}
		}
		return ControlResponse{}
	case "expunge":
		if err := acc().Expunge(req.Mailbox, req.UID); err != nil {
			return ControlResponse{Error: err.Error()}
		}
		return ControlResponse{}
	case "advance-clock":
		d, err := time.ParseDuration(req.Advance)
		if err != nil {
			return ControlResponse{Error: "bad duration: " + err.Error()}
		}
		s.world.Clock().Advance(d)
		return ControlResponse{Info: "clock " + s.world.Clock().Now().Format(time.RFC3339)}
	case "fault":
		f, err := req.Fault.fault()
		if err != nil {
			return ControlResponse{Error: err.Error()}
		}
		s.world.Fault(f)
		return ControlResponse{}
	case "state":
		if err := ApplyState(s.world, req.State); err != nil {
			return ControlResponse{Error: err.Error()}
		}
		return ControlResponse{Info: "state " + req.State}
	default:
		return ControlResponse{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// imapFlag accepts a friendly name from the CLI ("seen") as well as the wire
// form ("\\Seen"), so a human does not have to quote a backslash.
func imapFlag(name string) imap.Flag {
	switch strings.ToLower(name) {
	case "seen":
		return imap.FlagSeen
	case "flagged":
		return imap.FlagFlagged
	case "answered":
		return imap.FlagAnswered
	case "draft":
		return imap.FlagDraft
	case "deleted":
		return imap.FlagDeleted
	default:
		return imap.Flag(name)
	}
}

// Close stops the server, waits for in-flight requests and removes the socket.
func (s *ControlServer) Close() error {
	err := s.ln.Close()
	s.wg.Wait()
	return err
}
