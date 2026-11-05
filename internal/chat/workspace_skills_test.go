package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestWorkspaceSkillsSurviveSQLiteReopen(t *testing.T) {
	config := storage.SQLiteConfig{Path: filepath.Join(t.TempDir(), "chat.db"), TablePrefix: "skills"}
	client, err := storage.NewSQLiteClient(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(t.Context(), client)
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	runStoreWorkspaceSkillsRoundTrip(t, store)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	client, err = storage.NewSQLiteClient(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	store, err = NewSQLiteStore(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	session, found, err := store.Get(t.Context(), "skills")
	if err != nil || !found || len(session.WorkspaceSkills) != 1 || session.WorkspaceSkills[0].Body != "private" {
		t.Fatalf("snapshot missing after reopen: found=%v err=%v", found, err)
	}
}

func TestUnmarshalWorkspaceSkillsRejectsCorruptionPrivately(t *testing.T) {
	for _, raw := range []string{`[{"body": "private source"`, `"private source"`} {
		_, err := unmarshalWorkspaceSkills(raw)
		if err == nil || strings.Contains(err.Error(), "private source") {
			t.Fatalf("expected safe corruption error, got %v", err)
		}
	}
}

func runStoreWorkspaceSkillsRoundTrip(t *testing.T, store Store) {
	t.Helper()
	input := []types.WorkspaceSkillSnapshot{{WorkspaceSkillInfo: types.WorkspaceSkillInfo{Path: ".agents/skills/review/SKILL.md", Name: "review", SHA256: "digest", Bytes: 7}, Body: "private"}}
	created, err := store.Create(t.Context(), Session{ID: "skills", AgentID: DefaultAgentID, WorkspaceSkills: input})
	if err != nil {
		t.Fatal(err)
	}
	input[0].Body = "caller mutation"
	created.WorkspaceSkills[0].Body = "result mutation"
	stored, found, err := store.Get(t.Context(), "skills")
	if err != nil || !found || len(stored.WorkspaceSkills) != 1 || stored.WorkspaceSkills[0].Body != "private" {
		t.Fatalf("snapshot lost: found=%v err=%v", found, err)
	}
	if _, err := store.UpdateSession(t.Context(), "skills", func(session *Session) { session.Title = "renamed" }); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(t.Context())
	if err != nil || len(listed) != 1 || listed[0].WorkspaceSkills[0].Body != "private" {
		t.Fatalf("list snapshot lost: %v", err)
	}
}
