package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/pkg/types"
)

type fakeBrowserFlowRunner struct {
	requests []browserrunner.FlowRequest
	result   browserrunner.FlowResult
	err      error
}

func (f *fakeBrowserFlowRunner) RunFlow(_ context.Context, request browserrunner.FlowRequest) (browserrunner.FlowResult, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func browserFlowTask() types.Task {
	allowed := true
	return types.Task{
		AgentPresetID:                         "prof_browser_flow",
		OriginKind:                            "project_work_item",
		AgentPresetBrowserInteractionsAllowed: &allowed,
		AgentPresetBrowserAllowedOrigins:      []string{"https://app.example.test", "https://status.example.test"},
	}
}

func validBrowserFlowCall(id string) types.ToolCall {
	return agentLoopToolCall(id, AgentToolBrowserFlow, `{"url":"https://app.example.test/reports","actions":[{"kind":"click","role":"button","name":"Continue"},{"kind":"wait_for","role":"status","name":"Ready"}]}`)
}

func TestAgentLoopBrowserFlowCatalogRequiresIndependentProjectPresetGrant(t *testing.T) {
	t.Parallel()
	enabled := true
	disabled := false
	opts := agentToolDefinitionOptions{IncludeBrowserInspection: true, IncludeBrowserFlow: true}

	staticOnly := types.Task{
		AgentPresetID:                    "prof_static",
		OriginKind:                       "project_work_item",
		AgentPresetBrowserAllowed:        &enabled,
		AgentPresetBrowserAllowedOrigins: []string{"https://app.example.test"},
	}
	tools := agentToolDefinitionsForTask(staticOnly, opts)
	if !hasToolDefinition(tools, AgentToolBrowserInspect) || hasToolDefinition(tools, AgentToolBrowserFlow) {
		t.Fatalf("static-only tools = %+v, want inspect without flow", tools)
	}

	interactionOnly := staticOnly
	interactionOnly.AgentPresetBrowserAllowed = &disabled
	interactionOnly.AgentPresetBrowserInteractionsAllowed = &enabled
	tools = agentToolDefinitionsForTask(interactionOnly, opts)
	if hasToolDefinition(tools, AgentToolBrowserInspect) || !hasToolDefinition(tools, AgentToolBrowserFlow) {
		t.Fatalf("interaction-only tools = %+v, want flow without inspect", tools)
	}

	legacy := interactionOnly
	legacy.AgentPresetBrowserInteractionsAllowed = nil
	if hasToolDefinition(agentToolDefinitionsForTask(legacy, opts), AgentToolBrowserFlow) {
		t.Fatal("browser_flow advertised for a legacy task without the interaction snapshot")
	}
	chat := interactionOnly
	chat.OriginKind = "chat"
	if hasToolDefinition(agentToolDefinitionsForTask(chat, opts), AgentToolBrowserFlow) {
		t.Fatal("browser_flow advertised for Hecate Chat")
	}
	manual := interactionOnly
	manual.AgentPresetID = ""
	if hasToolDefinition(agentToolDefinitionsForTask(manual, opts), AgentToolBrowserFlow) {
		t.Fatal("browser_flow advertised without a resolved preset marker")
	}
}

func TestAgentLoopBrowserFlowAlwaysRequiresCompleteApprovalBeforeRuntime(t *testing.T) {
	t.Parallel()
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()
	gate := newAgentLoopApprovalGate(nil)
	gate.browserFlowAvailable = true

	pause, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{validBrowserFlowCall("flow-1")})
	if !ok {
		t.Fatal("browser flow did not pause for approval")
	}
	for _, part := range []string{
		"browser_flow",
		"https://app.example.test/reports",
		`1. click role="button" name="Continue"`,
		`2. wait_for role="status" name="Ready"`,
		"Page scripts and same-origin GET/HEAD requests may run",
		"cannot type, upload, download",
		"not a hard OS or enterprise identity boundary",
	} {
		if !strings.Contains(pause.Approval.Reason, part) {
			t.Fatalf("approval reason missing %q: %q", part, pause.Approval.Reason)
		}
	}
	if len(pause.Approval.ActionSummary) != 1 || !strings.Contains(pause.Approval.ActionSummary[0], "actions=2") || pause.Approval.ActionSummaryIncomplete {
		t.Fatalf("approval action summary = %v incomplete=%v", pause.Approval.ActionSummary, pause.Approval.ActionSummaryIncomplete)
	}

	bad := agentLoopToolCall("flow-other", AgentToolBrowserFlow, `{"url":"https://other.example.test/","actions":[{"kind":"wait_for","role":"status","name":"Ready"}]}`)
	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{bad}); ok {
		t.Fatal("out-of-policy browser flow requested approval")
	}
}

func TestAgentLoopBrowserFlowPresetBlockOverridesMandatoryApproval(t *testing.T) {
	t.Parallel()
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()
	spec.Task.AgentPresetApprovalPolicy = types.AgentPresetApprovalBlock
	gate := newAgentLoopApprovalGate(nil)
	gate.browserFlowAvailable = true
	call := validBrowserFlowCall("flow-blocked")

	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{call}); ok {
		t.Fatal("blocked browser flow paused for approval")
	}
	if !gate.isBlockedByAgentPreset(call, spec) {
		t.Fatal("preset block did not override mandatory browser-flow approval")
	}

	spec.Task.AgentPresetApprovalPolicy = types.AgentPresetApprovalAllow
	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{call}); !ok {
		t.Fatal("preset allow weakened the mandatory browser-flow approval gate")
	}
}

func TestAgentLoopBrowserFlowDispatchScopesOriginAndPersistsBoundedEvidence(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{result: browserrunner.FlowResult{
		FinalURL:    "https://app.example.test/complete?token=secret",
		FinalOrigin: "https://app.example.test",
		Title:       "Completed",
		InitialAccessibility: []browserrunner.AccessibilityNode{{
			Role: "button", Name: "Continue",
		}},
		FinalAccessibility: []browserrunner.AccessibilityNode{{
			Role: "status", Name: "Ready",
		}},
		Actions: []browserrunner.FlowActionResult{
			{Index: 0, Kind: "click", Role: "button", Name: "Continue", Status: "completed"},
			{Index: 1, Kind: "wait_for", Role: "status", Name: "Ready", Status: "completed"},
		},
		Network: browserrunner.NetworkSummary{Requests: 4, Navigations: 2},
	}}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := dispatcher.Dispatch(context.Background(), spec, validBrowserFlowCall("flow-1"), 3, nil, nil)
	if err != nil || result.ToolError || result.Step == nil || result.Step.Status != "completed" {
		t.Fatalf("Dispatch() = %+v, err=%v", result, err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("flow requests = %+v", runner.requests)
	}
	request := runner.requests[0]
	if request.AllowedOrigin != "https://app.example.test" || len(request.Actions) != 2 {
		t.Fatalf("flow request = %+v, want one exact origin and two actions", request)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Kind != "browser_flow_evidence" || result.Artifacts[0].MimeType != "text/plain" {
		t.Fatalf("flow artifacts = %+v", result.Artifacts)
	}
	artifact := result.Artifacts[0]
	if strings.Contains(artifact.ContentText, "token=secret") || strings.Contains(artifact.ContentText, "cookie") {
		t.Fatalf("flow artifact leaked sensitive URL/storage data: %q", artifact.ContentText)
	}
	for _, part := range []string{"Browser interaction evidence", "Continue", "Ready", "Final URL: https://app.example.test/complete"} {
		if !strings.Contains(artifact.ContentText, part) {
			t.Fatalf("flow artifact missing %q: %q", part, artifact.ContentText)
		}
	}
}

func TestAgentLoopBrowserFlowPersistsPartialEvidenceAfterPossibleClick(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{
		result: browserrunner.FlowResult{
			FinalURL:    "https://app.example.test/reports",
			FinalOrigin: "https://app.example.test",
			Actions: []browserrunner.FlowActionResult{
				{Index: 0, Kind: "click", Role: "button", Name: "Continue", Status: "completed"},
				{Index: 1, Kind: "wait_for", Role: "status", Name: "Ready", Status: "failed", ErrorKind: "target_not_found"},
			},
		},
		err: browserrunner.ErrFlowTargetNotFound,
	}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := dispatcher.Dispatch(context.Background(), spec, validBrowserFlowCall("flow-1"), 3, nil, nil)
	if err != nil || !result.ToolError || result.Step == nil || result.Step.Status != "failed" {
		t.Fatalf("Dispatch() = %+v, err=%v", result, err)
	}
	if len(result.Artifacts) != 1 || !strings.Contains(result.Artifacts[0].Description, "earlier click may already have changed") {
		t.Fatalf("partial evidence artifact = %+v", result.Artifacts)
	}
	if !strings.Contains(result.Text, "Partial untrusted browser interaction evidence") || !strings.Contains(result.Artifacts[0].ContentText, "target_not_found") {
		t.Fatalf("partial tool result = %q artifact=%q", result.Text, result.Artifacts[0].ContentText)
	}
}

func TestAgentLoopBrowserFlowRejectsRunnerEvidenceOutsideApprovedContract(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{result: browserrunner.FlowResult{
		FinalURL:    "https://other.example.test/private?token=secret",
		FinalOrigin: "https://other.example.test",
		Actions: []browserrunner.FlowActionResult{{
			Index: 0, Kind: "click", Role: "button", Name: "Different target", Status: "completed",
		}},
	}}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := dispatcher.Dispatch(context.Background(), spec, validBrowserFlowCall("flow-1"), 3, nil, nil)
	if err != nil || !result.ToolError || result.Step == nil || !strings.Contains(result.Text, "invalid evidence") || !strings.Contains(result.Text, "approved actions may already have run") {
		t.Fatalf("Dispatch() = %+v, err=%v", result, err)
	}
	if len(result.Artifacts) != 0 || strings.Contains(result.Text, "token=secret") || strings.Contains(result.Text, "Different target") {
		t.Fatalf("invalid runner evidence escaped typed boundary: %+v", result)
	}
}

func TestBrowserFlowResultMatchesOnlyCompletedPrefixAndTerminalFailure(t *testing.T) {
	t.Parallel()
	args, _, err := decodeBrowserFlowArgs(validBrowserFlowCall("flow-1").Function.Arguments)
	if err != nil {
		t.Fatalf("decode flow args: %v", err)
	}
	action := func(index int, status, errorKind string) browserrunner.FlowActionResult {
		want := args.Actions[index]
		return browserrunner.FlowActionResult{Index: index, Kind: want.Kind, Role: want.Role, Name: want.Name, Status: status, ErrorKind: errorKind}
	}
	for name, test := range map[string]struct {
		actions []browserrunner.FlowActionResult
		want    bool
	}{
		"completed prefix":     {actions: []browserrunner.FlowActionResult{action(0, browserrunner.FlowActionStatusCompleted, "")}, want: true},
		"terminal failure":     {actions: []browserrunner.FlowActionResult{action(0, browserrunner.FlowActionStatusCompleted, ""), action(1, browserrunner.FlowActionStatusFailed, "target_not_found")}, want: true},
		"action after failure": {actions: []browserrunner.FlowActionResult{action(0, browserrunner.FlowActionStatusFailed, "target_not_found"), action(1, browserrunner.FlowActionStatusCompleted, "")}},
		"multiple failures":    {actions: []browserrunner.FlowActionResult{action(0, browserrunner.FlowActionStatusFailed, "target_not_found"), action(1, browserrunner.FlowActionStatusFailed, "flow_failed")}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := browserrunner.FlowResult{FinalOrigin: "https://app.example.test", Actions: test.actions}
			if got := browserFlowResultMatchesArgs(result, args, "https://app.example.test", false); got != test.want {
				t.Fatalf("browserFlowResultMatchesArgs() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBrowserFlowReportQuotesPageControlledAccessibilityFields(t *testing.T) {
	t.Parallel()
	report := formatBrowserFlowReport(browserrunner.FlowResult{
		InitialAccessibility: []browserrunner.AccessibilityNode{{
			Role: "button; name=fake",
			Name: `Continue; description=fake "quoted" \\ path`,
		}},
	}, "https://app.example.test")
	for _, want := range []string{`role="button; name=fake"`, `name="Continue; description=fake \"quoted\" \\\\ path"`} {
		if !strings.Contains(report, want) {
			t.Fatalf("quoted flow report missing %q: %q", want, report)
		}
	}
}

func TestAgentLoopBrowserFlowSurfacesProfileCleanupActionAndPartialEvidence(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{
		result: browserrunner.FlowResult{
			FinalURL:    "https://app.example.test/reports",
			FinalOrigin: "https://app.example.test",
			Actions: []browserrunner.FlowActionResult{{
				Index: 0, Kind: "click", Role: "button", Name: "Continue", Status: "completed",
			}},
		},
		err: browserrunner.ErrProfileCleanupFailed,
	}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := dispatcher.Dispatch(context.Background(), spec, validBrowserFlowCall("flow-1"), 3, nil, nil)
	if err != nil || !result.ToolError || result.Step == nil || len(result.Artifacts) != 1 {
		t.Fatalf("Dispatch() = %+v, err=%v", result, err)
	}
	for _, want := range []string{"profile cleanup failed", "approved actions may already have run", "stale hecate-browser-flow-*"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("cleanup guidance missing %q: %q", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "/tmp/") || strings.Contains(result.Text, `C:\\`) {
		t.Fatalf("cleanup guidance exposed a concrete path: %q", result.Text)
	}
	if strings.Contains(result.Artifacts[0].Description, "profile data is retained") {
		t.Fatalf("cleanup-failure artifact contradicted retained profile state: %q", result.Artifacts[0].Description)
	}
}

func TestAgentLoopBrowserFlowPreservesPolicyAndCleanupFailures(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{
		result: browserrunner.FlowResult{
			FinalOrigin: "https://app.example.test",
			Actions: []browserrunner.FlowActionResult{{
				Index: 0, Kind: "click", Role: "button", Name: "Continue", Status: "failed", ErrorKind: "policy_violation",
			}},
		},
		err: errors.Join(browserrunner.ErrFlowPolicyViolation, browserrunner.ErrProfileCleanupFailed),
	}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := dispatcher.Dispatch(context.Background(), spec, validBrowserFlowCall("flow-1"), 3, nil, nil)
	if err != nil || !result.ToolError || result.Step == nil || len(result.Artifacts) != 1 {
		t.Fatalf("Dispatch() = %+v, err=%v", result, err)
	}
	for _, want := range []string{"transport policy", "was blocked", "profile cleanup failed", "approved actions may already have run"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("combined failure guidance missing %q: %q", want, result.Text)
		}
	}
}

func TestBrowserFlowErrorMessageAlwaysPreservesCleanupFailure(t *testing.T) {
	t.Parallel()
	for name, primary := range map[string]error{
		"target":      browserrunner.ErrFlowTargetNotFound,
		"private":     browserrunner.ErrPrivateNetwork,
		"unavailable": browserrunner.ErrUnavailable,
		"generic":     browserrunner.ErrInspectionFailed,
		"policy":      browserrunner.ErrFlowPolicyViolation,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			message := browserFlowErrorMessage(errors.Join(primary, browserrunner.ErrProfileCleanupFailed))
			for _, want := range []string{"profile cleanup failed", "stale hecate-browser-flow-*", "operating-system temporary directory"} {
				if !strings.Contains(message, want) {
					t.Fatalf("cleanup guidance missing %q: %q", want, message)
				}
			}
			if strings.Count(message, "profile cleanup failed") != 1 {
				t.Fatalf("cleanup guidance was duplicated: %q", message)
			}
		})
	}
}

func TestAgentLoopBrowserFlowPreservesCleanupFailureWhenEvidenceIsInvalid(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{
		result: browserrunner.FlowResult{
			FinalURL:    "https://other.example.test/private?token=secret",
			FinalOrigin: "https://other.example.test",
		},
		err: browserrunner.ErrProfileCleanupFailed,
	}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := dispatcher.Dispatch(context.Background(), spec, validBrowserFlowCall("flow-1"), 3, nil, nil)
	if err != nil || !result.ToolError || result.Step == nil {
		t.Fatalf("Dispatch() = %+v, err=%v", result, err)
	}
	for _, want := range []string{"invalid evidence", "profile cleanup failed", "stale hecate-browser-flow-*"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("invalid-evidence cleanup guidance missing %q: %q", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "token=secret") || strings.Contains(result.Text, "other.example.test") {
		t.Fatalf("invalid runner evidence escaped the cleanup warning: %q", result.Text)
	}
}

func TestDecodeBrowserFlowArgsRejectsUnknownDuplicateAndSensitiveValues(t *testing.T) {
	t.Parallel()
	valid := validBrowserFlowCall("flow-1").Function.Arguments
	args, canonical, err := decodeBrowserFlowArgs(" \n " + valid + " \n")
	if err != nil || canonical != valid || len(args.Actions) != 2 {
		t.Fatalf("decodeBrowserFlowArgs() = %+v, %q, %v", args, canonical, err)
	}

	for _, raw := range []string{
		`{"url":"https://app.example.test/?token=secret","actions":[{"kind":"wait_for","role":"status","name":"Ready"}]}`,
		`{"url":"https://app.example.test/","url":"https://other.example.test/","actions":[{"kind":"wait_for","role":"status","name":"Ready"}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"wait_for","role":"status","name":"Ready","selector":"#secret"}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"click","role":"heading","name":"Summary"}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"wait_for","role":"status","name":"Ready\nNow"}]}`,
	} {
		if _, _, err := decodeBrowserFlowArgs(raw); !errors.Is(err, errInvalidBrowserFlowArguments) {
			t.Fatalf("decodeBrowserFlowArgs(%q) error = %v", raw, err)
		}
		message := sanitizeBrowserFlowToolCalls(makeAssistantMsg("", agentLoopToolCall("flow", AgentToolBrowserFlow, raw)))
		if got := message.ToolCalls[0].Function.Arguments; got != `{}` {
			t.Fatalf("sanitized rejected flow = %q, want {}", got)
		}
	}
}

func TestValidateAgentToolCallBatchAllowsAtMostOneBrowserFlow(t *testing.T) {
	t.Parallel()
	if err := validateAgentToolCallBatch([]types.ToolCall{validBrowserFlowCall("flow-1")}); err != nil {
		t.Fatalf("one browser flow rejected: %v", err)
	}
	err := validateAgentToolCallBatch([]types.ToolCall{validBrowserFlowCall("flow-1"), validBrowserFlowCall("flow-2")})
	if err == nil || !strings.Contains(err.Error(), "more than one browser flow") {
		t.Fatalf("two browser flows error = %v", err)
	}
}

func TestAgentLoopBrowserFlowPausesBeforeRuntimeDispatch(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{}
	llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("", validBrowserFlowCall("flow-1")))}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}, WithBrowserFlowRunner(runner))
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "awaiting_approval" || len(result.PendingApprovals) != 1 {
		t.Fatalf("Execute() result = %+v", result)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("browser flow ran before approval: %+v", runner.requests)
	}
}

func TestAgentLoopBrowserFlowPresetBlockNeverStartsBrowser(t *testing.T) {
	t.Parallel()
	runner := &fakeBrowserFlowRunner{}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", validBrowserFlowCall("flow-1"))),
		makeChatResp(makeAssistantMsg("The work policy blocked browser interaction.")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}, WithBrowserFlowRunner(runner))
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()
	spec.Task.AgentPresetApprovalPolicy = types.AgentPresetApprovalBlock

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "completed" || len(result.PendingApprovals) != 0 {
		t.Fatalf("Execute() result = %+v, want completed recovery without approval", result)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("browser flow ran despite frozen block policy: %+v", runner.requests)
	}
	foundDenied := false
	for _, step := range result.Steps {
		if step.ErrorKind == "agent_preset_approval_denied" {
			foundDenied = true
		}
	}
	if !foundDenied {
		t.Fatalf("steps = %+v, want Agent Preset approval refusal", result.Steps)
	}
}

func TestSanitizeBrowserFlowToolCallsCanonicalizesBeforePersistence(t *testing.T) {
	t.Parallel()
	raw := ` { "actions" : [ { "name" : "Continue", "role" : "button", "kind" : "click" } ], "url" : "https://app.example.test/reports" } `
	message := sanitizeBrowserFlowToolCalls(makeAssistantMsg("", agentLoopToolCall("flow", AgentToolBrowserFlow, raw)))
	wantStruct := browserFlowArgs{URL: "https://app.example.test/reports", Actions: []browserFlowActionArgs{{Kind: "click", Role: "button", Name: "Continue"}}}
	wantJSON, err := json.Marshal(wantStruct)
	if err != nil {
		t.Fatalf("marshal wanted args: %v", err)
	}
	if got := message.ToolCalls[0].Function.Arguments; got != string(wantJSON) {
		t.Fatalf("sanitized arguments = %q, want %q", got, string(wantJSON))
	}
}
