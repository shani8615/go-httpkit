// Package pagination parses limit/offset and opaque cursor parameters and
// encodes keyset cursors, so list endpoints stay consistent across services.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Defaults for Parse.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Page is the parsed request.
type Page struct {
	Limit  int
	Offset int
	Cursor string // raw opaque cursor, decode with Decode
}

// ErrBadParam is returned for malformed parameters.
var ErrBadParam = errors.New("pagination: invalid parameter")

// Parse reads ?limit=&offset=&cursor= from r. Limit is clamped to [1, MaxLimit].
func Parse(r *http.Request) (Page, error) {
	q := r.URL.Query()
	p := Page{Limit: DefaultLimit, Cursor: q.Get("cursor")}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return p, ErrBadParam
		}
		p.Limit = min(n, MaxLimit)
	}
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return p, ErrBadParam
		}
		p.Offset = n
	}
	return p, nil
}

// Encode turns a keyset position (e.g. {CreatedAt, ID}) into an opaque cursor.
func Encode[T any](pos T) (string, error) {
	b, err := json.Marshal(pos)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode parses a cursor produced by Encode.
func Decode[T any](cursor string) (T, error) {
	var pos T
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return pos, ErrBadParam
	}
	if err := json.Unmarshal(b, &pos); err != nil {
		return pos, ErrBadParam
	}
	return pos, nil
}

// Result is a standard list response envelope.
type Result[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Build trims items fetched with LIMIT limit+1 into a Result, computing the
// next cursor from the last returned item.
func Build[T, P any](items []T, limit int, position func(T) P) (Result[T], error) {
	res := Result[T]{Items: items}
	if res.Items == nil {
		res.Items = []T{}
	}
	if len(items) > limit {
		res.Items = items[:limit]
		res.HasMore = true
		c, err := Encode(position(res.Items[limit-1]))
		if err != nil {
			return res, err
		}
		res.NextCursor = c
	}
	return res, nil
}
