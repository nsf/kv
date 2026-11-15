package kv

import (
	"errors"
	"testing"
)

// memStore is a minimal Store used to test the package-level helpers. It does
// not implement Transactional.
type memStore struct {
	m map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{m: map[string][]byte{}}
}

func (s *memStore) Get(key string) ([]byte, error) {
	v, ok := s.m[key]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}

func (s *memStore) Set(key string, value []byte) error {
	s.m[key] = value
	return nil
}

func (s *memStore) Delete(key string) error {
	delete(s.m, key)
	return nil
}

// txMemStore is a memStore that also implements Transactional by buffering
// the transaction's writes and applying them on commit.
type txMemStore struct {
	memStore
}

func (s *txMemStore) Transact(fn func(tx Store) error) error {
	t := &txView{base: s, buf: map[string][]byte{}}
	if err := fn(t); err != nil {
		return err
	}
	for k, v := range t.buf {
		if v == nil {
			delete(s.m, k) // deleted within the transaction
		} else {
			s.m[k] = v
		}
	}
	return nil
}

// txView is the transaction's Store: reads check the pending buffer first,
// writes go to the buffer.
type txView struct {
	base *txMemStore
	buf  map[string][]byte // nil value means deleted
}

func (t *txView) Get(key string) ([]byte, error) {
	if v, ok := t.buf[key]; ok {
		if v == nil {
			return nil, ErrNotFound
		}
		return v, nil
	}
	return t.base.Get(key)
}

func (t *txView) Set(key string, value []byte) error {
	t.buf[key] = value
	return nil
}

func (t *txView) Delete(key string) error {
	t.buf[key] = nil
	return nil
}

// TestTransactNotSupported checks that Transact reports ErrNotSupported for a
// Store that does not implement Transactional, without calling fn.
func TestTransactNotSupported(t *testing.T) {
	s := newMemStore()
	called := false

	err := Transact(s, func(tx Store) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Transact: err = %v, want ErrNotSupported", err)
	}
	if called {
		t.Fatal("fn must not be called when transactions are unsupported")
	}

	// JSONStore.Transact goes through the same path.
	err = NewJSONStore(s).Transact(func(tx JSONStore) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("JSONStore.Transact: err = %v, want ErrNotSupported", err)
	}
	if called {
		t.Fatal("fn must not be called when transactions are unsupported")
	}
}

// TestTransact checks that the Transact function delegates to a Transactional
// store: commit on nil, rollback on error.
func TestTransact(t *testing.T) {
	s := &txMemStore{memStore: *newMemStore()}
	if err := s.Set("k", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Commit path: tx sees its own writes; the store sees them after commit.
	err := Transact(s, func(tx Store) error {
		if got, err := tx.Get("k"); err != nil || string(got) != "v" {
			t.Errorf("tx.Get(k) = %q, %v; want %q, nil", got, err, "v")
		}
		if err := tx.Set("k", []byte("new")); err != nil {
			return err
		}
		if err := tx.Delete("gone"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if got, _ := s.Get("k"); string(got) != "new" {
		t.Fatalf("after commit: k = %q, want %q", got, "new")
	}

	// Rollback path: the error is passed through and nothing is applied.
	sentinel := errors.New("boom")
	err = Transact(s, func(tx Store) error {
		if err := tx.Set("k", []byte("rolled back")); err != nil {
			return err
		}
		if err := tx.Delete("k"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Transact: err = %v, want the sentinel error", err)
	}
	if got, _ := s.Get("k"); string(got) != "new" {
		t.Fatalf("after rollback: k = %q, want %q", got, "new")
	}
}

// TestJSONStoreTransact checks that JSONStore.Transact wraps the transaction
// in a JSONStore and propagates commit, rollback, and fn's error.
func TestJSONStoreTransact(t *testing.T) {
	type value struct {
		N int `json:"n"`
	}

	s := &txMemStore{memStore: *newMemStore()}
	j := NewJSONStore(s)

	if err := j.Set("a", value{N: 1}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Commit: tx is a JSONStore and sees typed pending writes.
	err := j.Transact(func(tx JSONStore) error {
		got, err := tx.Get[value]("a")
		if err != nil {
			return err
		}
		if got.N != 1 {
			t.Errorf("tx.Get(a): N = %d, want 1", got.N)
		}
		return tx.Set("b", value{N: 2})
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	got, err := j.Get[value]("b")
	if err != nil {
		t.Fatalf("Get(b): %v", err)
	}
	if got.N != 2 {
		t.Fatalf("after commit: b.N = %d, want 2", got.N)
	}

	// Rollback on error, with the error propagated.
	sentinel := errors.New("boom")
	err = j.Transact(func(tx JSONStore) error {
		if err := tx.Set("b", value{N: 3}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Transact: err = %v, want the sentinel error", err)
	}
	got, _ = j.Get[value]("b")
	if got.N != 2 {
		t.Fatalf("after rollback: b.N = %d, want 2", got.N)
	}

	// Missing keys inside a transaction still surface as ErrNotFound.
	err = j.Transact(func(tx JSONStore) error {
		_, err := tx.Get[value]("missing")
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("tx.Get(missing): err = %v, want ErrNotFound", err)
	}
}

// Ensure the fakes implement the interfaces they are meant to exercise.
var (
	_ Store         = (*memStore)(nil)
	_ Store         = (*txMemStore)(nil)
	_ Transactional = (*txMemStore)(nil)
)
