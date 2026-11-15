// Package sqlite provides a persistent implementation of the kv.Store
// interface backed by SQLite (github.com/mattn/go-sqlite3).
//
// Every store reads and writes a single table of the form
//
//	CREATE TABLE IF NOT EXISTS data (
//		key TEXT NOT NULL PRIMARY KEY,
//		value BLOB NOT NULL
//	) STRICT;
//
// whose name may be customized. Use CreateTable to create it.
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/nsf/kv"

	// Register the "sqlite3" database driver.
	_ "github.com/mattn/go-sqlite3"
)

// ErrInvalidTable is reported when a table name is not a plain SQL identifier.
// Table names cannot be passed as query parameters, so they are validated
// rather than escaped; anything else would be an injection risk.
var ErrInvalidTable = errors.New("sqlite: invalid table name")

// ident matches the table names this package is willing to interpolate.
var ident = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CreateTable creates the key/value table (named table) if it does not already
// exist, along with its primary key index, and switches the database to WAL
// journal mode. It is safe to call repeatedly.
func CreateTable(db *sql.DB, table string) error {
	if err := checkTable(table); err != nil {
		return err
	}

	if _, err := db.Exec(fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %q (
	key TEXT NOT NULL PRIMARY KEY,
	value BLOB NOT NULL
) STRICT;`, table)); err != nil {
		return fmt.Errorf("sqlite: create table %q: %w", table, err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		return fmt.Errorf("sqlite: enable WAL: %w", err)
	}

	return nil
}

// queries holds the SQL statements of a Store, formatted once for a specific
// table name (identifiers cannot be bound as parameters, so the name has to be
// interpolated; it is validated first by checkTable).
type queries struct {
	selectSQL string
	upsertSQL string
	deleteSQL string
}

// Store is a kv.Store persisted in a SQLite table.
//
// A Store holds no state of its own beyond the connection handle and the SQL
// statements derived from the table name, so the underlying *sql.DB is owned by
// the caller: closing it is the caller's job, and any number of Stores may
// share it. The *sql.DB is used directly for every operation, so a Store is
// safe for concurrent use as long as the database it wraps is (the preformatted
// statements are read-only). The zero value is not usable.
type Store struct {
	db  *sql.DB
	q   queries
	err error
}

// New returns a Store reading and writing the table named table in db. The
// table must already exist; call CreateTable to create it.
//
// The table name is validated and the SQL statements are formatted once here,
// so the hot path does no formatting. If table is not a plain SQL identifier,
// the returned Store reports ErrInvalidTable from every method.
func New(db *sql.DB, table string) *Store {
	s := &Store{db: db}
	if err := checkTable(table); err != nil {
		s.err = err
		return s
	}

	s.q = queries{
		selectSQL: fmt.Sprintf(`SELECT value FROM %q WHERE key = ?`, table),
		upsertSQL: fmt.Sprintf(`
INSERT INTO %q (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, table),
		deleteSQL: fmt.Sprintf(`DELETE FROM %q WHERE key = ?`, table),
	}
	return s
}

// Get retrieves the value associated with the given key.
// It returns kv.ErrNotFound if the key does not exist.
func (s *Store) Get(key string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}

	var value []byte
	row := s.db.QueryRow(s.q.selectSQL, key)
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, kv.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: get %q: %w", key, err)
	}

	// The driver returns a fresh []byte for each BLOB it scans, so the value
	// is already unaliased from any other copy of the data.
	return value, nil
}

// Set stores the given value, overwriting any existing value for the key.
// A nil value is stored as an empty blob, since the value column is NOT NULL;
// Get then returns an empty, non-nil slice.
func (s *Store) Set(key string, value []byte) error {
	if s.err != nil {
		return s.err
	}
	if value == nil {
		value = []byte{}
	}

	_, err := s.db.Exec(s.q.upsertSQL, key, value)
	if err != nil {
		return fmt.Errorf("sqlite: set %q: %w", key, err)
	}

	return nil
}

// Delete removes the given key. Deleting a key that does not exist is not an error.
func (s *Store) Delete(key string) error {
	if s.err != nil {
		return s.err
	}

	if _, err := s.db.Exec(s.q.deleteSQL, key); err != nil {
		return fmt.Errorf("sqlite: delete %q: %w", key, err)
	}

	return nil
}

// Transact runs fn inside a SQLite transaction: the writes made through tx
// are committed if fn returns nil and rolled back if it returns an error or
// panics (the panic propagates after the rollback). See kv.Transactional for
// the visibility rules of tx.
//
// Transactions must not be nested: the tx passed to fn does not implement
// kv.Transactional, so kv.Transact(tx, ...) reports kv.ErrNotSupported.
// Using tx after Transact returns reports an error (the underlying
// *sql.Tx is done).
//
// The transaction is started in SQLite's default DEFERRED mode, so a
// read-modify-write transaction can still fail with SQLITE_BUSY if another
// writer commits in between; callers with concurrent writers may want to add
// _txlock=immediate to their DSN.
func (s *Store) Transact(fn func(tx kv.Store) error) error {
	if s.err != nil {
		return s.err
	}

	sqlTx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin transaction: %w", err)
	}

	// Roll back unless the commit below succeeds, so both fn errors and
	// panics leave the database unchanged.
	committed := false
	defer func() {
		if !committed {
			sqlTx.Rollback() //nolint:errcheck // best-effort; the tx is being abandoned anyway
		}
	}()

	if err := fn(&txStore{tx: sqlTx, q: s.q}); err != nil {
		return err
	}

	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit transaction: %w", err)
	}
	committed = true

	return nil
}

// txStore is the kv.Store view of an open transaction handed to Transact's
// callback. It runs the same preformatted statements as its parent Store on
// the transaction's connection, so all writes are atomic with respect to
// other callers. It deliberately does NOT implement kv.Transactional, which
// makes nested transactions fail with kv.ErrNotSupported.
type txStore struct {
	tx *sql.Tx
	q  queries
}

// Get retrieves the value associated with the given key, observing any
// uncommitted writes made through this transaction.
// It returns kv.ErrNotFound if the key does not exist.
func (t *txStore) Get(key string) ([]byte, error) {
	var value []byte
	row := t.tx.QueryRow(t.q.selectSQL, key)
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, kv.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: tx get %q: %w", key, err)
	}

	// The driver returns a fresh []byte for each BLOB it scans, so the value
	// is already unaliased from any other copy of the data.
	return value, nil
}

// Set stores the given value within the transaction, overwriting any existing
// value for the key (including one written earlier in the same transaction).
// As with Store.Set, a nil value is stored as an empty blob.
func (t *txStore) Set(key string, value []byte) error {
	if value == nil {
		value = []byte{}
	}

	if _, err := t.tx.Exec(t.q.upsertSQL, key, value); err != nil {
		return fmt.Errorf("sqlite: tx set %q: %w", key, err)
	}
	return nil
}

// Delete removes the given key within the transaction. Deleting a key that
// does not exist is not an error.
func (t *txStore) Delete(key string) error {
	if _, err := t.tx.Exec(t.q.deleteSQL, key); err != nil {
		return fmt.Errorf("sqlite: tx delete %q: %w", key, err)
	}
	return nil
}

// checkTable reports whether table is a safe, plain SQL identifier.
func checkTable(table string) error {
	if !ident.MatchString(table) {
		return fmt.Errorf("%w: %q", ErrInvalidTable, table)
	}
	return nil
}

// Ensure Store implements kv.Store and kv.Transactional, and that the
// transaction view implements kv.Store, at compile time.
var (
	_ kv.Store         = (*Store)(nil)
	_ kv.Transactional = (*Store)(nil)
	_ kv.Store         = (*txStore)(nil)
)
