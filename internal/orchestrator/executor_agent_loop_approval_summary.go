package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/hecatehq/hecate/pkg/types"
)

const (
	maxApprovalActionSummaryCalls      = 16
	maxApprovalActionSummaryLineBytes  = 512
	maxApprovalActionSummaryBytes      = 4 << 10
	maxApprovalActionSummaryValueBytes = 256
)

// buildApprovalActionSummary creates a deliberately lossy, bounded view of
// every pending call in the model response. It is safe to return from the
// approval API, but is not an execution contract: dispatch still consumes the
// durably checkpointed tool calls after an approval is resolved.
func buildApprovalActionSummary(calls []types.ToolCall) ([]string, bool) {
	if len(calls) == 0 {
		return nil, false
	}
	limit := len(calls)
	incomplete := false
	if limit > maxApprovalActionSummaryCalls {
		limit = maxApprovalActionSummaryCalls
		incomplete = true
	}
	lines := make([]string, 0, limit)
	total := 0
	for _, call := range calls[:limit] {
		line, complete := summarizeApprovalToolCall(call)
		if !complete {
			incomplete = true
		}
		var truncated bool
		line, truncated = truncateApprovalSummaryUTF8(line, maxApprovalActionSummaryLineBytes)
		if truncated {
			incomplete = true
		}
		separatorBytes := 0
		if len(lines) > 0 {
			separatorBytes = 1
		}
		remaining := maxApprovalActionSummaryBytes - total - separatorBytes
		if remaining <= 0 {
			incomplete = true
			break
		}
		if len(line) > remaining {
			line, _ = truncateApprovalSummaryUTF8(line, remaining)
			incomplete = true
		}
		if line == "" {
			incomplete = true
			break
		}
		lines = append(lines, line)
		total += separatorBytes + len(line)
	}
	if len(lines) < limit {
		incomplete = true
	}
	return lines, incomplete
}

func summarizeApprovalToolCall(call types.ToolCall) (string, bool) {
	raw := call.Function.Arguments
	switch call.Function.Name {
	case "shell_exec":
		var args shellExecArgs
		if !decodeApprovalArgs(raw, &args, "command", "working_directory") || args.Command == "" || !safeApprovalPath(args.WorkingDirectory, true) {
			return "shell_exec details unavailable", false
		}
		if command, ok := safeApprovalGitCommand(args.Command, true); ok {
			return appendApprovalCWD(command, args.WorkingDirectory), true
		}
		return appendApprovalCWD(fmt.Sprintf("shell_exec command details withheld (command_bytes=%d)", len(args.Command)), args.WorkingDirectory), false
	case "git_exec":
		var args gitExecArgs
		if !decodeApprovalArgs(raw, &args, "command", "working_directory") || args.Command == "" || !safeApprovalPath(args.WorkingDirectory, true) {
			return "git_exec details unavailable", false
		}
		if command, ok := safeApprovalGitCommand(args.Command, false); ok {
			return appendApprovalCWD(command, args.WorkingDirectory), true
		}
		return appendApprovalCWD(fmt.Sprintf("git_exec command details withheld (command_bytes=%d)", len(args.Command)), args.WorkingDirectory), false
	case AgentToolTerminalOpen:
		var args terminalOpenArgs
		if !decodeApprovalArgs(raw, &args, "command", "args", "working_directory") || !safeApprovalPath(args.WorkingDirectory, true) {
			return "terminal_open details unavailable", false
		}
		if args.Command == "" && len(args.Args) == 0 {
			return appendApprovalCWD("terminal_open default shell", args.WorkingDirectory), true
		}
		argBytes := 0
		for _, arg := range args.Args {
			argBytes += len(arg)
		}
		return appendApprovalCWD(fmt.Sprintf("terminal_open command details withheld (command_bytes=%d arg_count=%d arg_bytes=%d)", len(args.Command), len(args.Args), argBytes), args.WorkingDirectory), false
	case AgentToolTerminalWrite:
		var args terminalWriteArgs
		if !decodeApprovalArgs(raw, &args, "terminal_id", "input") || !safeApprovalAtom(args.TerminalID, false) {
			return "terminal_write details unavailable", false
		}
		return fmt.Sprintf("terminal_write terminal=%s input_bytes=%d", args.TerminalID, len(args.Input)), false
	case AgentToolTerminalRead:
		var args terminalReadArgs
		if !decodeApprovalArgs(raw, &args, "terminal_id", "max_bytes") || !safeApprovalAtom(args.TerminalID, false) {
			return "terminal_read details unavailable", false
		}
		return fmt.Sprintf("terminal_read terminal=%s max_bytes=%d", args.TerminalID, args.MaxBytes), true
	case AgentToolTerminalWait:
		var args terminalWaitArgs
		if !decodeApprovalArgs(raw, &args, "terminal_id", "timeout_ms") || !safeApprovalAtom(args.TerminalID, false) {
			return "terminal_wait details unavailable", false
		}
		return fmt.Sprintf("terminal_wait terminal=%s timeout_ms=%d", args.TerminalID, args.TimeoutMS), true
	case AgentToolTerminalKill:
		var args terminalKillArgs
		if !decodeApprovalArgs(raw, &args, "terminal_id") || !safeApprovalAtom(args.TerminalID, false) {
			return "terminal_kill details unavailable", false
		}
		return "terminal_kill terminal=" + args.TerminalID, true
	case "file_write":
		var args fileWriteArgs
		operation := "write"
		if !decodeApprovalArgs(raw, &args, "path", "content", "operation") || !safeApprovalPath(args.Path, false) {
			return "file_write details unavailable", false
		}
		if args.Operation != "" {
			operation = args.Operation
		}
		if operation != "write" && operation != "append" {
			return "file_write details unavailable", false
		}
		return fmt.Sprintf("file_write %s path=%s content_bytes=%d", operation, args.Path, len(args.Content)), false
	case "file_edit":
		var args fileEditArgs
		if !decodeApprovalArgs(raw, &args, "path", "old_text", "new_text", "replace_all", "propose") || !safeApprovalPath(args.Path, false) {
			return "file_edit details unavailable", false
		}
		return fmt.Sprintf("file_edit path=%s old_bytes=%d new_bytes=%d replace_all=%t propose=%t", args.Path, len(args.OldText), len(args.NewText), args.ReplaceAll, args.Propose), false
	case "read_file":
		var args readFileArgs
		if !decodeApprovalArgs(raw, &args, "path", "max_bytes", "start_line", "end_line") || !safeApprovalPath(args.Path, false) {
			return "read_file details unavailable", false
		}
		return fmt.Sprintf("read_file path=%s max_bytes=%d lines=%d-%d", args.Path, args.MaxBytes, args.StartLine, args.EndLine), true
	case "grep":
		var args grepArgs
		if !decodeApprovalArgs(raw, &args, "pattern", "path", "include", "case_sensitive", "max_matches") || !safeApprovalPath(args.Path, true) {
			return "grep details unavailable", false
		}
		return fmt.Sprintf("grep path=%s pattern_bytes=%d include_bytes=%d max_matches=%d", approvalPathOrRoot(args.Path), len(args.Pattern), len(args.Include), args.MaxMatches), false
	case "glob":
		var args globArgs
		if !decodeApprovalArgs(raw, &args, "pattern", "path", "max_matches") || !safeApprovalPath(args.Path, true) {
			return "glob details unavailable", false
		}
		return fmt.Sprintf("glob path=%s pattern_bytes=%d max_matches=%d", approvalPathOrRoot(args.Path), len(args.Pattern), args.MaxMatches), false
	case "artifact_read":
		var args artifactReadArgs
		if !decodeApprovalArgs(raw, &args, "artifact_id", "max_bytes") || !safeApprovalAtom(args.ArtifactID, false) {
			return "artifact_read details unavailable", false
		}
		return fmt.Sprintf("artifact_read artifact=%s max_bytes=%d", args.ArtifactID, args.MaxBytes), true
	case "list_dir":
		var args listDirArgs
		if !decodeApprovalArgs(raw, &args, "path") || !safeApprovalPath(args.Path, true) {
			return "list_dir details unavailable", false
		}
		return "list_dir path=" + approvalPathOrRoot(args.Path), true
	case "git_status":
		var args gitStatusArgs
		if !decodeApprovalArgs(raw, &args) {
			return "git_status details unavailable", false
		}
		return "git_status", true
	case "git_diff":
		var args gitDiffArgs
		if !decodeApprovalArgs(raw, &args, "path", "staged", "max_bytes") || !safeApprovalPath(args.Path, true) {
			return "git_diff details unavailable", false
		}
		return fmt.Sprintf("git_diff path=%s staged=%t max_bytes=%d", approvalPathOrRoot(args.Path), args.Staged, args.MaxBytes), true
	case "apply_patch":
		var args applyPatchArgs
		if !decodeApprovalArgs(raw, &args, "patch_text", "propose") {
			return "apply_patch details unavailable", false
		}
		return fmt.Sprintf("apply_patch patch_bytes=%d propose=%t", len(args.PatchText), args.Propose), false
	case AgentToolHTTPRequest:
		var args httpRequestArgs
		if !decodeApprovalArgs(raw, &args, "url", "method", "headers", "body") {
			return "http_request details unavailable", false
		}
		origin, urlComplete := safeApprovalHTTPOrigin(args.URL)
		if origin == "" {
			return "http_request details unavailable", false
		}
		method := strings.ToUpper(strings.TrimSpace(args.Method))
		if method == "" {
			method = "GET"
		}
		if !approvalValueIn(method, "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD") {
			return "http_request details unavailable", false
		}
		return fmt.Sprintf("http_request method=%s origin=%s header_count=%d body_bytes=%d", method, origin, len(args.Headers), len(args.Body)), urlComplete && len(args.Headers) == 0 && args.Body == ""
	case AgentToolWebSearch:
		var args webSearchArgs
		if !decodeApprovalArgs(raw, &args, "query", "count", "offset", "freshness", "safe_search", "country", "search_lang") || !safeApprovalOptionalTokens(args.Freshness, args.SafeSearch, args.Country, args.SearchLang) {
			return "web_search details unavailable", false
		}
		return fmt.Sprintf("web_search query_bytes=%d count=%d offset=%d", len(args.Query), args.Count, args.Offset), false
	case AgentToolBrowserInspect:
		args, _, err := decodeBrowserInspectionArgs(raw)
		if err != nil {
			return "browser_inspect details unavailable", false
		}
		return "browser_inspect url=" + browserInspectionApprovalTarget(args), true
	case AgentToolCodeIntelligence:
		var args codeIntelligenceArgs
		if !decodeApprovalArgs(raw, &args, "operation", "path", "language", "query", "selector", "line", "column", "max_results") || !safeApprovalAtom(args.Operation, false) || !safeApprovalPath(args.Path, true) || !safeApprovalOptionalTokens(args.Language, args.Selector) {
			return "code_intelligence details unavailable", false
		}
		return fmt.Sprintf("code_intelligence operation=%s path=%s language=%s selector=%s line=%d column=%d query_bytes=%d max_results=%d", args.Operation, approvalPathOrRoot(args.Path), approvalTokenOrNone(args.Language), approvalTokenOrNone(args.Selector), args.Line, args.Column, len(args.Query), args.MaxResults), args.Query == ""
	case AgentToolDraftProjectProposal:
		var args projectAssistantDraftArgs
		if !decodeApprovalArgs(raw, &args, "request", "work_item_id", "role_id", "driver_kind") || !safeApprovalOptionalTokens(args.WorkItemID, args.RoleID, args.DriverKind) {
			return "draft_project_proposal details unavailable", false
		}
		return fmt.Sprintf("draft_project_proposal request_bytes=%d work_item=%s role=%s driver=%s", len(args.Request), approvalTokenOrNone(args.WorkItemID), approvalTokenOrNone(args.RoleID), approvalTokenOrNone(args.DriverKind)), false
	default:
		if safeApprovalMCPName(call.Function.Name) {
			return fmt.Sprintf("%s arguments withheld (argument_bytes=%d)", call.Function.Name, len(raw)), false
		}
		return "unrecognized tool call", false
	}
}

func decodeApprovalArgs(raw string, dst any, allowed ...string) bool {
	allowedFields := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedFields[field] = struct{}{}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{}, len(allowed))
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		name, ok := key.(string)
		if !ok {
			return false
		}
		if _, ok := allowedFields[name]; !ok {
			return false
		}
		if _, duplicate := seen[name]; duplicate {
			return false
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	closeToken, err := decoder.Token()
	if err != nil || closeToken != json.Delim('}') {
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return false
	}
	return json.Unmarshal([]byte(raw), dst) == nil
}

func safeApprovalGitCommand(command string, includesGit bool) (string, bool) {
	if command == "" || len(command) > maxApprovalActionSummaryValueBytes || !safeApprovalCommandText(command) {
		return "", false
	}
	parts := strings.Split(command, " ")
	if includesGit {
		if len(parts) < 2 || parts[0] != "git" {
			return "", false
		}
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", false
	}
	safe := false
	switch parts[0] {
	case "branch":
		safe = len(parts) <= 4
		for _, arg := range parts[1:] {
			safe = safe && approvalValueIn(arg, "-a", "-r", "-v", "-vv", "--all", "--remotes", "--verbose", "--list", "--show-current")
		}
	case "status":
		safe = len(parts) <= 5
		for _, arg := range parts[1:] {
			safe = safe && approvalValueIn(arg, "-s", "-sb", "--short", "--branch", "--porcelain", "--porcelain=v1", "--untracked-files=no", "--untracked-files=normal", "--untracked-files=all")
		}
	case "remote":
		safe = len(parts) == 1 || (len(parts) == 2 && parts[1] == "-v")
	case "rev-parse":
		safe = len(parts) == 2 && approvalValueIn(parts[1], "--show-toplevel", "--show-prefix", "--is-inside-work-tree", "--show-superproject-working-tree")
	}
	if !safe {
		return "", false
	}
	canonical := "git " + strings.Join(parts, " ")
	if redactCommandTelemetryValue(canonical) != canonical {
		return "", false
	}
	return canonical, true
}

func safeApprovalCommandText(value string) bool {
	if strings.TrimSpace(value) != value || strings.Contains(value, "  ") {
		return false
	}
	for _, r := range value {
		if r == ' ' || r == '-' || r == '=' || r == '.' || r == '/' || r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func safeApprovalPath(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > maxApprovalActionSummaryValueBytes {
		return false
	}
	for _, r := range value {
		if r == '.' || r == '/' || r == '\\' || r == '-' || r == '_' || r == '@' || r == '+' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func safeApprovalAtom(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > maxApprovalActionSummaryValueBytes {
		return false
	}
	for _, r := range value {
		if r == '-' || r == '_' || r == '.' || r == ':' || r == '/' || r == '@' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func safeApprovalOptionalTokens(values ...string) bool {
	for _, value := range values {
		if !safeApprovalAtom(value, true) {
			return false
		}
	}
	return true
}

func safeApprovalMCPName(name string) bool {
	if !strings.HasPrefix(name, "mcp__") || len(name) > 128 {
		return false
	}
	parts := strings.Split(name, "__")
	if len(parts) != 3 || parts[0] != "mcp" || parts[1] == "" || parts[2] == "" {
		return false
	}
	return safeApprovalAtom(parts[1], false) && safeApprovalAtom(parts[2], false)
}

func safeApprovalHTTPOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || !approvalValueIn(strings.ToLower(parsed.Scheme), "http", "https") || parsed.Host == "" {
		return "", false
	}
	if !safeApprovalHTTPHost(parsed.Host) {
		return "", false
	}
	origin := strings.ToLower(parsed.Scheme) + "://" + parsed.Host
	complete := (parsed.Path == "" || parsed.Path == "/") && parsed.RawPath == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawFragment == "" && parsed.Opaque == ""
	return origin, complete
}

func safeApprovalHTTPHost(host string) bool {
	if host == "" || len(host) > maxApprovalActionSummaryValueBytes {
		return false
	}
	for _, r := range host {
		if r == '.' || r == '-' || r == ':' || r == '[' || r == ']' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func appendApprovalCWD(line, cwd string) string {
	if cwd == "" {
		return line
	}
	return line + " cwd=" + cwd
}

func approvalPathOrRoot(path string) string {
	if path == "" {
		return "."
	}
	return path
}

func approvalTokenOrNone(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func approvalValueIn(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func truncateApprovalSummaryUTF8(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	if maxBytes <= 0 {
		return "", true
	}
	const marker = "..."
	if maxBytes <= len(marker) {
		return marker[:maxBytes], true
	}
	end := maxBytes - len(marker)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker, true
}
