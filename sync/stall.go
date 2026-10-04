package sync

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// defaultStallTimeout is how long a command may go without a single byte from
// the server before the connection is given up on (STANDARDS.md 4a). It is
// silence, not duration: a large body that keeps arriving never trips it.
const defaultStallTimeout = 2 * time.Minute

// stallConn puts a read deadline on a connection while a command is in flight.
//
// The go-imap fork we build on bounds a response once its first byte has
// arrived (30 s) and bounds writes, but deliberately waits without a deadline
// for a response to begin, since an IDLE connection is silent by design. That
// leaves a server that accepts the connection and then goes quiet able to hold
// a command forever. This fills the gap: the deadline restarts with every byte
// received, so a slow transfer is fine, and it is off between commands (local
// database work, an IDLE waiting for news).
//
// The library sets and clears read deadlines on the same connection, so
// SetReadDeadline is intercepted and the earlier of the library's deadline and
// ours is the one that applies.
type stallConn struct {
	net.Conn
	timeout time.Duration

	mu       sync.Mutex
	armed    bool
	activity time.Time // when we last heard from the server, or armed
	library  time.Time // the deadline the client asked for, zero for none
	stalled  bool      // our deadline ended a read
}

// errServerStalled marks a pass that ended because the server went quiet in the
// middle of a command, so it is reported as an unreachable host.
var errServerStalled = errors.New("the server stopped answering")

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// didStall reports whether the stall deadline has ended a read on this connection.
func (c *stallConn) didStall() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stalled
}

func (c *stallConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	_ = c.applyLocked()
	c.mu.Unlock()
	n, err := c.Conn.Read(p)
	if err != nil && isTimeout(err) {
		c.mu.Lock()
		// The client formats its read errors with %v, so the type is gone by the
		// time a caller sees one; remember that the silence was ours.
		if c.armed && !time.Now().Before(c.activity.Add(c.timeout)) {
			c.stalled = true
		}
		c.mu.Unlock()
	}
	if n > 0 {
		c.mu.Lock()
		c.activity = time.Now()
		_ = c.applyLocked()
		c.mu.Unlock()
	}
	return n, err
}

// SetReadDeadline records the client's own deadline and applies it together
// with the stall deadline.
func (c *stallConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.library = t
	return c.applyLocked()
}

// applyLocked sets the underlying deadline to the earliest of the two.
func (c *stallConn) applyLocked() error {
	deadline := c.library
	if c.armed {
		if ours := c.activity.Add(c.timeout); deadline.IsZero() || ours.Before(deadline) {
			deadline = ours
		}
	}
	return c.Conn.SetReadDeadline(deadline)
}

// arm starts the stall deadline. A Read already blocked is covered too, because
// a deadline applies to pending reads: the reader goroutine is normally already
// waiting when a command is sent.
func (c *stallConn) arm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = true
	c.activity = time.Now()
	_ = c.applyLocked()
}

func (c *stallConn) disarm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = false
	_ = c.applyLocked()
}

// session is an IMAP client plus the stall guard on its connection.
type session struct {
	*imapclient.Client
	stall *stallConn
}

// watch guards the commands that follow until the returned function is called:
// `defer c.watch()()` guards a whole function.
func (s *session) watch() func() {
	s.stall.arm()
	return s.stall.disarm
}
