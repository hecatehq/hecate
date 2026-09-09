package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	for _, routingRule := range []string{
		"Choose capabilities first with no other arguments",
		"structural_search is the only operation for Python, Rust",
		"definition/references/hover are Go/TypeScript/JavaScript semantic operations requiring path, line, and column",
	} {
		if !strings.Contains(string(tool.Function.Parameters), routingRule) {
			t.Errorf("tool schema omits routing rule %q", routingRule)
		}
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
		t.Fatalf("decode code-intelligence schema: %v", err)
	}
	for _, callShape := range []string{
		`{"operation":"capabilities"}`,
		`{"operation":"workspace_symbols","path":"relative/file.ts","query":"Symbol"}`,
		`{"operation":"structural_search","path":"relative/file.py","language":"python","query":"def $NAME($$$ARGS)"}`,
	} {
		if !strings.Contains(schema.Properties["operation"].Description, callShape) {
			t.Errorf("operation guidance omits canonical call %q", callShape)
		}
	}
	for _, pathRule := range []string{"existing workspace-relative", "Copy a task-supplied path exactly", "never prepend the workspace root"} {
		if !strings.Contains(schema.Properties["path"].Description, pathRule) {
			t.Errorf("path guidance omits rule %q", pathRule)
		}
	}
	if !strings.Contains(string(tool.Function.Parameters), `"selector"`) || !strings.Contains(string(tool.Function.Parameters), `^[A-Za-z_][A-Za-z0-9_]*$`) {
		t.Fatalf("tool schema omits the bounded structural selector: %s", tool.Function.Parameters)
	}
	for _, language := range []string{"Go", "TypeScript/JavaScript", "Python", "Rust", "C/C++/C#", "Bash"} {
		if !strings.Contains(tool.Function.Description+string(tool.Function.Parameters), language) {
			t.Errorf("tool self-documentation omits language family %q", language)
		}
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

func TestAgentLoopCodeIntelligenceCanonicalPythonPatternMatchesDogfoodFixture(t *testing.T) {
	if os.Getenv("HECATE_CODEINTEL_DOGFOOD") != "1" {
		t.Skip("set HECATE_CODEINTEL_DOGFOOD=1 to verify the canonical pattern with installed ast-grep")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	result, err := codeintel.NewService().Query(context.Background(), root, codeintel.Request{
		Operation:  codeintel.OpStructuralSearch,
		Path:       "e2e/testdata/code-intelligence-dogfood/python_target.py",
		Language:   "python",
		Query:      "def $NAME($$$ARGS)",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("run canonical Python structural pattern: %v", err)
	}
	if result.Provider != "ast-grep" || len(result.Items) != 2 {
		t.Fatalf("canonical Python structural result = provider %q items %d, want ast-grep and 2 fixture functions", result.Provider, len(result.Items))
	}
}

func TestAgentLoopCodeIntelligenceSelfDocumentationMatchesEffectivePolicy(t *testing.T) {
	tests := []struct {
		name              string
		wrapper           sandbox.WrapperKind
		task              types.Task
		run               types.TaskRun
		gatedTools        []string
		serviceConfigured bool
		want              []string
	}{
		{
			name:              "read only host blocks semantics but preserves discovery and fallbacks",
			wrapper:           sandbox.WrapperNone,
			task:              types.Task{SandboxReadOnly: true, SandboxNetwork: true},
			serviceConfigured: true,
			want: []string{
				"calls to `code_intelligence` with `operation=capabilities` or `operation=structural_search` are permitted without an approval pause",
				"semantic LSP operations are blocked: semantic language servers are disabled for read-only tasks",
				semanticCodeIntelligenceRepair,
				"`grep` is permitted without an approval pause",
			},
		},
		{
			name:              "bwrap permits read only semantic queries",
			wrapper:           sandbox.WrapperBwrap,
			task:              types.Task{SandboxReadOnly: true},
			serviceConfigured: true,
			want: []string{
				"semantic LSP operations are permitted by the current task and host isolation policy",
				"provider installation is not checked yet",
				"LSP initialization is verified on query",
			},
		},
		{
			name:              "read approval covers discovery semantics structure and grep",
			wrapper:           sandbox.WrapperSandboxExec,
			task:              types.Task{SandboxNetwork: true},
			gatedTools:        []string{AgentToolCodeIntelligence, "grep"},
			serviceConfigured: true,
			want: []string{
				"calls to `code_intelligence` with `operation=capabilities` or `operation=structural_search` require operator approval",
				"semantic LSP operations are approval-gated",
				"`grep` requires operator approval",
			},
		},
		{
			name:    "frozen preset require covers code intelligence and grep",
			wrapper: sandbox.WrapperSandboxExec,
			task: types.Task{
				OriginKind:                "project_work_item",
				AgentPresetID:             "architecture",
				AgentPresetApprovalPolicy: types.AgentPresetApprovalRequire,
				SandboxNetwork:            true,
			},
			serviceConfigured: true,
			want: []string{
				"calls to `code_intelligence` with `operation=capabilities` or `operation=structural_search` require operator approval",
				"semantic LSP operations are approval-gated",
				"`grep` requires operator approval",
			},
		},
		{
			name:    "frozen preset block overrides global read gates",
			wrapper: sandbox.WrapperSandboxExec,
			task: types.Task{
				OriginKind:                "project_work_item",
				AgentPresetID:             "architecture",
				AgentPresetApprovalPolicy: types.AgentPresetApprovalBlock,
				SandboxNetwork:            true,
			},
			gatedTools:        []string{AgentToolCodeIntelligence, "grep"},
			serviceConfigured: true,
			want: []string{
				"calls to `code_intelligence` with `operation=capabilities` or `operation=structural_search` are blocked by the frozen Agent Preset approval policy",
				"semantic LSP operations are blocked by the frozen Agent Preset approval policy",
				"`grep` is blocked by the frozen Agent Preset approval policy",
			},
		},
		{
			name:              "missing runtime service is explicit",
			wrapper:           sandbox.WrapperBwrap,
			serviceConfigured: false,
			want:              []string{"code_intelligence is unavailable because its runtime service is not configured"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reset := sandbox.SetWrapperForTesting(test.wrapper)
			defer reset()
			tools := agentToolDefinitionsForExecution(test.task, test.run, agentToolDefinitionOptions{})
			applyCodeIntelligenceSelfDocumentation(tools, ExecutionSpec{Task: test.task, Run: test.run}, newAgentLoopApprovalGate(test.gatedTools), test.serviceConfigured)
			tool := findToolDefinition(tools, AgentToolCodeIntelligence)
			if tool == nil {
				t.Fatal("code_intelligence tool definition missing")
			}
			for _, want := range test.want {
				if !strings.Contains(tool.Function.Description, want) {
					t.Errorf("description = %q, want %q", tool.Function.Description, want)
				}
			}
		})
	}
}

func TestEffectiveGuidanceToolAccessQAExcludesFrozenPresetPolicy(t *testing.T) {
	t.Parallel()
	spec := ExecutionSpec{
		Task: types.Task{
			OriginKind:                "project_work_item",
			AgentPresetID:             "review_qa",
			AgentPresetApprovalPolicy: types.AgentPresetApprovalRequire,
			SandboxNetwork:            true,
		},
		Run: types.TaskRun{
			WorkflowMode:    types.WorkflowModeQA,
			WorkflowVersion: "v0",
		},
	}
	tools := agentToolDefinitionsForExecution(spec.Task, spec.Run, agentToolDefinitionOptions{})
	if findToolDefinition(tools, AgentToolCodeIntelligence) != nil {
		t.Fatal("QA catalog unexpectedly advertises code_intelligence")
	}
	if got := effectiveGuidanceToolAccess(newAgentLoopApprovalGate(nil), spec, "grep", `{}`, &tools, false); got != "is permitted without an approval pause" {
		t.Fatalf("QA grep guidance = %q, want frozen preset policy excluded", got)
	}
}

func TestAgentLoopCodeIntelligenceFirstModelRequestGetsEffectiveSelfDocumentationWithoutProviderProbe(t *testing.T) {
	reset := sandbox.SetWrapperForTesting(sandbox.WrapperNone)
	defer reset()
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("Inspection is unavailable under this posture.")),
	}}
	fake := &fakeCodeIntelligenceService{}
	loop := NewAgentLoopExecutor(
		llm,
		&stubExecutor{},
		&stubExecutor{},
		&stubExecutor{},
		1,
		nil,
		HTTPRequestPolicy{},
		WithCodeIntelligenceService(fake),
	)
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()
	spec.Task.SandboxReadOnly = true
	spec.Task.SandboxNetwork = true
	res, err := loop.Execute(context.Background(), spec)
	if err != nil || res.Status != "completed" {
		t.Fatalf("Execute() result = %+v, error %v", res, err)
	}
	if len(llm.lastReqs) != 1 {
		t.Fatalf("model requests = %d, want 1", len(llm.lastReqs))
	}
	tool := findToolDefinition(llm.lastReqs[0].Tools, AgentToolCodeIntelligence)
	if tool == nil || !strings.Contains(tool.Function.Description, "semantic LSP operations are blocked") {
		t.Fatalf("first request tool = %+v, want effective semantic blocker", tool)
	}
	if fake.request.Operation != "" {
		t.Fatalf("self-documentation unexpectedly probed provider with request %+v", fake.request)
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

func TestAgentLoopCodeIntelligenceInvalidRequestSummaryUsesPrivacySafeReason(t *testing.T) {
	const (
		secretPath  = "private/customer-alpha.go"
		secretQuery = "customer-secret-query"
		secretError = "permission denied for customer-alpha"
	)
	fake := &fakeCodeIntelligenceService{err: errors.New(`open workspace file "` + secretPath + `": ` + secretError)}
	dispatcher := &agentLoopToolDispatcher{codeIntelligence: fake}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()
	spec.Task.SandboxNetwork = true

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"code-private",
		AgentToolCodeIntelligence,
		`{"operation":"definition","path":"`+secretPath+`","query":"`+secretQuery+`","line":1,"column":1}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.ErrorKind != "invalid_request" {
		t.Fatalf("Dispatch() step = %+v, want invalid_request", result.Step)
	}
	if got := result.Step.OutputSummary["invalid_request_reason"]; got != string(codeIntelligenceInvalidReasonWorkspaceFileUnavailable) {
		t.Fatalf("invalid request reason = %v, want %q", got, codeIntelligenceInvalidReasonWorkspaceFileUnavailable)
	}
	encoded, err := json.Marshal(result.Step.OutputSummary)
	if err != nil {
		t.Fatalf("Marshal(OutputSummary) error = %v", err)
	}
	for _, secret := range []string{secretPath, secretQuery, secretError} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("OutputSummary leaked %q: %s", secret, encoded)
		}
	}
}

func TestCodeIntelligenceInvalidRequestSummaryUsesClosedUnknownFallback(t *testing.T) {
	const untrustedReason = codeIntelligenceInvalidRequestReason("future validation leaked private/customer-beta.go")
	spec := newAgentLoopSpec(t)
	step := codeIntelligenceFailureStep(spec, codeIntelligenceArgs{
		Operation: "definition",
		Path:      "private/customer-beta.go",
		Query:     "customer-beta-secret",
	}, 1, time.Now().UTC(), AgentToolCodeIntelligence, "invalid_request", untrustedReason)

	if got := step.OutputSummary["invalid_request_reason"]; got != string(codeIntelligenceInvalidReasonUnknown) {
		t.Fatalf("invalid request reason = %v, want %q", got, codeIntelligenceInvalidReasonUnknown)
	}
	encoded, err := json.Marshal(step.OutputSummary)
	if err != nil {
		t.Fatalf("Marshal(OutputSummary) error = %v", err)
	}
	for _, secret := range []string{string(untrustedReason), "private/customer-beta.go", "customer-beta-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("OutputSummary leaked %q: %s", secret, encoded)
		}
	}
	if got := codeIntelligenceInvalidRequestReasonForError(errors.New("future validation leaked private/customer-beta.go")); got != codeIntelligenceInvalidReasonUnknown {
		t.Fatalf("unknown validation reason = %q, want %q", got, codeIntelligenceInvalidReasonUnknown)
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

func TestCodeIntelligenceInvalidRequestReasonMatchesValidationMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codeIntelligenceInvalidRequestReason
	}{
		{name: "operation byte limit", err: errors.New("code intelligence operation exceeds the 64-byte limit"), want: codeIntelligenceInvalidReasonOperationTooLong},
		{name: "path byte limit", err: errors.New("code intelligence path exceeds the 4096-byte limit"), want: codeIntelligenceInvalidReasonPathTooLong},
		{name: "language byte limit", err: errors.New("code intelligence language exceeds the 64-byte limit"), want: codeIntelligenceInvalidReasonLanguageTooLong},
		{name: "query byte limit", err: errors.New("code intelligence query exceeds the 16384-byte limit"), want: codeIntelligenceInvalidReasonQueryTooLong},
		{name: "selector byte limit", err: errors.New("code intelligence selector exceeds the 128-byte limit"), want: codeIntelligenceInvalidReasonSelectorTooLong},
		{name: "operation", err: errors.New(`unsupported code intelligence operation "private"`), want: codeIntelligenceInvalidReasonUnsupportedOperation},
		{name: "query required", err: errors.New("query is required for workspace_symbols"), want: codeIntelligenceInvalidReasonQueryRequired},
		{name: "selector operation", err: errors.New("selector is only supported for structural_search"), want: codeIntelligenceInvalidReasonSelectorNotAllowed},
		{name: "selector shape", err: errors.New("structural selector must be a single ASCII tree-sitter node-kind token"), want: codeIntelligenceInvalidReasonSelectorInvalid},
		{name: "path required", err: errors.New("path is required for hover"), want: codeIntelligenceInvalidReasonPathRequired},
		{name: "position required", err: errors.New("line and column are required positive 1-based values for definition"), want: codeIntelligenceInvalidReasonPositionRequired},
		{name: "position invalid", err: errors.New("column 10 is past line 2"), want: codeIntelligenceInvalidReasonPositionInvalid},
		{name: "file unavailable", err: errors.New(`open workspace file "private.go": file does not exist`), want: codeIntelligenceInvalidReasonWorkspaceFileUnavailable},
		{name: "file not regular", err: errors.New(`workspace path "private" is not a regular file`), want: codeIntelligenceInvalidReasonWorkspaceFileNotRegular},
		{name: "file too large", err: errors.New(`workspace file "private.go" exceeds the 524288-byte code-intelligence limit`), want: codeIntelligenceInvalidReasonWorkspaceFileTooLarge},
		{name: "file changed", err: errors.New(`workspace file "private.go" changed while it was being read`), want: codeIntelligenceInvalidReasonWorkspaceFileChanged},
		{name: "file encoding", err: errors.New(`workspace file "private.go" is not valid UTF-8`), want: codeIntelligenceInvalidReasonWorkspaceFileInvalidEncoding},
		{name: "structural path", err: errors.New(`structural search path "private" must be a regular file or directory`), want: codeIntelligenceInvalidReasonStructuralPathInvalid},
		{name: "language required", err: errors.New("language is required when structural_search targets a directory"), want: codeIntelligenceInvalidReasonLanguageRequired},
		{name: "language unsupported", err: errors.New(`structural-search language "private" is not allowlisted`), want: codeIntelligenceInvalidReasonLanguageUnsupported},
		{name: "language path mismatch", err: errors.New(`language "go" does not match path "private.ts"`), want: codeIntelligenceInvalidReasonLanguagePathMismatch},
		{name: "unknown", err: errors.New("future validation includes private input"), want: codeIntelligenceInvalidReasonUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codeIntelligenceInvalidRequestReasonForError(test.err); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
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
