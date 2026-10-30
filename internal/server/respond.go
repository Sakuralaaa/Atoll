package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errf(status int, code, format string, a ...any) error {
	return &apiError{status: status, code: code, msg: fmt.Sprintf(format, a...)}
}

func badRequest(format string, a ...any) error { return errf(400, "invalid", format, a...) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
	case errors.Is(err, store.ErrNotFound):
		ae = &apiError{404, "not_found", "not found"}
	case errors.Is(err, store.ErrConflict):
		ae = &apiError{409, "conflict", "already exists"}
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		ae = &apiError{500, "internal", err.Error()}
	}
	writeJSON(w, ae.status, api.ErrorBody{Error: ae.msg, Code: ae.code})
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) wrap(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			s.writeErr(w, r, err)
		}
	}
}

// decode reads a JSON body (max 4 MiB).
func decode(r *http.Request, v any) error {
	body := io.LimitReader(r.Body, 4<<20)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("request body required")
		}
		return badRequest("invalid JSON: %v", err)
	}
	return nil
}

func isTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
