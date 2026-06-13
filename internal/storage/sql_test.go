package storage

import "testing"

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
