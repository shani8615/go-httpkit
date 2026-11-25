// Package idempotency makes unsafe HTTP requests (POST, PATCH) safe to retry.
// A client sends an Idempotency-Key header; the first response for that key
// is stored and replayed for later requests with the same key and body.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// Header is the request header carrying the key.
const Header = "Idempotency-Key"

// Record is a stored response.
type Record struct {
	BodyHash string
	Status   int
	Header   http.Header
	Body     []byte
	Done     bool // false while the first request is still running
}

// Store persists records. Implement it with Redis or Postgres in production;
// MemoryStore is fine for tests and single-instance services.
type Store interface {
	// Begin atomically creates an in-progress record. It returns the
	// existing record and false if the key is already present.
	Begin(ctx context.Context, key string, rec Record, ttl time.Duration) (Record, bool, error)
	Complete(ctx context.Context, key string, rec Record, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Options configures the middleware.
type Options struct {
	Store   Store
	TTL     time.Duration // default 24h
	Methods []string      // default POST, PATCH
	// Scope namespaces keys, e.g. by authenticated user, so one client
	// can't replay another client's response. Strongly recommended.
	Scope    func(r *http.Request) string
	Required bool  // reject unsafe requests without a key (400)
	MaxBody  int64 // default 1 MiB
}

// Middleware returns the idempotency middleware.
func Middleware(o Options) func(http.Handler) http.Handler {
	if o.TTL <= 0 {
		o.TTL = 24 * time.Hour
	}
	if len(o.Methods) == 0 {
		o.Methods = []string{http.MethodPost, http.MethodPatch}
	}
	if o.MaxBody <= 0 {
		o.MaxBody = 1 << 20
	}
	methods := map[string]bool{}
	for _, m := range o.Methods {
		methods[m] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !methods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(Header)
			if key == "" {
				if o.Required {
					http.Error(w, "Idempotency-Key header required", http.StatusBadRequest)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > 255 {
				http.Error(w, "Idempotency-Key too long", http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, o.MaxBody+1))
			if err != nil || int64(len(body)) > o.MaxBody {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			storeKey := r.Method + " " + r.URL.Path + " " + key
			if o.Scope != nil {
				storeKey = o.Scope(r) + "|" + storeKey
			}
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])

			existing, created, err := o.Store.Begin(r.Context(), storeKey, Record{BodyHash: hash}, o.TTL)
			if err != nil {
				http.Error(w, "idempotency store unavailable", http.StatusServiceUnavailable)
				return
			}
			if !created {
				switch {
				case existing.BodyHash != hash:
					http.Error(w, "Idempotency-Key reused with a different request body", http.StatusUnprocessableEntity)
				case !existing.Done:
					http.Error(w, "a request with this Idempotency-Key is in progress", http.StatusConflict)
				default:
					for k, v := range existing.Header {
						w.Header()[k] = v
					}
					w.Header().Set("Idempotent-Replayed", "true")
					w.WriteHeader(existing.Status)
					_, _ = w.Write(existing.Body)
				}
				return
			}

			rec := &recorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			// Don't cache server errors: let the client retry them.
			ctx := context.WithoutCancel(r.Context())
			if rec.status >= 500 {
				_ = o.Store.Delete(ctx, storeKey)
				return
			}
			_ = o.Store.Complete(ctx, storeKey, Record{
				BodyHash: hash, Status: rec.status, Header: w.Header().Clone(), Body: rec.buf.Bytes(), Done: true,
			}, o.TTL)
		})
	}
}

type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}

// MemoryStore is an in-process Store with expiry.
type MemoryStore struct {
	mu  sync.Mutex
	m   map[string]memEntry
	Now func() time.Time
}

type memEntry struct {
	rec Record
	exp time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]memEntry{}, Now: time.Now} }

// ErrNotFound is returned by Complete when the key is unknown.
var ErrNotFound = errors.New("idempotency: key not found")

func (s *MemoryStore) Begin(_ context.Context, key string, rec Record, ttl time.Duration) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if e, ok := s.m[key]; ok && now.Before(e.exp) {
		return e.rec, false, nil
	}
	s.m[key] = memEntry{rec: rec, exp: now.Add(ttl)}
	return rec, true, nil
}

func (s *MemoryStore) Complete(_ context.Context, key string, rec Record, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		return ErrNotFound
	}
	s.m[key] = memEntry{rec: rec, exp: s.Now().Add(ttl)}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
