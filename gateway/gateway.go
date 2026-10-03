// Package gateway is the HTTP surface: handlers, SSE and static assets.
package gateway

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

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
	// now is the clock the read views render times against; injected so a test
	// can pin "today".
	now func() time.Time
}

// New builds a Server. version is the human build identifier reported by the
// version endpoint. static is the built frontend; it may be nil before the
// assets exist.
func New(dbs *store.DBs, version string, static fs.FS) *Server {
	return &Server{dbs: dbs, version: version, static: static, now: time.Now}
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
	api.HandleFunc("GET /api/v1/inbox", s.handleInbox)
	api.HandleFunc("GET /api/v1/messages/{id}", s.handleMessage)
	api.HandleFunc("GET /api/v1/messages/{id}/summary", s.handleMessageSummary)
	api.HandleFunc("GET /api/v1/messages/{id}/body", s.handleMessageBody)
	api.HandleFunc("GET /api/v1/messages/{id}/inline/{cid}", s.handleInline)
	api.HandleFunc("GET /api/v1/messages/{id}/attachments/{part}", s.handleAttachment)
	api.HandleFunc("GET /api/v1/mirror/health", s.handleMirrorHealth)

	root := http.NewServeMux()
	root.Handle("/api/", noStore(apiCSP(compress.Middleware(jsonErrors(api)))))
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
