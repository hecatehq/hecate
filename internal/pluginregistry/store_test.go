package pluginregistry

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestStoreConformance_PluginRegistryLifecycle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLitePluginTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)
			plugin := testPluginFromManifest(t, map[string]any{
				"schema_version": ManifestSchemaVersion,
				"id":             "GitHub",
				"name":           "GitHub",
				"description":    "Read and link GitHub work.",
				"version":        "0.1.0",
				"permissions":    []string{"network:github.com", "unsupported:host-hook"},
				"capabilities": map[string]any{
					"connectors": []map[string]any{{
						"id":           "issues",
						"display_name": "Issues",
						"permissions":  []string{"secret:github_token"},
						"auth": []map[string]any{{
							"name":       "github_token",
							"kind":       "token",
							"secret_ref": "secret:from-manifest",
						}},
					}},
					"slash_commands": []map[string]any{{
						"name":         "github",
						"display_name": "/github",
					}},
				},
			})

			stored, err := store.Upsert(ctx, plugin)
			if err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			if stored.ID != "github" || stored.Enabled {
				t.Fatalf("stored plugin = %+v, want normalized disabled plugin", stored)
			}
			if len(stored.Capabilities) != 2 || len(stored.Auth) != 1 {
				t.Fatalf("stored plugin = %+v, want capabilities and auth projection", stored)
			}
			if stored.Auth[0].SecretRef != "" || stored.Auth[0].Status != AuthStatusUnknown {
				t.Fatalf("stored auth = %+v, want manifest secret_ref ignored", stored.Auth[0])
			}
			if stored.RequestedPermissions[1].Classification != PermissionUnsupported {
				t.Fatalf("requested permissions = %+v, want unsupported classification", stored.RequestedPermissions)
			}

			updated, err := store.Update(ctx, "github", func(plugin *Plugin) {
				plugin.Enabled = true
				for idx := range plugin.Capabilities {
					if plugin.Capabilities[idx].ID == "issues" {
						plugin.Capabilities[idx].Enabled = false
					}
				}
			})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if !updated.Enabled || findCapabilityForTest(updated.Capabilities, "issues").Enabled {
				t.Fatalf("updated plugin = %+v, want plugin enabled and issues disabled", updated)
			}

			health := HealthFor(updated, []Plugin{updated})
			if !containsStringForTest(health.UnsupportedPermissions, "unsupported:host-hook") {
				t.Fatalf("health = %+v, want unsupported permission", health)
			}
			if !containsStringForTest(health.UnresolvedSecretBindings, "github_token") {
				t.Fatalf("health = %+v, want unresolved secret binding", health)
			}
			if !containsStringForTest(health.DisabledCapabilities, "issues") {
				t.Fatalf("health = %+v, want disabled capability", health)
			}

			if _, err := store.Update(ctx, "missing", func(*Plugin) {}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Update missing err = %v, want ErrNotFound", err)
			}
			cleared, err := store.Clear(ctx)
			if err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if cleared != 1 {
				t.Fatalf("Clear deleted = %d, want 1", cleared)
			}
			items, err := store.List(ctx)
			if err != nil {
				t.Fatalf("List after clear: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("items after clear = %+v, want none", items)
			}
		})
	}
}

func TestPluginFromManifestRequiresSchemaVersion(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"id":      "github",
		"name":    "GitHub",
		"version": "0.1.0",
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if _, err := PluginFromManifest(raw, SourceLocalPath, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("PluginFromManifest err = %v, want ErrInvalid", err)
	}
}

func TestPluginRegistry_CommandCollisionHealth(t *testing.T) {
	t.Parallel()
	first := testPluginFromManifest(t, map[string]any{
		"schema_version": ManifestSchemaVersion,
		"id":             "github",
		"name":           "GitHub",
		"version":        "0.1.0",
		"capabilities": map[string]any{
			"slash_commands": []map[string]any{{"name": "issue"}},
		},
	})
	second := testPluginFromManifest(t, map[string]any{
		"schema_version": ManifestSchemaVersion,
		"id":             "linear",
		"name":           "Linear",
		"version":        "0.1.0",
		"capabilities": map[string]any{
			"slash_commands": []map[string]any{{"name": "issue"}},
		},
	})
	health := HealthFor(first, []Plugin{first, second})
	if len(health.CommandCollisions) != 1 || health.CommandCollisions[0].Command != "issue" {
		t.Fatalf("health = %+v, want issue collision", health)
	}
}

func newSQLitePluginTestStore(t *testing.T) Store {
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
		t.Fatalf("new sqlite plugin store: %v", err)
	}
	return store
}

func testPluginFromManifest(t *testing.T, manifest map[string]any) Plugin {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	plugin, err := PluginFromManifest(raw, SourceLocalPath, "/plugins/"+manifest["id"].(string)+"/plugin.json")
	if err != nil {
		t.Fatalf("PluginFromManifest: %v", err)
	}
	return plugin
}

func findCapabilityForTest(items []Capability, id string) Capability {
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return Capability{}
}

func containsStringForTest(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
