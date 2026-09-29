package orchestrator

import (
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

func chatBrowserTask() types.Task {
	task := browserFlowTask()
	task.OriginKind = "chat"
	task.OriginID = "chat_browser"
	task.AgentPresetBrowserAllowed = enabledAgentPresetToolsSnapshot()
	task.AgentPresetApprovalPolicy = types.AgentPresetApprovalInherit
	return task
}

func chatBrowserCalls() []types.ToolCall {
	return []types.ToolCall{
		agentLoopToolCall("inspect-chat", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`),
		validBrowserFlowCall("flow-chat"),
	}
}

func TestChatBrowserCatalogAndDispatcherRequireCompleteFrozenAuthority(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		mutate        func(*types.Task)
		inspect, flow bool
	}{
		{name: "both explicit", inspect: true, flow: true},
		{name: "inspection only", mutate: func(task *types.Task) { task.AgentPresetBrowserInteractionsAllowed = nil }, inspect: true},
		{name: "interaction only", mutate: func(task *types.Task) { task.AgentPresetBrowserAllowed = nil }, flow: true},
		{name: "missing origin id", mutate: func(task *types.Task) { task.OriginID = " " }},
		{name: "unsupported origin", mutate: func(task *types.Task) { task.OriginKind = "external_agent" }},
		{name: "missing preset", mutate: func(task *types.Task) { task.AgentPresetID = "" }},
		{name: "legacy tools", mutate: func(task *types.Task) { task.AgentPresetToolsEnabled = nil }},
		{name: "tools off", mutate: func(task *types.Task) { disabled := false; task.AgentPresetToolsEnabled = &disabled }},
		{name: "legacy grants", mutate: func(task *types.Task) {
			task.AgentPresetBrowserAllowed = nil
			task.AgentPresetBrowserInteractionsAllowed = nil
		}},
		{name: "missing origins", mutate: func(task *types.Task) { task.AgentPresetBrowserAllowedOrigins = nil }},
		{name: "invalid origins", mutate: func(task *types.Task) {
			task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test/path"}
		}},
		{name: "QA", mutate: func(task *types.Task) {
			task.WorkflowMode = types.WorkflowModeQA
			task.WorkflowVersion = taskworkflow.QAVersion
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := newAgentLoopSpec(t)
			spec.Task = chatBrowserTask()
			if test.mutate != nil {
				test.mutate(&spec.Task)
			}
			tools := agentToolDefinitionsForTask(spec.Task, agentToolDefinitionOptions{IncludeBrowserInspection: true, IncludeBrowserFlow: true})
			if hasToolDefinition(tools, AgentToolBrowserInspect) != test.inspect || hasToolDefinition(tools, AgentToolBrowserFlow) != test.flow {
				t.Fatalf("catalog inspect/flow mismatch, want %v/%v", test.inspect, test.flow)
			}
			inspector, runner := &fakeBrowserInspector{}, &fakeBrowserFlowRunner{}
			dispatcher := &agentLoopToolDispatcher{browserInspector: inspector, browserFlowRunner: runner}
			for i, call := range chatBrowserCalls() {
				allowed := test.inspect
				if i == 1 {
					allowed = test.flow
				}
				if allowed {
					continue
				} // Positive execution still requires the loop's per-call approval gate.
				result, err := dispatcher.Dispatch(t.Context(), spec, call, 1, nil, nil)
				if err != nil || !result.ToolError {
					t.Fatalf("blocked dispatch = %+v err=%v", result, err)
				}
			}
			if len(inspector.requests) != 0 || len(runner.requests) != 0 {
				t.Fatal("blocked chat browser call reached runtime")
			}
		})
	}
}

func TestChatBrowserMandatoryApprovalPreservesAllowBlockAndUnavailablePosture(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{types.AgentPresetApprovalInherit, types.AgentPresetApprovalAllow, types.AgentPresetApprovalRequire, types.AgentPresetApprovalBlock} {
		for _, call := range chatBrowserCalls() {
			t.Run(policy+"/"+call.Function.Name, func(t *testing.T) {
				spec := newAgentLoopSpec(t)
				spec.Task = chatBrowserTask()
				spec.Task.AgentPresetApprovalPolicy = policy
				gate := newAgentLoopApprovalGate(nil)
				gate.browserInspectionAvailable, gate.browserFlowAvailable = true, true
				_, paused := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{call})
				if paused != (policy != types.AgentPresetApprovalBlock) || gate.isBlockedByAgentPreset(call, spec) != (policy == types.AgentPresetApprovalBlock) {
					t.Fatalf("policy=%s pause=%v did not retain mandatory browser gate", policy, paused)
				}
				gate.browserInspectionAvailable, gate.browserFlowAvailable = false, false
				if _, paused := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{call}); paused {
					t.Fatal("unavailable runtime requested approval")
				}
			})
		}
	}
}

func TestChatBrowserLoopNeverStartsBeforeApprovalOrWithUnavailableRuntime(t *testing.T) {
	t.Parallel()
	for _, posture := range []string{"configured", "blocked", "unavailable"} {
		for _, call := range chatBrowserCalls() {
			t.Run(posture+"/"+call.Function.Name, func(t *testing.T) {
				inspector, runner := &fakeBrowserInspector{}, &fakeBrowserFlowRunner{}
				llm := &scriptedLLM{responses: []*types.ChatResponse{
					makeChatResp(makeAssistantMsg("", call)),
					makeChatResp(makeAssistantMsg("Browser action was not performed.")),
				}}
				opts := []AgentLoopExecutorOption{}
				if posture != "unavailable" {
					opts = append(opts, WithBrowserInspector(inspector), WithBrowserFlowRunner(runner))
				}
				loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{}, opts...)
				spec := newAgentLoopSpec(t)
				spec.Task = chatBrowserTask()
				if posture == "blocked" {
					spec.Task.AgentPresetApprovalPolicy = types.AgentPresetApprovalBlock
				}
				result, err := loop.Execute(t.Context(), spec)
				if err != nil {
					t.Fatal(err)
				}
				wantApproval := posture == "configured"
				if (result.Status == "awaiting_approval") != wantApproval || (len(result.PendingApprovals) != 0) != wantApproval {
					t.Fatalf("result = %+v", result)
				}
				if len(inspector.requests) != 0 || len(runner.requests) != 0 {
					t.Fatal("browser runtime started without approval")
				}
				if posture == "unavailable" && hasToolDefinition(llm.lastReqs[0].Tools, call.Function.Name) {
					t.Fatal("unavailable browser advertised")
				}
			})
		}
	}
}
