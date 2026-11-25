package respond

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONAndProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, 201, map[string]int{"id": 1})
	if rec.Code != 201 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = httptest.NewRecorder()
	Error(rec, 404, "order not found")
	var p Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != 404 || p.Title != "Not Found" || p.Detail != "order not found" ||
		rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem=%+v", p)
	}
}

func TestDecode(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	cases := map[string]bool{
		`{"name":"a"}`:           true,
		`{"name":"a","x":1}`:     false, // unknown field
		`{"name":"a"}{"name":2}`: false, // trailing object
		``:                       false,
		`{"name":"` + strings.Repeat("a", 100) + `"}`: false, // too large for 50 bytes
	}
	for body, want := range cases {
		var dst in
		err := Decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body)), &dst, 50)
		if (err == nil) != want {
			t.Errorf("body %.20q: err=%v want ok=%v", body, err, want)
		}
	}
}
