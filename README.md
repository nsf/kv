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

Transactions are an optional extension (`kv.Transactional`), implemented by
`sqlite` only:

```go
err := s.Transact(func(tx kv.Store) error { // commit on nil, rollback on error/panic
    v, err := tx.Get("k")
    if err != nil { return err }
    return tx.Set("k", append(v, '!'))
})
// or generically: kv.Transact(store, fn) reports kv.ErrNotSupported
// if the store isn't Transactional; JSONStore has Transact too.
```

The API is deliberately context-less: it's meant for self-hosted use, where
stores are fast and local or on the LAN even when backed by a real DB.

Tests: `go vet ./... && go test ./...`
