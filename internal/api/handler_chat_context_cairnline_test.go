package api

import (
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
)

func cairnlineAssignmentContextForMemoryTest() cairnline.AssignmentContext {
	return cairnline.AssignmentContext{
		ID:      "ctx_memory_parity",
		Project: cairnline.Project{ID: "proj_ctx", Name: "Context Project"},
		WorkItem: cairnline.WorkItem{
			ID:        "work_ctx",
			ProjectID: "proj_ctx",
			Title:     "Context work item",
		},
		Assignment: cairnline.Assignment{
			ID:         "asgn_ctx",
			ProjectID:  "proj_ctx",
			WorkItemID: "work_ctx",
			RoleID:     "role_ctx",
			Status:     cairnline.AssignmentRunning,
			ExecutionRef: cairnline.ExecutionRef{
				Kind:             "task_run",
				TaskID:           "task_ctx",
				RunID:            "run_ctx",
				TraceID:          "trace_ctx",
				PendingApprovals: 1,
			},
		},
		Memory: []cairnline.MemoryEntry{
			{ID: "mem_timeout", ProjectID: "proj_ctx", Title: "Timeout invariant", Body: "Keep request timeouts under 30s.", TrustLabel: "operator", Enabled: true},
			{ID: "mem_retry", ProjectID: "proj_ctx", Title: "Retry ceiling", Body: "Never retry more than twice.", TrustLabel: "operator", Enabled: true},
		},
	}
}

func TestCairnlineAssignmentContextPacket_IncludesProjectMemory(t *testing.T) {
	t.Parallel()
	packet := cairnlineAssignmentContextPacket(cairnlineAssignmentContextForMemoryTest(), "")

	found := map[string]string{}
	for _, item := range packet.Items {
		if item.Kind == "memory" {
			found[item.Origin] = item.Body
		}
	}
	if len(found) != 2 {
		t.Fatalf("memory items = %+v, want both enabled entries projected into the portable context packet", found)
	}
	if found["mem_timeout"] != "Keep request timeouts under 30s." || found["mem_retry"] != "Never retry more than twice." {
		t.Fatalf("memory bodies = %+v, want full entry bodies carried", found)
	}
}

func TestCairnlineAssignmentContextPacket_RendersStructuredExecutionRef(t *testing.T) {
	t.Parallel()
	packet := cairnlineAssignmentContextPacket(cairnlineAssignmentContextForMemoryTest(), "")

	var assignmentBody string
	for _, item := range packet.Items {
		if item.Kind == "assignment" {
			assignmentBody = item.Body
		}
	}
	if !strings.Contains(assignmentBody, "Execution ref: kind=task_run task=task_ctx run=run_ctx trace=trace_ctx pending_approvals=1") {
		t.Fatalf("assignment body = %q, want structured execution ref line", assignmentBody)
	}
}

func TestCairnlineExecutionRefSummary(t *testing.T) {
	t.Parallel()
	if got := cairnlineExecutionRefSummary(cairnline.ExecutionRef{}); got != "" {
		t.Fatalf("cairnlineExecutionRefSummary(empty) = %q, want empty", got)
	}
	got := cairnlineExecutionRefSummary(cairnline.ExecutionRef{Kind: "chat_session", SessionID: "chat_1"})
	if got != "kind=chat_session session=chat_1" {
		t.Fatalf("cairnlineExecutionRefSummary(chat) = %q, want kind and session only", got)
	}
}

func TestProjectCairnlineAssignmentContextMemoryParity(t *testing.T) {
	t.Parallel()
	packet := cairnlineAssignmentContextPacket(cairnlineAssignmentContextForMemoryTest(), "")
	entries := []memory.Entry{
		{ID: "mem_timeout", ProjectID: "proj_ctx", Title: "Timeout invariant", Enabled: true},
		{ID: "mem_retry", ProjectID: "proj_ctx", Title: "Retry ceiling", Enabled: true},
		// Disabled entries stay out of launch reads and must not fail parity.
		{ID: "mem_disabled", ProjectID: "proj_ctx", Title: "Old rule", Enabled: false},
	}
	if err := projectCairnlineAssignmentContextMemoryParity(packet, entries); err != nil {
		t.Fatalf("memory parity = %v, want packet to satisfy the native composer selection", err)
	}

	missing := append(entries, memory.Entry{ID: "mem_missing", ProjectID: "proj_ctx", Title: "Uncarried", Enabled: true})
	if err := projectCairnlineAssignmentContextMemoryParity(packet, missing); err == nil {
		t.Fatal("memory parity with uncarried enabled entry = nil, want gate-blocking error")
	}

	if err := projectCairnlineAssignmentContextMemoryParity(chat.ContextPacket{}, entries); err == nil {
		t.Fatal("memory parity against empty packet = nil, want error for dropped memory")
	}
}
