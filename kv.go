// Package kv defines a generic key/value database interface.
package kv

import (
	"encoding/json"
	"errors"
)

// ErrNotFound is returned by Get when the requested key does not exist.
// Implementations should return it (directly or wrapped via errors.Is) so
// callers can distinguish a missing key from a genuine read error.
var ErrNotFound = errors.New("kv: key not found")

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
