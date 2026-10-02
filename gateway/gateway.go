// Package gateway is the HTTP surface: handlers, SSE and static assets.
package gateway

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"

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

	root := http.NewServeMux()
	root.Handle("/api/", compress.Middleware(api))
	if s.static != nil {
		root.Handle("/", asset.FileServer(s.static))
	} else {
		root.Handle("/", http.NotFoundHandler())
	}
	return root
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

func dbStatus(r *http.Request, db *sql.DB) string {
	if err := db.PingContext(r.Context()); err != nil {
		return "error"
	}
	return "ok"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
