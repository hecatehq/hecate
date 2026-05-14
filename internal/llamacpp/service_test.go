package llamacpp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hecate/agent-runtime/internal/controlplane"
)

func makeServiceWithBinary(t *testing.T, store controlplane.Store, dataDir, binaryPath string) *Service {
	t.Helper()
	svc, err := NewService(ServiceOptions{
		BinaryPath: binaryPath,
		DataDir:    dataDir,
		Store:      store,
		Starter:    &fakeStarter{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// makeFakeBinary writes a no-op file with the executable bit set so
// FeatureAvailability sees a "real" binary path.
func makeFakeBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}

func TestService_FeatureAvailability(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	dataDir := t.TempDir()

	t.Run("binary missing → unavailable", func(t *testing.T) {
		svc, _ := NewService(ServiceOptions{DataDir: dataDir, Store: store, Starter: &fakeStarter{}})
		fa := svc.FeatureAvailability()
		if fa.Available {
			t.Fatalf("expected unavailable, got %+v", fa)
		}
		if fa.Reason != "binary_not_found" {
			t.Fatalf("reason = %q; want binary_not_found", fa.Reason)
		}
	})

	t.Run("binary present + executable → available", func(t *testing.T) {
		bin := makeFakeBinary(t)
		svc := makeServiceWithBinary(t, store, dataDir, bin)
		fa := svc.FeatureAvailability()
		if !fa.Available {
			t.Fatalf("expected available; got %+v", fa)
		}
		if fa.BinaryPath != bin {
			t.Fatalf("BinaryPath = %q; want %q", fa.BinaryPath, bin)
		}
	})

	t.Run("binary present but not executable → not_executable", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "llama-server")
		if err := os.WriteFile(bin, []byte{}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		svc := makeServiceWithBinary(t, store, dataDir, bin)
		fa := svc.FeatureAvailability()
		if fa.Available {
			t.Fatalf("expected unavailable; got %+v", fa)
		}
		if fa.Reason != "binary_not_executable" {
			t.Fatalf("reason = %q; want binary_not_executable", fa.Reason)
		}
	})
}

func TestService_ListInstalledReconcilesMissingFiles(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	dataDir := t.TempDir()
	ctx := context.Background()

	// Two registry rows; one has a file on disk, the other doesn't.
	have := filepath.Join(dataDir, "models", "real.gguf")
	if err := os.MkdirAll(filepath.Dir(have), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(have, []byte("gguf"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.UpsertInstalledModel(ctx, InstalledModel{
		ID:       "real-model",
		FilePath: "models/real.gguf",
	}); err != nil {
		t.Fatalf("Upsert real: %v", err)
	}
	if _, err := store.UpsertInstalledModel(ctx, InstalledModel{
		ID:       "ghost-model",
		FilePath: "models/ghost.gguf",
	}); err != nil {
		t.Fatalf("Upsert ghost: %v", err)
	}

	svc := makeServiceWithBinary(t, store, dataDir, makeFakeBinary(t))
	got, err := svc.ListInstalled(ctx)
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(got) != 1 || got[0].ID != "real-model" {
		t.Fatalf("ListInstalled = %+v; want only real-model", got)
	}

	// The ghost row must be gone from the snapshot too.
	state, _ := store.Snapshot(ctx)
	for _, m := range state.InstalledModels {
		if m.ID == "ghost-model" {
			t.Fatal("ghost row should have been reconciled away")
		}
	}
}

func TestService_EnsureAutoRegisteredProvider_FreshState(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	svc := makeServiceWithBinary(t, store, t.TempDir(), makeFakeBinary(t))
	ctx := context.Background()

	if err := svc.EnsureAutoRegisteredProvider(ctx, "http://127.0.0.1:7321"); err != nil {
		t.Fatalf("EnsureAutoRegisteredProvider: %v", err)
	}

	state, _ := store.Snapshot(ctx)
	var found bool
	for _, p := range state.Providers {
		if p.PresetID == "llamacpp" {
			if p.BaseURL != "http://127.0.0.1:7321"+InternalProxyPathPrefix() {
				t.Fatalf("BaseURL = %q; want gateway-internal proxy path", p.BaseURL)
			}
			if p.Kind != "local" || p.Protocol != "openai" || !p.Enabled {
				t.Fatalf("auto-provider shape = %+v", p)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected auto-registered llamacpp provider")
	}
}

func TestService_EnsureAutoRegisteredProvider_OperatorOverride(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	ctx := context.Background()

	// Pre-existing operator-created llamacpp provider — the
	// service must leave it alone and surface
	// ErrAutoProviderOperatorOwned. The operator path through Add
	// Provider always stamps PresetID="llamacpp" when picking the
	// catalog preset, which is the stable signal we match on.
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		Name:     "llama.cpp",
		PresetID: "llamacpp",
		Kind:     "local",
		BaseURL:  "http://192.168.1.100:8080/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	svc := makeServiceWithBinary(t, store, t.TempDir(), makeFakeBinary(t))
	err := svc.EnsureAutoRegisteredProvider(ctx, "http://127.0.0.1:7321")
	if !errors.Is(err, ErrAutoProviderOperatorOwned) {
		t.Fatalf("expected ErrAutoProviderOperatorOwned, got %v", err)
	}

	// Ensure we didn't add a second row.
	state, _ := store.Snapshot(ctx)
	count := 0
	for _, p := range state.Providers {
		if p.PresetID == "llamacpp" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("llamacpp provider count = %d; want 1", count)
	}
}

func TestService_EnsureAutoRegisteredProvider_RefreshesManagedRow(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	svc := makeServiceWithBinary(t, store, t.TempDir(), makeFakeBinary(t))
	ctx := context.Background()

	// First boot — register at port 7321.
	if err := svc.EnsureAutoRegisteredProvider(ctx, "http://127.0.0.1:7321"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second boot — same row should refresh to the new port. The
	// reviewer flagged the v1 behavior here: the previous boot's
	// own auto-managed row got treated as an operator override and
	// left untouched, leaving the URL pointing at a stale port.
	if err := svc.EnsureAutoRegisteredProvider(ctx, "http://127.0.0.1:9999"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	state, _ := store.Snapshot(ctx)
	var hits int
	for _, p := range state.Providers {
		if p.ID != "llamacpp" {
			continue
		}
		hits++
		want := "http://127.0.0.1:9999" + InternalProxyPathPrefix()
		if p.BaseURL != want {
			t.Fatalf("BaseURL = %q; want refreshed %q", p.BaseURL, want)
		}
	}
	if hits != 1 {
		t.Fatalf("managed provider row count = %d; want exactly 1 (no duplicates on refresh)", hits)
	}
}

func TestService_EnsureAutoRegisteredProvider_DormantSkips(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	svc, err := NewService(ServiceOptions{
		DataDir: t.TempDir(),
		Store:   store,
		Starter: &fakeStarter{},
		// BinaryPath intentionally empty
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.EnsureAutoRegisteredProvider(context.Background(), "http://127.0.0.1:7321"); err != nil {
		t.Fatalf("EnsureAutoRegisteredProvider on dormant: %v", err)
	}
	state, _ := store.Snapshot(context.Background())
	for _, p := range state.Providers {
		if p.PresetID == "llamacpp" {
			t.Fatalf("dormant service should not auto-register; got %+v", p)
		}
	}
}

func TestService_RuntimeLookupAdapter(t *testing.T) {
	t.Parallel()
	// Verifies that the controlplaneModelLookup adapter the
	// Service wires actually resolves runtime model lookups
	// through the store. Smoke check — guards the boring path that
	// would silently break if the adapter regressed.
	store := controlplane.NewMemoryStore()
	ctx := context.Background()
	if _, err := store.UpsertInstalledModel(ctx, InstalledModel{
		ID:                 "qwen-test",
		FilePath:           "models/qwen.gguf",
		RecommendedContext: 4096,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := makeServiceWithBinary(t, store, t.TempDir(), makeFakeBinary(t))

	// Run an EnsureLoaded — fakeStarter satisfies the runtime so
	// the lookup is the real adapter under test.
	if _, err := svc.Runtime().EnsureLoaded(ctx, "qwen-test"); err != nil {
		t.Fatalf("EnsureLoaded via service: %v", err)
	}
	if svc.Runtime().Status().ActiveModelID != "qwen-test" {
		t.Fatalf("active model = %q", svc.Runtime().Status().ActiveModelID)
	}
}
