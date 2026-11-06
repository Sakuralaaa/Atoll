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
	DB  *sql.DB // the one writer connection; also used for reads that must see a transaction
	Box *secrets.Box
	// r is a small pool of query-only connections for the panel's heavy reads (lists,
	// metric ranges, audit), so they no longer queue behind writes from a dozen agents.
	// WAL lets them read the last committed state while the writer works.
	r *sql.DB
}

// readers is the size of the read pool.
const readers = 4

// Open opens (and migrates) the database at path.
func Open(path string, box *secrets.Box) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLite happy.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Box: box, r: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// opened after migrate: the reader never sees a half-migrated schema
	r, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	r.SetMaxOpenConns(readers)
	r.SetMaxIdleConns(readers)
	if err := r.Ping(); err != nil {
		r.Close()
		db.Close()
		return nil, err
	}
	s.r = r
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s.r != nil && s.r != s.DB {
		s.r.Close()
	}
	return s.DB.Close()
}

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

// AuditMeta is request information Audit records with every entry (set by the API layer).
type AuditMeta struct {
	IP        string
	TokenID   string
	SessionID string // first 16 hex chars of the session hash
	// the account behind the request (session or API token owner); 0/"" = none known
	UserID   int64
	UserName string
	UserRole string // "admin" | "member"
}

type auditMetaKey struct{}

// WithAuditMeta attaches request information to ctx for Audit.
func WithAuditMeta(ctx context.Context, m AuditMeta) context.Context {
	return context.WithValue(ctx, auditMetaKey{}, m)
}

// WithAuditUser records u as the account behind ctx's audit entries (keeps the rest of the meta).
func WithAuditUser(ctx context.Context, u *User) context.Context {
	if u == nil {
		return ctx
	}
	m, _ := ctx.Value(auditMetaKey{}).(AuditMeta)
	m.UserID, m.UserName, m.UserRole = u.ID, u.Username, u.Role
	if m.UserRole == "" {
		m.UserRole = RoleAdmin
	}
	return context.WithValue(ctx, auditMetaKey{}, m)
}

// Audit appends an audit log entry; errors are ignored by design.
func (s *Store) Audit(ctx context.Context, actor, action, target string, detail any) {
	m, _ := ctx.Value(auditMetaKey{}).(AuditMeta)
	s.DB.ExecContext(ctx, `INSERT INTO audit_log(actor, action, target, detail, created_at, ip, token_id, session_id, user_id, user_name, user_role) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		actor, action, target, mustJSON(detail), TS(time.Now()), m.IP, m.TokenID, m.SessionID, m.UserID, m.UserName, m.UserRole)
}

// AuditRow is one audit_log row.
type AuditRow struct {
	ID        int64
	Actor     string
	Action    string
	Target    string
	Detail    string
	IP        string
	TokenID   string
	SessionID string
	UserID    int64
	UserName  string
	UserRole  string
	CreatedAt time.Time
}

// AuditFilter selects audit rows: newest first, ids below Before (0 = from the newest).
// Actor matches exactly or as a prefix ending in ':' ("token:" = every token); Action matches
// exactly or, ending in '.' or '*', as a prefix ("control." = every control action).
type AuditFilter struct {
	Limit  int
	Before int64
	Actor  string
	Action string
	// User matches the account behind an entry by name: user_name, or for rows written
	// before it was recorded, actor "user:<name>".
	User string
}

// ListAudit returns audit rows matching f.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	q := `SELECT id, actor, action, target, detail, ip, token_id, session_id, user_id, user_name, user_role, created_at FROM audit_log WHERE 1=1`
	args := []any{}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	likeEsc := func(v string) string {
		return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
	}
	if a := f.Actor; a != "" {
		if strings.HasSuffix(a, ":") {
			q += ` AND actor LIKE ? ESCAPE '\'`
			args = append(args, likeEsc(a)+"%")
		} else {
			q += ` AND actor = ?`
			args = append(args, a)
		}
	}
	if a := f.Action; a != "" {
		if strings.HasSuffix(a, "*") || strings.HasSuffix(a, ".") {
			q += ` AND action LIKE ? ESCAPE '\'`
			args = append(args, likeEsc(strings.TrimSuffix(a, "*"))+"%")
		} else {
			q += ` AND action = ?`
			args = append(args, a)
		}
	}
	if u := f.User; u != "" {
		q += ` AND (user_name = ? OR (user_name = '' AND actor = ?))`
		args = append(args, u, "user:"+u)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var r AuditRow
		var c string
		if err := rows.Scan(&r.ID, &r.Actor, &r.Action, &r.Target, &r.Detail, &r.IP, &r.TokenID, &r.SessionID, &r.UserID, &r.UserName, &r.UserRole, &c); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTS(c)
		out = append(out, r)
	}
	return out, rows.Err()
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
