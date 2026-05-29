//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTaskApprovalCancelTerminalStateE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=shell_exec",
	)

	taskID := e2eCreateApprovalShellTask(t, baseURL, workDir)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/start", `{}`)
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("started run status = %q, want awaiting_approval", started.Data.Status)
	}

	approvals := getJSON[e2eTaskApprovalsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}
	if approvals.Data[0].Status != "pending" {
		t.Fatalf("approval status = %q, want pending", approvals.Data[0].Status)
	}

	cancelled := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/cancel", `{"reason":"operator stop"}`)
	if cancelled.Data.Status != "cancelled" {
		t.Fatalf("cancelled run status = %q, want cancelled", cancelled.Data.Status)
	}
	if !strings.Contains(cancelled.Data.LastError, "operator stop") {
		t.Fatalf("cancelled run last_error = %q, want operator stop", cancelled.Data.LastError)
	}

	afterCancel := getJSON[e2eTaskApprovalsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals")
	if len(afterCancel.Data) != 1 {
		t.Fatalf("approvals after cancel = %d, want 1", len(afterCancel.Data))
	}
	if afterCancel.Data[0].Status != "cancelled" || afterCancel.Data[0].ResolvedBy != "system" {
		t.Fatalf("approval after cancel = %+v, want system-cancelled", afterCancel.Data[0])
	}

	steps := getJSON[e2eTaskStepsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/steps")
	for _, step := range steps.Data {
		if step.Status == "awaiting_approval" || step.Status == "running" {
			t.Fatalf("active step after cancel = %+v, want terminal/non-active step state", step)
		}
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "approval.requested", "run.awaiting_approval", "approval.resolved", "run.cancelled", "task.updated"})
	assertE2EApprovalResolved(t, events.Data, approvals.Data[0].ID, "cancelled", "system")

	finalSnapshot := e2eReadTerminalTaskRunSnapshot(t, baseURL, taskID, started.Data.ID)
	if finalSnapshot.Data.Run.Status != "cancelled" {
		t.Fatalf("terminal stream run status = %q, want cancelled", finalSnapshot.Data.Run.Status)
	}
	if finalSnapshot.Data.Terminal != true {
		t.Fatalf("terminal stream flag = false, want true")
	}
	if len(finalSnapshot.Data.Approvals) != 1 {
		t.Fatalf("terminal stream approvals = %d, want 1", len(finalSnapshot.Data.Approvals))
	}
	if finalSnapshot.Data.Approvals[0].Status != "cancelled" {
		t.Fatalf("terminal stream approval status = %q, want cancelled", finalSnapshot.Data.Approvals[0].Status)
	}
	for _, approval := range finalSnapshot.Data.Approvals {
		if approval.Status == "pending" {
			t.Fatalf("terminal stream carried pending approval: %+v", approval)
		}
	}
	for _, step := range finalSnapshot.Data.Steps {
		if step.Status == "awaiting_approval" || step.Status == "running" {
			t.Fatalf("terminal stream carried active step: %+v", step)
		}
	}
}

func TestTaskApprovalRejectTerminalStateE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=shell_exec",
	)

	taskID := e2eCreateApprovalShellTask(t, baseURL, workDir)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/start", `{}`)
	if started.Data.Status != "awaiting_approval" {
		t.Fatalf("started run status = %q, want awaiting_approval", started.Data.Status)
	}

	approvals := getJSON[e2eTaskApprovalsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals")
	if len(approvals.Data) != 1 {
		t.Fatalf("approvals = %d, want 1", len(approvals.Data))
	}
	if approvals.Data[0].Status != "pending" {
		t.Fatalf("approval status = %q, want pending", approvals.Data[0].Status)
	}

	resolved := postJSONDecode[e2eTaskApprovalResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"reject","note":"not safe"}`)
	if resolved.Data.Status != "rejected" {
		t.Fatalf("resolved approval status = %q, want rejected", resolved.Data.Status)
	}
	if resolved.Data.ResolvedBy != "operator" || resolved.Data.ResolutionNote != "not safe" {
		t.Fatalf("resolved approval = %+v, want operator rejection note", resolved.Data)
	}

	repeated := postJSON(t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals/"+approvals.Data[0].ID+"/resolve", `{"decision":"approve"}`, nil)
	if repeated.StatusCode != http.StatusConflict {
		t.Fatalf("repeat resolve status = %d, want 409; body=%s", repeated.StatusCode, readBody(t, repeated))
	}
	repeated.Body.Close()

	run := getJSON[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID)
	if run.Data.Status != "cancelled" || run.Data.LastError != "approval rejected" {
		t.Fatalf("run after reject = %+v, want cancelled approval rejected", run.Data)
	}
	task := getJSON[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks/"+taskID)
	if task.Data.Status != "cancelled" || task.Data.LastError != "approval rejected" {
		t.Fatalf("task after reject = %+v, want cancelled approval rejected", task.Data)
	}

	steps := getJSON[e2eTaskStepsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/steps")
	for _, step := range steps.Data {
		if step.Status == "awaiting_approval" || step.Status == "running" {
			t.Fatalf("active step after reject = %+v, want terminal/non-active step state", step)
		}
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventTypes(t, events.Data, "approval.resolved", "run.cancelled", "task.updated")
	assertE2EApprovalResolved(t, events.Data, approvals.Data[0].ID, "rejected", "operator")

	finalSnapshot := e2eReadTerminalTaskRunSnapshot(t, baseURL, taskID, started.Data.ID)
	if finalSnapshot.Data.Run.Status != "cancelled" || finalSnapshot.Data.Run.LastError != "approval rejected" {
		t.Fatalf("terminal stream run = %+v, want cancelled approval rejected", finalSnapshot.Data.Run)
	}
	if !finalSnapshot.Data.Terminal {
		t.Fatalf("terminal stream flag = false, want true")
	}
	if len(finalSnapshot.Data.Approvals) != 1 || finalSnapshot.Data.Approvals[0].Status != "rejected" {
		t.Fatalf("terminal stream approvals = %+v, want one rejected approval", finalSnapshot.Data.Approvals)
	}
}

func e2eCreateApprovalShellTask(t *testing.T, baseURL, workDir string) string {
	t.Helper()
	body := fmt.Sprintf(`{
		"title": "approval cancel e2e",
		"prompt": "cancel before approval",
		"execution_kind": "shell",
		"shell_command": "printf 'should-not-run\n'",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	resp := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	if resp.Data.ID == "" {
		t.Fatal("created task id is empty")
	}
	return resp.Data.ID
}

func e2eReadTerminalTaskRunSnapshot(t *testing.T, baseURL, taskID, runID string) e2eTaskRunStreamResponse {
	t.Helper()
	resp, err := http.Get(baseURL + "/hecate/v1/tasks/" + taskID + "/runs/" + runID + "/stream") //nolint:noctx
	if err != nil {
		t.Fatalf("GET run stream: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("stream content-type = %q, want text/event-stream", ct)
	}
	events := readSSE(t, resp)
	deadline := time.Now().Add(1 * time.Second)
	var last e2eTaskRunStreamResponse
	for _, event := range events {
		var payload e2eTaskRunStreamResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			continue
		}
		if payload.Data.Run.ID == "" {
			continue
		}
		last = payload
		if payload.Data.Terminal || payload.Data.Run.Status == "cancelled" || payload.Data.Run.Status == "failed" || payload.Data.Run.Status == "completed" {
			return payload
		}
	}
	if time.Now().Before(deadline) && last.Data.Run.ID != "" {
		return last
	}
	t.Fatalf("terminal stream snapshot not found in %d SSE payloads", len(events))
	return e2eTaskRunStreamResponse{}
}

func assertE2EEventOrder(t *testing.T, events []e2eEventEnvelope, want []string) {
	t.Helper()
	cursor := 0
	for _, event := range events {
		if cursor >= len(want) {
			break
		}
		if event.Type == want[cursor] {
			cursor++
		}
	}
	if cursor == len(want) {
		return
	}
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.Type)
	}
	t.Fatalf("missing event order tail %v; got %v", want[cursor:], got)
}

func assertE2EApprovalResolved(t *testing.T, events []e2eEventEnvelope, approvalID, status, by string) {
	t.Helper()
	for _, event := range events {
		if event.Type != "approval.resolved" {
			continue
		}
		if event.Data["approval_id"] == approvalID && event.Data["status"] == status && event.Data["by"] == by {
			return
		}
	}
	t.Fatalf("approval.resolved approval=%q status=%q by=%q not found in %+v", approvalID, status, by, events)
}

func assertE2EEventTypes(t *testing.T, events []e2eEventEnvelope, want ...string) {
	t.Helper()
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		seen[event.Type] = true
	}
	for _, eventType := range want {
		if !seen[eventType] {
			got := make([]string, 0, len(events))
			for _, event := range events {
				got = append(got, event.Type)
			}
			t.Fatalf("missing event %q; got %v", eventType, got)
		}
	}
}

type e2eTaskResponse struct {
	Data struct {
		ID        string `json:"id"`
		Status    string `json:"status,omitempty"`
		LastError string `json:"last_error,omitempty"`
	} `json:"data"`
}

type e2eTaskRunResponse struct {
	Data e2eTaskRun `json:"data"`
}

type e2eTaskRun struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	LastError    string `json:"last_error,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ProviderKind string `json:"provider_kind,omitempty"`
	Model        string `json:"model,omitempty"`
}

type e2eTaskApprovalsResponse struct {
	Data []e2eTaskApproval `json:"data"`
}

type e2eTaskApprovalResponse struct {
	Data e2eTaskApproval `json:"data"`
}

type e2eTaskApproval struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ResolvedBy     string `json:"resolved_by,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
}

type e2eTaskStepsResponse struct {
	Data []e2eTaskStep `json:"data"`
}

type e2eTaskStep struct {
	ID            string         `json:"id"`
	Index         int            `json:"index,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Status        string         `json:"status"`
	ToolName      string         `json:"tool_name,omitempty"`
	Input         map[string]any `json:"input,omitempty"`
	OutputSummary map[string]any `json:"output_summary,omitempty"`
	Error         string         `json:"error,omitempty"`
	ErrorKind     string         `json:"error_kind,omitempty"`
}

type e2eTaskEventsResponse struct {
	Data []e2eEventEnvelope `json:"data"`
}

type e2eEventEnvelope struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type e2eTaskRunStreamResponse struct {
	Data struct {
		Terminal  bool              `json:"terminal,omitempty"`
		EventType string            `json:"event_type,omitempty"`
		Run       e2eTaskRun        `json:"run"`
		Steps     []e2eTaskStep     `json:"steps,omitempty"`
		Approvals []e2eTaskApproval `json:"approvals,omitempty"`
	} `json:"data"`
}
