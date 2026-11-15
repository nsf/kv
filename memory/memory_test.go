package memory

import (
	"errors"
	"testing"

	"github.com/nsf/kv"
)

func TestStore(t *testing.T) {
	s := kv.Store(New())

	// Get on missing key returns ErrNotFound.
	if _, err := s.Get("missing"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("Get(missing): err = %v, want kv.ErrNotFound", err)
	}

	// Set and Get round-trip.
	if err := s.Set("greeting", []byte("hello")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get("greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("Get = %q, want %q", got, "hello")
	}

	// Overwrite.
	if err := s.Set("greeting", []byte("hi")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ = s.Get("greeting")
	if string(got) != "hi" {
		t.Fatalf("Get after overwrite = %q, want %q", got, "hi")
	}

	// Delete removes the key; deleting again is not an error.
	if err := s.Delete("greeting"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("greeting"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want kv.ErrNotFound", err)
	}
	if err := s.Delete("greeting"); err != nil {
		t.Fatalf("Delete of missing key: %v", err)
	}
}

func TestNoAliasing(t *testing.T) {
	s := New()

	// Mutating the caller's slice after Set must not affect stored data.
	value := []byte("original")
	if err := s.Set("k", value); err != nil {
		t.Fatalf("Set: %v", err)
	}
	value[0] = 'X'

	// Mutating the slice returned by Get must not affect stored data either.
	got, err := s.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got[0] = 'Y'

	got, _ = s.Get("k")
	if string(got) != "original" {
		t.Fatalf("stored value = %q, want %q", got, "original")
	}
}
