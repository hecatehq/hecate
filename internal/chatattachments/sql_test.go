package chatattachments

import (
	"strings"
	"testing"
)

func TestPostgresAttachmentLockKeys(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, parts := range [][3]string{
		{"table", "total_quota", ""},
		{"table", "session", "total_quota"},
		{"table", "session", "owner"},
		{"other_table", "session", "owner"},
		{"table", "session", "other"},
		{"table", "session", "a\x00b"},
		{"table", "session", `a\x00b`},
		{"a:b", "session", "c"},
		{"a", "session", "b:c"},
	} {
		key := postgresAttachmentLockKey(parts[0], parts[1], parts[2])
		if strings.ContainsRune(key, 0) {
			t.Fatalf("key contains NUL: %q", key)
		}
		if seen[key] {
			t.Fatalf("key aliases another scope: %q", key)
		}
		seen[key] = true
		if repeated := postgresAttachmentLockKey(parts[0], parts[1], parts[2]); repeated != key {
			t.Fatalf("lock key is not stable: %q != %q", repeated, key)
		}
	}
}
