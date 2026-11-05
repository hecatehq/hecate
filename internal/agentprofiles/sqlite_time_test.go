package agentprofiles

import (
	"testing"
	"time"
)

func TestParseProfileTimePreservesTypedTimestamps(t *testing.T) {
	t.Parallel()
	value := time.Date(2026, 11, 5, 12, 0, 0, 123456789, time.UTC)
	for _, tc := range []struct {
		name  string
		input any
		want  time.Time
	}{
		{"sqlite unset", "", time.Time{}},
		{"sqlite whitespace", " \t", time.Time{}},
		{"sqlite text", "2026-11-05T14:00:00.123456789+02:00", value},
		{"postgres typed", value.In(time.FixedZone("offset", 7244)), value},
		{"postgres zero Madrid offset", time.Time{}.In(time.FixedZone("historical Madrid", -884)), time.Time{}},
		{"postgres zero positive offset", time.Time{}.In(time.FixedZone("historical", 44)), time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProfileTime(tc.input)
			if err != nil || !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("parseProfileTime(%v) = %v, %v; want UTC %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, invalid := range []any{"not-a-timestamp", 42, nil} {
		if _, err := parseProfileTime(invalid); err == nil {
			t.Fatalf("parseProfileTime(%v) accepted invalid timestamp", invalid)
		}
	}
}

func TestSQLiteStore_RejectsMalformedTimestamps(t *testing.T) {
	t.Parallel()
	for _, column := range []string{"created_at", "updated_at"} {
		t.Run(column, func(t *testing.T) {
			store := newSQLiteProfileTestStore(t)
			ctx := t.Context()
			if _, err := store.Create(ctx, Profile{ID: "invalid-time", Name: "Invalid time"}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.client.DB().ExecContext(ctx, "UPDATE "+store.table+" SET "+column+" = ? WHERE id = ?", "not-a-timestamp", "invalid-time"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Get(ctx, "invalid-time"); err == nil {
				t.Fatal("Get accepted a malformed timestamp")
			}
			if _, err := store.List(ctx); err == nil {
				t.Fatal("List accepted a malformed timestamp")
			}
			if _, err := store.Update(ctx, "invalid-time", nil); err == nil {
				t.Fatal("Update accepted a malformed timestamp")
			}
		})
	}
}
