package orchestrator

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/browserrunner"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

type agentLoopApprovalGate struct {
	gatedTools                 map[string]struct{}
	browserInspectionAvailable bool
	browserFlowAvailable       bool
}

type agentLoopApprovalPause struct {
	Approval types.TaskApproval
	Step     types.TaskStep
}

type agentLoopApprovalDisposition uint8

const (
	agentLoopApprovalNone agentLoopApprovalDisposition = iota
	agentLoopApprovalRequired
	agentLoopApprovalBlockedByPreset
)

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
	return g.evaluateForModelCallRef(spec, currentAgentLoopModelCallRef(spec, modelCall), stepIndex, when, calls, nil)
}

func (g agentLoopApprovalGate) EvaluateForModelCallRef(spec ExecutionSpec, modelCallRef agentLoopModelCallRef, stepIndex int, when time.Time, calls []types.ToolCall) (agentLoopApprovalPause, bool) {
	return g.evaluateForModelCallRef(spec, modelCallRef, stepIndex, when, calls, nil)
}

func (g agentLoopApprovalGate) EvaluateAdvertised(spec ExecutionSpec, modelCall, stepIndex int, when time.Time, calls []types.ToolCall, tools []types.Tool) (agentLoopApprovalPause, bool) {
	return g.evaluateForModelCallRef(spec, currentAgentLoopModelCallRef(spec, modelCall), stepIndex, when, calls, &tools)
}

func (g agentLoopApprovalGate) EvaluateForModelCallRefAdvertised(spec ExecutionSpec, modelCallRef agentLoopModelCallRef, stepIndex int, when time.Time, calls []types.ToolCall, tools []types.Tool) (agentLoopApprovalPause, bool) {
	return g.evaluateForModelCallRef(spec, modelCallRef, stepIndex, when, calls, &tools)
}

func (g agentLoopApprovalGate) evaluateForModelCallRef(spec ExecutionSpec, modelCallRef agentLoopModelCallRef, stepIndex int, when time.Time, calls []types.ToolCall, advertisedTools *[]types.Tool) (agentLoopApprovalPause, bool) {
	gatedNames := g.gatedToolsInModelCall(calls, spec, advertisedTools)
	if len(gatedNames) == 0 {
		return agentLoopApprovalPause{}, false
	}
	approval := buildApprovalForModelCall(spec, gatedNames, when)
	if effectiveAgentPresetApprovalPolicy(spec) == types.AgentPresetApprovalRequire {
		approval.Reason += ". The Task's frozen Agent Preset requires approval for every otherwise-permitted tool call"
	}
	approval.ActionSummary, approval.ActionSummaryIncomplete = buildApprovalActionSummary(calls)
	if detail := browserApprovalDetail(calls, spec.Task); detail != "" {
		approval.Reason += ". " + detail
	}
	step := buildAwaitingApprovalStepForModelCallRef(spec, stepIndex, modelCallRef, when, approval)
	step.Input[toolCallBundleDigestKey] = agentToolCallBundleDigest(calls)
	approval.StepID = step.ID
	return agentLoopApprovalPause{
		Approval: approval,
		Step:     step,
	}, true
}

func (g agentLoopApprovalGate) gatedToolsInModelCall(calls []types.ToolCall, spec ExecutionSpec, advertisedTools *[]types.Tool) []string {
	seen := make(map[string]struct{}, len(calls))
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		if !g.isGated(c, spec, advertisedTools) {
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

func (g agentLoopApprovalGate) isGated(call types.ToolCall, spec ExecutionSpec, advertisedTools ...*[]types.Tool) bool {
	return g.approvalDisposition(call, spec, firstAdvertisedToolCatalog(advertisedTools)) == agentLoopApprovalRequired
}

func (g agentLoopApprovalGate) isBlockedByAgentPreset(call types.ToolCall, spec ExecutionSpec, advertisedTools ...*[]types.Tool) bool {
	return g.approvalDisposition(call, spec, firstAdvertisedToolCatalog(advertisedTools)) == agentLoopApprovalBlockedByPreset
}

func firstAdvertisedToolCatalog(catalogs []*[]types.Tool) *[]types.Tool {
	if len(catalogs) == 0 {
		return nil
	}
	return catalogs[0]
}

func (g agentLoopApprovalGate) approvalDisposition(call types.ToolCall, spec ExecutionSpec, advertisedTools *[]types.Tool) agentLoopApprovalDisposition {
	task := spec.Task
	toolName := call.Function.Name
	workflowMode := taskworkflow.ModeForExecution(task, spec.Run)
	// Hard policy refusals run before approval semantics. Asking an operator
	// to approve a call that the dispatcher must still refuse is misleading,
	// and would turn a fail-closed decision into an unnecessary pause.
	blockedCodeIntelligence, _ := agentSandboxBlocksCodeIntelligence(task, call)
	if taskworkflow.BlocksTool(workflowMode, toolName) || taskworkflow.IsUnavailableEvidenceTool(workflowMode, toolName) || agentPresetDisablesTools(task) || agentPresetBlocksNativeNetwork(task, toolName) || agentPresetBlocksBrowser(task, toolName) || blockedCodeIntelligence || agentReadOnlyBlocksCall(task, call) || mcpServerPolicy(toolName, task) == types.MCPApprovalBlock {
		return agentLoopApprovalNone
	}

	requiresApproval := false
	if toolName == AgentToolBrowserInspect {
		requiresApproval = g.browserInspectionAvailable && browserInspectionCallAllowed(task, call)
	} else if toolName == AgentToolBrowserFlow {
		requiresApproval = g.browserFlowAvailable && browserFlowCallAllowed(task, call)
	} else {
		requiresApproval = g.requiresExplicitApproval(toolName) || mcpServerPolicy(toolName, task) == types.MCPApprovalRequireApproval
	}

	switch effectiveAgentPresetApprovalPolicy(spec) {
	case types.AgentPresetApprovalRequire:
		// Browser calls remain non-approvable when the configured capability or
		// runtime is unavailable. Every other call that survived the hard policy
		// checks receives the preset's additional approval gate.
		if toolName != AgentToolBrowserInspect && toolName != AgentToolBrowserFlow && toolWasAdvertised(toolName, advertisedTools) {
			return agentLoopApprovalRequired
		}
	case types.AgentPresetApprovalBlock:
		if requiresApproval {
			return agentLoopApprovalBlockedByPreset
		}
	}
	if requiresApproval {
		return agentLoopApprovalRequired
	}
	return agentLoopApprovalNone
}

func toolWasAdvertised(toolName string, advertisedTools *[]types.Tool) bool {
	if advertisedTools == nil {
		// Unit-level callers that predate catalog-aware admission already pass
		// calls selected from a known catalog. Production always supplies the
		// exact per-run tool list.
		return true
	}
	for _, tool := range *advertisedTools {
		if tool.Function.Name == toolName {
			return true
		}
	}
	return false
}

// effectiveAgentPresetApprovalPolicy returns only the immutable policy carried
// by a native project-assignment Task. An empty value is the legacy/manual
// compatibility state. An invalid non-empty stored value fails safely by
// requiring operator approval rather than silently allowing tool dispatch.
func effectiveAgentPresetApprovalPolicy(spec ExecutionSpec) string {
	task := spec.Task
	if taskworkflow.IsQAExecution(task, spec.Run) || task.OriginKind != "project_work_item" || strings.TrimSpace(task.AgentPresetID) == "" {
		return ""
	}
	policy := strings.TrimSpace(task.AgentPresetApprovalPolicy)
	if policy == "" {
		return ""
	}
	if !types.IsValidAgentPresetApprovalPolicy(policy) {
		return types.AgentPresetApprovalRequire
	}
	return policy
}

func (g agentLoopApprovalGate) requiresExplicitApproval(toolName string) bool {
	_, ok := g.gatedTools[strings.TrimSpace(toolName)]
	return ok
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

func browserFlowCallAllowed(task types.Task, call types.ToolCall) bool {
	if agentPresetBlocksBrowser(task, AgentToolBrowserFlow) {
		return false
	}
	args, _, err := decodeBrowserFlowArgs(call.Function.Arguments)
	if err != nil {
		return false
	}
	origin, err := browserrunner.InspectionOriginForURL(args.URL)
	return err == nil && browserFlowOriginAllowed(task, origin)
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
	details := make([]string, 0, 2)
	if inspections > 0 {
		values := make([]string, 0, len(targets))
		for target := range targets {
			values = append(values, target)
		}
		sort.Strings(values)
		pageNoun := "page"
		if inspections != 1 {
			pageNoun = "pages"
		}
		details = append(details, fmt.Sprintf("Browser evidence is read-only static inspection and will inspect %d requested %s in fresh temporary browser profiles: %s; page scripts and service workers are disabled, and it cannot click, type, upload, download, use saved browser state, or access clipboard/device permissions. A temporary profile is not a hard identity or network boundary: OS or enterprise browser policy can still provide authentication or client certificates", inspections, pageNoun, strings.Join(values, ", ")))
	}
	for _, call := range calls {
		if call.Function.Name != AgentToolBrowserFlow || !browserFlowCallAllowed(task, call) {
			continue
		}
		args, _, err := decodeBrowserFlowArgs(call.Function.Arguments)
		if err == nil {
			details = append(details, browserFlowApprovalDetail(args))
		}
	}
	return strings.Join(details, ". ")
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
