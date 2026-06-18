package agentprofiles

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryStore_ProfileRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()

	created, err := store.Create(ctx, Profile{
		ID:                  "prof_backend",
		Name:                "Backend reviewer",
		Description:         "Reviews backend changes",
		Instructions:        "Prefer small, tested changes.",
		Surface:             SurfaceHecateTask,
		ProviderHint:        "anthropic",
		ModelHint:           "claude-sonnet-4",
		ExecutionProfile:    "review",
		ToolsEnabled:        true,
		WritesAllowed:       false,
		NetworkAllowed:      false,
		ApprovalPolicy:      ApprovalRequire,
		ProjectMemoryPolicy: MemoryVisibleOnly,
		ContextSourcePolicy: ContextIncludeEnabled,
		SkillIDs:            []string{"review", "review"},
		ExternalAgentKind:   "codex",
		ExternalAgentOptions: map[string]string{
			"effort": "high",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not initialized: %+v", created)
	}
	if len(created.SkillIDs) != 1 || created.SkillIDs[0] != "review" {
		t.Fatalf("skill ids = %+v, want normalized unique list", created.SkillIDs)
	}

	got, ok, err := store.Get(ctx, "prof_backend")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want profile", ok, err)
	}
	if got.Name != "Backend reviewer" || got.ExecutionProfile != "review" || !got.ToolsEnabled {
		t.Fatalf("profile = %+v, want persisted fields", got)
	}

	updated, err := store.Update(ctx, "prof_backend", func(profile *Profile) {
		profile.Name = "Backend implementer"
		profile.WritesAllowed = true
		profile.ProjectMemoryPolicy = MemoryInclude
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Backend implementer" || !updated.WritesAllowed || updated.ProjectMemoryPolicy != MemoryInclude {
		t.Fatalf("updated = %+v, want patched fields", updated)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !profileIDExists(items, "implementation") || !profileIDExists(items, "prof_backend") {
		t.Fatalf("items = %+v, want built-ins plus created profile", items)
	}

	if err := store.Delete(ctx, "prof_backend"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "prof_backend"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_BuiltInProfiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()

	profile, ok, err := store.Get(ctx, "implementation")
	if err != nil || !ok {
		t.Fatalf("Get built-in ok=%v err=%v, want implementation profile", ok, err)
	}
	if !profile.BuiltIn || profile.ExecutionProfile != "coding_agent" || !profile.WritesAllowed {
		t.Fatalf("built-in profile = %+v, want coding implementation posture", profile)
	}
	if _, err := store.Create(ctx, Profile{ID: "implementation", Name: "Override"}); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Create built-in error = %v, want ErrBuiltIn", err)
	}
	if _, err := store.Update(ctx, "implementation", func(profile *Profile) {
		profile.Name = "Override"
	}); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Update built-in error = %v, want ErrBuiltIn", err)
	}
	if err := store.Delete(ctx, "implementation"); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Delete built-in error = %v, want ErrBuiltIn", err)
	}
}

func TestMemoryStore_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()

	if _, err := store.Create(ctx, Profile{ID: "prof_missing_name"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create missing name error = %v, want ErrInvalid", err)
	}
	if _, err := store.Create(ctx, Profile{ID: "prof_bad_surface", Name: "Bad", Surface: "terminal"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create bad surface error = %v, want ErrInvalid", err)
	}
	if _, err := store.Create(ctx, Profile{ID: "prof_bad_policy", Name: "Bad", ApprovalPolicy: "sometimes"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create bad policy error = %v, want ErrInvalid", err)
	}
}

func profileIDExists(items []Profile, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
