package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	// modernc.org/sqlite is the SQLite C source machine-translated to
	// pure Go via ccgo, so the gateway stays a single static binary
	// without CGO. The trade-off: it cannot load native SQLite
	// extensions (sqlite-vec, FTS5 fuzzy variants, etc.) because there
	// is no native engine to load against.
	_ "modernc.org/sqlite"
)

// identifierPattern matches characters that aren't allowed in a
// SQLite identifier we'll splice into a CREATE TABLE statement.
// We never substitute callers' identifiers via parameter binding —
// SQL doesn't allow that — so the only safe move is to scrub them
// to a known-good charset before formatting.
var identifierPattern = regexp.MustCompile(`[^a-z0-9_]+`)

// sanitizeIdentifier lowercases value, replaces non-alphanumeric runs
// with underscores, trims edge underscores, and returns the fallback
// if nothing usable remains. Used to build table prefixes and table
// names from operator-supplied config without inviting SQL injection.
func sanitizeIdentifier(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = identifierPattern.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return fallback
	}
	return value
}

// SQLiteConfig captures the on-disk path and connection knobs the
// SQLite-backed stores share. We keep TablePrefix here so multiple
// gateways pointing at the same file (rare, but supported in tests)
// don't collide. The driver name is "sqlite" — we use modernc.org/sqlite,
// the pure-Go translation of the SQLite C amalgamation, so the gateway
// stays a single static binary with no CGO requirement.
type SQLiteConfig struct {
	Path        string
	TablePrefix string
	BusyTimeout time.Duration
}

// SQLiteClient exposes the shared Close/DB/QualifiedTable/TableName
// surface used by durable subsystem stores. SQLite has no schemas, so QualifiedTable
// just returns the prefixed table name with no schema namespace.
type SQLiteClient struct {
	db          *sql.DB
	tablePrefix string
}

// NewSQLiteClient opens (and creates if missing) the SQLite database
// at cfg.Path. The parent directory is auto-created — operators expect
// `--sqlite-path .data/hecate.db` to Just Work without `mkdir -p` first.
//
// SQLite-specific tuning we apply on every connection:
//   - WAL journal mode: lets readers and one writer proceed concurrently
//     (default rollback journal blocks readers during writes — death for
//     a request-handling gateway).
//   - busy_timeout: SQLite's default behavior on a locked database is to
//     fail immediately; with WAL the lock window is short, but we still
//     need a timeout > 0 so concurrent transactions wait instead of
//     erroring out.
//   - foreign_keys ON: SQLite ships with FKs disabled by default, which
//     is a footgun for persisted relational state.
func NewSQLiteClient(ctx context.Context, cfg SQLiteConfig) (*SQLiteClient, error) {
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := ensureSQLiteDirectory(dir); err != nil {
			return nil, err
		}
	}
	if err := ensurePrivateSQLiteFile(path); err != nil {
		return nil, err
	}

	busyMs := int64(5000)
	if cfg.BusyTimeout > 0 {
		busyMs = cfg.BusyTimeout.Milliseconds()
	}

	// _pragma= URL params are how modernc.org/sqlite accepts pragmas at
	// connection open time. Applied to every connection in the pool.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)",
		path, busyMs,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite tolerates many readers but only one writer at a time. A
	// large open-conn pool just means more goroutines fighting for the
	// write lock. Cap to a small number; the actual concurrency comes
	// from WAL letting reads pass through.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure sqlite file %q: %w", path, err)
	}

	return &SQLiteClient{
		db:          db,
		tablePrefix: sanitizeIdentifier(cfg.TablePrefix, "hecate"),
	}, nil
}

func ensureSQLiteDirectory(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat sqlite directory %q: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sqlite directory %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure sqlite directory %q: %w", dir, err)
	}
	return nil
}

func ensurePrivateSQLiteFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create sqlite file %q: %w", path, err)
	}
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("close sqlite file %q: %w", path, closeErr)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure sqlite file %q: %w", path, err)
	}
	return nil
}

func (c *SQLiteClient) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *SQLiteClient) DB() *sql.DB {
	if c == nil {
		return nil
	}
	return c.db
}

// QualifiedTable returns the fully-qualified table name. SQLite has no
// schema namespacing, so this is just the prefixed table name wrapped
// in double quotes for safety against reserved-word collisions.
func (c *SQLiteClient) QualifiedTable(name string) string {
	return fmt.Sprintf(`"%s"`, c.TableName(name))
}

func (c *SQLiteClient) TableName(name string) string {
	base := sanitizeIdentifier(name, "table")
	if c.tablePrefix == "" {
		return base
	}
	return c.tablePrefix + "_" + base
}

// ClearData deletes rows from every Hecate-prefixed application table while
// preserving the schema, so a running gateway can start fresh without a
// relaunch and without rerunning migrations.
func (c *SQLiteClient) ClearData(ctx context.Context) (int, error) {
	if c == nil || c.db == nil {
		return 0, nil
	}
	prefix := c.tablePrefix + "_"
	rows, err := c.db.QueryContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name LIKE ?
	`, prefix+"%")
	if err != nil {
		return 0, fmt.Errorf("list sqlite tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, fmt.Errorf("scan sqlite table: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("list sqlite tables: %w", err)
	}
	sort.Strings(tables)
	if len(tables) == 0 {
		return 0, nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin sqlite clear: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := 0
	for _, table := range tables {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+quoteSQLiteIdentifier(table))
		if err != nil {
			return deleted, fmt.Errorf("clear sqlite table %q: %w", table, err)
		}
		if n, err := result.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return deleted, fmt.Errorf("commit sqlite clear: %w", err)
	}
	return deleted, nil
}

func quoteSQLiteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
