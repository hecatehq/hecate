package codeintel

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/sandbox"
)

func TestBuildProviderEnv_BwrapUsesPrivateWritableRuntime(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"GOPATH":     "/srv/go",
		"GOMODCACHE": "/srv/go/pkg/mod",
		"VOLTA_HOME": "/srv/volta",
	})
	env := buildProviderEnv(
		[]string{"PATH=/usr/bin", "HOME=/srv/operator", "LANG=en_US.UTF-8"},
		"/tmp/hecate-codeintel-runtime-test",
		sandbox.WrapperBwrap,
		"linux",
		lookup,
	)

	assertProviderEnv(t, env, "HOME", "/tmp", "linux")
	assertProviderEnv(t, env, "TMPDIR", "/tmp", "linux")
	assertProviderEnv(t, env, "GOPATH", "/srv/go", "linux")
	assertProviderEnv(t, env, "GOMODCACHE", "/srv/go/pkg/mod", "linux")
	assertProviderEnv(t, env, "VOLTA_HOME", "/srv/volta", "linux")
	assertProviderEnv(t, env, "GOCACHE", "/tmp/hecate-codeintel-runtime-test/go-build", "linux")
	assertProviderEnv(t, env, "GOPLSCACHE", "/tmp/hecate-codeintel-runtime-test/gopls", "linux")
	assertProviderEnv(t, env, "XDG_CACHE_HOME", "/tmp/hecate-codeintel-runtime-test/xdg", "linux")
	assertProviderEnv(t, env, "GOTELEMETRY", "off", "linux")
}

func TestBuildProviderEnv_WrapperlessUnixReplacesWritableHome(t *testing.T) {
	env := buildProviderEnv(
		[]string{"PATH=/usr/bin", "HOME=/Users/operator", "TMPDIR=/private/tmp"},
		"/tmp/hecate-codeintel-runtime-test",
		sandbox.WrapperNone,
		"darwin",
		mapLookup(nil),
	)

	assertProviderEnv(t, env, "HOME", "/tmp/hecate-codeintel-runtime-test", "darwin")
	assertProviderEnv(t, env, "TMPDIR", "/tmp/hecate-codeintel-runtime-test", "darwin")
	assertProviderEnv(t, env, "GOPATH", "/Users/operator/go", "darwin")
	assertProviderEnv(t, env, "GOCACHE", "/tmp/hecate-codeintel-runtime-test/go-build", "darwin")
}

func TestBuildProviderEnv_PreservesExplicitToolLocationsWithoutHome(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"GOPATH":     "/srv/go",
		"GOMODCACHE": "/srv/modules",
		"VOLTA_HOME": "/srv/volta",
	})
	env := buildProviderEnv(
		[]string{"PATH=/usr/bin", "LANG=en_US.UTF-8"},
		"/tmp/hecate-codeintel-runtime-test",
		sandbox.WrapperNone,
		"linux",
		lookup,
	)

	assertProviderEnv(t, env, "HOME", "/tmp/hecate-codeintel-runtime-test", "linux")
	assertProviderEnv(t, env, "GOPATH", "/srv/go", "linux")
	assertProviderEnv(t, env, "GOMODCACHE", "/srv/modules", "linux")
	assertProviderEnv(t, env, "VOLTA_HOME", "/srv/volta", "linux")
}

func TestBuildProviderEnv_WindowsReplacesProfileAndPreservesSystemRuntime(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"USERPROFILE": `C:\Users\operator`,
		"GOPATH":      `D:\go`,
		"SYSTEMROOT":  `C:\Windows`,
		"WINDIR":      `C:\Windows`,
		"COMSPEC":     `C:\Windows\System32\cmd.exe`,
		"PATHEXT":     `.COM;.EXE`,
	})
	env := buildProviderEnv(
		[]string{"Path=C:\\bin", "home=C:\\Users\\operator", "TEMP=C:\\old"},
		`C:\runtime`,
		sandbox.WrapperNone,
		"windows",
		lookup,
	)

	assertProviderEnv(t, env, "HOME", `C:\runtime`, "windows")
	assertProviderEnv(t, env, "USERPROFILE", `C:\runtime`, "windows")
	assertProviderEnv(t, env, "TEMP", `C:\runtime`, "windows")
	assertProviderEnv(t, env, "GOPATH", `D:\go`, "windows")
	assertProviderEnv(t, env, "SYSTEMROOT", `C:\Windows`, "windows")
	if countProviderEnv(env, "HOME", "windows") != 1 {
		t.Fatalf("HOME entries = %v, want one case-insensitive replacement", env)
	}
}

func TestPrepareProviderRuntime_CleanupRemovesPrivateDirectory(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()

	ctx, cleanup, err := prepareProviderRuntime(context.Background())
	if err != nil {
		t.Fatalf("prepare provider runtime: %v", err)
	}
	home, ok := providerEnvValue(providerProcessEnv(ctx), "HOME", runtime.GOOS)
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

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func assertProviderEnv(t *testing.T, env []string, key, want, goos string) {
	t.Helper()
	got, ok := providerEnvValue(env, key, goos)
	if !ok || got != want {
		t.Fatalf("%s = %q, %v; want %q, true (env %v)", key, got, ok, want, env)
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
