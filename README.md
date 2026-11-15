# kv

A small Go library: one key/value store interface, two backends.

```go
import (
    "github.com/nsf/kv"
    "github.com/nsf/kv/memory"
)

var s kv.Store = memory.New()
s.Set("greeting", []byte("hello"))
v, err := s.Get("greeting") // missing keys return kv.ErrNotFound (check errors.Is)
```

Backends: `memory` (in-memory, thread-safe) and `sqlite` (persistent, via
`database/sql` + go-sqlite3; CGO required). `kv.NewJSONStore` wraps any store
with typed JSON `Get[T]`/`Set[T]`.

The API is deliberately context-less: it's meant for self-hosted use, where
stores are fast and local or on the LAN even when backed by a real DB.

Tests: `go vet ./... && go test ./...`
