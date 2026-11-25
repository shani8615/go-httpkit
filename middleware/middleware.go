// Package middleware provides composable net/http middleware: request IDs,
// structured access logs (log/slog), panic recovery, CORS and timeouts.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so the first argument is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const requestIDKey ctxKey = iota

// RequestIDHeader is the header read and written by RequestID.
const RequestIDHeader = "X-Request-ID"

// RequestID reuses an incoming X-Request-ID (if short and printable) or
// generates one, stores it in the context and echoes it in the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validID(id) {
			id = newID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// GetRequestID returns the request ID stored by RequestID, or "".
func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// statusRecorder captures status code and bytes written.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logger writes one structured log line per request. It logs method, path,
// status, size, duration and request ID — never query strings, headers or
// bodies, which may carry secrets or personal data.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			log.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", GetRequestID(r.Context())),
			)
		})
	}
}

// Recover turns a panic into a 500 and logs the stack trace.
// http.ErrAbortHandler is re-panicked as net/http expects.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison is what net/http does
						panic(v)
					}
					log.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", v),
						slog.String("stack", string(debug.Stack())),
						slog.String("request_id", GetRequestID(r.Context())))
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORSOptions configures CORS. AllowedOrigins is an explicit allow-list;
// "*" is accepted but never combined with credentials.
type CORSOptions struct {
	AllowedOrigins   []string
	AllowedMethods   []string // default GET, POST, PUT, PATCH, DELETE
	AllowedHeaders   []string // default Content-Type, Authorization, X-Request-ID, Idempotency-Key
	AllowCredentials bool
	MaxAge           time.Duration
}

// CORS handles preflight requests and sets CORS headers for allowed origins.
func CORS(o CORSOptions) Middleware {
	if len(o.AllowedMethods) == 0 {
		o.AllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	}
	if len(o.AllowedHeaders) == 0 {
		o.AllowedHeaders = []string{"Content-Type", "Authorization", RequestIDHeader, "Idempotency-Key"}
	}
	wildcard := slices.Contains(o.AllowedOrigins, "*")
	methods := strings.Join(o.AllowedMethods, ", ")
	headers := strings.Join(o.AllowedHeaders, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			w.Header().Add("Vary", "Origin")
			allowed := origin != "" && (slices.Contains(o.AllowedOrigins, origin) || wildcard)
			if allowed {
				if wildcard && !o.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
				if o.AllowCredentials && !wildcard {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Methods", methods)
					w.Header().Set("Access-Control-Allow-Headers", headers)
					if o.MaxAge > 0 {
						w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(o.MaxAge.Seconds())))
					}
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout cancels the request context after d. Handlers must respect ctx.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MaxBodyBytes limits request body size.
func MaxBodyBytes(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
