package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
)

func TestBuildProviderEnv_BwrapUsesPrivateWritableRuntime(t *testing.T) {
	goPath := canonicalFixturePath(t, t.TempDir())
	moduleCache := filepath.Join(goPath, "pkg", "mod")
	if err := os.MkdirAll(moduleCache, 0o755); err != nil {
		t.Fatalf("create module cache: %v", err)
	}
	env := buildProviderEnv(
		[]string{"PATH=/usr/bin", "HOME=/srv/operator", "LANG=en_US.UTF-8"},
		providerEnvOptions{
			runtimeDirectory: "/tmp/hecate-codeintel-runtime-test",
			workspaceRoot:    "/workspace/project",
			provider:         "gopls",
			wrapper:          sandbox.WrapperBwrap,
			goos:             "linux",
			lookup: mapLookup(map[string]string{
				"GOPATH":     goPath,
				"GOMODCACHE": moduleCache,
			}),
		},
	)

	assertProviderEnv(t, env, "HOME", "/tmp", "linux")
	assertProviderEnv(t, env, "TMPDIR", "/tmp", "linux")
	assertProviderEnv(t, env, "GOPATH", goPath, "linux")
	assertProviderEnv(t, env, "GOMODCACHE", moduleCache, "linux")
	assertProviderEnvAbsent(t, env, "VOLTA_HOME", "linux")
	assertProviderEnv(t, env, "GOCACHE", "/tmp/hecate-codeintel-runtime-test/go-build", "linux")
	assertProviderEnv(t, env, "GOPLSCACHE", "/tmp/hecate-codeintel-runtime-test/gopls", "linux")
	assertProviderEnv(t, env, "XDG_CACHE_HOME", "/tmp/hecate-codeintel-runtime-test/xdg", "linux")
	assertProviderEnv(t, env, "GOTELEMETRY", "off", "linux")
}

func TestBuildProviderEnv_WrapperlessUnixReplacesWritableHome(t *testing.T) {
	originalHome := canonicalFixturePath(t, t.TempDir())
	goPath := filepath.Join(originalHome, "go")
	if err := os.Mkdir(goPath, 0o755); err != nil {
		t.Fatalf("create GOPATH: %v", err)
	}
	env := buildProviderEnv(
		[]string{"PATH=/usr/bin", "HOME=" + originalHome, "TMPDIR=/private/tmp"},
		providerEnvOptions{
			runtimeDirectory: "/tmp/hecate-codeintel-runtime-test",
			workspaceRoot:    "/workspace/project",
			provider:         "gopls",
			wrapper:          sandbox.WrapperNone,
			goos:             "darwin",
			lookup:           mapLookup(nil),
		},
	)

	assertProviderEnv(t, env, "HOME", "/tmp/hecate-codeintel-runtime-test", "darwin")
	assertProviderEnv(t, env, "TMPDIR", "/tmp/hecate-codeintel-runtime-test", "darwin")
	assertProviderEnv(t, env, "GOPATH", goPath, "darwin")
	assertProviderEnv(t, env, "GOCACHE", "/tmp/hecate-codeintel-runtime-test/go-build", "darwin")
}

func TestBuildProviderEnv_PreservesOnlyProviderRelevantSafeLocations(t *testing.T) {
	goPath := canonicalFixturePath(t, t.TempDir())
	moduleCache := canonicalFixturePath(t, t.TempDir())
	voltaHome := canonicalFixturePath(t, t.TempDir())
	lookup := mapLookup(map[string]string{
		"GOPATH":     goPath,
		"GOMODCACHE": moduleCache,
		"VOLTA_HOME": voltaHome,
	})
	baseOptions := providerEnvOptions{
		runtimeDirectory: "/tmp/hecate-codeintel-runtime-test",
		workspaceRoot:    "/workspace/project",
		wrapper:          sandbox.WrapperNone,
		goos:             runtime.GOOS,
		lookup:           lookup,
	}
	goplsOptions := baseOptions
	goplsOptions.provider = "gopls"
	goplsEnv := buildProviderEnv([]string{"PATH=/usr/bin", "LANG=en_US.UTF-8"}, goplsOptions)
	tscOptions := baseOptions
	tscOptions.provider = "tsc"
	tscEnv := buildProviderEnv([]string{"PATH=/usr/bin", "LANG=en_US.UTF-8"}, tscOptions)

	assertProviderEnv(t, goplsEnv, "GOPATH", goPath, runtime.GOOS)
	assertProviderEnv(t, goplsEnv, "GOMODCACHE", moduleCache, runtime.GOOS)
	assertProviderEnvAbsent(t, goplsEnv, "VOLTA_HOME", runtime.GOOS)
	assertProviderEnv(t, tscEnv, "VOLTA_HOME", voltaHome, runtime.GOOS)
	assertProviderEnvAbsent(t, tscEnv, "GOPATH", runtime.GOOS)
	assertProviderEnvAbsent(t, tscEnv, "GOMODCACHE", runtime.GOOS)
}

func TestBuildProviderEnv_WindowsReplacesProfileAndPreservesSystemRuntime(t *testing.T) {
	env := buildProviderEnv(
		[]string{"Path=C:\\bin", "home=C:\\Users\\operator", "TEMP=C:\\old"},
		providerEnvOptions{
			runtimeDirectory: `C:\runtime`,
			workspaceRoot:    `C:\workspace`,
			wrapper:          sandbox.WrapperNone,
			goos:             "windows",
			lookup: mapLookup(map[string]string{
				"USERPROFILE": `C:\Users\operator`,
				"SYSTEMROOT":  `C:\Windows`,
				"WINDIR":      `C:\Windows`,
				"COMSPEC":     `C:\Windows\System32\cmd.exe`,
				"PATHEXT":     `.COM;.EXE`,
			}),
		},
	)

	assertProviderEnv(t, env, "HOME", `C:\runtime`, "windows")
	assertProviderEnv(t, env, "USERPROFILE", `C:\runtime`, "windows")
	assertProviderEnv(t, env, "TEMP", `C:\runtime`, "windows")
	assertProviderEnv(t, env, "SYSTEMROOT", `C:\Windows`, "windows")
	if countProviderEnv(env, "HOME", "windows") != 1 {
		t.Fatalf("HOME entries = %v, want one case-insensitive replacement", env)
	}
}

func TestBuildProviderEnv_FiltersWorkspaceControlledIndirectExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix path fixture")
	}
	base := canonicalFixturePath(t, t.TempDir())
	workspace := filepath.Join(base, "project")
	workspaceBin := filepath.Join(workspace, "bin")
	sharedBin := filepath.Join(base, "tools")
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatalf("create project marker: %v", err)
	}
	if err := os.MkdirAll(workspaceBin, 0o755); err != nil {
		t.Fatalf("create workspace bin: %v", err)
	}
	if err := os.MkdirAll(sharedBin, 0o755); err != nil {
		t.Fatalf("create shared bin: %v", err)
	}
	writeExecutableFixture(t, workspaceBin, "go")
	nonDirectory := filepath.Join(base, "not-a-directory")
	if err := os.WriteFile(nonDirectory, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write non-directory PATH entry: %v", err)
	}
	escape := filepath.Join(base, "workspace-bin-link")
	if err := os.Symlink(workspaceBin, escape); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	systemBin := trustedSystemDirectory(t)
	pathValue := strings.Join([]string{"", ".", workspaceBin, sharedBin, escape, nonDirectory, systemBin}, string(os.PathListSeparator))
	env := buildProviderEnv(
		[]string{"PATH=" + pathValue, "HOME=/operator"},
		providerEnvOptions{
			runtimeDirectory: t.TempDir(),
			workspaceRoot:    workspace,
			provider:         "gopls",
			wrapper:          sandbox.WrapperNone,
			goos:             runtime.GOOS,
			lookup:           mapLookup(nil),
		},
	)

	assertProviderEnv(t, env, "PATH", systemBin, runtime.GOOS)
	if _, trusted := trustedProviderDirectory(escape, workspace, true); trusted {
		t.Fatal("symlink resolving into the project was trusted")
	}
}

func TestBuildProviderEnv_ExactProviderAllowsOnlyItsTrustedSiblingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix path fixture")
	}
	base := canonicalFixturePath(t, t.TempDir())
	workspace := filepath.Join(base, "project")
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatalf("create project marker: %v", err)
	}
	voltaHome := filepath.Join(base, "operator-tools", "volta")
	voltaBin := filepath.Join(voltaHome, "bin ")
	tsc := writeExecutableFixture(t, voltaBin, "tsc")
	systemBin := trustedSystemDirectory(t)
	baseEnv := []string{"PATH=" + strings.Join([]string{voltaBin, systemBin}, string(os.PathListSeparator))}
	lookup := mapLookup(map[string]string{"VOLTA_HOME": voltaHome})
	options := providerEnvOptions{
		runtimeDirectory:    t.TempDir(),
		workspaceRoot:       workspace,
		provider:            "tsc",
		trustedProviderPath: tsc,
		wrapper:             sandbox.WrapperNone,
		goos:                runtime.GOOS,
		lookup:              lookup,
	}
	tscEnv := buildProviderEnv(baseEnv, options)
	options.provider = "gopls"
	options.trustedProviderPath = ""
	goplsEnv := buildProviderEnv(baseEnv, options)

	assertProviderEnv(t, tscEnv, "PATH", strings.Join([]string{voltaBin, systemBin}, string(os.PathListSeparator)), runtime.GOOS)
	assertProviderEnv(t, tscEnv, "VOLTA_HOME", voltaHome, runtime.GOOS)
	assertProviderEnv(t, goplsEnv, "PATH", systemBin, runtime.GOOS)
	assertProviderEnvAbsent(t, goplsEnv, "VOLTA_HOME", runtime.GOOS)
}

func TestBuildProviderEnv_OmitsUnsafeGoAndVoltaDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix path fixture")
	}
	base := t.TempDir()
	workspace := filepath.Join(base, "project")
	unsafeGoPath := filepath.Join(workspace, "go")
	unsafeModuleCache := filepath.Join(workspace, "modules")
	unsafeVoltaHome := filepath.Join(workspace, "volta")
	for _, directory := range []string{filepath.Join(workspace, ".git"), unsafeGoPath, unsafeModuleCache, unsafeVoltaHome} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create unsafe provider directory: %v", err)
		}
	}
	systemDirectory := trustedSystemDirectory(t)
	lookup := mapLookup(map[string]string{
		"GOPATH":     strings.Join([]string{unsafeGoPath, systemDirectory}, string(os.PathListSeparator)),
		"GOMODCACHE": unsafeModuleCache,
		"VOLTA_HOME": unsafeVoltaHome,
	})
	options := providerEnvOptions{
		runtimeDirectory: t.TempDir(),
		workspaceRoot:    workspace,
		provider:         "gopls",
		wrapper:          sandbox.WrapperNone,
		goos:             runtime.GOOS,
		lookup:           lookup,
	}
	goplsEnv := buildProviderEnv([]string{"PATH=" + systemDirectory}, options)
	options.provider = "tsc"
	tscEnv := buildProviderEnv([]string{"PATH=" + systemDirectory}, options)
	ancestorProvider := writeExecutableFixture(t, filepath.Join(base, "tools"), "tsc")
	ancestorOptions := options
	ancestorOptions.trustedProviderPath = ancestorProvider
	ancestorOptions.lookup = mapLookup(map[string]string{"VOLTA_HOME": base})
	ancestorEnv := buildProviderEnv([]string{"PATH=" + systemDirectory}, ancestorOptions)

	assertProviderEnv(t, goplsEnv, "GOPATH", systemDirectory, runtime.GOOS)
	assertProviderEnvAbsent(t, goplsEnv, "GOMODCACHE", runtime.GOOS)
	assertProviderEnvAbsent(t, tscEnv, "VOLTA_HOME", runtime.GOOS)
	assertProviderEnvAbsent(t, ancestorEnv, "VOLTA_HOME", runtime.GOOS)
}

func TestService_GlobalProviderCannotResolveFakeWorkspaceGoFromPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix global provider fixture")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	workspaceBin := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatalf("create project marker: %v", err)
	}
	writeExecutableFixture(t, workspaceBin, "go")
	systemProvider := filepath.Join(trustedSystemDirectory(t), "true")
	if info, err := os.Stat(systemProvider); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Skipf("system executable fixture unavailable: %v", err)
	}
	t.Setenv("PATH", strings.Join([]string{workspaceBin, filepath.Dir(systemProvider)}, string(os.PathListSeparator)))
	runner := &recordingRunner{result: processrunner.Result{Stdout: "golang.org/x/tools/gopls v0.23.0\n"}}
	service := NewService()
	service.runner = runner
	service.lookPath = func(name string) (string, error) {
		if name == "gopls" {
			return systemProvider, nil
		}
		return "", os.ErrNotExist
	}

	if _, err := service.Query(context.Background(), workspace, Request{Operation: OpCapabilities}); err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("provider probes = %d, want 1", len(runner.requests))
	}
	assertProviderEnv(t, runner.requests[0].Env, "PATH", filepath.Dir(systemProvider), runtime.GOOS)
}

func TestPrepareProviderRuntime_CleanupRemovesPrivateDirectory(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	ctx, cleanup, err := prepareProviderRuntime(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("prepare provider runtime: %v", err)
	}
	home, ok := providerEnvValue(providerProcessEnv(ctx, "gopls"), "HOME", runtime.GOOS)
	if !ok || strings.TrimSpace(home) == "" {
		cleanup()
		t.Fatalf("provider HOME missing from runtime environment")
	}
	info, statErr := os.Stat(home)
	if statErr != nil || !info.IsDir() {
		cleanup()
		t.Fatalf("provider runtime directory %q: %v", home, statErr)
	}
	cleanup()
	if _, statErr := os.Stat(home); !os.IsNotExist(statErr) {
		t.Fatalf("provider runtime directory still exists after cleanup: %v", statErr)
	}
}

func TestProviderProcessEnv_WithoutPreparedRuntimeDoesNotInherit(t *testing.T) {
	env := providerProcessEnv(context.Background(), "gopls")
	if env == nil || len(env) != 0 {
		t.Fatalf("unprepared provider environment = %v, want explicit empty environment", env)
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func trustedSystemDirectory(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		directory := filepath.Join(os.Getenv("SYSTEMROOT"), "System32")
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			return directory
		}
		t.Skip("system executable directory unavailable")
	}
	for _, directory := range []string{"/usr/bin", "/bin"} {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			return directory
		}
	}
	t.Skip("system executable directory unavailable")
	return ""
}

func writeExecutableFixture(t *testing.T, directory, name string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create executable fixture directory: %v", err)
	}
	path := filepath.Join(directory, name)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func canonicalFixturePath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize fixture path %q: %v", path, err)
	}
	return canonical
}

func assertProviderEnv(t *testing.T, env []string, key, want, goos string) {
	t.Helper()
	got, ok := providerEnvValue(env, key, goos)
	if !ok || got != want {
		t.Fatalf("%s = %q, %v; want %q, true (env %v)", key, got, ok, want, env)
	}
}

func assertProviderEnvAbsent(t *testing.T, env []string, key, goos string) {
	t.Helper()
	if value, ok := providerEnvValue(env, key, goos); ok {
		t.Fatalf("%s = %q, want omitted (env %v)", key, value, env)
	}
}

func countProviderEnv(env []string, key, goos string) int {
	count := 0
	for _, item := range env {
		itemKey, _, _ := strings.Cut(item, "=")
		if itemKey == key || goos == "windows" && strings.EqualFold(itemKey, key) {
			count++
		}
	}
	return count
}
