// Package store is the persistence layer: SQLite today, portable SQL so PostgreSQL
// can follow. Every function returns fully materialised results (no open cursors),
// which keeps the single-connection pool safe.
package store

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// ErrNotFound is returned when a single-row lookup finds nothing.
var ErrNotFound = errors.New("not found")

// Q is the subset of *sql.DB / *sql.Tx the store uses.
type Q interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// DB wraps a SQL connection (or a transaction inside WithTx).
type DB struct {
	sql *sql.DB
	q   Q
}

// Open opens (creating if needed) the SQLite database at path and applies the schema.
// Use ":memory:" for tests.
func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	} else {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		dsn += "&_pragma=journal_mode(WAL)"
	}
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// ponytail: one connection serialises all writes; SQLite is fast enough for the demo.
	// Raise when moving to PostgreSQL.
	sdb.SetMaxOpenConns(1)
	sdb.SetConnMaxLifetime(0)
	db := &DB{sql: sdb, q: sdb}
	if _, err := sdb.Exec(schemaSQL); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return db, nil
}

// Close closes the underlying connection.
func (s *DB) Close() error { return s.sql.Close() }

// Raw exposes the query interface for ad-hoc reads (analytics, tests).
func (s *DB) Raw() Q { return s.q }

// WithTx runs fn inside a transaction; the DB passed to fn must be used for all queries.
func (s *DB) WithTx(fn func(tx *DB) error) error {
	if _, isTx := s.q.(*sql.Tx); isTx {
		return fn(s)
	}
	tx, err := s.sql.Begin()
	if err != nil {
		return err
	}
	if err := fn(&DB{sql: s.sql, q: tx}); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Exec runs a statement.
func (s *DB) Exec(query string, args ...any) error {
	_, err := s.q.Exec(query, args...)
	return err
}

// Count returns a single integer from a COUNT-style query.
func (s *DB) Count(query string, args ...any) (int, error) {
	var n int
	err := s.q.QueryRow(query, args...).Scan(&n)
	return n, err
}

// Pairs runs a two-column (TEXT, INTEGER) query and returns a map.
func (s *DB) Pairs(query string, args ...any) (map[string]int, error) {
	rows, err := s.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k sql.NullString
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k.String] = v
	}
	return out, rows.Err()
}

// Strings runs a single-column TEXT query.
func (s *DB) Strings(query string, args ...any) ([]string, error) {
	rows, err := s.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func all[T any](q Q, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func one[T any](q Q, scan func(scanner) (T, error), query string, args ...any) (T, error) {
	var zero T
	items, err := all(q, scan, query+" LIMIT 1", args...)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, ErrNotFound
	}
	return items[0], nil
}

// NewID returns a random 16-hex-char identifier.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Now returns the current UTC time in the canonical timestamp format.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// FormatTime formats a time in the canonical timestamp format.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ParseTime parses a canonical timestamp (or a date) leniently.
func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// JSON helpers ---------------------------------------------------------------

func js(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func unjs[T any](s string) T {
	var v T
	if s != "" {
		_ = json.Unmarshal([]byte(s), &v)
	}
	return v
}

func strs(s string) []string {
	v := unjs[[]string](s)
	if v == nil {
		v = []string{}
	}
	return v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Embedding blobs -----------------------------------------------------------

// EncodeVec serialises a float32 vector as little-endian bytes.
func EncodeVec(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// DecodeVec parses a little-endian float32 vector.
func DecodeVec(b []byte) []float32 {
	if len(b) < 4 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
