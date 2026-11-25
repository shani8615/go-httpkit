// Package respond writes JSON responses and RFC 9457 problem details, and
// decodes JSON request bodies strictly.
package respond

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// Problem is an RFC 9457 problem-details body.
type Problem struct {
	Type     string         `json:"type,omitempty"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Errors   map[string]any `json:"errors,omitempty"`
}

// Error writes a problem-details response. Detail must be safe to show to
// clients — never pass raw internal errors here.
func Error(w http.ResponseWriter, status int, detail string) {
	WriteProblem(w, Problem{Title: http.StatusText(status), Status: status, Detail: detail})
}

// WriteProblem writes p with content type application/problem+json.
func WriteProblem(w http.ResponseWriter, p Problem) {
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// Decode reads a single JSON object into dst, rejecting unknown fields,
// trailing data and bodies larger than maxBytes (default 1 MiB).
func Decode(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return fmt.Errorf("request body must not exceed %d bytes", maxBytes)
		}
		if errors.Is(err, io.EOF) {
			return errors.New("request body must not be empty")
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}
