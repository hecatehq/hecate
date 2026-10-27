package workspaceskills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func writeSkill(t *testing.T, root, name, body string) string {
	t.Helper()
	filename := filepath.Join(root, ".agents", "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestWorkspaceSkills_FreezeReviewedVersionAndMetadataOnlyCatalog(t *testing.T) {
	root := t.TempDir()
	filename := writeSkill(t, root, "review", "private-skill-sentinel\nReview tests before edits.")
	catalog, err := Catalog(t.Context(), root)
	if err != nil || len(catalog) != 1 {
		t.Fatalf("catalog count=%d err=%v", len(catalog), err)
	}
	raw, _ := json.Marshal(catalog)
	if strings.Contains(string(raw), "private-skill-sentinel") {
		t.Fatal("catalog leaked body")
	}
	selection := []types.WorkspaceSkillSelection{{Path: catalog[0].Path, SHA256: catalog[0].SHA256}}
	frozen, err := Freeze(t.Context(), root, selection)
	if err != nil || len(frozen) != 1 {
		t.Fatalf("freeze: %v", err)
	}
	if err := ValidateFrozen(frozen); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Freeze(t.Context(), root, selection); err == nil {
		t.Fatal("accepted changed skill")
	}
	messages := []types.Message{{Role: "user", Content: "review"}}
	request := Messages(messages, frozen)
	if len(messages) != 1 || len(request) != 2 || !request[0].ContentBlocks[0].WorkspaceSkillInput || !strings.Contains(request[0].ContentBlocks[0].Text, "private-skill-sentinel") {
		t.Fatal("request did not preserve frozen input")
	}
	if strings.Contains(request[0].ContentBlocks[0].Text, "changed") {
		t.Fatal("re-read mutable input")
	}
}

func TestWorkspaceSkills_RejectUnsafeAndBoundedInputs(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "review", "safe")
	catalog, err := Catalog(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	valid := types.WorkspaceSkillSelection{Path: catalog[0].Path, SHA256: catalog[0].SHA256}
	if _, err := Catalog(t.Context(), "."); err == nil {
		t.Fatal("catalog accepted an implicit working directory")
	}
	if _, err := Freeze(t.Context(), ".", []types.WorkspaceSkillSelection{valid}); err == nil {
		t.Fatal("freeze accepted an implicit working directory")
	}
	if _, err := Freeze(t.Context(), root, make([]types.WorkspaceSkillSelection, MaxSkills+1)); err == nil {
		t.Fatal("accepted too many selections")
	}
	for _, filename := range []string{"../SKILL.md", "/tmp/SKILL.md", ".agents/skills/review/../other/SKILL.md", ".agents/skills/review/readme.md", ".agents/skills/review/nested/SKILL.md", ".agents/skills/review\u202e/SKILL.md"} {
		t.Run(filename, func(t *testing.T) {
			if _, err := Freeze(t.Context(), root, []types.WorkspaceSkillSelection{{Path: filename, SHA256: valid.SHA256}}); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
	if _, err := Freeze(t.Context(), root, []types.WorkspaceSkillSelection{valid, valid}); err == nil {
		t.Fatal("accepted duplicate")
	}
	writeSkill(t, root, "large", strings.Repeat("x", MaxSkillBytes+1))
	writeSkill(t, root, "binary", "abc\x00def")
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := writeSkill(t, root, "link", "")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	catalog, err = Catalog(t.Context(), root)
	if err != nil || len(catalog) != 1 {
		t.Fatalf("unsafe catalog count=%d err=%v", len(catalog), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Freeze(ctx, root, []types.WorkspaceSkillSelection{valid}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestWorkspaceSkills_AggregateLimitAndSnapshotIntegrity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two", "three"} {
		writeSkill(t, root, name, strings.Repeat("x", MaxSkillBytes))
	}
	catalog, err := Catalog(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	var selection []types.WorkspaceSkillSelection
	for _, skill := range catalog {
		selection = append(selection, types.WorkspaceSkillSelection{Path: skill.Path, SHA256: skill.SHA256})
	}
	if _, err := Freeze(t.Context(), root, selection); err == nil {
		t.Fatal("aggregate limit not enforced")
	}
	frozen, err := Freeze(t.Context(), root, selection[:1])
	if err != nil {
		t.Fatal(err)
	}
	frozen[0].Body = "corrupt"
	if err := ValidateFrozen(frozen); err == nil {
		t.Fatal("accepted corrupt snapshot")
	}
}
