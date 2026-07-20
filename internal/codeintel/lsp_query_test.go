package codeintel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

func TestService_LSPOperationsNormalizeResults(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	source := "package sample\n\nfunc café() {}\nfunc use() { café() }\n"
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	tests := []struct {
		name       string
		request    Request
		wantName   string
		wantKind   string
		wantDetail string
	}{
		{name: "definition", request: Request{Operation: OpDefinition, Path: "sample.go", Line: 4, Column: 14}, wantDetail: "func café() {}"},
		{name: "references", request: Request{Operation: OpReferences, Path: "sample.go", Line: 4, Column: 14}, wantDetail: "func café() {}"},
		{name: "hover", request: Request{Operation: OpHover, Path: "sample.go", Line: 4, Column: 14}, wantDetail: "func café()"},
		{name: "document symbols", request: Request{Operation: OpDocumentSymbols, Path: "sample.go"}, wantName: "café", wantKind: "function"},
		{name: "workspace symbols", request: Request{Operation: OpWorkspaceSymbols, Language: "go", Query: "café"}, wantName: "café", wantKind: "function"},
		{name: "pull diagnostics", request: Request{Operation: OpDiagnostics, Path: "sample.go"}, wantDetail: "fixture diagnostic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := fakeLSPService(t, "normal")
			result, err := service.Query(context.Background(), workspace, test.request)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(result.Items) != 1 {
				t.Fatalf("items = %+v, want one", result.Items)
			}
			item := result.Items[0]
			if item.Path != "sample.go" {
				t.Fatalf("path = %q, want sample.go", item.Path)
			}
			if test.wantName != "" && item.Name != test.wantName {
				t.Fatalf("name = %q, want %q", item.Name, test.wantName)
			}
			if test.wantKind != "" && item.Kind != test.wantKind {
				t.Fatalf("kind = %q, want %q", item.Kind, test.wantKind)
			}
			combined := item.Detail + item.Message + item.Preview
			if test.wantDetail != "" && !strings.Contains(combined, test.wantDetail) {
				t.Fatalf("item = %+v, want detail %q", item, test.wantDetail)
			}
			if test.request.Operation == OpDefinition || test.request.Operation == OpReferences {
				if result.OmittedExternal != 1 {
					t.Fatalf("omitted = %d, want 1 external result", result.OmittedExternal)
				}
				if item.StartLine != 3 || item.StartColumn != 6 || item.EndColumn != 11 {
					t.Fatalf("range = %d:%d-%d:%d, want 3:6-3:11", item.StartLine, item.StartColumn, item.EndLine, item.EndColumn)
				}
			}
		})
	}
}

func TestLSPNormalizationRecordLimitBoundsSourceFileReads(t *testing.T) {
	workspace := t.TempDir()
	paths := []string{"one.go", "two.go", "three.go"}
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte("package sample\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		t.Fatalf("open workspace filesystem: %v", err)
	}
	invalidRange := fixtureRange(99, 0, 99, 1)
	locations := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		locations = append(locations, map[string]any{
			"uri":   pathToFileURI(filepath.Join(workspace, path)),
			"range": invalidRange,
		})
	}
	mustJSON := func(t *testing.T, value any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		return raw
	}
	type normalizer func(*testing.T, *sourceCache) ([]Item, int, bool, error)
	tests := []struct {
		name      string
		normalize normalizer
	}{
		{
			name: "definition links",
			normalize: func(t *testing.T, cache *sourceCache) ([]Item, int, bool, error) {
				links := make([]map[string]any, 0, len(locations))
				for _, location := range locations {
					links = append(links, map[string]any{
						"targetUri":            location["uri"],
						"targetRange":          location["range"],
						"targetSelectionRange": location["range"],
					})
				}
				return normalizeDefinition(cache, positionUTF8, mustJSON(t, links), 1)
			},
		},
		{
			name: "locations",
			normalize: func(t *testing.T, cache *sourceCache) ([]Item, int, bool, error) {
				return normalizeLocations(cache, positionUTF8, mustJSON(t, locations), 1)
			},
		},
		{
			name: "workspace symbols",
			normalize: func(t *testing.T, cache *sourceCache) ([]Item, int, bool, error) {
				symbols := make([]map[string]any, 0, len(locations))
				for index, location := range locations {
					symbols = append(symbols, map[string]any{"name": paths[index], "kind": 12, "location": location})
				}
				return normalizeWorkspaceSymbols(cache, positionUTF8, mustJSON(t, symbols), 1)
			},
		},
		{
			name: "nested document symbols",
			normalize: func(t *testing.T, cache *sourceCache) ([]Item, int, bool, error) {
				children := []any{
					map[string]any{"name": "two", "kind": 12, "location": locations[1]},
					map[string]any{"name": "three", "kind": 12, "location": locations[2]},
				}
				symbols := []any{map[string]any{"name": "one", "kind": 12, "location": locations[0], "children": children}}
				document := &sourceFile{relative: "document.go", data: []byte("package sample\n")}
				return normalizeDocumentSymbols(cache, document, positionUTF8, mustJSON(t, symbols), 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := newSourceCache(fsys)
			items, omitted, truncated, err := test.normalize(t, cache)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if len(items) != 0 || omitted != 1 || !truncated {
				t.Fatalf("items = %+v, omitted = %d, truncated = %v; want no items, one omitted, truncated", items, omitted, truncated)
			}
			if len(cache.files) != 1 || cache.files[paths[0]] == nil {
				t.Fatalf("opened files = %v, want only %q", cachedSourcePaths(cache), paths[0])
			}
		})
	}
}

func TestNormalizeDefinitionDoesNotMistakeURITextForLocationLink(t *testing.T) {
	workspace := t.TempDir()
	path := "targetUri.go"
	if err := os.WriteFile(filepath.Join(workspace, path), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		t.Fatalf("open workspace filesystem: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"uri":   pathToFileURI(filepath.Join(workspace, path)),
		"range": fixtureRange(0, 0, 0, 7),
	})
	if err != nil {
		t.Fatalf("marshal location: %v", err)
	}
	items, omitted, truncated, err := normalizeDefinition(newSourceCache(fsys), positionUTF8, raw, 1)
	if err != nil {
		t.Fatalf("normalize definition: %v", err)
	}
	if len(items) != 1 || items[0].Path != path || omitted != 0 || truncated {
		t.Fatalf("items=%+v omitted=%d truncated=%v, want one ordinary location", items, omitted, truncated)
	}
}

func TestDiagnosticsNormalizationRecordLimitBoundsInvalidWork(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	fsys, err := workspacefs.New(workspace)
	if err != nil {
		t.Fatalf("open workspace filesystem: %v", err)
	}
	diagnostics := []lspDiagnostic{
		{Range: lspRange{Start: lspPosition{Line: 99}, End: lspPosition{Line: 99, Character: 1}}, Message: "invalid"},
		{Range: lspRange{Start: lspPosition{}, End: lspPosition{Character: 1}}, Message: "must not be normalized"},
	}
	items, omitted, truncated, err := diagnosticsForFile(newSourceCache(fsys), pathToFileURI(path), diagnostics, positionUTF8, 1)
	if err != nil {
		t.Fatalf("normalize diagnostics: %v", err)
	}
	if len(items) != 0 || omitted != 1 || !truncated {
		t.Fatalf("items = %+v, omitted = %d, truncated = %v; want no items, one omitted, truncated", items, omitted, truncated)
	}
}

func cachedSourcePaths(cache *sourceCache) []string {
	paths := make([]string, 0, len(cache.files))
	for path := range cache.files {
		paths = append(paths, path)
	}
	return paths
}

func TestService_LSPPushDiagnosticsWaitsForRequestedURIAndEncoding(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\nvar 😀x = 1\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "push")
	service.diagnosticSettle = 20 * time.Millisecond
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpDiagnostics, Path: "sample.go"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want one requested-file diagnostic", result.Items)
	}
	item := result.Items[0]
	if item.StartColumn != 5 || item.EndColumn != 10 {
		t.Fatalf("UTF-8 range columns = %d-%d, want 5-10", item.StartColumn, item.EndColumn)
	}
	if result.OmittedExternal != 0 {
		t.Fatalf("omitted = %d, want unrelated publication ignored before normalization", result.OmittedExternal)
	}
}

func TestService_LSPPushDiagnosticsWaitsForDelayedFirstPublication(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\nvar x = 1\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "push_delayed")
	service.diagnosticInitialWait = 500 * time.Millisecond
	service.diagnosticSettle = 20 * time.Millisecond
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpDiagnostics, Path: "sample.go"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Message != "delayed push diagnostic" {
		t.Fatalf("items = %+v, want delayed requested-file diagnostic", result.Items)
	}
}

func TestService_LSPPushDiagnosticsUsesBufferedPreInitializePublication(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\nvar x = 1\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "push_before_initialize")
	service.diagnosticInitialWait = 60 * time.Millisecond
	service.diagnosticSettle = 10 * time.Millisecond
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpDiagnostics, Path: "sample.go"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Message != "early push diagnostic" {
		t.Fatalf("items = %+v, want buffered pre-initialize diagnostic", result.Items)
	}
}

func TestService_LSPPushDiagnosticsNormalizesEquivalentLocalFileURI(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\nvar x = 1\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "push_localhost")
	service.diagnosticInitialWait = 100 * time.Millisecond
	service.diagnosticSettle = 10 * time.Millisecond
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpDiagnostics, Path: "sample.go"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Message != "localhost push diagnostic" {
		t.Fatalf("items = %+v, want normalized equivalent-URI diagnostic", result.Items)
	}
}

func TestService_LSPPushDiagnosticsWithoutPublicationIsUnavailable(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "push_none")
	service.diagnosticInitialWait = 60 * time.Millisecond
	service.diagnosticSettle = 10 * time.Millisecond
	_, err := service.Query(context.Background(), workspace, Request{Operation: OpDiagnostics, Path: "sample.go"})
	if err == nil || err.Error() != errDiagnosticsUnavailable.Error() {
		t.Fatalf("error = %v, want sanitized diagnostics-unavailable error", err)
	}
}

func TestService_LSPCancellationKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PID liveness fixture is Unix-specific")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	service := fakeLSPService(t, "hang", pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queryDone := make(chan error, 1)
	go func() {
		_, err := service.Query(ctx, workspace, Request{Operation: OpDefinition, Path: "sample.go", Line: 1, Column: 1})
		queryDone <- err
	}()

	data := waitForLSPFixtureFile(t, pidFile, queryDone, "language-server descendant")
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil {
		t.Fatalf("parse child pid: %v", parseErr)
	}
	if process, findErr := os.FindProcess(pid); findErr == nil {
		t.Cleanup(func() { _ = process.Kill() })
	}

	started := time.Now()
	cancel()
	var queryErr error
	select {
	case queryErr = <-queryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("language-server cancellation exceeded 2s")
	}
	if !errors.Is(queryErr, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", queryErr)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s, want bounded cleanup", elapsed)
	}
	deadline := time.Now().Add(10 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("child process %d survived language-server cancellation", pid)
	}
}

func TestService_LSPCancellationInterruptsBlockedDocumentWrite(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	largeSource := "package sample\n// " + strings.Repeat("x", 256*1024)
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte(largeSource), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "block_did_open")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := service.Query(ctx, workspace, Request{Operation: OpDocumentSymbols, Path: "sample.go"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("blocked didOpen cancellation took %s, want under 1s", elapsed)
	}
}

func TestService_LSPEarlyExitCleanupIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PID liveness fixture is Unix-specific")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	service := fakeLSPService(t, "early_exit_child_pipe", pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queryDone := make(chan error, 1)
	go func() {
		_, err := service.Query(ctx, workspace, Request{Operation: OpDefinition, Path: "sample.go", Line: 1, Column: 1})
		queryDone <- err
	}()

	// Wait until the fixture has created the descendant whose inherited stdout
	// keeps the read loop blocked. A short deadline on the whole query races
	// process startup under full-suite race instrumentation and can expire
	// before the behavior under test exists.
	data := waitForLSPFixtureFile(t, pidFile, queryDone, "language-server descendant")
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil {
		t.Fatalf("parse child pid: %v", parseErr)
	}
	if process, findErr := os.FindProcess(pid); findErr == nil {
		t.Cleanup(func() { _ = process.Kill() })
	}

	cancel()
	var queryErr error
	select {
	case queryErr = <-queryDone:
	case <-time.After(10 * time.Second):
		t.Fatal("early-exit cleanup exceeded 10s")
	}
	if !errors.Is(queryErr, context.Canceled) {
		t.Fatalf("error = %v, want cancellation while descendant holds stdout open", queryErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("child process %d survived early language-server exit", pid)
	}
}

func TestService_LSPSuccessKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PID liveness fixture is Unix-specific")
	}
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n\nfunc café() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	service := fakeLSPService(t, "child_on_success", pidFile)
	result, err := service.Query(context.Background(), workspace, Request{
		Operation: OpDefinition,
		Path:      "sample.go",
		Line:      1,
		Column:    1,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want successful query result", result.Items)
	}
	data, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("read child pid: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil {
		t.Fatalf("parse child pid: %v", parseErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("child process %d survived successful language-server exit", pid)
	}
}

func TestService_LSPMalformedAndOversizedFramesAreSanitized(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for _, scenario := range []string{"malformed", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			service := fakeLSPService(t, scenario)
			_, err := service.Query(context.Background(), workspace, Request{Operation: OpDefinition, Path: "sample.go", Line: 1, Column: 1})
			if err == nil || err.Error() != "language server protocol failed" {
				t.Fatalf("error = %v, want fixed sanitized protocol error", err)
			}
		})
	}
}

func TestService_LSPHandlesCommonServerRequestsDuringInitialization(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n\nfunc café() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := fakeLSPService(t, "server_request")
	result, err := service.Query(context.Background(), workspace, Request{Operation: OpDocumentSymbols, Path: "sample.go"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want successful query after server request", result.Items)
	}
}

func TestCodeIntel_LSPHelperProcess(t *testing.T) {
	scenario, args, ok := lspHelperArgs(os.Args)
	if !ok {
		return
	}
	if scenario == "malformed" {
		_, _ = fmt.Fprint(os.Stderr, "/private/secret hostile stderr")
		_, _ = fmt.Fprint(os.Stdout, "Content-Length: 2\r\n\r\n{}")
		return
	}
	if scenario == "oversized" {
		_, _ = fmt.Fprint(os.Stdout, "Content-Length: 1048577\r\n\r\n")
		return
	}
	conn := newLSPConn(os.Stdin, os.Stdout, 2*1024*1024, 8*1024*1024)
	initialize, err := conn.read()
	if err != nil || initialize.Method != "initialize" {
		return
	}
	if scenario == "early_exit_child_pipe" {
		child := exec.Command("sh", "-c", "sleep 60")
		child.Stdout = os.Stdout
		if child.Start() == nil && len(args) > 0 {
			_ = publishLSPHelperPID(args[0], child.Process.Pid)
		}
		_ = os.Stdout.Close()
		return
	}
	var initializeParams struct {
		RootURI string `json:"rootUri"`
	}
	_ = json.Unmarshal(initialize.Params, &initializeParams)
	if scenario == "server_request" {
		_ = conn.request(91, "workspace/configuration", map[string]any{"items": []any{map[string]any{"section": "one"}, map[string]any{"section": "two"}}})
		response, err := conn.read()
		if err != nil {
			return
		}
		var values []any
		if response.Method != "" || json.Unmarshal(response.Result, &values) != nil || len(values) != 2 {
			return
		}
	}
	capabilities := map[string]any{"positionEncoding": "utf-16", "diagnosticProvider": map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false}}
	if strings.HasPrefix(scenario, "push") {
		capabilities = map[string]any{"positionEncoding": "utf-8"}
	}
	if scenario == "push_before_initialize" {
		documentURI := strings.TrimSuffix(initializeParams.RootURI, "/") + "/sample.go"
		_ = conn.notify("textDocument/publishDiagnostics", map[string]any{
			"uri":         documentURI,
			"diagnostics": []any{map[string]any{"range": fixtureRange(1, 4, 1, 5), "severity": 2, "message": "early push diagnostic"}},
		})
	}
	_ = conn.respond(initialize.ID, map[string]any{"capabilities": capabilities, "serverInfo": map[string]string{"name": "fixture"}})
	initialized, err := conn.read()
	if err != nil || initialized.Method != "initialized" {
		return
	}
	if scenario == "block_did_open" {
		for {
			time.Sleep(time.Hour)
		}
	}
	var documentURI string
	frame, err := conn.read()
	if err != nil {
		return
	}
	if frame.Method == "textDocument/didOpen" {
		var didOpen struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(frame.Params, &didOpen)
		documentURI = didOpen.TextDocument.URI
		if strings.HasPrefix(scenario, "push") {
			if scenario == "push_none" {
				serveShutdown(conn)
				return
			}
			if scenario == "push_before_initialize" {
				serveShutdown(conn)
				return
			}
			if scenario == "push_delayed" {
				time.Sleep(120 * time.Millisecond)
				_ = conn.notify("textDocument/publishDiagnostics", map[string]any{
					"uri":         documentURI,
					"diagnostics": []any{map[string]any{"range": fixtureRange(1, 4, 1, 5), "severity": 2, "message": "delayed push diagnostic"}},
				})
				serveShutdown(conn)
				return
			}
			if scenario == "push_localhost" {
				_ = conn.notify("textDocument/publishDiagnostics", map[string]any{
					"uri":         strings.Replace(documentURI, "file://", "file://localhost", 1),
					"diagnostics": []any{map[string]any{"range": fixtureRange(1, 4, 1, 5), "severity": 2, "message": "localhost push diagnostic"}},
				})
				serveShutdown(conn)
				return
			}
			_ = conn.notify("textDocument/publishDiagnostics", map[string]any{
				"uri":         "file:///external/secret.go",
				"diagnostics": []any{map[string]any{"range": fixtureRange(0, 0, 0, 1), "severity": 1, "message": "external secret"}},
			})
			_ = conn.notify("textDocument/publishDiagnostics", map[string]any{
				"uri":         documentURI,
				"diagnostics": []any{map[string]any{"range": fixtureRange(1, 4, 1, 9), "severity": 2, "message": "push diagnostic"}},
			})
			serveShutdown(conn)
			return
		}
		frame, err = conn.read()
		if err != nil {
			return
		}
	}
	if scenario == "hang" {
		if len(args) > 0 {
			child := exec.Command("sh", "-c", "sleep 60")
			if child.Start() == nil {
				_ = publishLSPHelperPID(args[0], child.Process.Pid)
			}
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	method := frame.Method
	result := fixtureLSPResult(method, documentURI, initializeParams.RootURI)
	if scenario == "child_on_success" && len(args) > 0 {
		child := exec.Command("sh", "-c", "sleep 60")
		if child.Start() == nil {
			_ = publishLSPHelperPID(args[0], child.Process.Pid)
		}
	}
	_ = conn.respond(frame.ID, result)
	serveShutdown(conn)
}

func publishLSPHelperPID(path string, pid int) error {
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func waitForLSPFixtureFile(t *testing.T, path string, queryDone <-chan error, label string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read %s state: %v", label, err)
		}
		select {
		case err := <-queryDone:
			t.Fatalf("query returned before %s startup: %v", label, err)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not start within 10s", label)
		}
	}
}

func fixtureLSPResult(method, documentURI, rootURI string) any {
	location := map[string]any{"uri": documentURI, "range": fixtureRange(2, 5, 2, 9)}
	external := map[string]any{"uri": "file:///external/secret.go", "range": fixtureRange(0, 0, 0, 1)}
	switch method {
	case "textDocument/definition", "textDocument/references":
		return []any{location, external}
	case "textDocument/hover":
		return map[string]any{"contents": map[string]any{"kind": "plaintext", "value": "func café()"}, "range": fixtureRange(2, 5, 2, 9)}
	case "textDocument/documentSymbol":
		return []any{map[string]any{"name": "café", "kind": 12, "range": fixtureRange(2, 0, 2, 14), "selectionRange": fixtureRange(2, 5, 2, 9)}}
	case "workspace/symbol":
		uri := strings.TrimSuffix(rootURI, "/") + "/sample.go"
		return []any{map[string]any{"name": "café", "kind": 12, "location": map[string]any{"uri": uri, "range": fixtureRange(2, 5, 2, 9)}}}
	case "textDocument/diagnostic":
		return map[string]any{"kind": "full", "items": []any{map[string]any{"range": fixtureRange(2, 5, 2, 9), "severity": 2, "source": "fixture", "message": "fixture diagnostic"}}}
	default:
		return nil
	}
}

func fixtureRange(startLine, startCharacter, endLine, endCharacter int) map[string]any {
	return map[string]any{
		"start": map[string]int{"line": startLine, "character": startCharacter},
		"end":   map[string]int{"line": endLine, "character": endCharacter},
	}
}

func serveShutdown(conn *lspConn) {
	for {
		frame, err := conn.read()
		if err != nil {
			return
		}
		if frame.Method == "shutdown" {
			_ = conn.respond(frame.ID, nil)
			continue
		}
		if frame.Method == "exit" {
			return
		}
	}
}

func fakeLSPService(t *testing.T, scenario string, extra ...string) *Service {
	t.Helper()
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	args := []string{"-test.run=TestCodeIntel_LSPHelperProcess", "--", "--hecate-lsp-helper", scenario}
	args = append(args, extra...)
	service := NewService()
	service.servers = map[string][]serverSpec{
		"go": {{language: "go", command: "fixture-lsp", args: args, extensions: extensionSet(".go")}},
	}
	setProviderPath(service, "fixture-lsp", executable)
	service.lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	return service
}

func lspHelperArgs(args []string) (string, []string, bool) {
	for index, arg := range args {
		if arg == "--hecate-lsp-helper" && index+1 < len(args) {
			return args[index+1], args[index+2:], true
		}
	}
	return "", nil, false
}
