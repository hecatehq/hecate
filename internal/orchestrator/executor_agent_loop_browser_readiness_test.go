package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/pkg/types"
)

type dynamicBrowserRuntime struct {
	available atomic.Bool
	started   atomic.Int32
}

func (d *dynamicBrowserRuntime) Available() bool { return d.available.Load() }
func (d *dynamicBrowserRuntime) Inspect(context.Context, browserrunner.InspectRequest) (browserrunner.InspectResult, error) {
	d.started.Add(1)
	return browserrunner.InspectResult{}, browserrunner.ErrInspectionFailed
}
func (d *dynamicBrowserRuntime) RunFlow(context.Context, browserrunner.FlowRequest) (browserrunner.FlowResult, error) {
	d.started.Add(1)
	return browserrunner.FlowResult{}, browserrunner.ErrInspectionFailed
}

func TestDynamicBrowserRuntimeNeverWeakensMandatoryApproval(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{types.AgentPresetApprovalInherit, types.AgentPresetApprovalAllow, types.AgentPresetApprovalRequire, types.AgentPresetApprovalBlock} {
		for _, call := range chatBrowserCalls() {
			t.Run(policy+"/"+call.Function.Name, func(t *testing.T) {
				runtime := &dynamicBrowserRuntime{}
				loop := NewAgentLoopExecutor(nil, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{}, WithBrowserInspector(runtime), WithBrowserFlowRunner(runtime))
				spec := newAgentLoopSpec(t)
				spec.Task = chatBrowserTask()
				spec.Task.AgentPresetApprovalPolicy = policy
				// A disabled attempt never gains runtime authority mid-execution.
				dispatcher, gate := loop.browserRuntimeSnapshot()
				tools := agentToolDefinitionsForExecution(spec.Task, spec.Run, agentToolDefinitionOptions{})
				if browserRuntimeAvailable(runtime) {
					t.Fatal("unexpected availability")
				}
				_, paused := gate.EvaluateAdvertised(spec, 1, 1, time.Now(), []types.ToolCall{call}, tools)
				runtime.available.Store(true)
				result, err := dispatcher.Dispatch(t.Context(), spec, call, 1, nil, nil)
				if paused || err != nil || !result.ToolError || runtime.started.Load() != 0 {
					t.Fatalf("late enable acquired authority: paused=%v result=%+v err=%v", paused, result, err)
				}
				// The next execution observes enabled configuration and retains
				// mandatory approval even if availability changes again.
				_, gate = loop.browserRuntimeSnapshot()
				runtime.available.Store(false)
				_, paused = gate.EvaluateAdvertised(spec, 1, 1, time.Now(), []types.ToolCall{call}, tools)
				blocked := gate.isBlockedByAgentPreset(call, spec, &tools)
				if policy == types.AgentPresetApprovalBlock {
					if !blocked || paused {
						t.Fatal("block posture was weakened")
					}
				} else if !paused {
					t.Fatal("enabling after gate evaluation could bypass approval")
				}
				if runtime.started.Load() != 0 {
					t.Fatal("approval evaluation executed a browser")
				}
			})
		}
	}
}

func TestDynamicBrowserDisableClosesDispatchAndNextCatalog(t *testing.T) {
	t.Parallel()
	for _, call := range chatBrowserCalls() {
		t.Run(call.Function.Name, func(t *testing.T) {
			runtime := &dynamicBrowserRuntime{}
			runtime.available.Store(true)
			llm := &scriptedLLM{responses: []*types.ChatResponse{makeChatResp(makeAssistantMsg("No browser operation requested."))}}
			loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{}, WithBrowserInspector(runtime), WithBrowserFlowRunner(runtime))
			spec := newAgentLoopSpec(t)
			spec.Task = chatBrowserTask()
			runtime.available.Store(false)
			result, err := loop.toolDispatcher.Dispatch(t.Context(), spec, call, 1, nil, nil)
			if err != nil || !result.ToolError || runtime.started.Load() != 0 {
				t.Fatalf("disabled dispatch = %+v, %v", result, err)
			}
			if _, err := loop.Execute(t.Context(), spec); err != nil {
				t.Fatal(err)
			}
			if hasToolDefinition(llm.lastReqs[0].Tools, AgentToolBrowserInspect) || hasToolDefinition(llm.lastReqs[0].Tools, AgentToolBrowserFlow) {
				t.Fatal("disabled runtime advertised browser tools")
			}
		})
	}
}
