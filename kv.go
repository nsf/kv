// Package kv defines a generic key/value database interface.
package kv

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotFound is returned by Get when the requested key does not exist.
// Implementations should return it (directly or wrapped via errors.Is) so
// callers can distinguish a missing key from a genuine read error.
var ErrNotFound = errors.New("kv: key not found")

// ErrNotSupported is returned when an operation is not supported by a Store
// implementation (possibly wrapped; check with errors.Is). It is reported by
// the Transact function when the given Store does not implement
// Transactional.
var ErrNotSupported = errors.New("kv: not supported")

// Store is the interface implemented by key/value databases.
type Store interface {
	// Get retrieves the value associated with the given key.
	// It returns ErrNotFound (possibly wrapped) if the key does not exist.
	Get(key string) ([]byte, error)

	// Set associates the given value with the given key.
	// It overwrites any existing value for that key.
	Set(key string, value []byte) error

	// Delete removes the given key and its associated value.
	// Deleting a key that does not exist is not an error.
	Delete(key string) error
}

// Transactional is implemented by backends that can run a unit of work
// atomically. It is an optional extension of Store, deliberately kept out of
// the Store interface itself so simple backends need not implement it.
type Transactional interface {
	// Transact runs fn inside a transaction. The writes made through tx are
	// committed if fn returns nil and rolled back if it returns an error or
	// panics (the panic propagates to the caller after the rollback).
	//
	// tx reads observe the store as of the start of the transaction plus any
	// writes made through tx during fn; those writes are not visible to other
	// callers until the commit. tx has the same semantics as Store otherwise
	// (ErrNotFound on missing keys, idempotent Delete, and so on).
	//
	// tx must not be used after Transact returns; doing so returns an error.
	// Transactions must not be nested: calling Transact (or Transact on a
	// JSONStore wrapping tx) from within fn returns an error.
	Transact(fn func(tx Store) error) error
}

// Transact runs fn inside a transaction on s. If s does not implement
// Transactional, it returns ErrNotSupported (wrapped; check with errors.Is)
// without calling fn; otherwise it returns exactly what s.Transact returns.
func Transact(s Store, fn func(tx Store) error) error {
	t, ok := s.(Transactional)
	if !ok {
		return fmt.Errorf("kv: %T does not support transactions: %w", s, ErrNotSupported)
	}
	return t.Transact(fn)
}

// JSONStore wraps a Store and adds JSON encoding/decoding on top of it, so
// callers can Set and Get typed values instead of raw []byte. It performs no
// copying or caching of its own; the underlying Store's semantics (including
// any defensive-copy and ErrNotFound behavior) are preserved.
type JSONStore struct {
	s Store
}

// NewJSONStore returns a JSONStore that stores JSON-encoded values in the
// given Store. The Store is used as-is; JSONStore never closes or mutates it.
func NewJSONStore(s Store) JSONStore {
	return JSONStore{s: s}
}

// Get retrieves the value stored under the given key and unmarshals its JSON
// representation into a T. It returns the zero value of T and the underlying
// error on failure; a missing key surfaces as ErrNotFound (possibly wrapped),
// and malformed JSON (or a value incompatible with T) is returned as a
// *json.UnmarshalError from encoding/json.
func (j JSONStore) Get[T any](key string) (T, error) {
	var out T
	data, err := j.s.Get(key)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

// Set marshals value as JSON and stores the resulting bytes under the given
// key, overwriting any existing value. If value cannot be marshaled (e.g. it
// contains a channel, func, or unencodable type), the underlying Store is not
// touched and the *json.MarshalError from encoding/json is returned.
func (j JSONStore) Set[T any](key string, value T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return j.s.Set(key, data)
}

// Delete removes the given key and its associated value, delegating directly
// to the underlying Store. As with Store.Delete, deleting a key that does not
// exist is not an error.
func (j JSONStore) Delete(key string) error {
	return j.s.Delete(key)
}

// Transact runs fn inside a transaction on the underlying Store, wrapping the
// transaction's Store in a JSONStore so fn can Set and Get typed JSON values
// atomically. It delegates to the Transact function, so a Store that does not
// implement Transactional reports ErrNotSupported without calling fn; fn's
// return value (and the transaction's commit or rollback) is propagated as-is.
func (j JSONStore) Transact(fn func(tx JSONStore) error) error {
	return Transact(j.s, func(tx Store) error {
		return fn(JSONStore{s: tx})
	})
}
