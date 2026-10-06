// Package gateway is the HTTP surface: handlers, SSE and static assets.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/internal/asset"
	"github.com/AutumnsGrove/Ivy/internal/compress"
	"github.com/AutumnsGrove/Ivy/search"
	"github.com/AutumnsGrove/Ivy/store"
)

// Server holds the handler's dependencies (STANDARDS.md section 4: I/O
// dependencies are passed in, never reached for as globals).
type Server struct {
	dbs     *store.DBs
	version string
	static  fs.FS
	// allowedHosts are the names, besides loopback, the API answers to.
	allowedHosts map[string]bool
	// events is the hint hub behind /api/v1/events; nil means no stream is served.
	events    *events.Hub
	heartbeat time.Duration
	// backupTargets are the folders a purge must erase a blob from as well, so
	// "purge forever" includes the off-device copies (N24). Empty is fine for a
	// server with no backup configured.
	backupTargets []string
	// newID generates an outbox op id (C3: the store never invents one). Tests
	// replace it with a deterministic generator.
	newID func() string
	// now is the clock behind every timestamp the handlers write.
	now func() time.Time
	// searchService does hybrid ranking; nil means a plain keyword search.
	searchService *search.Service
	// queryEmbed turns a search query into a vector for one account, through the
	// gate. nil means keyword-only.
	queryEmbed QueryEmbedder
	// updater requests a self-update through the host-side watcher; nil means
	// this deployment has no update path.
	updater Updater
	// updateCtx owns the resolve goroutine, so it must outlive the request.
	updateCtx context.Context
	// update is the single in-process update slot.
	update updateStatus
	// connector tests, stores and starts an account typed into the app; nil
	// means this deployment cannot connect accounts from the app.
	connector AccountConnector
}

// WithSearch enables hybrid ranking. qe may be nil, in which case search stays
// keyword-only and never reaches a provider.
func (s *Server) WithSearch(qe QueryEmbedder) *Server {
	s.searchService = search.New(s.dbs, nil)
	s.queryEmbed = qe
	return s
}

// WithClock replaces the clock, so a test can assert on the times a handler
// records.
func (s *Server) WithClock(fn func() time.Time) *Server {
	s.now = fn
	return s
}

// WithIDFunc replaces the outbox op id generator, so a test can assert on ids.
func (s *Server) WithIDFunc(fn func() string) *Server {
	s.newID = fn
	return s
}

// WithBackupTargets sets the folders a purge erases blobs from in addition to
// the local store.
func (s *Server) WithBackupTargets(targets []string) *Server {
	s.backupTargets = targets
	return s
}

// WithAllowedHosts sets the host names the API answers to besides loopback
// (config allowed_hosts and the listen host). Without it only loopback works.
func (s *Server) WithAllowedHosts(hosts []string) *Server {
	s.allowedHosts = make(map[string]bool, len(hosts))
	for _, h := range hosts {
		s.allowedHosts[normalizeHost(h)] = true
	}
	return s
}

// New builds a Server. version is the human build identifier reported by the
// version endpoint. static is the built frontend; it may be nil before the
// assets exist.
func New(dbs *store.DBs, version string, static fs.FS) *Server {
	return &Server{dbs: dbs, version: version, static: static, newID: randomID, now: time.Now}
}

// randomID is a 128-bit random hex string, unique enough for an op id without a
// dependency. An op id is never derived from mail, so a collision is harmless to
// correctness (the idempotency key is separate), but the primary key needs it
// unique.
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on the platforms Ivy runs on; if it somehow
		// does, fall back to the clock so an op is still created.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type versionResponse struct {
	Version string `json:"version"`
}

type healthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Databases map[string]string `json:"databases"`
}

// Handler returns the full HTTP surface: the API (compressed per request) and
// the embedded frontend.
func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/version", s.handleVersion)
	api.HandleFunc("GET /api/v1/health", s.handleHealth)
	api.HandleFunc("GET /api/v1/accounts", s.handleAccounts)
	api.HandleFunc("POST /api/v1/accounts", s.handleConnectAccount)
	api.HandleFunc("PUT /api/v1/accounts/{id}/password", s.handleUpdateAccountPassword)
	api.HandleFunc("PATCH /api/v1/accounts/{id}", s.handleUpdateAccountProfile)
	api.HandleFunc("GET /api/v1/accounts/{id}/photo", s.handleAccountPhoto)
	api.HandleFunc("PUT /api/v1/accounts/{id}/photo", s.handleUploadAccountPhoto)
	api.HandleFunc("DELETE /api/v1/accounts/{id}/photo", s.handleDeleteAccountPhoto)
	api.HandleFunc("GET /api/v1/inbox", s.handleInbox)
	api.HandleFunc("GET /api/v1/reading", s.handleListReading)
	api.HandleFunc("GET /api/v1/search", s.handleSearch)
	api.HandleFunc("GET /api/v1/messages/{id}", s.handleMessage)
	api.HandleFunc("GET /api/v1/messages/{id}/summary", s.handleMessageSummary)
	api.HandleFunc("GET /api/v1/messages/{id}/body", s.handleMessageBody)
	api.HandleFunc("GET /api/v1/messages/{id}/inline/{cid}", s.handleInline)
	api.HandleFunc("GET /api/v1/messages/{id}/attachments/{part}", s.handleAttachment)
	api.HandleFunc("POST /api/v1/messages/{id}/snooze", s.handleSnoozeMessage)
	api.HandleFunc("DELETE /api/v1/messages/{id}/snooze", s.handleUnsnoozeMessage)
	api.HandleFunc("GET /api/v1/mirror/health", s.handleMirrorHealth)
	api.HandleFunc("POST /api/v1/mirror/messages/{id}/restore", s.handleRestoreMessage)
	api.HandleFunc("POST /api/v1/mirror/accounts/{id}/restore", s.handleRestoreAccountHidden)
	api.HandleFunc("DELETE /api/v1/mirror/messages/{id}", s.handlePurgeMessage)
	api.HandleFunc("GET /api/v1/tags", s.handleListTags)
	api.HandleFunc("POST /api/v1/tags", s.handleCreateTag)
	api.HandleFunc("PATCH /api/v1/tags/{id}", s.handleUpdateTag)
	api.HandleFunc("DELETE /api/v1/tags/{id}", s.handleDeleteTag)
	api.HandleFunc("GET /api/v1/people", s.handleListPeople)
	api.HandleFunc("GET /api/v1/people/{id}", s.handleGetPerson)
	api.HandleFunc("POST /api/v1/people/{id}/addresses", s.handleLinkPersonAddress)
	api.HandleFunc("DELETE /api/v1/people/{id}/addresses/{address}", s.handleUnlinkPersonAddress)
	api.HandleFunc("GET /api/v1/rules", s.handleListRules)
	api.HandleFunc("POST /api/v1/rules", s.handleCreateRule)
	api.HandleFunc("POST /api/v1/rules/dry-run", s.handleDryRunRule)
	api.HandleFunc("GET /api/v1/rules/{id}", s.handleGetRule)
	api.HandleFunc("PUT /api/v1/rules/{id}", s.handleUpdateRule)
	api.HandleFunc("PATCH /api/v1/rules/{id}", s.handleSetRuleEnabled)
	api.HandleFunc("DELETE /api/v1/rules/{id}", s.handleDeleteRule)
	api.HandleFunc("POST /api/v1/rules/{id}/apply", s.handleApplyRule)
	api.HandleFunc("POST /api/v1/outbox", s.handleEnqueueOutbox)
	api.HandleFunc("POST /api/v1/send", s.handleSendMessage)
	api.HandleFunc("GET /api/v1/send", s.handleListSends)
	api.HandleFunc("GET /api/v1/send/{id}", s.handleGetSend)
	api.HandleFunc("POST /api/v1/send/{id}/undo", s.handleUndoSend)
	api.HandleFunc("GET /api/v1/outbox", s.handleListOutbox)
	api.HandleFunc("POST /api/v1/outbox/{id}/retry", s.handleRetryOutbox)
	api.HandleFunc("DELETE /api/v1/outbox/{id}", s.handleDismissOutbox)
	api.HandleFunc("POST /api/v1/update", s.handleUpdate)
	api.HandleFunc("GET /api/v1/update", s.handleUpdateStatus)
	if s.events != nil {
		api.HandleFunc("GET /api/v1/events", s.handleEvents)
	}

	root := http.NewServeMux()
	root.Handle("/api/", s.hostGuard(noStore(apiCSP(compress.Middleware(originGuard(jsonErrors(api)))))))
	if s.static != nil {
		root.Handle("/", asset.FileServer(s.static))
	} else {
		root.Handle("/", http.NotFoundHandler())
	}
	return hardened(root)
}

// hardened sets the response headers every page and API reply needs. The full
// Content-Security-Policy arrives with the sandboxed reader (render/), because
// SvelteKit's inline bootstrap needs hashes chosen alongside it.
func hardened(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		// A link clicked in a mail must not tell the sender's site which Ivy
		// URL (and so which message) it came from.
		h.Set("Referrer-Policy", "no-referrer")
		// SAMEORIGIN, not DENY: the reader frames its own sanitised bodies.
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// apiCSP is a deny-all policy for API replies. It is inert on JSON and blocks
// everything if a response is ever loaded as a document. The strict policy for
// the reader's body document is render.ContentSecurityPolicy in Go and the
// frame's sandbox in the web app.
func apiCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
		next.ServeHTTP(w, r)
	})
}

// noStore is the API default; a handler that sets its own Cache-Control wins.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// hostGuard answers only to loopback and the configured host names. The Origin
// check below compares Origin to Host, and in a DNS-rebinding attack both are
// the attacker's own name, so a rebound page passes it and could read and write
// the whole API. Pinning Host to names the operator listed closes that, for
// reads as well as writes (N11 in papercuts.md). A missing Host never matches.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		if host == "" || (!isLoopbackHost(host) && !s.allowedHosts[host]) {
			writeError(w, http.StatusForbidden, "forbidden", "This address is not allowed to reach Ivy")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostOnly lower-cases a Host header and drops its port and any trailing dot.
func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return normalizeHost(strings.Trim(host, "[]"))
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// isLoopbackHost is exact: "localhost" and loopback IPs, not "x.localhost".
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// originGuard rejects a browser cross-site write. With no auth, a mutating
// request is only as trusted as its Origin: a page on another site open in the
// same browser must not be able to rename an account (STANDARDS.md section 8).
// A non-browser client sends no Origin and already had network access, so it is
// allowed; GET/HEAD stay open so a link can be shared.
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "forbidden", "This request came from another site")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{Version: s.version})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbs := map[string]string{
		"mirror": dbStatus(r, s.dbs.Mirror),
		"state":  dbStatus(r, s.dbs.State),
	}
	status := http.StatusOK
	overall := "ok"
	for _, v := range dbs {
		if v != "ok" {
			status = http.StatusServiceUnavailable
			overall = "unavailable"
			break
		}
	}
	writeJSON(w, status, healthResponse{Status: overall, Version: s.version, Databases: dbs})
}

func dbStatus(r *http.Request, db *store.DB) string {
	if err := db.Ping(r.Context()); err != nil {
		return "error"
	}
	return "ok"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
