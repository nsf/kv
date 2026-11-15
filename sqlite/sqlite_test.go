package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nsf/kv"
)

// open returns a Store backed by a fresh database file in a temp directory,
// together with the *sql.DB it wraps. WAL mode requires a real file, so the
// tests do not use ":memory:".
func open(t *testing.T, table string) (*Store, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := CreateTable(db, table); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	return New(db, table), db
}

func TestStore(t *testing.T) {
	s, _ := open(t, "data")
	store := kv.Store(s)

	// Get on missing key returns ErrNotFound.
	if _, err := store.Get("missing"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("Get(missing): err = %v, want kv.ErrNotFound", err)
	}

	// Set and Get round-trip.
	if err := store.Set("greeting", []byte("hello")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get("greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("Get = %q, want %q", got, "hello")
	}

	// Overwrite.
	if err := store.Set("greeting", []byte("hi")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ = store.Get("greeting")
	if string(got) != "hi" {
		t.Fatalf("Get after overwrite = %q, want %q", got, "hi")
	}

	// Empty and binary values round-trip.
	if err := store.Set("empty", nil); err != nil {
		t.Fatalf("Set(empty): %v", err)
	}
	got, _ = store.Get("empty")
	if len(got) != 0 {
		t.Fatalf("Get(empty) = %q, want empty", got)
	}
	if err := store.Set("blob", []byte{0x00, 0xff, 0x10, 0x00}); err != nil {
		t.Fatalf("Set(blob): %v", err)
	}
	got, _ = store.Get("blob")
	if len(got) != 4 || got[1] != 0xff {
		t.Fatalf("Get(blob) = %v, want [0 255 16 0]", got)
	}

	// Keys are stored as TEXT, so arbitrary strings work.
	if err := store.Set("a b\nc\"d", []byte("x")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ = store.Get("a b\nc\"d")
	if string(got) != "x" {
		t.Fatalf("Get = %q, want %q", got, "x")
	}

	// Delete removes the key; deleting again is not an error.
	if err := store.Delete("greeting"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get("greeting"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want kv.ErrNotFound", err)
	}
	if err := store.Delete("greeting"); err != nil {
		t.Fatalf("Delete of missing key: %v", err)
	}
}

func TestNoAliasing(t *testing.T) {
	s, _ := open(t, "data")

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

// TestPersistence checks that data written through one Store is visible to a
// different Store over a newly opened connection to the same database file.
func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := CreateTable(db, "kv"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := New(db, "kv").Set("k", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	defer db2.Close()

	got, err := New(db2, "kv").Get("k")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if string(got) != "v" {
		t.Fatalf("Get after reopen = %q, want %q", got, "v")
	}
}

// TestCustomTable checks that two stores may share one database, each with its
// own table, without seeing each other's keys.
func TestCustomTable(t *testing.T) {
	s1, db := open(t, "data")
	s2 := New(db, "other_data")
	if err := CreateTable(db, "other_data"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if err := s1.Set("k", []byte("one")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s2.Set("k", []byte("two")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := s2.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "two" {
		t.Fatalf("other_data Get = %q, want %q", got, "two")
	}
	if _, err := s1.Get("missing"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("data Get(missing): err = %v, want kv.ErrNotFound", err)
	}
}

// TestCreateTableIdempotent checks that creating the same table twice is fine
// and that WAL journal mode is enabled.
func TestCreateTableIdempotent(t *testing.T) {
	s, db := open(t, "data")
	if err := CreateTable(db, "data"); err != nil {
		t.Fatalf("second CreateTable: %v", err)
	}
	if err := s.Set("k", []byte("v")); err != nil {
		t.Fatalf("Set after second CreateTable: %v", err)
	}

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode;`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestInvalidTable(t *testing.T) {
	s, db := open(t, "data")

	for _, table := range []string{"", "data; DROP TABLE other", "1data", `da"ta`, "data-1"} {
		if err := CreateTable(db, table); !errors.Is(err, ErrInvalidTable) {
			t.Fatalf("CreateTable(%q): err = %v, want ErrInvalidTable", table, err)
		}

		bad := New(db, table)
		if _, err := bad.Get("k"); !errors.Is(err, ErrInvalidTable) {
			t.Fatalf("Get on table %q: err = %v, want ErrInvalidTable", table, err)
		}
		if err := bad.Set("k", []byte("v")); !errors.Is(err, ErrInvalidTable) {
			t.Fatalf("Set on table %q: err = %v, want ErrInvalidTable", table, err)
		}
		if err := bad.Delete("k"); !errors.Is(err, ErrInvalidTable) {
			t.Fatalf("Delete on table %q: err = %v, want ErrInvalidTable", table, err)
		}
	}

	// The valid table is untouched.
	if err := s.Set("k", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v" {
		t.Fatalf("Get = %q, want %q", got, "v")
	}
}
