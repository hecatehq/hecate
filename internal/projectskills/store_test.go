package projectskills

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestStoreConformance_ProjectSkillsLifecycle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLiteSkillTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)

			items, err := store.UpsertDiscovered(ctx, "proj_alpha", []Skill{
				{
					ID:          "backend",
					ProjectID:   "other_project",
					Title:       "Backend",
					Description: "Backend implementation guidance.",
					Path:        ".hecate/skills/backend/SKILL.md",
					RootID:      "root_a",
					Format:      FormatSkillMD,
					Enabled:     true,
					Status:      StatusAvailable,
					TrustLabel:  TrustWorkspaceSkill,
				},
				{
					ID:       "qa",
					Title:    "QA",
					Path:     ".agents/skills/qa/SKILL.md",
					Format:   FormatSkillMD,
					Enabled:  true,
					Status:   StatusAvailable,
					Warnings: []string{"manual warning"},
				},
			})
			if err != nil {
				t.Fatalf("UpsertDiscovered: %v", err)
			}
			if len(items) != 2 || items[0].ID != "backend" || items[1].ID != "qa" {
				t.Fatalf("items = %+v, want sorted backend and qa", items)
			}
			if items[0].ProjectID != "proj_alpha" || !items[0].Enabled || items[0].Status != StatusAvailable {
				t.Fatalf("backend skill = %+v, want normalized project skill", items[0])
			}

			updated, err := store.Update(ctx, "proj_alpha", "backend", func(skill *Skill) {
				skill.Enabled = false
				skill.Title = "Backend Lead"
				skill.Description = "Operator edited description."
				skill.TrustLabel = "operator_curated_skill"
			})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if updated.Enabled || updated.Title != "Backend Lead" || updated.Description != "Operator edited description." || updated.TrustLabel != "operator_curated_skill" {
				t.Fatalf("updated skill = %+v, want operator overrides", updated)
			}

			items, err = store.UpsertDiscovered(ctx, "proj_alpha", []Skill{{
				ID:          "backend",
				Title:       "Backend From Disk",
				Description: "Disk description.",
				Path:        ".hecate/skills/backend/SKILL.md",
				RootID:      "root_a",
				Format:      FormatSkillMD,
				Enabled:     true,
				Status:      StatusAvailable,
				TrustLabel:  TrustWorkspaceSkill,
			}})
			if err != nil {
				t.Fatalf("Rediscover: %v", err)
			}
			backend := findSkillForTest(items, "backend")
			if backend == nil || backend.Enabled || backend.Title != "Backend Lead" || backend.Description != "Operator edited description." || backend.TrustLabel != "operator_curated_skill" || backend.Status != StatusAvailable {
				t.Fatalf("rediscovered backend = %+v, want preserved operator overrides", backend)
			}
			qa := findSkillForTest(items, "qa")
			if qa == nil || qa.Status != StatusMissing || !containsStringForTest(qa.Warnings, "Skill was not found during the latest discovery.") {
				t.Fatalf("rediscovered qa = %+v, want missing warning", qa)
			}

			if _, err := store.Update(ctx, "proj_alpha", "missing", func(*Skill) {}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Update missing err = %v, want ErrNotFound", err)
			}
			deleted, err := store.DeleteProject(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("DeleteProject: %v", err)
			}
			if deleted != 2 {
				t.Fatalf("DeleteProject deleted = %d, want 2", deleted)
			}
			items, err = store.List(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("List after delete: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("items after delete = %+v, want none", items)
			}
		})
	}
}

func newSQLiteSkillTestStore(t *testing.T) Store {
	t.Helper()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "hecate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("new sqlite client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close sqlite client: %v", err)
		}
	})
	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("new sqlite project skill store: %v", err)
	}
	return store
}

func findSkillForTest(items []Skill, id string) *Skill {
	for idx := range items {
		if items[idx].ID == id {
			return &items[idx]
		}
	}
	return nil
}

func containsStringForTest(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
