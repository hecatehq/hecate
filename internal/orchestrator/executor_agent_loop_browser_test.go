package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/pkg/types"
)

type fakeBrowserInspector struct {
	requests []browserrunner.InspectRequest
	result   browserrunner.InspectResult
	err      error
}

func (f *fakeBrowserInspector) Inspect(_ context.Context, req browserrunner.InspectRequest) (browserrunner.InspectResult, error) {
	f.requests = append(f.requests, req)
	return f.result, f.err
}

func TestAgentLoopBrowserToolCatalogFailsClosedWithoutPresetSnapshot(t *testing.T) {
	t.Parallel()
	allowed := true
	disabled := false
	opts := agentToolDefinitionOptions{IncludeBrowserInspection: true}

	legacy := agentToolDefinitionsForTask(types.Task{}, opts)
	if hasToolDefinition(legacy, AgentToolBrowserInspect) {
		t.Fatal("browser_inspect advertised for a legacy task without a browser snapshot")
	}
	denied := agentToolDefinitionsForTask(types.Task{
		AgentPresetBrowserAllowed:        &disabled,
		AgentPresetBrowserAllowedOrigins: []string{"https://example.test"},
	}, opts)
	if hasToolDefinition(denied, AgentToolBrowserInspect) {
		t.Fatal("browser_inspect advertised for a browser-denied preset snapshot")
	}
	manual := agentToolDefinitionsForTask(types.Task{
		OriginKind:                       "project_work_item",
		AgentPresetBrowserAllowed:        &allowed,
		AgentPresetBrowserAllowedOrigins: []string{"https://example.test"},
	}, opts)
	if hasToolDefinition(manual, AgentToolBrowserInspect) {
		t.Fatal("browser_inspect advertised for a manually constructed task without a resolved preset ID")
	}
	enabled := agentToolDefinitionsForTask(types.Task{
		AgentPresetID:                    "prof_browser",
		OriginKind:                       "project_work_item",
		AgentPresetBrowserAllowed:        &allowed,
		AgentPresetBrowserAllowedOrigins: []string{"https://example.test"},
	}, opts)
	if !hasToolDefinition(enabled, AgentToolBrowserInspect) {
		t.Fatalf("browser_inspect missing from enabled catalog: %+v", enabled)
	}
	chat := agentToolDefinitionsForTask(types.Task{
		OriginKind:                       "chat",
		AgentPresetBrowserAllowed:        &allowed,
		AgentPresetBrowserAllowedOrigins: []string{"https://example.test"},
	}, opts)
	if hasToolDefinition(chat, AgentToolBrowserInspect) {
		t.Fatal("browser_inspect advertised for a non-project task")
	}
}

func TestAgentLoopBrowserInspectionAlwaysRequiresSpecificApproval(t *testing.T) {
	t.Parallel()
	allowed := true
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}
	gate := newAgentLoopApprovalGate(nil)
	gate.browserInspectionAvailable = true

	pause, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`),
	})
	if !ok {
		t.Fatal("browser inspection did not pause for approval")
	}
	if !strings.Contains(pause.Approval.Reason, AgentToolBrowserInspect) || !strings.Contains(pause.Approval.Reason, "https://app.example.test/reports") {
		t.Fatalf("approval reason = %q, want tool and exact requested page", pause.Approval.Reason)
	}
	for _, detail := range []string{"read-only static inspection", "fresh temporary browser profiles", "not a hard identity or network boundary", "cannot click"} {
		if !strings.Contains(pause.Approval.Reason, detail) {
			t.Fatalf("approval reason missing %q: %q", detail, pause.Approval.Reason)
		}
	}

	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("browser-other", AgentToolBrowserInspect, `{"url":"https://other.example.test/"}`),
	}); ok {
		t.Fatal("out-of-policy browser inspection asked for approval instead of failing closed")
	}
	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("browser-query", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports?token=secret"}`),
	}); ok {
		t.Fatal("query-bearing browser inspection asked for approval instead of failing closed")
	}
	manualTask := spec.Task
	manualTask.AgentPresetID = ""
	manualCall := agentLoopToolCall("browser-manual", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`)
	if browserInspectionCallAllowed(manualTask, manualCall) {
		t.Fatal("manually constructed browser task is approval-eligible without a resolved preset")
	}
	if detail := browserApprovalDetail([]types.ToolCall{manualCall}, manualTask); detail != "" {
		t.Fatalf("manual browser call contributed approval detail %q", detail)
	}
}

func TestAgentLoopBrowserInspectionPersistsSafeTextEvidence(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{result: browserrunner.InspectResult{
		FinalURL: "https://app.example.test/reports?token=secret#fragment",
		Title:    "Quarterly report",
		Accessibility: []browserrunner.AccessibilityNode{{
			Role: "heading",
			Name: "Revenue",
		}},
		Console: []browserrunner.ConsoleMessage{{Level: "warning", Text: "slow endpoint"}},
		Network: browserrunner.NetworkSummary{Requests: 3, Navigations: 1, BlockedRequests: 1},
	}}
	dispatcher := &agentLoopToolDispatcher{browserInspector: inspector}
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`), 3, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.ToolError || result.Step == nil || result.Step.Status != "completed" {
		t.Fatalf("Dispatch() = %+v, want completed browser evidence", result)
	}
	if len(inspector.requests) != 1 || inspector.requests[0].URL != "https://app.example.test/reports" {
		t.Fatalf("browser requests = %+v", inspector.requests)
	}
	if got := inspector.requests[0].AllowedOrigins; len(got) != 1 || got[0] != "https://app.example.test" {
		t.Fatalf("browser request origins = %v, want only the approved target origin", got)
	}
	if result.Step.Input["origin"] != "https://app.example.test" {
		t.Fatalf("step input = %+v, want redacted origin", result.Step.Input)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v, want one browser evidence artifact", result.Artifacts)
	}
	artifact := result.Artifacts[0]
	if artifact.Kind != "browser_evidence" || artifact.MimeType != "text/plain" || artifact.StorageKind != "inline" {
		t.Fatalf("artifact = %+v, want text browser evidence", artifact)
	}
	if strings.Contains(artifact.ContentText, "token=secret") || strings.Contains(artifact.ContentText, "operator_token=secret") {
		t.Fatalf("artifact leaked URL query data: %q", artifact.ContentText)
	}
	if !strings.Contains(result.Text, "Untrusted browser evidence") || !strings.Contains(artifact.ContentText, "Revenue") {
		t.Fatalf("tool evidence = %q artifact = %q", result.Text, artifact.ContentText)
	}
}

func TestAgentLoopBrowserInspectionScopesEachCallToItsApprovedOrigin(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{result: browserrunner.InspectResult{
		FinalURL: "https://status.example.test/health",
	}}
	dispatcher := &agentLoopToolDispatcher{browserInspector: inspector}
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{
		"https://app.example.test",
		"https://status.example.test",
	}

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://status.example.test/health"}`), 3, nil, nil)
	if err != nil || result.ToolError {
		t.Fatalf("Dispatch() result=%+v err=%v", result, err)
	}
	if len(inspector.requests) != 1 {
		t.Fatalf("browser requests = %+v, want one request", inspector.requests)
	}
	if got := inspector.requests[0].AllowedOrigins; len(got) != 1 || got[0] != "https://status.example.test" {
		t.Fatalf("browser request origins = %v, want only the approved status origin", got)
	}
}

func TestAgentLoopBrowserInspectionRejectsInspectorFinalOriginOutsideCallScope(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{result: browserrunner.InspectResult{
		FinalURL: "https://status.example.test/health",
	}}
	dispatcher := &agentLoopToolDispatcher{browserInspector: inspector}
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{
		"https://app.example.test",
		"https://status.example.test",
	}

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`), 3, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !result.ToolError || result.Step == nil || !strings.Contains(result.Step.Error, "unexpected final origin") {
		t.Fatalf("Dispatch() = %+v, want final-origin policy failure", result)
	}
	if len(result.Artifacts) != 0 {
		t.Fatalf("unexpected evidence artifacts after final-origin policy failure: %+v", result.Artifacts)
	}
}

func TestAgentLoopBrowserInspectionRejectsQueryBeforeDispatch(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{}
	dispatcher := &agentLoopToolDispatcher{browserInspector: inspector}
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports?operator_token=secret"}`), 3, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Text != "invalid arguments for "+AgentToolBrowserInspect || result.Step != nil {
		t.Fatalf("Dispatch() = %+v, want generic invalid-arguments result", result)
	}
	if len(inspector.requests) != 0 {
		t.Fatalf("browser inspector received a query-bearing target: %+v", inspector.requests)
	}
	if strings.Contains(result.Text, "operator_token") {
		t.Fatalf("browser policy result leaked query data: %+v", result)
	}
}

func TestAgentLoopBrowserInspectionApprovalListsEveryRequestedPage(t *testing.T) {
	t.Parallel()
	allowed := true
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test", "https://status.example.test"}
	gate := newAgentLoopApprovalGate(nil)
	gate.browserInspectionAvailable = true

	pause, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://status.example.test/health"}`),
		agentLoopToolCall("browser-2", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`),
	})
	if !ok {
		t.Fatal("browser inspection did not pause for approval")
	}
	for _, detail := range []string{"2 requested pages", "https://app.example.test/reports", "https://status.example.test/health"} {
		if !strings.Contains(pause.Approval.Reason, detail) {
			t.Fatalf("approval reason missing %q: %q", detail, pause.Approval.Reason)
		}
	}
}

func TestAgentLoopBrowserInspectionRejectsUnknownOrDuplicateArguments(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{}
	dispatcher := &agentLoopToolDispatcher{browserInspector: inspector}
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}

	for _, raw := range []string{
		`{"url":"https://app.example.test/reports","note":"https://other.example.test/?operator_token=secret"}`,
		`{"url":"https://app.example.test/reports","url":"https://other.example.test/?operator_token=secret"}`,
	} {
		t.Run(raw[:12], func(t *testing.T) {
			result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall("browser-1", AgentToolBrowserInspect, raw), 3, nil, nil)
			if err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}
			if result.Text != "invalid arguments for "+AgentToolBrowserInspect {
				t.Fatalf("Dispatch() text = %q", result.Text)
			}
			if strings.Contains(result.Text, "operator_token") {
				t.Fatalf("Dispatch() leaked rejected arguments: %q", result.Text)
			}
		})
	}
	if len(inspector.requests) != 0 {
		t.Fatalf("browser inspector received rejected arguments: %+v", inspector.requests)
	}
}

func TestAgentLoopBrowserInspectionRejectsTargetTooLongForApproval(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{}
	dispatcher := &agentLoopToolDispatcher{browserInspector: inspector}
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}
	url := "https://app.example.test/" + strings.Repeat("a", maxBrowserApprovalTargetBytes)
	raw, err := json.Marshal(browserInspectArgs{URL: url})
	if err != nil {
		t.Fatalf("marshal browser arguments: %v", err)
	}
	call := agentLoopToolCall("browser-long", AgentToolBrowserInspect, string(raw))

	result, err := dispatcher.Dispatch(context.Background(), spec, call, 3, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Text != "invalid arguments for "+AgentToolBrowserInspect || result.Step != nil {
		t.Fatalf("Dispatch() = %+v, want privacy-safe invalid arguments", result)
	}
	if len(inspector.requests) != 0 {
		t.Fatalf("browser inspector received oversized approval target: %+v", inspector.requests)
	}

	gate := newAgentLoopApprovalGate(nil)
	gate.browserInspectionAvailable = true
	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{call}); ok {
		t.Fatal("oversized target requested approval instead of failing closed")
	}
	sanitized := sanitizeBrowserInspectionToolCalls(makeAssistantMsg("", call))
	if got := sanitized.ToolCalls[0].Function.Arguments; got != `{}` {
		t.Fatalf("oversized arguments = %q, want {} before persistence", got)
	}
}

func TestAgentLoopBrowserInspectionRedactsRejectedArgumentsBeforePersistence(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("checking", agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports","note":"https://other.example.test/?operator_token=secret"}`))),
		makeChatResp(makeAssistantMsg("The requested browser URL needs a query-free path.")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}, WithBrowserInspector(inspector))
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}
	var artifacts []types.TaskArtifact
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		artifacts = append(artifacts, artifact)
		return nil
	}
	var events captureRunEvent
	spec.EmitRunEvent = events.emit

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("Execute() status = %q, want completed", result.Status)
	}
	if len(inspector.requests) != 0 {
		t.Fatalf("browser inspector received a rejected target: %+v", inspector.requests)
	}
	artifactJSON, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatalf("marshal artifacts: %v", err)
	}
	eventJSON, err := json.Marshal(events.events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, persisted := range []string{string(artifactJSON), string(eventJSON)} {
		if strings.Contains(persisted, "operator_token=secret") || strings.Contains(persisted, "https://other.example.test/?") {
			t.Fatalf("persisted browser call leaked rejected query data: %s", persisted)
		}
	}
}

func TestSanitizeBrowserInspectionToolCallsCanonicalizesSafeArguments(t *testing.T) {
	t.Parallel()
	message := makeAssistantMsg("", agentLoopToolCall("browser-1", AgentToolBrowserInspect, ` { "url" : "https://app.example.test/reports" } `))
	sanitized := sanitizeBrowserInspectionToolCalls(message)
	if got := sanitized.ToolCalls[0].Function.Arguments; got != `{"url":"https://app.example.test/reports"}` {
		t.Fatalf("sanitized arguments = %q", got)
	}

	message = makeAssistantMsg("", agentLoopToolCall("browser-2", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports","url":"https://other.example.test/?operator_token=secret"}`))
	sanitized = sanitizeBrowserInspectionToolCalls(message)
	if got := sanitized.ToolCalls[0].Function.Arguments; got != `{}` {
		t.Fatalf("duplicate-key arguments = %q, want {}", got)
	}
}

func TestAgentLoopBrowserInspectionPausesBeforeRuntimeDispatch(t *testing.T) {
	t.Parallel()
	allowed := true
	inspector := &fakeBrowserInspector{}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("browser-1", AgentToolBrowserInspect, `{"url":"https://app.example.test/"}`))),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 8, nil, HTTPRequestPolicy{}, WithBrowserInspector(inspector))
	spec := newAgentLoopSpec(t)
	spec.Task.AgentPresetID = "prof_browser"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "awaiting_approval" || len(result.PendingApprovals) != 1 {
		t.Fatalf("Execute() result = %+v, want one pending approval", result)
	}
	if len(inspector.requests) != 0 {
		t.Fatalf("browser inspector ran before approval: %+v", inspector.requests)
	}
}
