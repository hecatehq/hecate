package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/codeintel"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type codeIntelligenceArgs struct {
	Operation  string `json:"operation"`
	Path       string `json:"path,omitempty"`
	Language   string `json:"language,omitempty"`
	Query      string `json:"query,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type codeIntelligenceInvalidRequestReason string

const (
	codeIntelligenceStepStringBytes = 64
	codeIntelligenceDefaultResults  = 50
	codeIntelligenceMaximumResults  = 200
	semanticCodeIntelligenceRepair  = "use `grep` or `code_intelligence` with `operation=structural_search`, or run semantic intelligence with a compatible OS sandbox/network-enabled preset"

	codeIntelligenceInvalidReasonUnknown                      codeIntelligenceInvalidRequestReason = "unknown"
	codeIntelligenceInvalidReasonOperationTooLong             codeIntelligenceInvalidRequestReason = "operation_too_long"
	codeIntelligenceInvalidReasonPathTooLong                  codeIntelligenceInvalidRequestReason = "path_too_long"
	codeIntelligenceInvalidReasonLanguageTooLong              codeIntelligenceInvalidRequestReason = "language_too_long"
	codeIntelligenceInvalidReasonQueryTooLong                 codeIntelligenceInvalidRequestReason = "query_too_long"
	codeIntelligenceInvalidReasonSelectorTooLong              codeIntelligenceInvalidRequestReason = "selector_too_long"
	codeIntelligenceInvalidReasonUnsupportedOperation         codeIntelligenceInvalidRequestReason = "unsupported_operation"
	codeIntelligenceInvalidReasonQueryRequired                codeIntelligenceInvalidRequestReason = "query_required"
	codeIntelligenceInvalidReasonSelectorNotAllowed           codeIntelligenceInvalidRequestReason = "selector_not_allowed"
	codeIntelligenceInvalidReasonSelectorInvalid              codeIntelligenceInvalidRequestReason = "selector_invalid"
	codeIntelligenceInvalidReasonPathRequired                 codeIntelligenceInvalidRequestReason = "path_required"
	codeIntelligenceInvalidReasonPositionRequired             codeIntelligenceInvalidRequestReason = "position_required"
	codeIntelligenceInvalidReasonPositionInvalid              codeIntelligenceInvalidRequestReason = "position_invalid"
	codeIntelligenceInvalidReasonWorkspaceFileUnavailable     codeIntelligenceInvalidRequestReason = "workspace_file_unavailable"
	codeIntelligenceInvalidReasonWorkspaceFileNotRegular      codeIntelligenceInvalidRequestReason = "workspace_file_not_regular"
	codeIntelligenceInvalidReasonWorkspaceFileTooLarge        codeIntelligenceInvalidRequestReason = "workspace_file_too_large"
	codeIntelligenceInvalidReasonWorkspaceFileChanged         codeIntelligenceInvalidRequestReason = "workspace_file_changed"
	codeIntelligenceInvalidReasonWorkspaceFileInvalidEncoding codeIntelligenceInvalidRequestReason = "workspace_file_invalid_encoding"
	codeIntelligenceInvalidReasonWorkspaceFileInvalid         codeIntelligenceInvalidRequestReason = "workspace_file_invalid"
	codeIntelligenceInvalidReasonStructuralPathInvalid        codeIntelligenceInvalidRequestReason = "structural_path_invalid"
	codeIntelligenceInvalidReasonLanguageRequired             codeIntelligenceInvalidRequestReason = "language_required"
	codeIntelligenceInvalidReasonLanguageUnsupported          codeIntelligenceInvalidRequestReason = "language_unsupported"
	codeIntelligenceInvalidReasonLanguagePathMismatch         codeIntelligenceInvalidRequestReason = "language_path_mismatch"
)

func agentSandboxBlocksCodeIntelligence(task types.Task, call types.ToolCall) (bool, string) {
	if call.Function.Name != AgentToolCodeIntelligence {
		return false, ""
	}
	var args struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return false, ""
	}
	switch codeintel.Operation(strings.TrimSpace(args.Operation)) {
	case codeintel.OpCapabilities, codeintel.OpStructuralSearch:
		return false, ""
	case codeintel.OpDefinition, codeintel.OpReferences, codeintel.OpHover,
		codeintel.OpDocumentSymbols, codeintel.OpWorkspaceSymbols, codeintel.OpDiagnostics:
		return semanticCodeIntelligencePolicyBlock(task)
	default:
		// Unknown operations never start a provider; let normal argument
		// validation return the useful error to the model.
		return false, ""
	}
}

func semanticCodeIntelligencePolicyBlock(task types.Task) (bool, string) {
	wrapper := sandbox.DetectWrapper(context.Background())
	if task.SandboxReadOnly && wrapper != sandbox.WrapperBwrap {
		return true, "semantic language servers are disabled for read-only tasks unless the OS wrapper enforces a read-only workspace"
	}
	if !task.SandboxNetwork && wrapper == sandbox.WrapperNone {
		return true, "semantic language servers are disabled because this host cannot enforce the task's network-denied policy"
	}
	return false, ""
}

func codeIntelligenceToolDefinition() types.Tool {
	return types.Tool{
		Type: "function",
		Function: types.ToolFunction{
			Name:        AgentToolCodeIntelligence,
			Description: "Use read-only semantic or structural code intelligence inside the task workspace. Routing rule: use semantic LSP operations only for Go through gopls and TypeScript/JavaScript through TypeScript 7+ native LSP; use `operation=structural_search` for Python, Rust, Java, C/C++/C#, HTML/CSS, JSON/YAML, Bash, or syntax patterns in any supported language. Never use semantic operations for those structural-only languages. Call `code_intelligence` with `operation=capabilities` first to inspect trusted provider availability and versions; installed_unverified means initialization or invocation is verified only by a real query. If the selected provider is unavailable, do not call its operations; use `operation=structural_search` when ast-grep is available or fall back to `grep`. Returned paths are workspace-confined.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"operation": {"type": "string", "enum": ["capabilities", "definition", "references", "hover", "document_symbols", "workspace_symbols", "diagnostics", "structural_search"], "description": "Choose capabilities first with no other arguments. definition/references/hover are Go/TypeScript/JavaScript semantic operations requiring path, line, and column. document_symbols/diagnostics are semantic operations requiring path. workspace_symbols is semantic, requires query, and also requires path for TypeScript/JavaScript or language=go for Go. structural_search is the only operation for Python, Rust, Java, C/C++/C#, HTML/CSS, JSON/YAML, and Bash; it requires query and requires language when path is a directory. Canonical calls: {\"operation\":\"capabilities\"}; {\"operation\":\"workspace_symbols\",\"path\":\"relative/file.ts\",\"query\":\"Symbol\"}; {\"operation\":\"structural_search\",\"path\":\"relative/file.py\",\"language\":\"python\",\"query\":\"def $NAME($$$ARGS)\"}."},
					"path": {"type": "string", "maxLength": 4096, "description": "An existing workspace-relative source file, or directory for structural_search, capped at 4096 UTF-8 bytes. Copy a task-supplied path exactly; never prepend the workspace root or use an absolute path. Required for document-scoped LSP operations and TypeScript workspace_symbols; optional for Go workspace_symbols when language is supplied; optional structural_search scope defaults to '.'."},
					"language": {"type": "string", "maxLength": 64, "description": "Optional language id capped at 64 UTF-8 bytes, usually inferred from path. Semantic LSP accepts Go and TypeScript/JavaScript. structural_search additionally accepts Python, Rust, Java, C/C++/C#, HTML/CSS, JSON/YAML, and Bash. Required for workspace_symbols without path and for directory-scoped structural_search."},
					"query": {"type": "string", "maxLength": 16384, "description": "Required symbol-name query for workspace_symbols or ast-grep pattern for structural_search, capped at 16384 UTF-8 bytes. ast-grep metavariables use $NAME for one node and $$$ARGS or $$$BODY for multiple nodes."},
					"selector": {"type": "string", "maxLength": 128, "pattern": "^[A-Za-z_][A-Za-z0-9_]*$", "description": "Optional structural_search-only tree-sitter node kind used to select the match within a contextual pattern, for example call_expression. Must be one ASCII identifier token."},
					"line": {"type": "integer", "minimum": 1, "description": "1-based source line. Required for definition, references, and hover."},
					"column": {"type": "integer", "minimum": 1, "description": "1-based UTF-8 byte column. Required for definition, references, and hover."},
					"max_results": {"type": "integer", "minimum": 1, "maximum": 200, "default": 50}
				},
				"required": ["operation"]
			}`),
		},
	}
}

func applyCodeIntelligenceSelfDocumentation(tools []types.Tool, spec ExecutionSpec, gate agentLoopApprovalGate, serviceConfigured bool) {
	for index := range tools {
		if tools[index].Function.Name != AgentToolCodeIntelligence {
			continue
		}
		tools[index].Function.Description += " " + effectiveCodeIntelligenceGuidance(spec, gate, serviceConfigured, tools)
		return
	}
}

func effectiveCodeIntelligenceGuidance(spec ExecutionSpec, gate agentLoopApprovalGate, serviceConfigured bool, advertisedTools []types.Tool) string {
	grepAccess := effectiveGuidanceToolAccess(gate, spec, "grep", `{}`, &advertisedTools, false)
	if !serviceConfigured {
		return "Effective access for this run: code_intelligence is unavailable because its runtime service is not configured. grep " + grepAccess + "."
	}

	toolAccess := effectiveGuidanceToolAccess(gate, spec, AgentToolCodeIntelligence, `{"operation":"capabilities"}`, &advertisedTools, true)
	blocked, reason := semanticCodeIntelligencePolicyBlock(spec.Task)
	semanticAccess := "permitted by the current task and host isolation policy; provider installation is not checked yet and LSP initialization is verified on query"
	if blocked {
		semanticAccess = "blocked: " + reason + "; " + semanticCodeIntelligenceRepair
	} else {
		switch gate.approvalDisposition(types.ToolCall{Function: types.ToolCallFunction{
			Name:      AgentToolCodeIntelligence,
			Arguments: `{"operation":"definition"}`,
		}}, spec, &advertisedTools) {
		case agentLoopApprovalRequired:
			semanticAccess = "approval-gated; provider installation is not checked yet and LSP initialization is verified on query"
		case agentLoopApprovalBlockedByPreset:
			semanticAccess = "blocked by the frozen Agent Preset approval policy"
		}
	}
	return "Effective access for this run: calls to `code_intelligence` with `operation=capabilities` or `operation=structural_search` " + toolAccess + "; semantic LSP operations are " + semanticAccess + ". `grep` " + grepAccess + " as the text fallback. Dispatch-time policy remains authoritative."
}

func effectiveGuidanceToolAccess(gate agentLoopApprovalGate, spec ExecutionSpec, name, arguments string, advertisedTools *[]types.Tool, plural bool) string {
	disposition := gate.approvalDisposition(types.ToolCall{Function: types.ToolCallFunction{
		Name:      name,
		Arguments: arguments,
	}}, spec, advertisedTools)
	switch disposition {
	case agentLoopApprovalRequired:
		if plural {
			return "require operator approval"
		}
		return "requires operator approval"
	case agentLoopApprovalBlockedByPreset:
		if plural {
			return "are blocked by the frozen Agent Preset approval policy"
		}
		return "is blocked by the frozen Agent Preset approval policy"
	default:
		if plural {
			return "are permitted without an approval pause"
		}
		return "is permitted without an approval pause"
	}
}

func (d *agentLoopToolDispatcher) codeIntelligenceTool(ctx context.Context, spec ExecutionSpec, args codeIntelligenceArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if d == nil || d.codeIntelligence == nil {
		text := "code_intelligence: service is not configured"
		step := codeIntelligenceFailureStep(spec, args, stepIndex, startedAt, toolName, "not_configured", "")
		return text, &step, nil, nil
	}
	root, errMsg := workspaceRoot(spec)
	if errMsg != "" {
		text := "code_intelligence: " + errMsg
		step := codeIntelligenceFailureStep(spec, args, stepIndex, startedAt, toolName, "invalid_workspace", "")
		return text, &step, nil, nil
	}
	operation := codeintel.Operation(strings.TrimSpace(args.Operation))
	maxResults := effectiveCodeIntelligenceMaxResults(args.MaxResults)
	result, err := d.codeIntelligence.Query(ctx, root, codeintel.Request{
		Operation:  operation,
		Path:       args.Path,
		Language:   args.Language,
		Query:      args.Query,
		Selector:   args.Selector,
		Line:       args.Line,
		Column:     args.Column,
		MaxResults: maxResults,
	})
	if err != nil {
		category := codeIntelligenceErrorCategory(err)
		step := codeIntelligenceFailureStep(spec, args, stepIndex, startedAt, toolName, category, codeIntelligenceInvalidRequestReasonForError(err))
		return fmt.Sprintf("code_intelligence: %v", err), &step, nil, nil
	}
	semanticPolicyBlocked, semanticPolicyReason := semanticCodeIntelligencePolicyBlock(spec.Task)
	if operation == codeintel.OpCapabilities && semanticPolicyBlocked {
		policyStatus := "semantic_policy=blocked reason=" + semanticPolicyReason
		if strings.TrimSpace(result.Text) == "" {
			result.Text = policyStatus
		} else {
			result.Text = policyStatus + "\n" + result.Text
		}
	}
	step := buildGenericReadToolStep(spec, stepIndex, startedAt, toolName, map[string]any{
		"operation":   operation,
		"path":        args.Path,
		"language":    args.Language,
		"selector":    strings.TrimSpace(args.Selector),
		"line":        args.Line,
		"column":      args.Column,
		"max_results": maxResults,
		"query_bytes": len(args.Query),
	})
	step.OutputSummary = map[string]any{
		"provider":                result.Provider,
		"items":                   len(result.Items),
		"capabilities":            len(result.Capabilities),
		"truncated":               result.Truncated,
		"omitted_external":        result.OmittedExternal,
		"semantic_policy_blocked": semanticPolicyBlocked,
	}
	return result.Text, &step, nil, nil
}

func codeIntelligenceFailureStep(spec ExecutionSpec, args codeIntelligenceArgs, stepIndex int, startedAt time.Time, toolName, category string, invalidRequestReason codeIntelligenceInvalidRequestReason) types.TaskStep {
	finishedAt := time.Now().UTC()
	category = firstNonEmpty(strings.TrimSpace(category), "provider_error")
	outputSummary := map[string]any{
		"error_category": category,
		"duration_ms":    finishedAt.Sub(startedAt).Milliseconds(),
	}
	if category == "invalid_request" {
		outputSummary["invalid_request_reason"] = boundedCodeIntelligenceInvalidRequestReason(invalidRequestReason)
	}
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    stepIndex,
		Kind:     "tool",
		Title:    toolName + " (failed)",
		Status:   "failed",
		Phase:    "execution",
		Result:   telemetry.ResultError,
		ToolName: toolName,
		Input: map[string]any{
			"operation":   truncateUTF8(strings.TrimSpace(args.Operation), codeIntelligenceStepStringBytes),
			"language":    truncateUTF8(strings.TrimSpace(args.Language), codeIntelligenceStepStringBytes),
			"selector":    truncateUTF8(strings.TrimSpace(args.Selector), codeIntelligenceStepStringBytes),
			"line":        args.Line,
			"column":      args.Column,
			"max_results": effectiveCodeIntelligenceMaxResults(args.MaxResults),
			"query_bytes": len(args.Query),
		},
		OutputSummary: outputSummary,
		Error:         "code intelligence query failed",
		ErrorKind:     category,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		RequestID:     spec.RequestID,
		TraceID:       spec.TraceID,
	}
}

func boundedCodeIntelligenceInvalidRequestReason(reason codeIntelligenceInvalidRequestReason) string {
	switch reason {
	case codeIntelligenceInvalidReasonOperationTooLong,
		codeIntelligenceInvalidReasonPathTooLong,
		codeIntelligenceInvalidReasonLanguageTooLong,
		codeIntelligenceInvalidReasonQueryTooLong,
		codeIntelligenceInvalidReasonSelectorTooLong,
		codeIntelligenceInvalidReasonUnsupportedOperation,
		codeIntelligenceInvalidReasonQueryRequired,
		codeIntelligenceInvalidReasonSelectorNotAllowed,
		codeIntelligenceInvalidReasonSelectorInvalid,
		codeIntelligenceInvalidReasonPathRequired,
		codeIntelligenceInvalidReasonPositionRequired,
		codeIntelligenceInvalidReasonPositionInvalid,
		codeIntelligenceInvalidReasonWorkspaceFileUnavailable,
		codeIntelligenceInvalidReasonWorkspaceFileNotRegular,
		codeIntelligenceInvalidReasonWorkspaceFileTooLarge,
		codeIntelligenceInvalidReasonWorkspaceFileChanged,
		codeIntelligenceInvalidReasonWorkspaceFileInvalidEncoding,
		codeIntelligenceInvalidReasonWorkspaceFileInvalid,
		codeIntelligenceInvalidReasonStructuralPathInvalid,
		codeIntelligenceInvalidReasonLanguageRequired,
		codeIntelligenceInvalidReasonLanguageUnsupported,
		codeIntelligenceInvalidReasonLanguagePathMismatch:
		return string(reason)
	default:
		return string(codeIntelligenceInvalidReasonUnknown)
	}
}

func effectiveCodeIntelligenceMaxResults(value int) int {
	if value <= 0 {
		return codeIntelligenceDefaultResults
	}
	if value > codeIntelligenceMaximumResults {
		return codeIntelligenceMaximumResults
	}
	return value
}

func codeIntelligenceErrorCategory(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "diagnostics are unavailable"):
		return "diagnostics_incomplete"
	case strings.HasPrefix(message, "workspace root"), strings.HasPrefix(message, "resolve workspace root"),
		strings.HasPrefix(message, "stat workspace root"):
		return "invalid_workspace"
	case isCodeIntelligenceInvalidRequestMessage(message):
		return "invalid_request"
	case strings.Contains(message, "protocol"), strings.Contains(message, "language server exited"),
		strings.HasPrefix(message, "decode "), strings.Contains(message, "malformed json stream"),
		strings.Contains(message, "selected an unsupported position encoding"):
		return "provider_protocol"
	case strings.Contains(message, "not installed"), strings.Contains(message, "unavailable"),
		strings.Contains(message, "install "), strings.Contains(message, "not found on path"),
		strings.Contains(message, "need major"), strings.HasPrefix(message, "failed to start"):
		return "provider_unavailable"
	default:
		return "provider_error"
	}
}

func isCodeIntelligenceInvalidRequestMessage(message string) bool {
	return codeIntelligenceInvalidRequestReasonForMessage(message) != codeIntelligenceInvalidReasonUnknown
}

func codeIntelligenceInvalidRequestReasonForError(err error) codeIntelligenceInvalidRequestReason {
	if err == nil {
		return codeIntelligenceInvalidReasonUnknown
	}
	return codeIntelligenceInvalidRequestReasonForMessage(err.Error())
}

func codeIntelligenceInvalidRequestReasonForMessage(message string) codeIntelligenceInvalidRequestReason {
	message = strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.HasPrefix(message, "code intelligence operation exceeds "):
		return codeIntelligenceInvalidReasonOperationTooLong
	case strings.HasPrefix(message, "code intelligence path exceeds "):
		return codeIntelligenceInvalidReasonPathTooLong
	case strings.HasPrefix(message, "code intelligence language exceeds "):
		return codeIntelligenceInvalidReasonLanguageTooLong
	case strings.HasPrefix(message, "code intelligence query exceeds "):
		return codeIntelligenceInvalidReasonQueryTooLong
	case strings.HasPrefix(message, "code intelligence selector exceeds "):
		return codeIntelligenceInvalidReasonSelectorTooLong
	case strings.HasPrefix(message, "unsupported code intelligence operation "):
		return codeIntelligenceInvalidReasonUnsupportedOperation
	case strings.HasPrefix(message, "query is required "):
		return codeIntelligenceInvalidReasonQueryRequired
	case strings.HasPrefix(message, "selector is only supported "):
		return codeIntelligenceInvalidReasonSelectorNotAllowed
	case strings.HasPrefix(message, "structural selector "):
		return codeIntelligenceInvalidReasonSelectorInvalid
	case strings.HasPrefix(message, "path is required "), strings.HasPrefix(message, "file path is required"):
		return codeIntelligenceInvalidReasonPathRequired
	case strings.HasPrefix(message, "line and column are required "):
		return codeIntelligenceInvalidReasonPositionRequired
	case strings.HasPrefix(message, "line and column must be "), strings.HasPrefix(message, "column "), strings.HasPrefix(message, "line "):
		return codeIntelligenceInvalidReasonPositionInvalid
	case strings.HasPrefix(message, "open workspace file "), strings.HasPrefix(message, "read workspace file "), strings.HasPrefix(message, "inspect workspace file "):
		return codeIntelligenceInvalidReasonWorkspaceFileUnavailable
	case strings.HasPrefix(message, "workspace path "):
		return codeIntelligenceInvalidReasonWorkspaceFileNotRegular
	case strings.HasPrefix(message, "workspace file ") && strings.Contains(message, " exceeds the "):
		return codeIntelligenceInvalidReasonWorkspaceFileTooLarge
	case strings.HasPrefix(message, "workspace file ") && strings.Contains(message, " changed while "):
		return codeIntelligenceInvalidReasonWorkspaceFileChanged
	case strings.HasPrefix(message, "workspace file ") && strings.Contains(message, " is not valid utf-8"):
		return codeIntelligenceInvalidReasonWorkspaceFileInvalidEncoding
	case strings.HasPrefix(message, "workspace file "):
		return codeIntelligenceInvalidReasonWorkspaceFileInvalid
	case strings.HasPrefix(message, "resolve structural search path "), strings.HasPrefix(message, "structural search path "):
		return codeIntelligenceInvalidReasonStructuralPathInvalid
	case strings.HasPrefix(message, "language is required "):
		return codeIntelligenceInvalidReasonLanguageRequired
	case strings.HasPrefix(message, "language \"") && strings.Contains(message, " does not match path "),
		strings.HasPrefix(message, "structural-search language ") && strings.Contains(message, " does not match path "):
		return codeIntelligenceInvalidReasonLanguagePathMismatch
	case strings.HasPrefix(message, "no allowlisted "), strings.HasPrefix(message, "language \""), strings.HasPrefix(message, "structural-search language "):
		return codeIntelligenceInvalidReasonLanguageUnsupported
	default:
		return codeIntelligenceInvalidReasonUnknown
	}
}
