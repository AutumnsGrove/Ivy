package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
)

// writeError emits the contract's error envelope: a stable code the UI maps to
// its own copy, and no internal detail.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.Error{Code: code, Message: message})
}

// serverError logs the real cause and answers with a generic body: mail and
// internal detail never leave the process in a response.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "gateway: request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong")
}

func (s *Server) notFound(w http.ResponseWriter, _ *http.Request, kind string) {
	writeError(w, http.StatusNotFound, "not_found", "No such "+kind)
}

// jsonErrors rewrites the mux's plain-text 404 and 405 replies into the
// contract's JSON envelope (N6 in papercuts.md), so the client never parses a
// bare string. Only the mux's own text/plain errors are touched; a handler's
// JSON 404 passes straight through.
func jsonErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&errorWriter{ResponseWriter: w}, r)
	})
}

type errorWriter struct {
	http.ResponseWriter
	replaced bool
}

func (e *errorWriter) WriteHeader(status int) {
	if !e.replaced && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) &&
		strings.HasPrefix(e.Header().Get("Content-Type"), "text/plain") {
		e.replaced = true
		code := "not_found"
		if status == http.StatusMethodNotAllowed {
			code = "method_not_allowed"
		}
		e.Header().Set("Content-Type", "application/json")
		e.ResponseWriter.WriteHeader(status)
		_ = json.NewEncoder(e.ResponseWriter).Encode(api.Error{Code: code, Message: http.StatusText(status)})
		return
	}
	e.ResponseWriter.WriteHeader(status)
}

// Write discards the plain-text body that follows a rewritten error; the JSON
// envelope was already written.
func (e *errorWriter) Write(p []byte) (int, error) {
	if e.replaced {
		return len(p), nil
	}
	return e.ResponseWriter.Write(p)
}
