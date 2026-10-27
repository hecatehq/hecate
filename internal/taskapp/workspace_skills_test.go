package taskapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/workspaceskills"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestTaskApplication_WorkspaceSkillsExplicitFrozenInputs(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("Inspect before editing."), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := workspaceskills.Catalog(t.Context(), root)
	if err != nil || len(catalog) != 1 {
		t.Fatalf("catalog: %v", err)
	}
	selection := []types.WorkspaceSkillSelection{{Path: catalog[0].Path, SHA256: catalog[0].SHA256}}
	store := taskstate.NewMemoryStore()
	runner := &recordingTaskApplicationRunner{}
	app := newTestTaskApplication(store, runner)
	task, err := app.CreateTask(t.Context(), CreateCommand{Prompt: "review", ExecutionKind: "agent_loop", WorkingDirectory: root, WorkspaceSkills: selection})
	if err != nil || len(task.WorkspaceSkills) != 1 {
		t.Fatalf("create: %v", err)
	}
	if task.SystemPrompt != "" || task.AgentPresetID != "" || task.SandboxNetwork {
		t.Fatal("skill changed policy or public prompt")
	}
	implicit, err := app.CreateTask(t.Context(), CreateCommand{Prompt: "review", WorkingDirectory: root, WorkspaceSkills: selection})
	if err != nil || implicit.ExecutionKind != "agent_loop" || len(implicit.WorkspaceSkills) != 1 {
		t.Fatalf("skills did not select native execution: kind=%q err=%v", implicit.ExecutionKind, err)
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.GetTask(t.Context(), task.ID)
	if err != nil || !found || stored.WorkspaceSkills[0].Body != "Inspect before editing." {
		t.Fatalf("snapshot not retained: %v", err)
	}
	_, err = app.CreateTask(t.Context(), CreateCommand{Prompt: "review", ExecutionKind: "agent_loop", WorkflowMode: "qa", WorkingDirectory: root, WorkspaceSkills: selection})
	if err == nil {
		t.Fatal("QA accepted skill instructions")
	}
}
