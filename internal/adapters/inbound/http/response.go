package http

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// maxBodyBytes bounds every request body.
const maxBodyBytes = 64 << 10

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 9457 body of every error response.
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON decodes the request body into dest: unknown fields, an empty body
// and trailing data are all malformed.
func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("%w: %v", errMalformedRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after the JSON body", errMalformedRequest)
	}
	return nil
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	detail := err.Error()
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", err, "method", r.Method, "path", r.URL.Path)
		detail = "an unexpected error occurred"
	}
	writeProblem(w, r, p, detail)
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath(),
	})
}
