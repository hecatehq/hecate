package orchestrator

import (
	"strings"
	"time"

	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopApprovalGate struct {
	gatedTools map[string]struct{}
}

type agentLoopApprovalPause struct {
	Approval types.TaskApproval
	Step     types.TaskStep
}

func newAgentLoopApprovalGate(gatedTools []string) agentLoopApprovalGate {
	gated := make(map[string]struct{}, len(gatedTools))
	for _, name := range gatedTools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		gated[name] = struct{}{}
	}
	return agentLoopApprovalGate{gatedTools: gated}
}

func (g agentLoopApprovalGate) Evaluate(spec ExecutionSpec, turn, stepIndex int, when time.Time, calls []types.ToolCall) (agentLoopApprovalPause, bool) {
	gatedNames := g.gatedToolsInTurn(calls, spec.Task)
	if len(gatedNames) == 0 {
		return agentLoopApprovalPause{}, false
	}
	approval := buildApprovalForTurn(spec, turn, gatedNames, when)
	return agentLoopApprovalPause{
		Approval: approval,
		Step:     buildAwaitingApprovalStep(spec, stepIndex, turn, when, approval),
	}, true
}

func (g agentLoopApprovalGate) gatedToolsInTurn(calls []types.ToolCall, task types.Task) []string {
	seen := make(map[string]struct{}, len(calls))
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		if !g.isGated(c, task) {
			continue
		}
		if _, dup := seen[c.Function.Name]; dup {
			continue
		}
		seen[c.Function.Name] = struct{}{}
		out = append(out, c.Function.Name)
	}
	return out
}

func (g agentLoopApprovalGate) isGated(call types.ToolCall, task types.Task) bool {
	toolName := call.Function.Name
	// Hard policy refusals run before approval semantics. Asking an operator
	// to approve a call that the dispatcher must still refuse is misleading,
	// and would turn a fail-closed decision into an unnecessary pause.
	if agentPresetDisablesTools(task) || agentPresetBlocksNativeNetwork(task, toolName) || agentReadOnlyBlocksCall(task, call) || mcpServerPolicy(toolName, task) == types.MCPApprovalBlock {
		return false
	}
	if _, ok := g.gatedTools[toolName]; ok {
		return true
	}
	return mcpServerPolicy(toolName, task) == types.MCPApprovalRequireApproval
}

func mcpServerPolicy(toolName string, task types.Task) string {
	if !isMCPToolName(toolName) {
		return ""
	}
	server, _, ok := mcpclient.SplitNamespacedToolName(toolName)
	if !ok {
		return ""
	}
	for _, cfg := range task.MCPServers {
		if cfg.Name == server {
			return cfg.ApprovalPolicy
		}
	}
	return ""
}
