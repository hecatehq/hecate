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
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

const (
	codeIntelligenceStepStringBytes = 64
	codeIntelligenceDefaultResults  = 50
	codeIntelligenceMaximumResults  = 200
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
			Description: "Use read-only semantic or structural code intelligence inside the task workspace. LSP operations provide definitions, references, hover, symbols, and diagnostics when an allowlisted language server is installed. structural_search uses optional ast-grep. Call capabilities to inspect availability; fall back to grep when a provider is missing. Returned paths are workspace-confined.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"operation": {"type": "string", "enum": ["capabilities", "definition", "references", "hover", "document_symbols", "workspace_symbols", "diagnostics", "structural_search"]},
					"path": {"type": "string", "maxLength": 4096, "description": "Workspace-relative source file, capped at 4096 UTF-8 bytes. Required for document-scoped LSP operations and TypeScript workspace_symbols; optional for Go workspace_symbols when language is supplied; optional structural_search scope defaults to '.'."},
					"language": {"type": "string", "maxLength": 64, "description": "Optional language id capped at 64 UTF-8 bytes, usually inferred from path. Required for workspace_symbols without path and for directory-scoped structural_search."},
					"query": {"type": "string", "maxLength": 16384, "description": "Required workspace-symbol query or ast-grep pattern for structural_search, capped at 16384 UTF-8 bytes."},
					"line": {"type": "integer", "minimum": 1, "description": "1-based source line. Required for definition, references, and hover."},
					"column": {"type": "integer", "minimum": 1, "description": "1-based UTF-8 byte column. Required for definition, references, and hover."},
					"max_results": {"type": "integer", "minimum": 1, "maximum": 200, "default": 50}
				},
				"required": ["operation"]
			}`),
		},
	}
}

func (d *agentLoopToolDispatcher) codeIntelligenceTool(ctx context.Context, spec ExecutionSpec, args codeIntelligenceArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if d == nil || d.codeIntelligence == nil {
		text := "code_intelligence: service is not configured"
		step := codeIntelligenceFailureStep(spec, args, stepIndex, startedAt, toolName, "not_configured")
		return text, &step, nil, nil
	}
	root, errMsg := workspaceRoot(spec)
	if errMsg != "" {
		text := "code_intelligence: " + errMsg
		step := codeIntelligenceFailureStep(spec, args, stepIndex, startedAt, toolName, "invalid_workspace")
		return text, &step, nil, nil
	}
	operation := codeintel.Operation(strings.TrimSpace(args.Operation))
	maxResults := effectiveCodeIntelligenceMaxResults(args.MaxResults)
	result, err := d.codeIntelligence.Query(ctx, root, codeintel.Request{
		Operation:  operation,
		Path:       args.Path,
		Language:   args.Language,
		Query:      args.Query,
		Line:       args.Line,
		Column:     args.Column,
		MaxResults: maxResults,
	})
	if err != nil {
		step := codeIntelligenceFailureStep(spec, args, stepIndex, startedAt, toolName, codeIntelligenceErrorCategory(err))
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

func codeIntelligenceFailureStep(spec ExecutionSpec, args codeIntelligenceArgs, stepIndex int, startedAt time.Time, toolName, category string) types.TaskStep {
	finishedAt := time.Now().UTC()
	category = firstNonEmpty(strings.TrimSpace(category), "provider_error")
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
			"line":        args.Line,
			"column":      args.Column,
			"max_results": effectiveCodeIntelligenceMaxResults(args.MaxResults),
			"query_bytes": len(args.Query),
		},
		OutputSummary: map[string]any{
			"error_category": category,
			"duration_ms":    finishedAt.Sub(startedAt).Milliseconds(),
		},
		Error:      "code intelligence query failed",
		ErrorKind:  category,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
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
	for _, prefix := range []string{
		"code intelligence operation exceeds ",
		"code intelligence path exceeds ",
		"code intelligence language exceeds ",
		"code intelligence query exceeds ",
		"unsupported code intelligence operation ",
		"query is required ",
		"path is required ",
		"line and column are required ",
		"line and column must be ",
		"file path is required",
		"open workspace file ",
		"read workspace file ",
		"inspect workspace file ",
		"workspace path ",
		"workspace file ",
		"resolve structural search path ",
		"no allowlisted ",
		"language is required ",
		"language \"",
		"structural-search language ",
		"column ",
		"line ",
	} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}
