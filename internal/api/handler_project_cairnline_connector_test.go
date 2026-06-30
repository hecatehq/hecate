package api

import (
	"context"
	"encoding/json"
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
	for _, name := range []string{"projects.update", "roots.create", "context_sources.update", "profiles.create", "assignments.create", "memory_entries.create", "assistant.apply", "memory_candidates.delete"} {
		if !containsString(got.RequiredTools, name) {
			t.Fatalf("required tools = %+v, want %q", got.RequiredTools, name)
		}
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
	if !containsString(got.MissingTools, "projects.get") || !containsString(got.MissingTools, "assignments.context") || !containsString(got.MissingTools, "assignments.launch_packet") || !containsString(got.MissingTools, "assistant.propose") || !containsString(got.MissingTools, "assistant.apply") || !containsString(got.MissingTools, "memory_candidates.delete") {
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
	for _, name := range []string{"projects.activity", "skills.discover", "work_items.closeout_readiness", "artifacts.create", "handoffs.update_status", "memory_candidates.promote"} {
		if !containsString(got.RequiredTools, name) {
			t.Fatalf("required tools = %+v, want %q", got.RequiredTools, name)
		}
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
	if !containsString(got.MissingTools, "projects.get") || !containsString(got.MissingTools, "assignments.context") || !containsString(got.MissingTools, "assignments.launch_packet") || !containsString(got.MissingTools, "assistant.propose") || !containsString(got.MissingTools, "assistant.apply") || !containsString(got.MissingTools, "memory_candidates.delete") {
		t.Fatalf("missing tools = %+v, want representative missing contract tools", got.MissingTools)
	}
}

func TestProjectCairnlineSidecarReadSmoke_ProjectsListUsesPersistentClientCache(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarReadSmoke(t.Context())
	if !got.Ready || got.Status != "sidecar_read_ready" {
		t.Fatalf("read smoke = %+v, want ready", got)
	}
	if got.Tool != "projects.list" || !got.ReadOnly || got.ToolIsError {
		t.Fatalf("tool fields = tool:%q read_only:%t is_error:%t, want projects.list read-only success", got.Tool, got.ReadOnly, got.ToolIsError)
	}
	if !strings.Contains(got.ToolText, "proj_fixture") {
		t.Fatalf("tool text = %q, want fixture project evidence", got.ToolText)
	}
	if !got.StructuredReady || got.StructuredProjectCount != 1 {
		t.Fatalf("structured readiness/count = %t/%d, want ready with one project", got.StructuredReady, got.StructuredProjectCount)
	}
	if len(got.StructuredProjects) != 1 || got.StructuredProjects[0].ID != "proj_fixture" || got.StructuredProjects[0].Name != "Fixture Project" {
		t.Fatalf("structured projects = %+v, want fixture project", got.StructuredProjects)
	}
	if len(got.StructuredProjects[0].Roots) != 1 || got.StructuredProjects[0].Roots[0].ID != "root_fixture" {
		t.Fatalf("structured roots = %+v, want fixture root", got.StructuredProjects[0].Roots)
	}
	if !got.PersistentClient || !got.ClientCacheConfigured {
		t.Fatalf("read smoke persistent/cache flags = persistent:%t configured:%t, want true/true", got.PersistentClient, got.ClientCacheConfigured)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
}

func TestProjectCairnlineSidecarReadSmoke_TextOnlyProjectsListWarns(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "text-only"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarReadSmoke(t.Context())
	if !got.Ready || got.Status != "sidecar_read_ready" {
		t.Fatalf("read smoke = %+v, want tool-call ready", got)
	}
	if got.StructuredReady || got.StructuredProjectCount != 0 || len(got.StructuredProjects) != 0 {
		t.Fatalf("structured fields = ready:%t count:%d projects:%+v, want text-only downgrade", got.StructuredReady, got.StructuredProjectCount, got.StructuredProjects)
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "Cairnline sidecar projects.list did not return structuredContent") {
		t.Fatalf("warnings = %+v, want missing structuredContent warning", got.Warnings)
	}
}

func TestProjectCairnlineSidecarReadSmoke_ToolLevelError(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarReadSmoke(t.Context())
	if got.Ready || got.Status != "sidecar_read_tool_failed" {
		t.Fatalf("read smoke = %+v, want tool-level failure", got)
	}
	if !got.ToolIsError {
		t.Fatalf("tool_is_error = false, want true")
	}
	if !strings.Contains(got.ToolText, "fixture projects.list failed") {
		t.Fatalf("tool text = %q, want fixture tool-level error evidence", got.ToolText)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
}

func TestProjectCairnlineSidecarDetailSmoke_SelectsProjectFromStructuredList(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarDetailSmoke(t.Context(), ProjectCairnlineSidecarDetailRequest{})
	if !got.Ready || got.Status != "sidecar_detail_ready" {
		t.Fatalf("detail smoke = %+v, want ready", got)
	}
	if got.Tool != "projects.get" || !got.ReadOnly || got.ToolIsError {
		t.Fatalf("tool fields = tool:%q read_only:%t is_error:%t, want projects.get read-only success", got.Tool, got.ReadOnly, got.ToolIsError)
	}
	if got.SelectedProjectID != "proj_fixture" || got.SelectedProjectSource != "projects.list" {
		t.Fatalf("selected project = %q source %q, want fixture from list", got.SelectedProjectID, got.SelectedProjectSource)
	}
	if !got.ListStructuredReady || got.ListProjectCount != 1 {
		t.Fatalf("list structured readiness/count = %t/%d, want ready with one project", got.ListStructuredReady, got.ListProjectCount)
	}
	if !got.StructuredReady || got.StructuredProject.ID != "proj_fixture" || got.StructuredProject.Name != "Fixture Project" {
		t.Fatalf("structured project = ready:%t project:%+v, want fixture project", got.StructuredReady, got.StructuredProject)
	}
	if len(got.StructuredProject.Roots) != 1 || got.StructuredProject.Roots[0].ID != "root_fixture" {
		t.Fatalf("structured roots = %+v, want fixture root", got.StructuredProject.Roots)
	}
	if !got.PersistentClient || !got.ClientCacheConfigured {
		t.Fatalf("detail smoke persistent/cache flags = persistent:%t configured:%t, want true/true", got.PersistentClient, got.ClientCacheConfigured)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
}

func TestProjectCairnlineSidecarDetailSmoke_UsesRequestedProjectID(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarDetailSmoke(t.Context(), ProjectCairnlineSidecarDetailRequest{ProjectID: "proj_requested"})
	if !got.Ready || got.Status != "sidecar_detail_ready" {
		t.Fatalf("detail smoke = %+v, want ready", got)
	}
	if got.RequestedProjectID != "proj_requested" || got.SelectedProjectID != "proj_requested" || got.SelectedProjectSource != "request" {
		t.Fatalf("project selection = requested:%q selected:%q source:%q, want explicit request", got.RequestedProjectID, got.SelectedProjectID, got.SelectedProjectSource)
	}
	if got.ListStructuredReady || got.ListProjectCount != 0 || got.ListToolText != "" {
		t.Fatalf("list fields = ready:%t count:%d text:%q, want no projects.list call for explicit id", got.ListStructuredReady, got.ListProjectCount, got.ListToolText)
	}
	if !got.StructuredReady || got.StructuredProject.ID != "proj_requested" {
		t.Fatalf("structured project = ready:%t project:%+v, want requested id", got.StructuredReady, got.StructuredProject)
	}
}

func TestProjectCairnlineSidecarDetailSmoke_TextOnlyListCannotSelectProject(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "text-only"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarDetailSmoke(t.Context(), ProjectCairnlineSidecarDetailRequest{})
	if got.Ready || got.Status != "sidecar_detail_no_project" {
		t.Fatalf("detail smoke = %+v, want no typed project to fetch", got)
	}
	if got.ListStructuredReady || got.SelectedProjectID != "" || got.StructuredReady {
		t.Fatalf("structured fields = list:%t selected:%q detail:%t, want no typed selection", got.ListStructuredReady, got.SelectedProjectID, got.StructuredReady)
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "projects.list did not return structuredContent") {
		t.Fatalf("warnings = %+v, want missing structuredContent warning", got.Warnings)
	}
}

func TestProjectCairnlineSidecarDetailSmoke_ToolLevelError(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "get-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarDetailSmoke(t.Context(), ProjectCairnlineSidecarDetailRequest{ProjectID: "proj_requested"})
	if got.Ready || got.Status != "sidecar_detail_tool_failed" {
		t.Fatalf("detail smoke = %+v, want tool-level failure", got)
	}
	if !got.ToolIsError {
		t.Fatalf("tool_is_error = false, want true")
	}
	if !strings.Contains(got.ToolText, "fixture projects.get failed") {
		t.Fatalf("tool text = %q, want fixture tool-level error evidence", got.ToolText)
	}
}

func TestProjectCairnlineSidecarCoordinationSmoke_ListsPortableSurfaces(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarCoordinationSmoke(t.Context(), ProjectCairnlineSidecarCoordinationRequest{})
	if !got.Ready || got.Status != "sidecar_coordination_ready" || !got.StructuredReady {
		t.Fatalf("coordination smoke = %+v, want structured ready", got)
	}
	if got.SelectedProjectID != "proj_fixture" || got.SelectedProjectSource != "projects.list" {
		t.Fatalf("selected project = %q source %q, want fixture from projects.list", got.SelectedProjectID, got.SelectedProjectSource)
	}
	if got.ToolCount != len(projectCairnlineSidecarCoordinationListTools) || len(got.Lists) != len(projectCairnlineSidecarCoordinationListTools) {
		t.Fatalf("tool count/list count = %d/%d, want %d", got.ToolCount, len(got.Lists), len(projectCairnlineSidecarCoordinationListTools))
	}
	for _, tool := range []string{"projects.list", "profiles.list", "execution_profiles.list", "skills.list", "roles.list", "work_items.list", "assignments.list"} {
		item, ok := projectCairnlineSidecarCoordinationTestList(got.Lists, tool)
		if !ok {
			t.Fatalf("missing coordination list result for %s: %+v", tool, got.Lists)
		}
		if item.ToolIsError || !item.StructuredReady || item.StructuredCount != 1 || item.StructuredParseError != "" {
			t.Fatalf("%s result = %+v, want one structured item", tool, item)
		}
	}
	if item, _ := projectCairnlineSidecarCoordinationTestList(got.Lists, "skills.list"); item.ProjectID != "proj_fixture" || !item.ProjectScoped {
		t.Fatalf("skills.list project scope = id:%q scoped:%t, want fixture project scoped", item.ProjectID, item.ProjectScoped)
	}
	if !got.PersistentClient || !got.ClientCacheConfigured {
		t.Fatalf("coordination smoke persistent/cache flags = persistent:%t configured:%t, want true/true", got.PersistentClient, got.ClientCacheConfigured)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
}

func TestProjectCairnlineSidecarCoordinationSmoke_UsesRequestedProjectID(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarCoordinationSmoke(t.Context(), ProjectCairnlineSidecarCoordinationRequest{ProjectID: "proj_requested"})
	if !got.Ready || got.Status != "sidecar_coordination_ready" || !got.StructuredReady {
		t.Fatalf("coordination smoke = %+v, want structured ready", got)
	}
	if got.RequestedProjectID != "proj_requested" || got.SelectedProjectID != "proj_requested" || got.SelectedProjectSource != "request" {
		t.Fatalf("project selection = requested:%q selected:%q source:%q, want explicit request", got.RequestedProjectID, got.SelectedProjectID, got.SelectedProjectSource)
	}
	if item, _ := projectCairnlineSidecarCoordinationTestList(got.Lists, "assignments.list"); item.ProjectID != "proj_requested" {
		t.Fatalf("assignments.list project id = %q, want requested project", item.ProjectID)
	}
}

func TestProjectCairnlineSidecarCoordinationSmoke_TextOnlyListsWarn(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "text-only"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarCoordinationSmoke(t.Context(), ProjectCairnlineSidecarCoordinationRequest{ProjectID: "proj_requested"})
	if !got.Ready || got.Status != "sidecar_coordination_ready" || got.StructuredReady {
		t.Fatalf("coordination smoke = %+v, want ready with structured warnings", got)
	}
	if len(got.Lists) != len(projectCairnlineSidecarCoordinationListTools) {
		t.Fatalf("list count = %d, want %d", len(got.Lists), len(projectCairnlineSidecarCoordinationListTools))
	}
	for _, item := range got.Lists {
		if item.StructuredReady || item.StructuredCount != 0 || item.StructuredParseError != "" {
			t.Fatalf("%s result = %+v, want text-only downgrade", item.Tool, item)
		}
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "did not return structuredContent") {
		t.Fatalf("warnings = %+v, want missing structuredContent warning", got.Warnings)
	}
}

func TestProjectCairnlineSidecarCoordinationSmoke_TextOnlyListCannotSelectProject(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "text-only"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarCoordinationSmoke(t.Context(), ProjectCairnlineSidecarCoordinationRequest{})
	if got.Ready || got.Status != "sidecar_coordination_no_project" {
		t.Fatalf("coordination smoke = %+v, want no typed project selection", got)
	}
	if got.SelectedProjectID != "" || len(got.Lists) != 3 {
		t.Fatalf("selection/lists = selected:%q lists:%+v, want global list evidence only", got.SelectedProjectID, got.Lists)
	}
	for _, tool := range []string{"projects.list", "profiles.list", "execution_profiles.list"} {
		if _, ok := projectCairnlineSidecarCoordinationTestList(got.Lists, tool); !ok {
			t.Fatalf("lists = %+v, want %s before project-scoped stop", got.Lists, tool)
		}
	}
}

func TestProjectCairnlineSidecarCoordinationSmoke_ToolLevelError(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "coordination-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarCoordinationSmoke(t.Context(), ProjectCairnlineSidecarCoordinationRequest{ProjectID: "proj_requested"})
	if got.Ready || got.Status != "sidecar_coordination_tool_failed" {
		t.Fatalf("coordination smoke = %+v, want tool-level failure", got)
	}
	item, ok := projectCairnlineSidecarCoordinationTestList(got.Lists, "skills.list")
	if !ok || !item.ToolIsError || !strings.Contains(item.ToolText, "fixture skills.list failed") {
		t.Fatalf("skills.list result = %+v ok=%t, want tool-level error", item, ok)
	}
}

func TestProjectCairnlineSidecarAssignmentContextSmoke_SelectsAssignmentFromStructuredLists(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarAssignmentContextSmoke(t.Context(), ProjectCairnlineSidecarAssignmentContextRequest{})
	if !got.Ready || got.Status != "sidecar_assignment_context_ready" || !got.StructuredReady {
		t.Fatalf("assignment context smoke = %+v, want structured ready", got)
	}
	if got.SelectedProjectID != "proj_fixture" || got.SelectedProjectSource != "projects.list" {
		t.Fatalf("selected project = %q source %q, want fixture from projects.list", got.SelectedProjectID, got.SelectedProjectSource)
	}
	if got.SelectedAssignmentID != "asg_fixture" || got.SelectedAssignmentSource != "assignments.list" {
		t.Fatalf("selected assignment = %q source %q, want fixture from assignments.list", got.SelectedAssignmentID, got.SelectedAssignmentSource)
	}
	if got.ProjectList == nil || got.AssignmentList == nil || !got.ProjectList.StructuredReady || got.ProjectList.StructuredCount != 1 || !got.AssignmentList.StructuredReady || got.AssignmentList.StructuredCount != 1 {
		t.Fatalf("list readiness = projects %+v assignments %+v, want typed selection lists", got.ProjectList, got.AssignmentList)
	}
	if got.StructuredIDs.AssignmentID != "asg_fixture" || got.StructuredIDs.ProjectID != "proj_fixture" || got.StructuredIDs.WorkItemID != "work_fixture" || got.StructuredIDs.RoleID != "role_fixture" {
		t.Fatalf("structured ids = %+v, want assignment/project/work/role ids", got.StructuredIDs)
	}
	if !strings.Contains(got.ToolText, "Assignment context asg_fixture") {
		t.Fatalf("tool text = %q, want assignment context evidence", got.ToolText)
	}
	if !got.PersistentClient || !got.ClientCacheConfigured {
		t.Fatalf("assignment context persistent/cache flags = persistent:%t configured:%t, want true/true", got.PersistentClient, got.ClientCacheConfigured)
	}
	if got.ClientCacheEntries != 1 || got.ClientCacheInUse != 0 || got.ClientCacheIdle != 1 {
		t.Fatalf("cache stats = entries:%d in_use:%d idle:%d, want 1/0/1", got.ClientCacheEntries, got.ClientCacheInUse, got.ClientCacheIdle)
	}
}

func TestProjectCairnlineSidecarAssignmentContextSmoke_UsesRequestedIDs(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarAssignmentContextSmoke(t.Context(), ProjectCairnlineSidecarAssignmentContextRequest{ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if !got.Ready || got.Status != "sidecar_assignment_context_ready" || !got.StructuredReady {
		t.Fatalf("assignment context smoke = %+v, want structured ready", got)
	}
	if got.SelectedProjectID != "proj_requested" || got.SelectedProjectSource != "request" || got.SelectedAssignmentID != "asg_requested" || got.SelectedAssignmentSource != "request" {
		t.Fatalf("selection = project %q/%q assignment %q/%q, want request ids", got.SelectedProjectID, got.SelectedProjectSource, got.SelectedAssignmentID, got.SelectedAssignmentSource)
	}
	if got.ProjectList != nil || got.AssignmentList != nil {
		t.Fatalf("selection lists = projects %+v assignments %+v, want skipped lists for explicit ids", got.ProjectList, got.AssignmentList)
	}
	if got.StructuredIDs.AssignmentID != "asg_requested" {
		t.Fatalf("structured assignment id = %q, want requested", got.StructuredIDs.AssignmentID)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(encoded), "project_list") || strings.Contains(string(encoded), "assignment_list") {
		t.Fatalf("encoded response = %s, want omitted selection lists for explicit ids", encoded)
	}
}

func TestProjectCairnlineSidecarAssignmentContextSmoke_TextOnlyContextWarns(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "text-only"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarAssignmentContextSmoke(t.Context(), ProjectCairnlineSidecarAssignmentContextRequest{ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if !got.Ready || got.Status != "sidecar_assignment_context_ready" || got.StructuredReady {
		t.Fatalf("assignment context smoke = %+v, want ready with structured warning", got)
	}
	if got.StructuredIDs.AssignmentID != "" || !strings.Contains(strings.Join(got.Warnings, "\n"), "assignments.context did not return structuredContent") {
		t.Fatalf("structured ids/warnings = %+v / %+v, want text-only warning", got.StructuredIDs, got.Warnings)
	}
}

func TestProjectCairnlineSidecarAssignmentContextSmoke_NoAssignment(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "assignment-list-empty"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarAssignmentContextSmoke(t.Context(), ProjectCairnlineSidecarAssignmentContextRequest{})
	if got.Ready || got.Status != "sidecar_assignment_context_no_assignment" {
		t.Fatalf("assignment context smoke = %+v, want no assignment", got)
	}
	if got.AssignmentList == nil || got.SelectedProjectID != "proj_fixture" || got.SelectedAssignmentID != "" || !got.AssignmentList.StructuredReady || got.AssignmentList.StructuredCount != 0 {
		t.Fatalf("selection/list = project:%q assignment:%q list:%+v, want empty assignment list after project selection", got.SelectedProjectID, got.SelectedAssignmentID, got.AssignmentList)
	}
}

func TestProjectCairnlineSidecarAssignmentContextSmoke_ToolLevelError(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "context-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarAssignmentContextSmoke(t.Context(), ProjectCairnlineSidecarAssignmentContextRequest{ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if got.Ready || got.Status != "sidecar_assignment_context_tool_failed" {
		t.Fatalf("assignment context smoke = %+v, want context tool-level failure", got)
	}
	if !got.ToolIsError || !strings.Contains(got.ToolText, "fixture assignments.context failed") {
		t.Fatalf("tool result = error:%t text:%q, want fixture context tool error", got.ToolIsError, got.ToolText)
	}
}

func TestProjectCairnlineSidecarLaunchPacketSmoke_SelectsAssignmentFromStructuredLists(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLaunchPacketSmoke(t.Context(), ProjectCairnlineSidecarLaunchPacketRequest{})
	if !got.Ready || got.Status != "sidecar_launch_packet_ready" || !got.StructuredReady {
		t.Fatalf("launch packet smoke = %+v, want structured ready", got)
	}
	if got.SelectedProjectID != "proj_fixture" || got.SelectedProjectSource != "projects.list" || got.SelectedAssignmentID != "asg_fixture" || got.SelectedAssignmentSource != "assignments.list" {
		t.Fatalf("selection = project %q/%q assignment %q/%q, want list-selected ids", got.SelectedProjectID, got.SelectedProjectSource, got.SelectedAssignmentID, got.SelectedAssignmentSource)
	}
	if got.ProjectList == nil || got.AssignmentList == nil || !got.ProjectList.StructuredReady || got.ProjectList.StructuredCount != 1 || !got.AssignmentList.StructuredReady || got.AssignmentList.StructuredCount != 1 {
		t.Fatalf("list readiness = projects %+v assignments %+v, want typed selection lists", got.ProjectList, got.AssignmentList)
	}
	if got.StructuredIDs.LaunchPacketID != "launch_fixture" || got.StructuredIDs.Kind != "assignment_launch_packet" || got.StructuredIDs.ProjectID != "proj_fixture" || got.StructuredIDs.AssignmentID != "asg_fixture" || got.StructuredIDs.WorkItemID != "work_fixture" || got.StructuredIDs.RoleID != "role_fixture" {
		t.Fatalf("structured ids = %+v, want launch/project/assignment/work/role ids", got.StructuredIDs)
	}
	if got.StructuredIDs.ProfileID != "profile_fixture" || got.StructuredIDs.ExecutionProfileID != "exec_fixture" {
		t.Fatalf("structured profile ids = %+v, want profile/execution ids", got.StructuredIDs)
	}
	if got.StructuredCounts.Skills != 1 || got.StructuredCounts.Artifacts != 1 || got.StructuredCounts.Evidence != 1 || got.StructuredCounts.Reviews != 1 || got.StructuredCounts.Handoffs != 1 || got.StructuredCounts.Memory != 1 || got.StructuredCounts.MemoryCandidates != 1 || got.StructuredCounts.Warnings != 1 {
		t.Fatalf("structured counts = %+v, want one item in every launch-packet bucket", got.StructuredCounts)
	}
	if len(got.StructuredWarnings) != 1 || got.StructuredWarnings[0] != "fixture warning" {
		t.Fatalf("structured warnings = %+v, want fixture warning", got.StructuredWarnings)
	}
	if !strings.Contains(got.ToolText, "Launch packet launch_fixture") {
		t.Fatalf("tool text = %q, want launch packet evidence", got.ToolText)
	}
}

func TestProjectCairnlineSidecarLaunchPacketSmoke_UsesRequestedIDs(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLaunchPacketSmoke(t.Context(), ProjectCairnlineSidecarLaunchPacketRequest{ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if !got.Ready || got.Status != "sidecar_launch_packet_ready" || !got.StructuredReady {
		t.Fatalf("launch packet smoke = %+v, want structured ready", got)
	}
	if got.SelectedProjectID != "proj_requested" || got.SelectedProjectSource != "request" || got.SelectedAssignmentID != "asg_requested" || got.SelectedAssignmentSource != "request" {
		t.Fatalf("selection = project %q/%q assignment %q/%q, want request ids", got.SelectedProjectID, got.SelectedProjectSource, got.SelectedAssignmentID, got.SelectedAssignmentSource)
	}
	if got.ProjectList != nil || got.AssignmentList != nil {
		t.Fatalf("selection lists = projects %+v assignments %+v, want skipped lists for explicit ids", got.ProjectList, got.AssignmentList)
	}
	if got.StructuredIDs.ProjectID != "proj_requested" || got.StructuredIDs.AssignmentID != "asg_requested" {
		t.Fatalf("structured ids = %+v, want requested project/assignment ids", got.StructuredIDs)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(encoded), "project_list") || strings.Contains(string(encoded), "assignment_list") {
		t.Fatalf("encoded response = %s, want omitted selection lists for explicit ids", encoded)
	}
}

func TestProjectCairnlineSidecarLaunchPacketSmoke_TextOnlyWarns(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "text-only"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLaunchPacketSmoke(t.Context(), ProjectCairnlineSidecarLaunchPacketRequest{ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if !got.Ready || got.Status != "sidecar_launch_packet_ready" || got.StructuredReady {
		t.Fatalf("launch packet smoke = %+v, want ready with structured warning", got)
	}
	if got.StructuredIDs.LaunchPacketID != "" || !strings.Contains(strings.Join(got.Warnings, "\n"), "assignments.launch_packet did not return structuredContent") {
		t.Fatalf("structured ids/warnings = %+v / %+v, want text-only warning", got.StructuredIDs, got.Warnings)
	}
}

func TestProjectCairnlineSidecarLaunchPacketSmoke_NoAssignment(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "assignment-list-empty"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLaunchPacketSmoke(t.Context(), ProjectCairnlineSidecarLaunchPacketRequest{})
	if got.Ready || got.Status != "sidecar_launch_packet_no_assignment" {
		t.Fatalf("launch packet smoke = %+v, want no assignment", got)
	}
	if got.AssignmentList == nil || got.SelectedProjectID != "proj_fixture" || got.SelectedAssignmentID != "" || !got.AssignmentList.StructuredReady || got.AssignmentList.StructuredCount != 0 {
		t.Fatalf("selection/list = project:%q assignment:%q list:%+v, want empty assignment list after project selection", got.SelectedProjectID, got.SelectedAssignmentID, got.AssignmentList)
	}
}

func TestProjectCairnlineSidecarLaunchPacketSmoke_ToolLevelError(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "launch-packet-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLaunchPacketSmoke(t.Context(), ProjectCairnlineSidecarLaunchPacketRequest{ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if got.Ready || got.Status != "sidecar_launch_packet_tool_failed" {
		t.Fatalf("launch packet smoke = %+v, want launch-packet tool-level failure", got)
	}
	if !got.ToolIsError || !strings.Contains(got.ToolText, "fixture assignments.launch_packet failed") {
		t.Fatalf("tool result = error:%t text:%q, want fixture launch-packet tool error", got.ToolIsError, got.ToolText)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_RequiresConfirmation(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{})
	if got.Ready || got.Status != "sidecar_lifecycle_confirmation_required" || got.ConfirmedMutation {
		t.Fatalf("lifecycle smoke = %+v, want confirmation-required without mutation", got)
	}
	if len(got.Steps) != 0 || got.ClientCacheEntries != 0 {
		t.Fatalf("steps/cache = %d/%d, want no sidecar calls before confirmation", len(got.Steps), got.ClientCacheEntries)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_SelectsNextAssignmentAndCompletes(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{ConfirmMutation: true})
	if !got.Ready || got.Status != "sidecar_lifecycle_ready" {
		t.Fatalf("lifecycle smoke = %+v, want ready", got)
	}
	if got.SelectedProjectID != "proj_fixture" || got.SelectedProjectSource != "projects.list" || got.SelectedAssignmentID != "asg_fixture" || got.SelectedAssignmentSource != "assignments.next" {
		t.Fatalf("selection = project %q/%q assignment %q/%q, want next-selected ids", got.SelectedProjectID, got.SelectedProjectSource, got.SelectedAssignmentID, got.SelectedAssignmentSource)
	}
	if got.NextAssignmentList == nil || !got.NextAssignmentList.StructuredReady || got.NextAssignmentList.StructuredCount != 1 {
		t.Fatalf("next assignment list = %+v, want one typed compatible assignment", got.NextAssignmentList)
	}
	if len(got.Steps) != 7 {
		t.Fatalf("steps = %+v, want claim/context/running/context/launch/complete/context flow", got.Steps)
	}
	if got.Steps[0].Name != "claim" || got.Steps[2].Name != "mark_running" || got.Steps[4].Name != "launch_packet" || got.Steps[5].Name != "complete" || got.Steps[6].Name != "context_after_complete" {
		t.Fatalf("step names = %+v, want lifecycle order", got.Steps)
	}
	if !got.LaunchPacketReady || got.LaunchPacketIDs.AssignmentID != "asg_fixture" || got.LaunchPacketCounts.MemoryCandidates != 1 {
		t.Fatalf("launch packet summary = ready:%t ids:%+v counts:%+v, want typed launch packet", got.LaunchPacketReady, got.LaunchPacketIDs, got.LaunchPacketCounts)
	}
	if got.FinalAssignment.ID != "asg_fixture" || got.FinalAssignment.Status != "completed" || got.FinalAssignment.ExecutionRef != "hecate-sidecar-smoke" {
		t.Fatalf("final assignment = %+v, want completed assignment with smoke execution ref", got.FinalAssignment)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_UsesRequestedIDs(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{
		ProjectID:        "proj_requested",
		AssignmentID:     "asg_requested",
		ConfirmMutation:  true,
		ClaimedBy:        "agent-requested",
		ExecutionRef:     "run-requested",
		CompletionStatus: "awaiting_review",
	})
	if !got.Ready || got.Status != "sidecar_lifecycle_ready" {
		t.Fatalf("lifecycle smoke = %+v, want ready", got)
	}
	if got.SelectedProjectID != "proj_requested" || got.SelectedProjectSource != "request" || got.SelectedAssignmentID != "asg_requested" || got.SelectedAssignmentSource != "request" {
		t.Fatalf("selection = project %q/%q assignment %q/%q, want request ids", got.SelectedProjectID, got.SelectedProjectSource, got.SelectedAssignmentID, got.SelectedAssignmentSource)
	}
	if got.ProjectList != nil || got.NextAssignmentList != nil {
		t.Fatalf("selection lists = projects %+v next %+v, want skipped lists for explicit ids", got.ProjectList, got.NextAssignmentList)
	}
	if got.FinalAssignment.Status != "awaiting_review" || got.FinalAssignment.ClaimedBy != "agent-requested" || got.FinalAssignment.ExecutionRef != "run-requested" {
		t.Fatalf("final assignment = %+v, want requested lifecycle metadata", got.FinalAssignment)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_NoCompatibleAssignment(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "assignment-list-empty"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{ConfirmMutation: true})
	if got.Ready || got.Status != "sidecar_lifecycle_no_assignment" {
		t.Fatalf("lifecycle smoke = %+v, want no compatible assignment", got)
	}
	if got.NextAssignmentList == nil || !got.NextAssignmentList.StructuredReady || got.NextAssignmentList.StructuredCount != 0 {
		t.Fatalf("next assignment list = %+v, want empty typed next result", got.NextAssignmentList)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_ToolLevelError(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "claim-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{ConfirmMutation: true, ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if got.Ready || got.Status != "sidecar_lifecycle_tool_failed" {
		t.Fatalf("lifecycle smoke = %+v, want tool-level failure", got)
	}
	if len(got.Steps) != 1 || got.Steps[0].Tool != "assignments.claim" || !got.Steps[0].ToolIsError {
		t.Fatalf("steps = %+v, want failed claim step only", got.Steps)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_ReleasesEarlyClaimWhenFailureFollowsMutation(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "update-status-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{ConfirmMutation: true, ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if got.Ready || got.Status != "sidecar_lifecycle_tool_failed" {
		t.Fatalf("lifecycle smoke = %+v, want tool-level failure after mutation", got)
	}
	if len(got.Steps) != 4 || got.Steps[0].Status != "ready" || got.Steps[2].Tool != "assignments.update_status" || !got.Steps[2].ToolIsError || got.Steps[3].Tool != "assignments.release" || got.Steps[3].Status != "ready" {
		t.Fatalf("steps = %+v, want claim/context ready, update_status failure, then release cleanup", got.Steps)
	}
	warnings := strings.Join(got.Warnings, "\n")
	if !strings.Contains(warnings, "released the standalone Cairnline sidecar assignment") || strings.Contains(warnings, "may have been mutated") {
		t.Fatalf("warnings = %+v, want release cleanup warning without unresolved mutation warning", got.Warnings)
	}
}

func TestProjectCairnlineSidecarWriteSmoke_RequiresConfirmation(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarWriteSmoke(t.Context(), ProjectCairnlineSidecarWriteRequest{ProjectName: "Fixture write smoke"})
	if got.Ready || got.Status != "sidecar_write_confirmation_required" || got.ConfirmedMutation {
		t.Fatalf("write smoke = %+v, want confirmation-required without mutation", got)
	}
	if len(got.Steps) != 0 || got.ClientCacheEntries != 0 {
		t.Fatalf("steps/cache = %d/%d, want no sidecar calls before confirmation", len(got.Steps), got.ClientCacheEntries)
	}
}

func TestProjectCairnlineSidecarWriteSmoke_CreatesUpdatesDeletesTemporaryProject(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "full"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarWriteSmoke(t.Context(), ProjectCairnlineSidecarWriteRequest{
		ConfirmMutation: true,
		ProjectName:     "Fixture write smoke",
	})
	if !got.Ready || got.Status != "sidecar_write_ready" || !got.CleanupVerified {
		t.Fatalf("write smoke = %+v, want ready with verified cleanup", got)
	}
	if got.SelectedProjectID == "" || got.CreatedProject.Name != "Fixture write smoke" || got.UpdatedProject.Name != "Fixture write smoke updated" {
		t.Fatalf("project ids = selected:%q created:%+v updated:%+v, want named temporary project", got.SelectedProjectID, got.CreatedProject, got.UpdatedProject)
	}
	if len(got.Steps) != 6 {
		t.Fatalf("steps = %+v, want create/list/update/get/delete/get flow", got.Steps)
	}
	if got.Steps[0].Tool != "projects.create" || got.Steps[1].Tool != "projects.list" || got.Steps[2].Tool != "projects.update" || got.Steps[3].Tool != "projects.get" || got.Steps[4].Tool != "projects.delete" || got.Steps[5].Status != "expected_missing" {
		t.Fatalf("steps = %+v, want write smoke order and missing-after-delete verification", got.Steps)
	}
}

func TestProjectCairnlineSidecarWriteSmoke_CleansUpAfterUpdateFailure(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "project-update-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarWriteSmoke(t.Context(), ProjectCairnlineSidecarWriteRequest{
		ConfirmMutation: true,
		ProjectName:     "Fixture write smoke cleanup",
	})
	if got.Ready || got.Status != "sidecar_write_tool_failed" || !got.CleanupVerified {
		t.Fatalf("write smoke = %+v, want update tool failure with verified cleanup", got)
	}
	if len(got.Steps) != 5 || got.Steps[2].Tool != "projects.update" || !got.Steps[2].ToolIsError || got.Steps[3].Name != "cleanup_delete" || got.Steps[4].Status != "expected_missing" {
		t.Fatalf("steps = %+v, want update failure followed by cleanup delete and verification", got.Steps)
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "deleted and verified removal") {
		t.Fatalf("warnings = %+v, want verified cleanup warning", got.Warnings)
	}
}

func TestProjectCairnlineSidecarLifecycleSmoke_WarnsWhenFailureAfterRunningCannotRelease(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			CairnlineConnector:           "sidecar",
			CairnlineSidecarCommand:      os.Args[0],
			CairnlineSidecarArgs:         []string{cairnlineSidecarFixtureArgPrefix + "launch-packet-tool-error"},
			CairnlineSidecarProbeTimeout: 5 * time.Second,
		},
	}, quietLogger(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })

	got := handler.projectCairnlineSidecarLifecycleSmoke(t.Context(), ProjectCairnlineSidecarLifecycleRequest{ConfirmMutation: true, ProjectID: "proj_requested", AssignmentID: "asg_requested"})
	if got.Ready || got.Status != "sidecar_lifecycle_tool_failed" {
		t.Fatalf("lifecycle smoke = %+v, want tool-level failure after running", got)
	}
	if len(got.Steps) != 5 || got.Steps[2].Tool != "assignments.update_status" || got.Steps[2].Status != "ready" || got.Steps[4].Tool != "assignments.launch_packet" || !got.Steps[4].ToolIsError {
		t.Fatalf("steps = %+v, want running state then launch_packet failure", got.Steps)
	}
	for _, step := range got.Steps {
		if step.Tool == "assignments.release" {
			t.Fatalf("steps = %+v, did not expect release after running", got.Steps)
		}
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "may have been mutated") {
		t.Fatalf("warnings = %+v, want unresolved mutation warning after running failure", got.Warnings)
	}
}

func projectCairnlineSidecarCoordinationTestList(items []ProjectCairnlineSidecarCoordinationListResult, tool string) (ProjectCairnlineSidecarCoordinationListResult, bool) {
	for _, item := range items {
		if item.Tool == tool {
			return item, true
		}
	}
	return ProjectCairnlineSidecarCoordinationListResult{}, false
}
