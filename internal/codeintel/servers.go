package codeintel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

type serverSpec struct {
	language   string
	provider   string
	command    string
	args       []string
	extensions map[string]struct{}
	minMajor   int
	probeArgs  []string
}

func defaultServers() map[string][]serverSpec {
	return map[string][]serverSpec{
		"go": {{
			language: "go", command: "gopls", args: []string{"serve"}, probeArgs: []string{"version"}, extensions: extensionSet(".go"),
		}},
		"typescript": {{
			language: "typescript", command: "tsc", args: []string{"--lsp", "--stdio"}, probeArgs: []string{"--version"}, minMajor: 7,
			extensions: extensionSet(".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"),
		}},
	}
}

func extensionSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func normalizeLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "go", "golang":
		return "go"
	case "typescript", "ts", "tsx", "javascript", "js", "jsx":
		return "typescript"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (s *Service) selectServer(ctx context.Context, fsys *workspacefs.FS, request Request) (serverSpec, string, error) {
	language := request.Language
	if request.Path != "" {
		ext := strings.ToLower(filepath.Ext(request.Path))
		inferred := ""
		for candidate, specs := range s.servers {
			for _, spec := range specs {
				if _, ok := spec.extensions[ext]; ok {
					inferred = candidate
					break
				}
			}
			if inferred != "" {
				break
			}
		}
		if inferred == "" {
			return serverSpec{}, "", fmt.Errorf("no allowlisted language server supports %q", ext)
		}
		if language != "" && language != inferred {
			return serverSpec{}, "", fmt.Errorf("language %q does not match path %q", request.Language, request.Path)
		}
		language = inferred
	}
	if language == "" {
		return serverSpec{}, "", fmt.Errorf("language is required when path is omitted")
	}
	specs := s.servers[language]
	if len(specs) == 0 {
		return serverSpec{}, "", fmt.Errorf("language %q has no allowlisted language server", language)
	}
	var names []string
	var skipped []string
	for _, spec := range specs {
		provider := spec.command
		path, err := s.resolveTrustedBinary(fsys, spec.command)
		if err != nil {
			names = append(names, spec.command)
			if !errors.Is(err, exec.ErrNotFound) {
				skipped = append(skipped, err.Error())
			}
			continue
		}
		spec.provider = provider
		spec.command = path
		if err := s.checkServerCompatibility(ctx, fsys, spec); err != nil {
			skipped = append(skipped, err.Error())
			continue
		}
		return spec, language, nil
	}
	providers := strings.Join(names, " or ")
	if providers == "" {
		providers = specs[0].command
	}
	detail := ""
	if len(skipped) > 0 {
		detail = "; skipped " + strings.Join(skipped, ", ")
	}
	return serverSpec{}, "", fmt.Errorf("%s code intelligence is unavailable: install %s on a trusted global PATH or configure its exact provider path%s", language, providers, detail)
}

func (s *Service) capabilities(ctx context.Context, fsys *workspacefs.FS, _ Request) (Result, error) {
	languages := make([]string, 0, len(s.servers))
	for language := range s.servers {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	result := Result{Operation: OpCapabilities}
	for _, language := range languages {
		specs := s.servers[language]
		capability := Capability{
			Language: language,
			Operations: []Operation{
				OpDefinition, OpReferences, OpHover, OpDocumentSymbols, OpWorkspaceSymbols, OpDiagnostics,
			},
		}
		var attempted []string
		var skipped []string
		for _, spec := range specs {
			attempted = append(attempted, spec.command)
			provider := spec.command
			path, err := s.resolveTrustedBinary(fsys, spec.command)
			if err != nil {
				if !errors.Is(err, exec.ErrNotFound) {
					skipped = append(skipped, err.Error())
				}
				continue
			}
			spec.provider = provider
			spec.command = path
			if err := s.checkServerCompatibility(ctx, fsys, spec); err != nil {
				skipped = append(skipped, err.Error())
				continue
			}
			capability.Available = true
			capability.Status = "installed_unverified"
			capability.Provider = filepath.Base(path)
			capability.Detail = "trusted executable and version probe passed; LSP initialization is verified on query"
			if len(skipped) > 0 {
				capability.Detail += "; skipped " + strings.Join(skipped, ", ")
			}
			break
		}
		if !capability.Available {
			capability.Status = "unavailable"
			capability.Provider = strings.Join(attempted, " or ")
			capability.Detail = "not found on a trusted global PATH"
			if len(skipped) > 0 {
				capability.Detail += "; skipped " + strings.Join(skipped, ", ")
			}
		}
		result.Capabilities = append(result.Capabilities, capability)
	}
	structural := Capability{
		Language:   "structural",
		Provider:   "ast-grep",
		Operations: []Operation{OpStructuralSearch},
	}
	if _, err := s.resolveTrustedBinary(fsys, "ast-grep"); err == nil {
		structural.Available = true
		structural.Status = "installed_unverified"
		structural.Detail = "trusted executable found; invocation is verified on query"
	} else {
		structural.Status = "unavailable"
		structural.Detail = concise(err.Error(), 256)
	}
	result.Capabilities = append(result.Capabilities, structural)
	return result, nil
}

func (s *Service) checkServerCompatibility(ctx context.Context, fsys *workspacefs.FS, spec serverSpec) error {
	if len(spec.probeArgs) == 0 {
		return nil
	}
	argv := sandbox.WrapReadOnlyArgv(append([]string{spec.command}, spec.probeArgs...), fsys.Root(), false)
	if len(argv) == 0 {
		return fmt.Errorf("%s compatibility probe is empty", filepath.Base(spec.command))
	}
	result, err := s.runner.Run(ctx, processrunner.Request{
		Command:        argv[0],
		Args:           argv[1:],
		Dir:            os.TempDir(),
		Env:            providerProcessEnv(ctx, spec.provider),
		Timeout:        2 * time.Second,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 1024,
	})
	if err != nil {
		return fmt.Errorf("%s version probe failed", filepath.Base(spec.command))
	}
	version := strings.TrimSpace(result.Stdout)
	if version == "" {
		version = strings.TrimSpace(result.Stderr)
	}
	if version == "" {
		return fmt.Errorf("%s version probe returned no version", filepath.Base(spec.command))
	}
	if spec.minMajor <= 0 {
		return nil
	}
	major, ok := versionMajor(version)
	if !ok || major < spec.minMajor {
		return fmt.Errorf("%s version does not support the required native LSP mode (need major >= %d)", filepath.Base(spec.command), spec.minMajor)
	}
	return nil
}

func versionMajor(value string) (int, bool) {
	for _, field := range strings.Fields(value) {
		field = strings.TrimPrefix(strings.TrimSpace(field), "v")
		majorText, _, _ := strings.Cut(field, ".")
		major, err := strconv.Atoi(majorText)
		if err == nil {
			return major, true
		}
	}
	return 0, false
}

func (s *Service) resolveTrustedBinary(fsys *workspacefs.FS, name string) (string, error) {
	path, explicitlyConfigured, err := s.providerPath(name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", exec.ErrNotFound
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s resolved to a relative PATH entry, which is not trusted", name)
	}
	path = filepath.Clean(path)
	trustedBoundary := projectTrustRoot(fsys.Root())
	if pathWithinRoot(trustedBoundary, path) {
		return "", fmt.Errorf("%s resolves inside the project workspace, which is not trusted", name)
	}
	if !explicitlyConfigured && pathSharesUntrustedWorkspaceAncestor(fsys.Root(), path) {
		return "", fmt.Errorf("%s resolves below a filesystem ancestor shared with the workspace, which is not trusted", name)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s executable cannot be resolved", name)
	}
	if pathWithinRoot(trustedBoundary, canonical) {
		return "", fmt.Errorf("%s resolves through a symlink into the project workspace, which is not trusted", name)
	}
	if !explicitlyConfigured && pathSharesUntrustedWorkspaceAncestor(fsys.Root(), canonical) {
		return "", fmt.Errorf("%s resolves through a symlink below a filesystem ancestor shared with the workspace, which is not trusted", name)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("%s executable cannot be inspected", name)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s does not resolve to a regular executable", name)
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(canonical), ".exe") {
		return "", fmt.Errorf("%s must resolve to a native .exe on Windows", name)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s does not resolve to an executable file", name)
	}
	return trustedBinaryInvocationPath(path, canonical), nil
}

func trustedBinaryInvocationPath(discovered, canonical string) string {
	if runtime.GOOS == "windows" {
		// A Windows shim can resolve to a native executable, but invoking the shim
		// would execute a different path from the .exe that passed validation.
		return canonical
	}
	// Unix tool managers such as Volta use multi-call shims selected by argv[0].
	// Preserve that path after its canonical target has passed every trust check.
	return discovered
}

func (s *Service) providerPath(name string) (string, bool, error) {
	if s != nil {
		if path := s.providerPaths[name]; strings.TrimSpace(path) != "" {
			return path, true, nil
		}
		if s.lookPath != nil {
			path, err := s.lookPath(name)
			return path, false, err
		}
	}
	return "", false, exec.ErrNotFound
}

// projectTrustRoot broadens the trust check for task roots nested in a
// repository or monorepo. It scans all ancestors, rather than stopping at the
// nearest nested repository, because an outer checkout's node_modules or tool
// directory is project code too. Only marker existence is inspected; no Git or
// project command is executed.
func projectTrustRoot(root string) string {
	current := filepath.Clean(root)
	boundary := current
	home := canonicalUserHome()
	for {
		if current != home && hasProjectRootMarker(current) {
			boundary = current
		}
		parent := filepath.Dir(current)
		if parent == current || current == home {
			return boundary
		}
		current = parent
	}
}

func hasProjectRootMarker(directory string) bool {
	for _, marker := range []string{
		".git", ".hg", ".svn", "go.work", "go.mod", "package.json",
		"pnpm-workspace.yaml", "Cargo.toml", "pyproject.toml",
	} {
		if _, err := os.Lstat(filepath.Join(directory, marker)); err == nil {
			return true
		}
	}
	return false
}

// A marker-less workspace can still inherit an executable from a writable
// parent checkout (for example /repo/packages/app plus /repo/bin/gopls). PATH
// discovery therefore fails closed whenever the provider and workspace share
// an ancestor more specific than the filesystem root or the operator's home.
// Operators with an intentional layout such as /srv/app plus /srv/tools can
// approve one exact provider with HECATE_CODEINTEL_<PROVIDER>_PATH.
func pathSharesUntrustedWorkspaceAncestor(workspace, candidate string) bool {
	workspace = filepath.Clean(workspace)
	candidate = filepath.Clean(candidate)
	for ancestor := filepath.Dir(workspace); ; ancestor = filepath.Dir(ancestor) {
		if pathWithinRoot(ancestor, candidate) {
			home := canonicalUserHome()
			return filepath.Dir(ancestor) != ancestor && (home == "" || !samePath(ancestor, home))
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return false
		}
	}
}

func canonicalUserHome() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err == nil {
		home = canonical
	}
	return filepath.Clean(home)
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		relative = strings.ToLower(relative)
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
