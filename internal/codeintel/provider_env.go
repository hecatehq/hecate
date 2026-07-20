package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

type providerRuntimeContextKey struct{}

type providerRuntime struct {
	mu                   sync.Mutex
	baseEnv              []string
	options              providerEnvOptions
	trustedProviderPaths map[string]string
	providerEnv          map[string][]string
}

type providerEnvOptions struct {
	runtimeDirectory    string
	workspaceRoot       string
	provider            string
	trustedProviderPath string
	wrapper             sandbox.WrapperKind
	goos                string
	lookup              func(string) (string, bool)
}

type providerEnvironmentTrust struct {
	workspaceRoot string
	projectRoot   string
	home          string
}

func prepareProviderRuntime(ctx context.Context, workspaceRoot string, trustedProviderPaths map[string]string) (context.Context, func(), error) {
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
	baseEnv := sandbox.SanitizedEnv()
	wrapper := sandbox.DetectWrapper(ctx)
	options := providerEnvOptions{
		runtimeDirectory: directory,
		workspaceRoot:    workspaceRoot,
		wrapper:          wrapper,
		goos:             runtime.GOOS,
		lookup:           os.LookupEnv,
	}
	trustedPaths := make(map[string]string, len(trustedProviderPaths))
	for provider, path := range trustedProviderPaths {
		trustedPaths[provider] = path
	}
	runtimeValue := &providerRuntime{
		baseEnv:              baseEnv,
		options:              options,
		trustedProviderPaths: trustedPaths,
		providerEnv:          make(map[string][]string),
	}
	runtimeContext := context.WithValue(ctx, providerRuntimeContextKey{}, runtimeValue)
	return runtimeContext, func() { _ = os.RemoveAll(directory) }, nil
}

func providerProcessEnv(ctx context.Context, provider string) []string {
	if value, ok := ctx.Value(providerRuntimeContextKey{}).(*providerRuntime); ok {
		value.mu.Lock()
		defer value.mu.Unlock()
		if env, exists := value.providerEnv[provider]; exists {
			return append([]string(nil), env...)
		}
		options := value.options
		options.provider = provider
		options.trustedProviderPath = value.trustedProviderPaths[provider]
		env := buildProviderEnv(value.baseEnv, options)
		value.providerEnv[provider] = env
		return append([]string(nil), env...)
	}
	// A nil exec.Cmd environment inherits the gateway environment. Return an
	// explicit empty environment when a caller has skipped runtime preparation
	// so an internal misuse cannot restore the unsafe inherited PATH.
	return []string{}
}

func (s *Service) trustedConfiguredProviderPaths(fsys *workspacefs.FS) map[string]string {
	trusted := make(map[string]string)
	if s == nil || fsys == nil {
		return trusted
	}
	for provider := range s.providerPaths {
		path, err := s.resolveTrustedBinary(fsys, provider)
		if err == nil {
			trusted[provider] = path
		}
	}
	return trusted
}

func buildProviderEnv(base []string, options providerEnvOptions) []string {
	env := append([]string(nil), base...)
	trust := newProviderEnvironmentTrust(options.workspaceRoot)
	lookup := options.lookup
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	originalHome, _ := providerEnvValue(env, "HOME", options.goos)
	if options.goos == "windows" {
		if profile, ok := lookup("USERPROFILE"); ok && strings.TrimSpace(profile) != "" {
			originalHome = profile
		}
		// These are OS runtime locations, not credentials. User-writable
		// profile/cache locations are replaced with the private runtime below.
		for _, key := range []string{
			"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT",
		} {
			if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
				env = setProviderEnv(env, key, value, options.goos)
			}
		}
	}

	for _, key := range []string{"PATH", "GOPATH", "GOMODCACHE", "VOLTA_HOME"} {
		env = removeProviderEnv(env, key, options.goos)
	}
	if pathValue, ok := providerEnvValue(base, "PATH", options.goos); ok {
		if trustedPath := filterProviderPath(pathValue, trust, options.trustedProviderPath, options.goos); trustedPath != "" {
			env = setProviderEnv(env, "PATH", trustedPath, options.goos)
		}
	} else if trustedPath := filterProviderPath("", trust, options.trustedProviderPath, options.goos); trustedPath != "" {
		env = setProviderEnv(env, "PATH", trustedPath, options.goos)
	}

	temporaryDirectory := options.runtimeDirectory
	runtimeHome := options.runtimeDirectory
	if options.wrapper == sandbox.WrapperBwrap {
		// The per-process bwrap tmpfs always contains /tmp, while the host
		// placeholder itself is hidden. Cache subdirectories are created by
		// the provider inside that private namespace.
		temporaryDirectory = "/tmp"
		runtimeHome = "/tmp"
	}
	switch options.provider {
	case "gopls":
		goPath := ""
		if value, ok := lookup("GOPATH"); ok {
			goPath = filterProviderDirectoryList(value, trust, options.goos)
		} else if strings.TrimSpace(originalHome) != "" {
			goPath = filterProviderDirectoryList(filepath.Join(originalHome, "go"), trust, options.goos)
		}
		if goPath != "" {
			env = setProviderEnv(env, "GOPATH", goPath, options.goos)
		}
		if value, ok := lookup("GOMODCACHE"); ok {
			if directory, trusted := trustedProviderDirectoryWithTrust(value, trust, false); trusted {
				env = setProviderEnv(env, "GOMODCACHE", directory, options.goos)
			}
		}
	case "tsc":
		voltaHome := ""
		if value, ok := lookup("VOLTA_HOME"); ok && strings.TrimSpace(value) != "" {
			voltaHome = value
		} else if strings.TrimSpace(originalHome) != "" {
			voltaHome = filepath.Join(originalHome, ".volta")
		}
		if voltaHome != "" {
			allowShared := configuredProviderUsesDirectory(options.trustedProviderPath, voltaHome)
			if directory, trusted := trustedProviderDirectoryWithTrust(voltaHome, trust, allowShared); trusted {
				env = setProviderEnv(env, "VOLTA_HOME", directory, options.goos)
			}
		}
	}
	env = setProviderEnv(env, "HOME", runtimeHome, options.goos)
	if options.goos == "windows" {
		env = setProviderEnv(env, "USERPROFILE", options.runtimeDirectory, options.goos)
		env = setProviderEnv(env, "LOCALAPPDATA", filepath.Join(options.runtimeDirectory, "local"), options.goos)
		env = setProviderEnv(env, "APPDATA", filepath.Join(options.runtimeDirectory, "roaming"), options.goos)
		volume := filepath.VolumeName(options.runtimeDirectory)
		if volume != "" {
			env = setProviderEnv(env, "HOMEDRIVE", volume, options.goos)
			env = setProviderEnv(env, "HOMEPATH", strings.TrimPrefix(options.runtimeDirectory, volume), options.goos)
		}
	}
	for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
		env = setProviderEnv(env, key, temporaryDirectory, options.goos)
	}
	env = setProviderEnv(env, "GOCACHE", filepath.Join(options.runtimeDirectory, "go-build"), options.goos)
	env = setProviderEnv(env, "GOPLSCACHE", filepath.Join(options.runtimeDirectory, "gopls"), options.goos)
	env = setProviderEnv(env, "XDG_CACHE_HOME", filepath.Join(options.runtimeDirectory, "xdg"), options.goos)
	env = setProviderEnv(env, "GOTELEMETRY", "off", options.goos)
	return env
}

func filterProviderPath(value string, trust providerEnvironmentTrust, trustedProviderPath, goos string) string {
	configuredDirectory, configuredCanonical, configured := trustedConfiguredProviderDirectory(trust, trustedProviderPath)
	entries := splitProviderPathList(value, goos)
	if configured {
		entries = append(entries, configuredDirectory)
	}
	trusted := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" || !filepath.IsAbs(entry) {
			continue
		}
		allowShared := configured && (samePath(entry, configuredDirectory) || samePath(entry, configuredCanonical))
		directory, ok := trustedProviderDirectoryWithTrust(entry, trust, allowShared)
		if !ok {
			continue
		}
		canonical, err := filepath.EvalSymlinks(directory)
		if err != nil {
			continue
		}
		key := filepath.Clean(canonical)
		if goos == "windows" {
			key = strings.ToLower(key)
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		trusted = append(trusted, directory)
	}
	return strings.Join(trusted, string(providerPathListSeparator(goos)))
}

func filterProviderDirectoryList(value string, trust providerEnvironmentTrust, goos string) string {
	entries := splitProviderPathList(value, goos)
	trusted := make([]string, 0, len(entries))
	for _, entry := range entries {
		if directory, ok := trustedProviderDirectoryWithTrust(entry, trust, false); ok {
			trusted = append(trusted, directory)
		}
	}
	return strings.Join(trusted, string(providerPathListSeparator(goos)))
}

func splitProviderPathList(value, goos string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, string(providerPathListSeparator(goos)))
}

func providerPathListSeparator(goos string) rune {
	if goos == "windows" {
		return ';'
	}
	return ':'
}

func trustedProviderDirectory(value, workspaceRoot string, allowSharedAncestor bool) (string, bool) {
	return trustedProviderDirectoryWithTrust(value, newProviderEnvironmentTrust(workspaceRoot), allowSharedAncestor)
}

func trustedProviderDirectoryWithTrust(value string, trust providerEnvironmentTrust, allowSharedAncestor bool) (string, bool) {
	if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) {
		return "", false
	}
	directory := filepath.Clean(value)
	if pathWithinRoot(trust.projectRoot, directory) || pathWithinRoot(directory, trust.projectRoot) {
		return "", false
	}
	if !allowSharedAncestor && trust.sharesUntrustedWorkspaceAncestor(directory) {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", false
	}
	if pathWithinRoot(trust.projectRoot, canonical) || pathWithinRoot(canonical, trust.projectRoot) {
		return "", false
	}
	if !allowSharedAncestor && trust.sharesUntrustedWorkspaceAncestor(canonical) {
		return "", false
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", false
	}
	// Providers do not need PATH-style directory aliases to preserve argv[0].
	// Passing the canonical directory prevents a validated symlink entry from
	// being retargeted between environment construction and a later child exec.
	return filepath.Clean(canonical), true
}

func trustedConfiguredProviderDirectory(trust providerEnvironmentTrust, providerPath string) (string, string, bool) {
	if strings.TrimSpace(providerPath) == "" || !filepath.IsAbs(providerPath) {
		return "", "", false
	}
	providerPath = filepath.Clean(providerPath)
	if pathWithinRoot(trust.projectRoot, providerPath) {
		return "", "", false
	}
	canonicalProvider, err := filepath.EvalSymlinks(providerPath)
	if err != nil || pathWithinRoot(trust.projectRoot, canonicalProvider) {
		return "", "", false
	}
	info, err := os.Stat(canonicalProvider)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", "", false
	}
	directory := filepath.Dir(providerPath)
	canonicalDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil || pathWithinRoot(trust.projectRoot, canonicalDirectory) {
		return "", "", false
	}
	return directory, canonicalDirectory, true
}

func canonicalProviderWorkspaceRoot(workspaceRoot string) string {
	workspaceRoot = filepath.Clean(workspaceRoot)
	if canonical, err := filepath.EvalSymlinks(workspaceRoot); err == nil {
		return filepath.Clean(canonical)
	}
	return workspaceRoot
}

func newProviderEnvironmentTrust(workspaceRoot string) providerEnvironmentTrust {
	workspaceRoot = canonicalProviderWorkspaceRoot(workspaceRoot)
	return providerEnvironmentTrust{
		workspaceRoot: workspaceRoot,
		projectRoot:   projectTrustRoot(workspaceRoot),
		home:          canonicalUserHome(),
	}
}

func (trust providerEnvironmentTrust) sharesUntrustedWorkspaceAncestor(candidate string) bool {
	candidate = filepath.Clean(candidate)
	for ancestor := filepath.Dir(trust.workspaceRoot); ; ancestor = filepath.Dir(ancestor) {
		if pathWithinRoot(ancestor, candidate) {
			return filepath.Dir(ancestor) != ancestor && (trust.home == "" || !samePath(ancestor, trust.home))
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return false
		}
	}
}

func configuredProviderUsesDirectory(providerPath, directory string) bool {
	if strings.TrimSpace(providerPath) == "" || strings.TrimSpace(directory) == "" ||
		!filepath.IsAbs(providerPath) || !filepath.IsAbs(directory) {
		return false
	}
	if !pathWithinRoot(filepath.Clean(directory), filepath.Clean(providerPath)) {
		return false
	}
	canonicalProvider, providerErr := filepath.EvalSymlinks(providerPath)
	canonicalDirectory, directoryErr := filepath.EvalSymlinks(directory)
	return providerErr == nil && directoryErr == nil && pathWithinRoot(canonicalDirectory, canonicalProvider)
}

func removeProviderEnv(env []string, key, goos string) []string {
	filtered := env[:0]
	for _, item := range env {
		itemKey, _, _ := strings.Cut(item, "=")
		match := itemKey == key
		if goos == "windows" {
			match = strings.EqualFold(itemKey, key)
		}
		if !match {
			filtered = append(filtered, item)
		}
	}
	return filtered
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
