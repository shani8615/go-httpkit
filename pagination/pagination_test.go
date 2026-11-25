package pagination

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		q       string
		limit   int
		offset  int
		wantErr bool
	}{
		{"", DefaultLimit, 0, false},
		{"?limit=5&offset=10", 5, 10, false},
		{"?limit=1000", MaxLimit, 0, false},
		{"?limit=0", 0, 0, true},
		{"?limit=abc", 0, 0, true},
		{"?offset=-1", 0, 0, true},
	}
	for _, c := range cases {
		p, err := Parse(httptest.NewRequest("GET", "/items"+c.q, nil))
		if c.wantErr {
			if !errors.Is(err, ErrBadParam) {
				t.Errorf("%q: want error", c.q)
			}
			continue
		}
		if err != nil || p.Limit != c.limit || p.Offset != c.offset {
			t.Errorf("%q: got %+v err=%v", c.q, p, err)
		}
	}
}

type pos struct {
	CreatedAt time.Time `json:"t"`
	ID        int64     `json:"id"`
}

func TestCursorRoundTrip(t *testing.T) {
	in := pos{CreatedAt: time.Unix(1700000000, 0).UTC(), ID: 7}
	c, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode[pos](c)
	if err != nil || !out.CreatedAt.Equal(in.CreatedAt) || out.ID != 7 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if _, err := Decode[pos]("!!!"); !errors.Is(err, ErrBadParam) {
		t.Fatal("garbage cursor must fail")
	}
}

func TestBuild(t *testing.T) {
	items := []int{1, 2, 3, 4}
	res, err := Build(items, 3, func(i int) int { return i })
	if err != nil || len(res.Items) != 3 || !res.HasMore {
		t.Fatalf("res=%+v", res)
	}
	last, _ := Decode[int](res.NextCursor)
	if last != 3 {
		t.Fatalf("cursor points to %d", last)
	}
	res, _ = Build[int, int](nil, 3, func(i int) int { return i })
	if res.Items == nil || res.HasMore {
		t.Fatal("empty result must have non-nil items and no more pages")
	}
}
