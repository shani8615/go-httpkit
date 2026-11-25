package idempotency

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func setup(status int) (http.Handler, *int32) {
	var calls int32
	h := Middleware(Options{Store: NewMemoryStore()})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "order-%d", n)
	}))
	return h, &calls
}

func post(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/orders", strings.NewReader(body))
	if key != "" {
		req.Header.Set(Header, key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReplaysFirstResponse(t *testing.T) {
	h, calls := setup(201)
	a := post(h, "k1", `{"qty":1}`)
	b := post(h, "k1", `{"qty":1}`)
	if *calls != 1 {
		t.Fatalf("handler ran %d times", *calls)
	}
	if b.Code != 201 || b.Body.String() != a.Body.String() || b.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay mismatch: %d %q", b.Code, b.Body.String())
	}
}

func TestDifferentBodySameKeyRejected(t *testing.T) {
	h, _ := setup(201)
	post(h, "k1", `{"qty":1}`)
	if rec := post(h, "k1", `{"qty":2}`); rec.Code != 422 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestServerErrorsNotCached(t *testing.T) {
	h, calls := setup(503)
	post(h, "k1", "x")
	post(h, "k1", "x")
	if *calls != 2 {
		t.Fatalf("5xx must allow retry, calls=%d", *calls)
	}
}

func TestNoKeyPassesThroughUnlessRequired(t *testing.T) {
	h, calls := setup(200)
	post(h, "", "x")
	post(h, "", "x")
	if *calls != 2 {
		t.Fatalf("calls=%d", *calls)
	}
	req := Middleware(Options{Store: NewMemoryStore(), Required: true})(http.NotFoundHandler())
	if rec := post(req, "", "x"); rec.Code != 400 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestScopeIsolatesClients(t *testing.T) {
	var calls int32
	h := Middleware(Options{Store: NewMemoryStore(), Scope: func(r *http.Request) string { return r.Header.Get("X-User") }})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1) }))
	for _, user := range []string{"alice", "bob"} {
		req := httptest.NewRequest("POST", "/", strings.NewReader("x"))
		req.Header.Set(Header, "same")
		req.Header.Set("X-User", user)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if calls != 2 {
		t.Fatalf("scoped keys must not collide, calls=%d", calls)
	}
}
