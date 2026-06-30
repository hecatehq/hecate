package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
)

func TestProjectCairnlineSidecarMCPConfig_DefaultsToInMemoryProbe(t *testing.T) {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)

	cfg, dbPath, timeout := handler.projectCairnlineSidecarMCPConfig()
	if cfg.Name != projectCairnlineSidecarMCPServerName || cfg.Command != "cairnline" {
		t.Fatalf("config = %+v, want default Cairnline stdio config", cfg)
	}
	if len(cfg.Args) != 0 {
		t.Fatalf("args = %+v, want empty args for default in-memory Cairnline probe", cfg.Args)
	}
	if dbPath != "" {
		t.Fatalf("database path = %q, want empty default", dbPath)
	}
	if timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", timeout)
	}
}

func TestProjectCairnlineSidecarMCPConfig_UsesConfiguredCommandArgsAndRelativeDatabase(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: dataDir},
		Projects: config.ProjectsConfig{
			CairnlineSidecarCommand:      "custom-cairnline",
			CairnlineSidecarArgs:         []string{"serve", "--stdio"},
			CairnlineSidecarDatabasePath: "sidecar/projects.db",
			CairnlineSidecarProbeTimeout: 3 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)

	cfg, dbPath, timeout := handler.projectCairnlineSidecarMCPConfig()
	if cfg.Command != "custom-cairnline" {
		t.Fatalf("command = %q, want custom-cairnline", cfg.Command)
	}
	if strings.Join(cfg.Args, " ") != "serve --stdio" {
		t.Fatalf("args = %+v, want custom args without automatic db append", cfg.Args)
	}
	wantDB := filepath.Join(dataDir, "sidecar", "projects.db")
	if abs, err := filepath.Abs(wantDB); err == nil {
		wantDB = abs
	}
	if dbPath != wantDB {
		t.Fatalf("database path = %q, want %q", dbPath, wantDB)
	}
	if timeout != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", timeout)
	}
}

func TestProjectCairnlineSidecarMCPConfig_AppendsDatabaseWhenArgsUnset(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cairnline.db")
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineSidecarDatabasePath: dbPath,
		},
	}, quietLogger(), nil, nil, nil, nil)

	cfg, gotDB, _ := handler.projectCairnlineSidecarMCPConfig()
	if gotDB != dbPath {
		t.Fatalf("database path = %q, want %q", gotDB, dbPath)
	}
	if len(cfg.Args) != 2 || cfg.Args[0] != "-db" || cfg.Args[1] != dbPath {
		t.Fatalf("args = %+v, want automatic -db path", cfg.Args)
	}
}

func TestProjectCairnlineSidecarProbe_Ready(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)

	got := handler.projectCairnlineSidecarProbe(t.Context())
	if !got.Ready || got.Status != "sidecar_probe_ready" {
		t.Fatalf("probe = %+v, want ready", got)
	}
	if got.ToolCount != len(projectCairnlineSidecarRequiredTools) {
		t.Fatalf("tool count = %d, want %d", got.ToolCount, len(projectCairnlineSidecarRequiredTools))
	}
	if len(got.MissingTools) != 0 {
		t.Fatalf("missing tools = %+v, want none", got.MissingTools)
	}
	if got.Command != os.Args[0] || len(got.Args) != 1 {
		t.Fatalf("probe config = command %q args %+v, want fixture command", got.Command, got.Args)
	}
}

func TestProjectCairnlineSidecarProbe_MissingRequiredTools(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "missing"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)

	got := handler.projectCairnlineSidecarProbe(t.Context())
	if got.Ready || got.Status != "sidecar_contract_incomplete" {
		t.Fatalf("probe = %+v, want incomplete contract", got)
	}
	if !containsString(got.MissingTools, "assignments.context") || !containsString(got.MissingTools, "assistant.propose") {
		t.Fatalf("missing tools = %+v, want representative missing contract tools", got.MissingTools)
	}
	if got.ToolCount != 1 {
		t.Fatalf("tool count = %d, want 1", got.ToolCount)
	}
}

func TestProjectCairnlineSidecarConnect_ReadyUsesPersistentClientCache(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarConnect(t.Context())
	if !got.Ready || got.Status != "sidecar_client_ready" {
		t.Fatalf("connect = %+v, want ready", got)
	}
	if !got.PersistentClient || !got.ClientCacheConfigured {
		t.Fatalf("connect persistent/cache flags = persistent:%t configured:%t, want true/true", got.PersistentClient, got.ClientCacheConfigured)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
	if got.ToolCount != len(projectCairnlineSidecarRequiredTools) || len(got.MissingTools) != 0 {
		t.Fatalf("tool count=%d missing=%+v, want full contract", got.ToolCount, got.MissingTools)
	}
}

func TestProjectCairnlineSidecarConnect_MissingRequiredTools(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "missing"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarConnect(t.Context())
	if got.Ready || got.Status != "sidecar_contract_incomplete" {
		t.Fatalf("connect = %+v, want incomplete contract", got)
	}
	if !got.PersistentClient || !got.ClientCacheConfigured {
		t.Fatalf("connect persistent/cache flags = persistent:%t configured:%t, want true/true", got.PersistentClient, got.ClientCacheConfigured)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
	if !containsString(got.MissingTools, "assignments.context") || !containsString(got.MissingTools, "assistant.propose") {
		t.Fatalf("missing tools = %+v, want representative missing contract tools", got.MissingTools)
	}
}
