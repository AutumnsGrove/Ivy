package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/update"
)

// Updater requests a self-update through the host-side watcher. The gateway
// owns no pull, recreate or rollback logic: it resolves the target and writes a
// signal file, and the host does the rest. A nil Updater means this deployment
// has no update path (bare-metal dev), and the endpoint says so.
type Updater interface {
	Request(ctx context.Context) (target string, err error)
	Pending() bool
	Result() *update.Result
}

// Watcher polling bounds. Polling stops as soon as the watcher removes the
// requested file. Vars so a test does not wait real seconds.
var (
	watcherPollInterval = 2 * time.Second
	watcherMaxWait      = 15 * time.Minute
)

// updateStatus is the one update slot, shared across requests so closing the
// settings panel mid-run cannot lose track of it and invite a second click that
// races the first.
type updateStatus struct {
	mu      sync.Mutex
	running bool
	done    bool
	success bool
	target  string
	errMsg  string
}

func (u *updateStatus) tryStart() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.running {
		return false
	}
	u.running = true
	u.done = false
	u.success = false
	u.target = ""
	u.errMsg = ""
	return true
}

func (u *updateStatus) setTarget(target string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.target = target
}

func (u *updateStatus) finish(success bool, target, errMsg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.running = false
	u.done = true
	u.success = success
	u.target = target
	u.errMsg = errMsg
}

func (u *updateStatus) snapshot() updateStatusResponse {
	u.mu.Lock()
	defer u.mu.Unlock()
	return updateStatusResponse{
		Running: u.running,
		Done:    u.done,
		Success: u.success,
		Target:  u.target,
		Error:   u.errMsg,
	}
}

// updateStatusResponse is the contract's UpdateStatus. Unavailable is set when
// the deployment has no updater at all, so the UI can say so plainly rather
// than offering a button that can never work.
type updateStatusResponse struct {
	Unavailable bool           `json:"unavailable"`
	Running     bool           `json:"running"`
	Done        bool           `json:"done"`
	Success     bool           `json:"success"`
	Target      string         `json:"target,omitempty"`
	Error       string         `json:"error,omitempty"`
	Watcher     *update.Result `json:"watcher,omitempty"`
}

// WithUpdate enables the self-update endpoints. u may be nil. ctx owns the
// resolve goroutine; it must outlive the request that starts an update, so pass
// the server's own run context, not r.Context.
func (s *Server) WithUpdate(ctx context.Context, u Updater) *Server {
	s.updater = u
	s.updateCtx = ctx
	return s
}

// handleUpdate claims the slot and starts the resolve in the background, then
// answers immediately. It cannot block for the CI wait: the frontend's fetch
// times out after 30 seconds, and a publish can take minutes.
func (s *Server) handleUpdate(w http.ResponseWriter, _ *http.Request) {
	if s.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update_unavailable", "This Ivy has no update path")
		return
	}
	if !s.update.tryStart() {
		writeError(w, http.StatusConflict, "update_running", "An update is already in progress")
		return
	}
	s.publishUpdate()
	ctx := s.updateCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go s.runUpdate(ctx) //nolint:contextcheck // the update outlives this request, so it must not inherit r.Context
	writeJSON(w, http.StatusAccepted, s.updateSnapshot())
}

// runUpdate resolves the target, hands it to the watcher, then waits for the
// watcher's result so the UI and the SSE stream can report the real outcome,
// not just that the request was written.
func (s *Server) runUpdate(ctx context.Context) {
	target, err := s.updater.Request(ctx)
	if err != nil {
		s.update.finish(false, "", err.Error())
		s.publishUpdate()
		return
	}
	s.update.setTarget(target)
	s.publishUpdate()
	s.awaitWatcher(ctx, target)
}

func (s *Server) awaitWatcher(ctx context.Context, target string) {
	deadline := time.Now().Add(watcherMaxWait)
	for s.updater.Pending() {
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(watcherPollInterval):
		}
	}
	res := s.updater.Result()
	if res != nil && res.Target != "" {
		target = res.Target
	}
	success := res != nil && res.Status == "ok"
	detail := ""
	if res != nil {
		detail = res.Detail
	} else {
		detail = "the host update watcher reported no result"
	}
	s.update.finish(success, target, detail)
	s.publishUpdate()
}

// handleUpdateStatus reports the slot and the watcher's own last result, which
// is the only place the real reason a pull or recreate failed is ever known.
func (s *Server) handleUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.updateSnapshot())
}

func (s *Server) updateSnapshot() updateStatusResponse {
	snap := s.update.snapshot()
	snap.Unavailable = s.updater == nil
	if s.updater != nil && !snap.Running {
		if res := s.updater.Result(); res != nil {
			snap.Watcher = res
		}
	}
	return snap
}

func (s *Server) publishUpdate() {
	if s.events != nil {
		s.events.Publish(events.Event{Type: events.UpdateState})
	}
}
