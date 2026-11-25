# go-httpkit

[![ci](https://github.com/shani8615/go-httpkit/actions/workflows/ci.yml/badge.svg)](https://github.com/shani8615/go-httpkit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/shani8615/go-httpkit.svg)](https://pkg.go.dev/github.com/shani8615/go-httpkit)

Batteries for plain `net/http` services. No framework, no dependencies outside the standard library, and it works with `http.ServeMux` (Go 1.22+ routing), chi, or anything else that speaks `http.Handler`.

```bash
go get github.com/shani8615/go-httpkit
```

| Package | What it gives you |
|---|---|
| `middleware` | `Chain`, `RequestID`, `Logger` (slog), `Recover`, `CORS`, `Timeout`, `MaxBodyBytes` |
| `idempotency` | `Idempotency-Key` support for POST/PATCH: replays the first response, rejects key reuse with a different body, never caches 5xx |
| `pagination` | `?limit/offset/cursor` parsing, opaque keyset cursors, `Result[T]` envelope |
| `respond` | `JSON`, RFC 9457 problem details, strict JSON body decoding |

## Example

```go
package main

import (
    "log/slog"
    "net/http"
    "os"
    "time"

    "github.com/shani8615/go-httpkit/idempotency"
    "github.com/shani8615/go-httpkit/middleware"
    "github.com/shani8615/go-httpkit/respond"
)

type createOrder struct {
    SKU string `json:"sku"`
    Qty int    `json:"qty"`
}

func main() {
    log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

    mux := http.NewServeMux()
    mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
        var in createOrder
        if err := respond.Decode(w, r, &in, 0); err != nil {
            respond.Error(w, http.StatusBadRequest, err.Error())
            return
        }
        respond.JSON(w, http.StatusCreated, map[string]any{"sku": in.SKU, "qty": in.Qty})
    })

    h := middleware.Chain(mux,
        middleware.RequestID,
        middleware.Logger(log),
        middleware.Recover(log),
        middleware.CORS(middleware.CORSOptions{AllowedOrigins: []string{"https://app.example.com"}}),
        middleware.Timeout(10*time.Second),
        idempotency.Middleware(idempotency.Options{Store: idempotency.NewMemoryStore()}),
    )
    _ = http.ListenAndServe(":8080", h)
}
```

## Design notes

- **The logger never logs query strings, headers or bodies.** Those often carry tokens or personal data. A test enforces this.
- **Idempotency keys should be scoped.** Set `Options.Scope` to the authenticated user or tenant so one client can't replay another client's response. For more than one instance, implement `idempotency.Store` on Redis or Postgres.
- **`respond.Error` takes a client-safe message.** Log the internal error yourself and don't pass it to the client.

## License

MIT
