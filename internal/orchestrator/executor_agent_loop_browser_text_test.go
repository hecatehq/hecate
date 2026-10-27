package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestBrowserTextEntryRequiresFrozenGrantAndExactApproval(t *testing.T) {
	t.Parallel()
	call := agentLoopToolCall("fill", AgentToolBrowserFlow, `{"url":"https://app.example.test/","actions":[{"kind":"fill_text","role":"textbox","name":"Search","text":"ordinary search"}]}`)
	spec := newAgentLoopSpec(t)
	spec.Task = browserFlowTask()
	gate := newAgentLoopApprovalGate(nil)
	gate.browserFlowAvailable = true
	runner := &fakeBrowserFlowRunner{result: browserrunner.FlowResult{FinalURL: "https://app.example.test/", FinalOrigin: "https://app.example.test", Actions: []browserrunner.FlowActionResult{{Index: 0, Kind: "fill_text", Role: "textbox", Name: "Search", Status: "completed"}}}}
	dispatcher := &agentLoopToolDispatcher{browserFlowRunner: runner}
	if _, ok := gate.Evaluate(spec, 1, 2, time.Now(), []types.ToolCall{call}); ok {
		t.Fatal("legacy policy made text approvable")
	}
	denied, err := dispatcher.Dispatch(context.Background(), spec, call, 1, nil, nil)
	if err != nil || !denied.ToolError || len(runner.requests) != 0 {
		t.Fatalf("denied dispatch: %+v %v", denied, err)
	}
	allowed := true
	spec.Task.AgentPresetBrowserTextEntryAllowed = &allowed
	for _, policy := range []string{types.AgentPresetApprovalAllow, types.AgentPresetApprovalInherit} {
		spec.Task.AgentPresetApprovalPolicy = policy
		pause, ok := gate.Evaluate(spec, 1, 2, time.Now(), []types.ToolCall{call})
		if !ok || !strings.Contains(pause.Approval.Reason, `text="ordinary search"`) || !strings.Contains(pause.Approval.Reason, "disclosed to the page") {
			t.Fatalf("approval = %+v", pause)
		}
	}
	result, err := dispatcher.Dispatch(context.Background(), spec, call, 1, nil, nil)
	if err != nil || result.ToolError || len(runner.requests) != 1 || runner.requests[0].Actions[0].Text == nil || *runner.requests[0].Actions[0].Text != "ordinary search" {
		t.Fatalf("dispatch = %+v %v requests=%+v", result, err, runner.requests)
	}
	if strings.Contains(result.Artifacts[0].ContentText, "ordinary search") {
		t.Fatal("report duplicated input text")
	}
	spec.Task.AgentPresetApprovalPolicy = types.AgentPresetApprovalBlock
	if !gate.isBlockedByAgentPreset(call, spec) {
		t.Fatal("approval block allowed typing")
	}
}

func TestBrowserTextEntryCatalogAndStrictArguments(t *testing.T) {
	t.Parallel()
	task := browserFlowTask()
	for _, granted := range []bool{false, true} {
		task.AgentPresetBrowserTextEntryAllowed = &granted
		for _, tool := range agentToolDefinitionsForTask(task, agentToolDefinitionOptions{IncludeBrowserFlow: true}) {
			if tool.Function.Name == AgentToolBrowserFlow && strings.Contains(string(tool.Function.Parameters), "fill_text") != granted {
				t.Fatalf("grant %v: %s", granted, tool.Function.Parameters)
			}
		}
	}
	for _, raw := range []string{
		`{"url":"https://app.example.test/","actions":[{"kind":"fill_text","role":"textbox","name":"Search"}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"fill_text","role":"textbox","name":"Search","text":null}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"fill_text","role":"textbox","name":"Search","text":"a","text":"b"}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"click","role":"button","name":"Search","text":"a"}]}`,
		`{"url":"https://app.example.test/","actions":[{"kind":"fill_text","role":"textbox","name":"Search","text":"a","selector":"input"}]}`,
	} {
		if _, _, err := decodeBrowserFlowArgs(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
