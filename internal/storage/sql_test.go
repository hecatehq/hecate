package storage

import (
	"strings"
	"testing"
	"time"
)

func TestTimestampValue(t *testing.T) {
	t.Parallel()
	value := time.Date(2026, 10, 26, 12, 0, 0, 123456789, time.FixedZone("test", 2*60*60))
	for _, tc := range []struct {
		name   string
		client SQLClient
		value  time.Time
		want   any
	}{
		{"sqlite unset", &SQLiteClient{}, time.Time{}, ""},
		{"sqlite timestamp", &SQLiteClient{}, value, "2026-10-26T10:00:00.123456789Z"},
		{"postgres unset", &PostgresClient{}, time.Time{}, time.Time{}},
		{"postgres timestamp", &PostgresClient{}, value, value.UTC()},
		{"default unset", nil, time.Time{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TimestampValue(tc.client, tc.value); got != tc.want {
				t.Fatalf("TimestampValue() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestBoundedIdentifierPreservesDistinctLongPurposes(t *testing.T) {
	t.Parallel()
	base := strings.Repeat("long_prefix_", 8)
	insertName := BoundedIdentifier(base + "workspace_owner_insert")
	updateName := BoundedIdentifier(base + "workspace_owner_update")
	for _, name := range []string{insertName, updateName} {
		if len(name) > sqlIdentifierMaxBytes {
			t.Fatalf("BoundedIdentifier() length = %d, want <= %d: %q", len(name), sqlIdentifierMaxBytes, name)
		}
	}
	if insertName == updateName {
		t.Fatalf("long derived identifiers collided: %q", insertName)
	}
}

func TestRebindPostgresPlaceholders(t *testing.T) {
	query := `SELECT '?' AS literal, "weird?name" FROM "table" WHERE a = ? AND b = ? AND c = 'it''s ?'`

	got := Rebind(query, DialectPostgres)
	want := `SELECT '?' AS literal, "weird?name" FROM "table" WHERE a = $1 AND b = $2 AND c = 'it''s ?'`
	if got != want {
		t.Fatalf("Rebind() = %q, want %q", got, want)
	}
}

func TestRebindSQLiteLeavesQuestionPlaceholders(t *testing.T) {
	query := `SELECT * FROM "table" WHERE a = ? AND b = ?`

	if got := Rebind(query, DialectSQLite); got != query {
		t.Fatalf("Rebind(sqlite) = %q, want original query", got)
	}
}
