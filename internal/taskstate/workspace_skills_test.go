package taskstate

import (
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func runStoreWorkspaceSkillsRoundTrip(t *testing.T, store Store) {
	t.Helper()
	task := types.Task{ID: "skill-task", WorkspaceSkills: []types.WorkspaceSkillSnapshot{{WorkspaceSkillInfo: types.WorkspaceSkillInfo{Path: ".agents/skills/review/SKILL.md", Name: "review", SHA256: "digest", Bytes: 7}, Body: "private"}}}
	if _, err := store.CreateTask(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	task.WorkspaceSkills[0].Body = "caller mutation"
	stored, found, err := store.GetTask(t.Context(), task.ID)
	if err != nil || !found || len(stored.WorkspaceSkills) != 1 || stored.WorkspaceSkills[0].Body != "private" {
		t.Fatalf("snapshot lost: found=%v err=%v", found, err)
	}
	stored.WorkspaceSkills[0].Body = "reader mutation"
	again, _, err := store.GetTask(t.Context(), task.ID)
	if err != nil || again.WorkspaceSkills[0].Body != "private" {
		t.Fatalf("snapshot alias: %v", err)
	}
}
