package codeintel

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/sandbox"
)

type recordingRunner struct {
	requests []processrunner.Request
	result   processrunner.Result
	err      error
	run      func(processrunner.Request) (processrunner.Result, error)
}

func (r *recordingRunner) Run(_ context.Context, request processrunner.Request) (processrunner.Result, error) {
	r.requests = append(r.requests, request)
	if r.run != nil {
		return r.run(request)
	}
	return r.result, r.err
}

func (r *recordingRunner) RunStreaming(ctx context.Context, request processrunner.Request, _ func(processrunner.Chunk)) (processrunner.Result, error) {
	return r.Run(ctx, request)
}

func TestService_RejectsTraversalAndSymlinkInput(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.go"), []byte("package outside\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	service := NewService()
	_, err := service.Query(context.Background(), workspace, Request{Operation: OpDefinition, Path: "../outside.go", Line: 1, Column: 1})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("traversal error = %v, want unsafe path", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink(filepath.Join(outside, "outside.go"), filepath.Join(workspace, "linked.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = service.Query(context.Background(), workspace, Request{Operation: OpDefinition, Path: "linked.go", Line: 1, Column: 1})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v, want symlink rejection", err)
	}
}

func TestService_RejectsOversizedRequestBeforeProviderDiscovery(t *testing.T) {
	workspace := t.TempDir()
	service := NewService()
	service.lookPath = func(string) (string, error) {
		t.Fatal("provider discovery must not run for an oversized request")
		return "", os.ErrNotExist
	}
	_, err := service.Query(context.Background(), workspace, Request{
		Operation: OpStructuralSearch,
		Path:      ".",
		Language:  "go",
		// This is below the schema's code-point maxLength but above the
		// native service's encoded-byte budget.
		Query: strings.Repeat("é", maxQueryBytes/2+1),
	})
	if err == nil || !strings.Contains(err.Error(), "query exceeds") {
		t.Fatalf("error = %v, want bounded-query rejection", err)
	}
}

func TestService_TypeScriptWorkspaceSymbolsRequireProjectFile(t *testing.T) {
	workspace := t.TempDir()
	service := NewService()
	service.lookPath = func(string) (string, error) {
		t.Fatal("provider discovery must not run without the TypeScript bootstrap file")
		return "", os.ErrNotExist
	}
	_, err := service.Query(context.Background(), workspace, Request{
		Operation: OpWorkspaceSymbols,
		Language:  "typescript",
		Query:     "symbol",
	})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("error = %v, want TypeScript project-file requirement", err)
	}
}

func TestNewServiceLoadsExactOperatorProviderPaths(t *testing.T) {
	const configured = "/opt/hecate/providers/gopls"
	t.Setenv(providerExecutableEnv["gopls"], configured)
	service := NewService()
	if got := service.providerPaths["gopls"]; got != configured {
		t.Fatalf("configured gopls path = %q, want %q", got, configured)
	}
}

func TestService_TrustedBinaryResolutionRejectsWorkspaceAndRelativePaths(t *testing.T) {
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "gopls")
	if err := os.WriteFile(inside, []byte("fixture"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	service := NewService()

	service.lookPath = func(string) (string, error) { return "./gopls", nil }
	if _, err := service.resolveTrustedBinary(fsys, "gopls"); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("relative error = %v, want rejection", err)
	}
	service.lookPath = func(string) (string, error) { return inside, nil }
	if _, err := service.resolveTrustedBinary(fsys, "gopls"); err == nil || !strings.Contains(err.Error(), "project workspace") {
		t.Fatalf("workspace error = %v, want rejection", err)
	}
	setProviderPath(service, "gopls", inside)
	if _, err := service.resolveTrustedBinary(fsys, "gopls"); err == nil || !strings.Contains(err.Error(), "project workspace") {
		t.Fatalf("configured workspace error = %v, want rejection", err)
	}
	delete(service.providerPaths, "gopls")
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	link := filepath.Join(outside, "gopls")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	service.lookPath = func(string) (string, error) { return link, nil }
	if _, err := service.resolveTrustedBinary(fsys, "gopls"); err == nil || !strings.Contains(err.Error(), "symlink into the project workspace") {
		t.Fatalf("symlink error = %v, want rejection", err)
	}
}

func TestService_TrustedBinaryResolutionRejectsGitProjectSiblingOfNestedRoot(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("create git marker: %v", err)
	}
	nested := filepath.Join(project, "packages", "child")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested workspace: %v", err)
	}
	binaryDir := filepath.Join(project, "node_modules", ".bin")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		t.Fatalf("create binary directory: %v", err)
	}
	binary := executableFixture(t, binaryDir, "typescript-language-server")
	fsys, err := openWorkspace(nested)
	if err != nil {
		t.Fatalf("open nested workspace: %v", err)
	}
	service := NewService()
	service.lookPath = func(string) (string, error) { return binary, nil }
	if _, err := service.resolveTrustedBinary(fsys, "typescript-language-server"); err == nil || !strings.Contains(err.Error(), "project workspace") {
		t.Fatalf("error = %v, want nested Git project rejection", err)
	}
}

func TestService_TrustedBinaryResolutionRejectsOuterRepositoryToolFromNestedRepository(t *testing.T) {
	outer := t.TempDir()
	if err := os.Mkdir(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatalf("create outer Git marker: %v", err)
	}
	inner := filepath.Join(outer, "packages", "child")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatalf("create nested repository: %v", err)
	}
	binaryDir := filepath.Join(outer, "node_modules", ".bin")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		t.Fatalf("create outer binary directory: %v", err)
	}
	binary := executableFixture(t, binaryDir, "tsc")
	fsys, err := openWorkspace(inner)
	if err != nil {
		t.Fatalf("open nested workspace: %v", err)
	}
	service := NewService()
	service.lookPath = func(string) (string, error) { return binary, nil }
	if _, err := service.resolveTrustedBinary(fsys, "tsc"); err == nil || !strings.Contains(err.Error(), "project workspace") {
		t.Fatalf("error = %v, want outer repository executable rejection", err)
	}
}

func TestService_TrustedBinaryResolutionRejectsMarkerlessParentBinaryDirectory(t *testing.T) {
	project := t.TempDir()
	workspace := filepath.Join(project, "packages", "child")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create markerless workspace: %v", err)
	}
	binaryDir := filepath.Join(project, "bin")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		t.Fatalf("create parent binary directory: %v", err)
	}
	binary := executableFixture(t, binaryDir, "gopls")
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	service := NewService()
	service.lookPath = func(string) (string, error) { return binary, nil }
	if _, err := service.resolveTrustedBinary(fsys, "gopls"); err == nil || !strings.Contains(err.Error(), "shared with the workspace") {
		t.Fatalf("error = %v, want markerless parent binary rejection", err)
	}
}

func TestService_TrustedBinaryResolutionAllowsExactOperatorConfiguredSibling(t *testing.T) {
	project := t.TempDir()
	workspace := filepath.Join(project, "packages", "child")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create markerless workspace: %v", err)
	}
	binary := executableFixture(t, filepath.Join(project, "bin"), "gopls")
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	service := NewService()
	setProviderPath(service, "gopls", binary)

	resolved, err := service.resolveTrustedBinary(fsys, "gopls")
	if err != nil {
		t.Fatalf("resolve configured sibling: %v", err)
	}
	if resolved != binary {
		t.Fatalf("resolved path = %q, want %q", resolved, binary)
	}
}

func TestService_TrustedBinaryResolutionPreservesTrustedShimPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink fixture")
	}
	workspace := t.TempDir()
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	shimDir := t.TempDir()
	target := executableFixture(t, shimDir, "volta-shim")
	shim := filepath.Join(shimDir, "tsc")
	if err := os.Symlink(target, shim); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	service := NewService()
	setProviderPath(service, "tsc", shim)

	resolved, err := service.resolveTrustedBinary(fsys, "tsc")
	if err != nil {
		t.Fatalf("resolve trusted shim: %v", err)
	}
	if resolved != shim {
		t.Fatalf("resolved path = %q, want invocation shim %q", resolved, shim)
	}
}

func TestService_TypeScriptSelectionRejectsOldTSC(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	fsys, err := openWorkspace(workspace)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	binDir := t.TempDir()
	tsc := executableFixture(t, binDir, "tsc")
	runner := &recordingRunner{run: func(request processrunner.Request) (processrunner.Result, error) {
		if filepath.Base(request.Command) == "tsc" && len(request.Args) == 1 && request.Args[0] == "--version" {
			return processrunner.Result{Stdout: "Version 5.9.2\n"}, nil
		}
		return processrunner.Result{}, nil
	}}
	service := NewService()
	service.runner = runner
	setProviderPath(service, "tsc", tsc)
	service.lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	_, _, err = service.selectServer(context.Background(), fsys, Request{Path: "app.ts"})
	if err == nil || !strings.Contains(err.Error(), "need major >= 7") {
		t.Fatalf("selection error = %v, want incompatible native tsc rejection", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("probe requests = %d, want 1", len(runner.requests))
	}
}

func TestService_CapabilitiesDistinguishVerifiedExecutableFromLSPReadiness(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	gopls := executableFixture(t, t.TempDir(), "gopls")
	runner := &recordingRunner{result: processrunner.Result{Stdout: "golang.org/x/tools/gopls v0.20.0\n"}}
	service := NewService()
	service.runner = runner
	setProviderPath(service, "gopls", gopls)
	service.lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpCapabilities})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	var goCapability *Capability
	for index := range result.Capabilities {
		if result.Capabilities[index].Language == "go" {
			goCapability = &result.Capabilities[index]
			break
		}
	}
	if goCapability == nil || !goCapability.Available || goCapability.Status != "installed_unverified" {
		t.Fatalf("Go capability = %+v, want installed_unverified", goCapability)
	}
	if len(runner.requests) != 1 || len(runner.requests[0].Args) != 1 || runner.requests[0].Args[0] != "version" {
		t.Fatalf("probe requests = %+v, want fixed gopls version probe", runner.requests)
	}
}

func TestService_CapabilitiesPreserveCancellationDuringProbe(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	gopls := executableFixture(t, t.TempDir(), "gopls")
	ctx, cancel := context.WithCancel(context.Background())
	runner := &recordingRunner{run: func(processrunner.Request) (processrunner.Result, error) {
		cancel()
		return processrunner.Result{ExitCode: -1}, context.Canceled
	}}
	service := NewService()
	service.runner = runner
	setProviderPath(service, "gopls", gopls)
	service.lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	_, err := service.Query(ctx, workspace, Request{Operation: OpCapabilities})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestService_StructuralSearchUsesFixedNeutralInvocationAndParsesMatches(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(sourcePath, []byte("package sample\n\nfunc target() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	canonicalSourcePath, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("canonicalize source: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outsidePath, []byte("package outside\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	binary := executableFixture(t, t.TempDir(), "ast-grep")
	stdout := strings.Join([]string{
		`{"file":` + quoteJSON(canonicalSourcePath) + `,"text":"func target() {}","range":{"start":{"line":2,"column":0},"end":{"line":2,"column":16}}}`,
		`{"file":` + quoteJSON(outsidePath) + `,"text":"outside","range":{"start":{"line":0,"column":0},"end":{"line":0,"column":7}}}`,
	}, "\n")
	runner := &recordingRunner{result: processrunner.Result{Stdout: stdout}}
	service := NewService()
	service.runner = runner
	setProviderPath(service, "ast-grep", binary)
	service.lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	result, err := service.Query(context.Background(), workspace, Request{
		Operation: OpStructuralSearch,
		Path:      "sample.go",
		Query:     "$FUNC()",
	})
	if err != nil {
		t.Fatalf("structural search: %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	request := runner.requests[0]
	if request.Command != binary {
		t.Fatalf("command = %q, want %q", request.Command, binary)
	}
	wantArgs := []string{"run", "--pattern", "$FUNC()", "--lang", "go", "--json=stream", "--color", "never", "--threads", "1", canonicalSourcePath}
	if strings.Join(request.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("args = %#v, want %#v", request.Args, wantArgs)
	}
	if request.Dir == workspace || !strings.Contains(filepath.Base(request.Dir), "hecate-codeintel-") {
		t.Fatalf("cwd = %q, want neutral temporary directory", request.Dir)
	}
	if len(result.Items) != 1 || result.Items[0].Path != "sample.go" || result.Items[0].StartLine != 3 {
		t.Fatalf("items = %+v, want one normalized workspace match", result.Items)
	}
	if result.OmittedExternal != 1 {
		t.Fatalf("omitted external = %d, want 1", result.OmittedExternal)
	}
}

func TestStructuralNeutralDirectoryRejectsProjectControlledTempBase(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("create project marker: %v", err)
	}
	tempBase := filepath.Join(project, ".tmp")
	if err := os.Mkdir(tempBase, 0o755); err != nil {
		t.Fatalf("create project temp base: %v", err)
	}
	if directory, err := createStructuralNeutralDirectory(project, tempBase); err == nil {
		_ = os.RemoveAll(directory)
		t.Fatalf("neutral directory = %q, want project-boundary rejection", directory)
	}
	entries, err := os.ReadDir(tempBase)
	if err != nil {
		t.Fatalf("read temp base: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected neutral directory was not cleaned up: %+v", entries)
	}
}

func TestService_StructuralSearchMissingBinaryAndFailureAreSanitized(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := NewService()
	service.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	_, err := service.Query(context.Background(), workspace, Request{Operation: OpStructuralSearch, Path: "sample.go", Query: "$X"})
	if err == nil || !strings.Contains(err.Error(), "install ast-grep") {
		t.Fatalf("missing error = %v, want installation guidance", err)
	}

	binary := executableFixture(t, t.TempDir(), "ast-grep")
	setProviderPath(service, "ast-grep", binary)
	service.runner = &recordingRunner{result: processrunner.Result{ExitCode: 2, Stderr: "/private/secret: hostile stderr"}, err: errors.New("hostile process error")}
	_, err = service.Query(context.Background(), workspace, Request{Operation: OpStructuralSearch, Path: "sample.go", Query: "$X"})
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "hostile") {
		t.Fatalf("failure error = %v, want sanitized error", err)
	}
}

func TestService_StructuralSearchExitOneMeansNoMatches(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	binary := executableFixture(t, t.TempDir(), "ast-grep")
	service := NewService()
	setProviderPath(service, "ast-grep", binary)
	service.runner = &recordingRunner{
		result: processrunner.Result{ExitCode: 1},
		err:    &exec.ExitError{},
	}

	result, err := service.Query(context.Background(), workspace, Request{
		Operation: OpStructuralSearch,
		Path:      "sample.go",
		Query:     "$X",
	})
	if err != nil {
		t.Fatalf("structural no-match query: %v", err)
	}
	if result.Provider != "ast-grep" || len(result.Items) != 0 || result.Text != "structural_search returned no results" {
		t.Fatalf("result = %+v, want successful empty structural search", result)
	}
}

func TestService_StructuralSearchRejectsMalformedStreamAndBoundsResults(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(sourcePath, []byte("package sample\nvar one = 1\nvar two = 2\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	canonicalSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("canonical source: %v", err)
	}
	binary := executableFixture(t, t.TempDir(), "ast-grep")
	service := NewService()
	setProviderPath(service, "ast-grep", binary)
	service.runner = &recordingRunner{result: processrunner.Result{Stdout: `{not-json}`}}
	_, err = service.Query(context.Background(), workspace, Request{Operation: OpStructuralSearch, Path: "sample.go", Query: "$X"})
	if err == nil || !strings.Contains(err.Error(), "malformed JSON stream") || strings.Contains(err.Error(), "not-json") {
		t.Fatalf("malformed error = %v, want sanitized parse failure", err)
	}

	line := func(sourceLine int) string {
		return `{"file":` + quoteJSON(canonicalSource) + `,"text":"match","range":{"start":{"line":` + strconv.Itoa(sourceLine) + `,"column":0},"end":{"line":` + strconv.Itoa(sourceLine) + `,"column":3}}}`
	}
	service.runner = &recordingRunner{result: processrunner.Result{Stdout: line(1) + "\n" + line(2)}}
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpStructuralSearch, Path: "sample.go", Query: "$X", MaxResults: 1})
	if err != nil {
		t.Fatalf("bounded search: %v", err)
	}
	if len(result.Items) != 1 || !result.Truncated {
		t.Fatalf("result = %+v, want one truncated item", result)
	}

	// A record beyond MaxResults must not be parsed or normalized. In
	// particular, a provider cannot use omitted records to amplify filesystem
	// work past the caller's result budget.
	service.runner = &recordingRunner{result: processrunner.Result{Stdout: line(1) + "\n{not-json}"}}
	result, err = service.Query(context.Background(), workspace, Request{Operation: OpStructuralSearch, Path: "sample.go", Query: "$X", MaxResults: 1})
	if err != nil {
		t.Fatalf("post-limit malformed record was inspected: %v", err)
	}
	if len(result.Items) != 1 || !result.Truncated {
		t.Fatalf("post-limit result = %+v, want one truncated item", result)
	}
}

func executableFixture(t *testing.T, directory, name string) string {
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

func setProviderPath(service *Service, name, path string) {
	if service.providerPaths == nil {
		service.providerPaths = make(map[string]string)
	}
	service.providerPaths[name] = path
}

func quoteJSON(value string) string {
	quoted := strings.ReplaceAll(value, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	return `"` + quoted + `"`
}
