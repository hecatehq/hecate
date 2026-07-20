package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/codeintel"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type fakeCodeIntelligenceService struct {
	root    string
	request codeintel.Request
	result  codeintel.Result
	err     error
}

func (f *fakeCodeIntelligenceService) Query(_ context.Context, root string, request codeintel.Request) (codeintel.Result, error) {
	f.root = root
	f.request = request
	return f.result, f.err
}

func TestAgentLoopCodeIntelligenceToolIsAdvertisedAndReadOnlySafe(t *testing.T) {
	tool := findToolDefinition(agentToolDefinitions(), AgentToolCodeIntelligence)
	if tool == nil {
		t.Fatalf("%s tool definition missing", AgentToolCodeIntelligence)
	}
	for _, operation := range []string{"capabilities", "definition", "references", "hover", "document_symbols", "workspace_symbols", "diagnostics", "structural_search"} {
		if !strings.Contains(string(tool.Function.Parameters), `"`+operation+`"`) {
			t.Errorf("tool schema omits operation %q", operation)
		}
	}
	if !strings.Contains(string(tool.Function.Parameters), `"selector"`) || !strings.Contains(string(tool.Function.Parameters), `^[A-Za-z_][A-Za-z0-9_]*$`) {
		t.Fatalf("tool schema omits the bounded structural selector: %s", tool.Function.Parameters)
	}
	toolsEnabled := true
	readOnlyTools := agentToolDefinitionsForExecution(types.Task{
		AgentPresetID:           "review-read-only",
		AgentPresetToolsEnabled: &toolsEnabled,
		SandboxReadOnly:         true,
	}, types.TaskRun{}, agentToolDefinitionOptions{})
	if !hasToolDefinition(readOnlyTools, AgentToolCodeIntelligence) {
		t.Fatalf("read-only catalog omits %s", AgentToolCodeIntelligence)
	}
}

func TestAgentLoopCodeIntelligenceToolDispatchesBoundedReadQuery(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeCodeIntelligenceService{result: codeintel.Result{
		Operation:       codeintel.OpDefinition,
		Provider:        "fake-lsp",
		Items:           []codeintel.Item{{Path: "main.go", StartLine: 7, StartColumn: 3}},
		Text:            "provider=fake-lsp operation=definition results=1\nmain.go:7:3",
		Truncated:       true,
		OmittedExternal: 2,
	}}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = dir
	spec.Task.SandboxNetwork = true

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-1",
		AgentToolCodeIntelligence,
		`{"operation":"definition","path":"main.go","line":12,"column":9,"max_results":25}`,
	), 4, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if fake.root != filepath.Clean(dir) {
		t.Fatalf("Query() root = %q, want %q", fake.root, filepath.Clean(dir))
	}
	if fake.request.Operation != codeintel.OpDefinition || fake.request.Path != "main.go" || fake.request.Line != 12 || fake.request.Column != 9 || fake.request.MaxResults != 25 {
		t.Fatalf("Query() request = %+v", fake.request)
	}
	if result.Step == nil || result.Step.ToolName != AgentToolCodeIntelligence || result.Step.Index != 4 {
		t.Fatalf("Dispatch() step = %+v", result.Step)
	}
	if result.Text != fake.result.Text {
		t.Fatalf("Dispatch() text = %q, want %q", result.Text, fake.result.Text)
	}
	if got := result.Step.OutputSummary["provider"]; got != "fake-lsp" {
		t.Fatalf("step provider = %v, want fake-lsp", got)
	}
	if got := result.Step.OutputSummary["omitted_external"]; got != 2 {
		t.Fatalf("step omitted_external = %v, want 2", got)
	}
	if got := result.Step.Input["query_bytes"]; got != 0 {
		t.Fatalf("step query_bytes = %v, want 0", got)
	}
	if got := result.Step.Input["max_results"]; got != 25 {
		t.Fatalf("step max_results = %v, want 25", got)
	}
}

func TestAgentLoopCodeIntelligenceToolFailuresRemainToolErrors(t *testing.T) {
	fake := &fakeCodeIntelligenceService{err: errors.New("gopls is not installed; use grep or install gopls")}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()
	spec.Task.SandboxNetwork = true

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-1",
		AgentToolCodeIntelligence,
		`{"operation":"definition","path":"main.go","line":1,"column":1}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Status != "failed" || result.Step.Result != telemetry.ResultError || !strings.Contains(result.Text, "gopls is not installed") {
		t.Fatalf("Dispatch() = %+v, want provider error with failed step", result)
	}
	if result.Step.ErrorKind != "provider_unavailable" || result.Step.OutputSummary["error_category"] != "provider_unavailable" {
		t.Fatalf("Dispatch() step = %+v, want sanitized provider_unavailable category", result.Step)
	}
	if _, leakedPath := result.Step.Input["path"]; leakedPath {
		t.Fatalf("failed step input leaked path: %+v", result.Step.Input)
	}
	if _, misleading := result.Step.Input["query_chars"]; misleading {
		t.Fatalf("failed step uses misleading query_chars metadata: %+v", result.Step.Input)
	}
	if got := result.Step.Input["max_results"]; got != codeIntelligenceDefaultResults {
		t.Fatalf("failed step max_results = %v, want effective default %d", got, codeIntelligenceDefaultResults)
	}

	oversized := strings.Repeat("x", 16*1024)
	bounded, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-oversized",
		AgentToolCodeIntelligence,
		`{"operation":"`+oversized+`","language":"`+oversized+`","selector":"`+oversized+`"}`,
	), 2, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch(oversized) error = %v", err)
	}
	if bounded.Step == nil {
		t.Fatal("Dispatch(oversized) omitted failed step")
	}
	for _, field := range []string{"operation", "language", "selector"} {
		value, _ := bounded.Step.Input[field].(string)
		if len(value) > codeIntelligenceStepStringBytes {
			t.Fatalf("failed step %s bytes = %d, want <= %d", field, len(value), codeIntelligenceStepStringBytes)
		}
	}

	malformed, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-2",
		AgentToolCodeIntelligence,
		`not-json`,
	), 2, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch(malformed) error = %v", err)
	}
	if malformed.Step != nil || !strings.Contains(malformed.Text, "invalid arguments for "+AgentToolCodeIntelligence) {
		t.Fatalf("Dispatch(malformed) = %+v", malformed)
	}
}

func TestAgentLoopCodeIntelligenceSemanticQueriesFailClosedWithoutRequiredIsolation(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	fake := &fakeCodeIntelligenceService{result: codeintel.Result{Text: "must not run"}}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()
	toolsEnabled := true
	spec.Task.AgentPresetID = "review-read-only"
	spec.Task.AgentPresetToolsEnabled = &toolsEnabled
	spec.Task.SandboxReadOnly = true

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-1",
		AgentToolCodeIntelligence,
		`{"operation":"definition","path":"main.go","line":1,"column":1}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if fake.request.Operation != "" {
		t.Fatalf("semantic provider ran with request %+v", fake.request)
	}
	if result.Step == nil || result.Step.Phase != "policy" || result.Step.Result != telemetry.ResultDenied || !result.ToolError {
		t.Fatalf("Dispatch() = %+v, want audited policy denial", result)
	}
	if !strings.Contains(result.Text, "read-only tasks") {
		t.Fatalf("Dispatch() text = %q, want isolation reason", result.Text)
	}

	capabilities, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-2",
		AgentToolCodeIntelligence,
		`{"operation":"capabilities"}`,
	), 2, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch(capabilities) error = %v", err)
	}
	if capabilities.Step == nil || fake.request.Operation != codeintel.OpCapabilities {
		t.Fatalf("capabilities dispatch = %+v request=%+v, want safe query", capabilities, fake.request)
	}
	if !strings.Contains(capabilities.Text, "semantic_policy=blocked") || capabilities.Step.OutputSummary["semantic_policy_blocked"] != true {
		t.Fatalf("capabilities dispatch = %+v, want explicit semantic policy blocker", capabilities)
	}

	structural, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-3",
		AgentToolCodeIntelligence,
		`{"operation":"structural_search","path":".","language":"go","query":"fmt.Errorf($A)","selector":"call_expression"}`,
	), 3, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch(structural_search) error = %v", err)
	}
	if structural.Step == nil || fake.request.Operation != codeintel.OpStructuralSearch || fake.request.Selector != "call_expression" {
		t.Fatalf("structural dispatch = %+v request=%+v, want safe query", structural, fake.request)
	}
	if got := structural.Step.Input["selector"]; got != "call_expression" {
		t.Fatalf("structural step selector = %v, want call_expression", got)
	}
}

func TestAgentLoopCodeIntelligenceSemanticQueriesUseReadOnlyBwrap(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperBwrap)
	defer reset()
	fake := &fakeCodeIntelligenceService{result: codeintel.Result{Operation: codeintel.OpDefinition, Text: "results=0"}}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()
	spec.Task.SandboxReadOnly = true

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-1",
		AgentToolCodeIntelligence,
		`{"operation":"definition","path":"main.go","line":1,"column":1}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Result == telemetry.ResultDenied || fake.request.Operation != codeintel.OpDefinition {
		t.Fatalf("Dispatch() = %+v request=%+v, want semantic query under bwrap", result, fake.request)
	}
}

func TestAgentLoopCodeIntelligenceSemanticQueriesFailClosedWithoutNetworkWrapper(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	fake := &fakeCodeIntelligenceService{result: codeintel.Result{Text: "must not run"}}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-1",
		AgentToolCodeIntelligence,
		`{"operation":"hover","path":"main.go","line":1,"column":1}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Result != telemetry.ResultDenied || fake.request.Operation != "" || !strings.Contains(result.Text, "network-denied policy") {
		t.Fatalf("Dispatch() = %+v request=%+v, want network isolation denial", result, fake.request)
	}
}

func TestAgentLoopCodeIntelligenceSemanticQueriesUseSandboxExecNetworkDenial(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperSandboxExec)
	defer reset()
	fake := &fakeCodeIntelligenceService{result: codeintel.Result{Operation: codeintel.OpHover, Text: "results=0"}}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-1",
		AgentToolCodeIntelligence,
		`{"operation":"hover","path":"main.go","line":1,"column":1}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Result == telemetry.ResultDenied || fake.request.Operation != codeintel.OpHover {
		t.Fatalf("Dispatch() = %+v request=%+v, want semantic query under sandbox-exec", result, fake.request)
	}
}

func TestCodeIntelligenceErrorCategorySeparatesInputFromProviderFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "byte limit", err: errors.New("code intelligence query exceeds the 16384-byte limit"), want: "invalid_request"},
		{name: "selector byte limit", err: errors.New("code intelligence selector exceeds the 128-byte limit"), want: "invalid_request"},
		{name: "selector shape", err: errors.New("structural selector must be a single ASCII tree-sitter node-kind token"), want: "invalid_request"},
		{name: "extension", err: errors.New(`no allowlisted language server supports ".rb"`), want: "invalid_request"},
		{name: "structural language", err: errors.New(`structural-search language "ruby" is not allowlisted`), want: "invalid_request"},
		{name: "position", err: errors.New("column 10 is past line 2"), want: "invalid_request"},
		{name: "adversarial protocol filename", err: errors.New(`open workspace file "protocol.go": file does not exist`), want: "invalid_request"},
		{name: "adversarial unavailable filename", err: errors.New(`open workspace file "unavailable.go": file does not exist`), want: "invalid_request"},
		{name: "provider unavailable", err: errors.New("typescript code intelligence is unavailable"), want: "provider_unavailable"},
		{name: "provider compatibility", err: errors.New("typescript code intelligence is unavailable: tsc version does not support the required native LSP mode"), want: "provider_unavailable"},
		{name: "protocol", err: errors.New("language server protocol failed"), want: "provider_protocol"},
		{name: "malformed structural output", err: errors.New("ast-grep returned a malformed JSON stream at line 2"), want: "provider_protocol"},
		{name: "unknown provider failure", err: errors.New("provider failed unexpectedly"), want: "provider_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codeIntelligenceErrorCategory(test.err); got != test.want {
				t.Fatalf("category = %q, want %q", got, test.want)
			}
		})
	}
}

func findToolDefinition(tools []types.Tool, name string) *types.Tool {
	for index := range tools {
		if tools[index].Function.Name == name {
			return &tools[index]
		}
	}
	return nil
}
