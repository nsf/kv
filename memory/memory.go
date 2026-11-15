// Package memory provides an in-memory implementation of the kv.Store interface.
package memory

import (
	"slices"
	"sync"

	"github.com/nsf/kv"
)

// Store is a thread-safe, in-memory kv.Store backed by a map.
// Data does not persist and lives only for the lifetime of the process.
type Store struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// New returns an initialized in-memory Store.
func New() *Store {
	return &Store{data: make(map[string][]byte)}
}

// Get retrieves a copy of the value associated with the given key.
// It returns kv.ErrNotFound if the key does not exist.
func (s *Store) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.data[key]
	if !ok {
		return nil, kv.ErrNotFound
	}
	// Return a copy so callers cannot mutate stored data.
	return slices.Clone(v), nil
}

// Set stores a copy of the given value, overwriting any existing value for the key.
func (s *Store) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Copy so later caller mutations of value don't affect stored data.
	s.data[key] = slices.Clone(value)
	return nil
}

// Delete removes the given key. Deleting a key that does not exist is not an error.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)
	return nil
}

// Len returns the number of entries currently stored.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.data)
}

// Ensure Store implements kv.Store at compile time.
var _ kv.Store = (*Store)(nil)
