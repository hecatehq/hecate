package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hecatehq/hecate/internal/sandbox"
)

type providerRuntimeContextKey struct{}

type providerRuntime struct {
	env []string
}

func prepareProviderRuntime(ctx context.Context) (context.Context, func(), error) {
	base := ""
	if runtime.GOOS != "windows" {
		// bwrap replaces /tmp with a private writable tmpfs. Keeping the host
		// placeholder under the same path makes cache variables valid both
		// inside that namespace and on wrapper-less Unix hosts.
		base = "/tmp"
	}
	directory, err := os.MkdirTemp(base, "hecate-codeintel-runtime-")
	if err != nil {
		return ctx, func() {}, err
	}
	env := buildProviderEnv(
		sandbox.SanitizedEnv(),
		directory,
		sandbox.DetectWrapper(ctx),
		runtime.GOOS,
		os.LookupEnv,
	)
	runtimeContext := context.WithValue(ctx, providerRuntimeContextKey{}, providerRuntime{env: env})
	return runtimeContext, func() { _ = os.RemoveAll(directory) }, nil
}

func providerProcessEnv(ctx context.Context) []string {
	if value, ok := ctx.Value(providerRuntimeContextKey{}).(providerRuntime); ok {
		return append([]string(nil), value.env...)
	}
	return sandbox.SanitizedEnv()
}

func buildProviderEnv(base []string, runtimeDirectory string, wrapper sandbox.WrapperKind, goos string, lookup func(string) (string, bool)) []string {
	env := append([]string(nil), base...)
	originalHome, _ := providerEnvValue(env, "HOME", goos)
	if goos == "windows" {
		if profile, ok := lookup("USERPROFILE"); ok && strings.TrimSpace(profile) != "" {
			originalHome = profile
		}
		// These are OS runtime locations, not credentials. User-writable
		// profile/cache locations are replaced with the private runtime below.
		for _, key := range []string{
			"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT",
		} {
			if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
				env = setProviderEnv(env, key, value, goos)
			}
		}
	}

	temporaryDirectory := runtimeDirectory
	runtimeHome := runtimeDirectory
	if wrapper == sandbox.WrapperBwrap {
		// The per-process bwrap tmpfs always contains /tmp, while the host
		// placeholder itself is hidden. Cache subdirectories are created by
		// the provider inside that private namespace.
		temporaryDirectory = "/tmp"
		runtimeHome = "/tmp"
	}
	if value, ok := lookup("GOPATH"); ok && strings.TrimSpace(value) != "" {
		env = setProviderEnv(env, "GOPATH", value, goos)
	} else if strings.TrimSpace(originalHome) != "" {
		env = setProviderEnv(env, "GOPATH", filepath.Join(originalHome, "go"), goos)
	}
	if value, ok := lookup("GOMODCACHE"); ok && strings.TrimSpace(value) != "" {
		env = setProviderEnv(env, "GOMODCACHE", value, goos)
	}
	if value, ok := lookup("VOLTA_HOME"); ok && strings.TrimSpace(value) != "" {
		env = setProviderEnv(env, "VOLTA_HOME", value, goos)
	} else if strings.TrimSpace(originalHome) != "" {
		env = setProviderEnv(env, "VOLTA_HOME", filepath.Join(originalHome, ".volta"), goos)
	}
	env = setProviderEnv(env, "HOME", runtimeHome, goos)
	if goos == "windows" {
		env = setProviderEnv(env, "USERPROFILE", runtimeDirectory, goos)
		env = setProviderEnv(env, "LOCALAPPDATA", filepath.Join(runtimeDirectory, "local"), goos)
		env = setProviderEnv(env, "APPDATA", filepath.Join(runtimeDirectory, "roaming"), goos)
		volume := filepath.VolumeName(runtimeDirectory)
		if volume != "" {
			env = setProviderEnv(env, "HOMEDRIVE", volume, goos)
			env = setProviderEnv(env, "HOMEPATH", strings.TrimPrefix(runtimeDirectory, volume), goos)
		}
	}
	for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
		env = setProviderEnv(env, key, temporaryDirectory, goos)
	}
	env = setProviderEnv(env, "GOCACHE", filepath.Join(runtimeDirectory, "go-build"), goos)
	env = setProviderEnv(env, "GOPLSCACHE", filepath.Join(runtimeDirectory, "gopls"), goos)
	env = setProviderEnv(env, "XDG_CACHE_HOME", filepath.Join(runtimeDirectory, "xdg"), goos)
	env = setProviderEnv(env, "GOTELEMETRY", "off", goos)
	return env
}

func setProviderEnv(env []string, key, value, goos string) []string {
	prefix := key + "="
	for index, item := range env {
		itemKey, _, _ := strings.Cut(item, "=")
		match := itemKey == key
		if goos == "windows" {
			match = strings.EqualFold(itemKey, key)
		}
		if match {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func providerEnvValue(env []string, key, goos string) (string, bool) {
	for _, item := range env {
		itemKey, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		match := itemKey == key
		if goos == "windows" {
			match = strings.EqualFold(itemKey, key)
		}
		if match {
			return value, true
		}
	}
	return "", false
}
