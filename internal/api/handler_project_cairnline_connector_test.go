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
	if !containsString(got.MissingTools, "projects.get") || !containsString(got.MissingTools, "assignments.context") || !containsString(got.MissingTools, "assistant.propose") {
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
	if !containsString(got.MissingTools, "projects.get") || !containsString(got.MissingTools, "assignments.context") || !containsString(got.MissingTools, "assistant.propose") {
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

func projectCairnlineSidecarCoordinationTestList(items []ProjectCairnlineSidecarCoordinationListResult, tool string) (ProjectCairnlineSidecarCoordinationListResult, bool) {
	for _, item := range items {
		if item.Tool == tool {
			return item, true
		}
	}
	return ProjectCairnlineSidecarCoordinationListResult{}, false
}
