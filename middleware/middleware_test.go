package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ok(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }

func TestChainOrder(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(ok), mk("a"), mk("b"), mk("c"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(order, "") != "abc" {
		t.Fatalf("order=%v", order)
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = GetRequestID(r.Context()) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if seen == "" || rec.Header().Get(RequestIDHeader) != seen {
		t.Fatalf("generated id missing: %q", seen)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "abc-123" {
		t.Fatalf("incoming id not reused: %q", seen)
	}

	req.Header.Set(RequestIDHeader, "bad id\nwith newline")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == "bad id\nwith newline" {
		t.Fatal("unsafe id must be replaced")
	}
}

func TestLoggerDoesNotLogQuery(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) }), RequestID, Logger(log))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/orders?token=secret", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["status"].(float64) != 201 || line["path"] != "/orders" {
		t.Fatalf("line=%v", line)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Fatal("query string leaked into logs")
	}
}

func TestRecover(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	h := Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	h := CORS(CORSOptions{AllowedOrigins: []string{"https://app.example.com"}, AllowCredentials: true})(http.HandlerFunc(ok))

	pre := httptest.NewRequest("OPTIONS", "/", nil)
	pre.Header.Set("Origin", "https://app.example.com")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pre)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight headers=%v", rec.Header())
	}

	evil := httptest.NewRequest("GET", "/", nil)
	evil.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, evil)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin must not be allowed")
	}
}

func TestMaxBodyBytes(t *testing.T) {
	h := MaxBodyBytes(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(413)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("way too long")))
	if rec.Code != 413 {
		t.Fatalf("code=%d", rec.Code)
	}
}
