// Package gateway is the HTTP surface: handlers, SSE and static assets.
package gateway

import (
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/internal/asset"
	"github.com/AutumnsGrove/Ivy/internal/compress"
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
	return &Server{dbs: dbs, version: version, static: static}
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
	api.HandleFunc("PATCH /api/v1/accounts/{id}", s.handleUpdateAccountProfile)
	api.HandleFunc("GET /api/v1/accounts/{id}/photo", s.handleAccountPhoto)
	api.HandleFunc("PUT /api/v1/accounts/{id}/photo", s.handleUploadAccountPhoto)
	api.HandleFunc("DELETE /api/v1/accounts/{id}/photo", s.handleDeleteAccountPhoto)
	api.HandleFunc("GET /api/v1/inbox", s.handleInbox)
	api.HandleFunc("GET /api/v1/messages/{id}", s.handleMessage)
	api.HandleFunc("GET /api/v1/messages/{id}/summary", s.handleMessageSummary)
	api.HandleFunc("GET /api/v1/messages/{id}/body", s.handleMessageBody)
	api.HandleFunc("GET /api/v1/messages/{id}/inline/{cid}", s.handleInline)
	api.HandleFunc("GET /api/v1/messages/{id}/attachments/{part}", s.handleAttachment)
	api.HandleFunc("GET /api/v1/mirror/health", s.handleMirrorHealth)
	api.HandleFunc("POST /api/v1/mirror/messages/{id}/restore", s.handleRestoreMessage)
	api.HandleFunc("POST /api/v1/mirror/accounts/{id}/restore", s.handleRestoreAccountHidden)
	api.HandleFunc("DELETE /api/v1/mirror/messages/{id}", s.handlePurgeMessage)
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
