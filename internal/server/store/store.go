// Package store is atoll-server's SQLite persistence (modernc.org/sqlite, WAL,
// foreign keys on). Methods return api types; secrets are encrypted with the
// master key before they touch disk.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/server/secrets"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on unique constraint violations.
var ErrConflict = errors.New("already exists")

// Store wraps the database.
type Store struct {
	DB  *sql.DB
	Box *secrets.Box
}

// Open opens (and migrates) the database at path.
func Open(path string, box *secrets.Box) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLite happy; reads are fast enough for a control plane.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Box: box}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		var x string
		err := s.DB.QueryRow(`SELECT name FROM schema_migrations WHERE name=?`, n).Scan(&x)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := migrationFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(name, applied_at) VALUES(?,?)`, n, TS(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// TS formats a time for storage.
func TS(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func parseTSPtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTS(ns.String)
	return &t
}

func nullTS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return TS(*t)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

// Audit appends an audit log entry; errors are ignored by design.
func (s *Store) Audit(ctx context.Context, actor, action, target string, detail any) {
	s.DB.ExecContext(ctx, `INSERT INTO audit_log(actor, action, target, detail, created_at) VALUES(?,?,?,?,?)`,
		actor, action, target, mustJSON(detail), TS(time.Now()))
}

// encryptJSON marshals v and encrypts it.
func (s *Store) encryptJSON(v any) (string, error) {
	return s.Box.Encrypt(mustJSON(v))
}

func (s *Store) decryptJSON(c string, v any) error {
	p, err := s.Box.Decrypt(c)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(p), v)
}
