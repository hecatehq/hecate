package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestBuildApprovalActionSummaryBoundsAndWithholdsSecrets(t *testing.T) {
	t.Parallel()

	calls := []types.ToolCall{
		agentLoopToolCall("shell", "shell_exec", `{"command":"curl --token ultra-secret-token https://example.com"}`),
		agentLoopToolCall("http", AgentToolHTTPRequest, `{"url":"https://alice:password@example.com/private?token=query-secret","headers":{"Authorization":"Bearer header-secret"},"body":"body-secret"}`),
		agentLoopToolCall("unknown-field", "read_file", `{"path":"README.md","password":"extra-secret"}`),
		agentLoopToolCall("mcp", "mcp__github__create_issue", `{"token":"mcp-secret"}`),
		agentLoopToolCall("write", "file_write", `{"path":"out.txt","content":"content-secret"}`),
	}
	for index := len(calls); index < 20; index++ {
		calls = append(calls, agentLoopToolCall(fmt.Sprintf("read-%d", index), "read_file", fmt.Sprintf(`{"path":"file-%d.txt"}`, index)))
	}

	summary, incomplete := buildApprovalActionSummary(calls)
	if len(summary) != maxApprovalActionSummaryCalls {
		t.Fatalf("summary lines = %d, want cap %d", len(summary), maxApprovalActionSummaryCalls)
	}
	if !incomplete {
		t.Fatal("summary incomplete = false, want omitted/withheld marker")
	}
	joined := strings.Join(summary, "\n")
	for _, secret := range []string{"ultra-secret-token", "password", "query-secret", "header-secret", "body-secret", "extra-secret", "mcp-secret", "content-secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("summary exposed %q: %s", secret, joined)
		}
	}
	if !strings.Contains(joined, "command_bytes=") || !strings.Contains(joined, "argument_bytes=") || !strings.Contains(joined, "content_bytes=") {
		t.Fatalf("summary omitted safe action shapes: %s", joined)
	}
	if got := len(joined); got > maxApprovalActionSummaryBytes {
		t.Fatalf("summary bytes = %d, want <= %d", got, maxApprovalActionSummaryBytes)
	}
	for _, line := range summary {
		if len(line) > maxApprovalActionSummaryLineBytes {
			t.Fatalf("line bytes = %d, want <= %d: %q", len(line), maxApprovalActionSummaryLineBytes, line)
		}
	}
}

func TestBuildApprovalActionSummaryEnforcesLineAndAggregateByteCaps(t *testing.T) {
	t.Parallel()

	longToken := strings.Repeat("a", maxApprovalActionSummaryValueBytes)
	calls := []types.ToolCall{agentLoopToolCall("intel", AgentToolCodeIntelligence, fmt.Sprintf(
		`{"operation":"structural_search","path":"%s","language":"%s","selector":"%s"}`,
		longToken, longToken, longToken,
	))}
	for index := 1; index < maxApprovalActionSummaryCalls; index++ {
		calls = append(calls, agentLoopToolCall(fmt.Sprintf("write-%d", index), "file_write", fmt.Sprintf(`{"path":"%s","content":"x"}`, longToken)))
	}

	summary, incomplete := buildApprovalActionSummary(calls)
	if !incomplete {
		t.Fatal("summary incomplete = false, want truncation marker")
	}
	if len(summary) == 0 || len(summary) > maxApprovalActionSummaryCalls {
		t.Fatalf("summary lines = %d, want 1..%d", len(summary), maxApprovalActionSummaryCalls)
	}
	for _, line := range summary {
		if len(line) > maxApprovalActionSummaryLineBytes {
			t.Fatalf("line bytes = %d, want <= %d", len(line), maxApprovalActionSummaryLineBytes)
		}
	}
	if got := len(strings.Join(summary, "\n")); got > maxApprovalActionSummaryBytes {
		t.Fatalf("summary bytes = %d, want <= %d", got, maxApprovalActionSummaryBytes)
	}
}

func TestSummarizeApprovalToolCallRejectsDuplicateAndUnknownFields(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"path":"README.md","path":"secret.txt"}`,
		`{"path":"README.md","secret":"do-not-show"}`,
	} {
		line, complete := summarizeApprovalToolCall(types.ToolCall{Function: types.ToolCallFunction{Name: "read_file", Arguments: raw}})
		if complete || line != "read_file details unavailable" {
			t.Fatalf("summarizeApprovalToolCall(%s) = %q complete=%v", raw, line, complete)
		}
		if strings.Contains(line, "secret") || strings.Contains(line, "do-not-show") {
			t.Fatalf("summary exposed rejected args: %q", line)
		}
	}
}
