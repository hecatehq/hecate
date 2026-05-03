package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteClient_OpensInTempDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hecate.db")

	client, err := NewSQLiteClient(context.Background(), SQLiteConfig{
		Path:        path,
		TablePrefix: "test",
		BusyTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if got := client.QualifiedTable("foo"); got != `"test_foo"` {
		t.Fatalf("QualifiedTable: got %q, want %q", got, `"test_foo"`)
	}
	if got := client.TableName("foo-bar"); got != "test_foo_bar" {
		t.Fatalf("TableName: got %q, want %q", got, "test_foo_bar")
	}

	// Confirm WAL mode actually applied — the readiness contract for
	// concurrent reads + one writer hinges on this. A regression here
	// (e.g. dropping the _pragma= URL params) would let writes block
	// reads in production without surfacing in functional tests.
	var mode string
	if err := client.DB().QueryRowContext(context.Background(), "PRAGMA journal_mode;").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode: got %q, want %q", mode, "wal")
	}

	// Round-trip a real CREATE/INSERT/SELECT to confirm the connection
	// actually writes to disk and that table-prefix sanitization is
	// safe to substitute into a CREATE statement.
	ctx := context.Background()
	if _, err := client.DB().ExecContext(ctx, `CREATE TABLE `+client.QualifiedTable("scratch")+` (k TEXT PRIMARY KEY, v INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx, `INSERT INTO `+client.QualifiedTable("scratch")+` (k, v) VALUES (?, ?)`, "answer", 42); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var v int
	if err := client.DB().QueryRowContext(ctx, `SELECT v FROM `+client.QualifiedTable("scratch")+` WHERE k = ?`, "answer").Scan(&v); err != nil {
		t.Fatalf("select: %v", err)
	}
	if v != 42 {
		t.Fatalf("round-trip value: got %d, want 42", v)
	}
}

func TestSQLiteClient_RejectsEmptyPath(t *testing.T) {
	_, err := NewSQLiteClient(context.Background(), SQLiteConfig{Path: ""})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback string
		want     string
	}{
		{
			name:     "hyphen replacement",
			value:    "task-runs",
			fallback: "fallback",
			want:     "task_runs",
		},
		{
			name:     "unsupported characters",
			value:    `Tenant"; DROP TABLE tasks; --`,
			fallback: "fallback",
			want:     "tenant_drop_table_tasks",
		},
		{
			name:     "empty input fallback",
			value:    " !!! ",
			fallback: "safe_default",
			want:     "safe_default",
		},
		{
			name:     "trims edge underscores",
			value:    "__hecate__",
			fallback: "fallback",
			want:     "hecate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeIdentifier(tt.value, tt.fallback); got != tt.want {
				t.Fatalf("sanitizeIdentifier(%q, %q) = %q, want %q", tt.value, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestSQLiteClient_NilSafe(t *testing.T) {
	// Stores hold *SQLiteClient pointers; a nil pointer (e.g. when no
	// SQLite-backed subsystem is configured) must not panic on Close()
	// or DB().
	var c *SQLiteClient
	if err := c.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
	if db := c.DB(); db != nil {
		t.Fatalf("nil DB: got %v, want nil", db)
	}
}
