package orchestrator

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopApprovalGate struct {
	gatedTools                 map[string]struct{}
	browserInspectionAvailable bool
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

func (g agentLoopApprovalGate) Evaluate(spec ExecutionSpec, modelCall, stepIndex int, when time.Time, calls []types.ToolCall) (agentLoopApprovalPause, bool) {
	gatedNames := g.gatedToolsInModelCall(calls, spec.Task)
	if len(gatedNames) == 0 {
		return agentLoopApprovalPause{}, false
	}
	approval := buildApprovalForModelCall(spec, gatedNames, when)
	if detail := browserApprovalDetail(calls, spec.Task); detail != "" {
		approval.Reason += ". " + detail
	}
	return agentLoopApprovalPause{
		Approval: approval,
		Step:     buildAwaitingApprovalStep(spec, stepIndex, modelCall, when, approval),
	}, true
}

func (g agentLoopApprovalGate) gatedToolsInModelCall(calls []types.ToolCall, task types.Task) []string {
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
	if agentPresetDisablesTools(task) || agentPresetBlocksNativeNetwork(task, toolName) || agentPresetBlocksBrowser(task, toolName) || agentReadOnlyBlocksCall(task, call) || mcpServerPolicy(toolName, task) == types.MCPApprovalBlock {
		return false
	}
	if toolName == AgentToolBrowserInspect {
		return g.browserInspectionAvailable && browserInspectionCallAllowed(task, call)
	}
	if _, ok := g.gatedTools[toolName]; ok {
		return true
	}
	return mcpServerPolicy(toolName, task) == types.MCPApprovalRequireApproval
}

func browserInspectionCallAllowed(task types.Task, call types.ToolCall) bool {
	if agentPresetBlocksBrowser(task, AgentToolBrowserInspect) {
		return false
	}
	args, _, err := decodeBrowserInspectionArgs(call.Function.Arguments)
	if err != nil {
		return false
	}
	origin, err := browserrunner.InspectionOriginForURL(args.URL)
	if err != nil {
		return false
	}
	origins, err := browserrunner.NormalizeAllowedOrigins(task.AgentPresetBrowserAllowedOrigins)
	if err != nil {
		return false
	}
	for _, allowed := range origins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func browserApprovalDetail(calls []types.ToolCall, task types.Task) string {
	targets := make(map[string]struct{})
	inspections := 0
	for _, call := range calls {
		if call.Function.Name != AgentToolBrowserInspect {
			continue
		}
		args, _, err := decodeBrowserInspectionArgs(call.Function.Arguments)
		if err != nil || !browserInspectionCallAllowed(task, call) {
			continue
		}
		inspections++
		targets[browserInspectionApprovalTarget(args)] = struct{}{}
	}
	if inspections == 0 {
		return ""
	}
	values := make([]string, 0, len(targets))
	for target := range targets {
		values = append(values, target)
	}
	sort.Strings(values)
	pageNoun := "page"
	if inspections != 1 {
		pageNoun = "pages"
	}
	return fmt.Sprintf("Browser evidence is read-only static inspection and will inspect %d requested %s in fresh temporary browser profiles: %s; page scripts and service workers are disabled, and it cannot click, type, upload, download, use saved browser state, or access clipboard/device permissions. A temporary profile is not a hard identity or network boundary: OS or enterprise browser policy can still provide authentication or client certificates", inspections, pageNoun, strings.Join(values, ", "))
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
