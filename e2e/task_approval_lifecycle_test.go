//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
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

func TestTaskRunStreamResumeDoesNotAppendEventsE2E(t *testing.T) {
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
	cancelled := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/cancel", `{"reason":"operator stop"}`)
	if cancelled.Data.Status != "cancelled" {
		t.Fatalf("cancelled run status = %q, want cancelled", cancelled.Data.Status)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+started.Data.ID+"/events")
	if len(events.Data) == 0 {
		t.Fatal("events = 0, want terminal run events")
	}
	lastSequence := events.Data[len(events.Data)-1].Sequence
	if lastSequence == 0 {
		t.Fatalf("last event sequence = 0; events=%+v", events.Data)
	}

	finalSnapshot := e2eReadTerminalTaskRunSnapshotAfter(t, baseURL, taskID, started.Data.ID, lastSequence)
	if finalSnapshot.Data.Sequence != int(lastSequence) {
		t.Fatalf("resumed stream sequence = %d, want cursor %d", finalSnapshot.Data.Sequence, lastSequence)
	}
	if finalSnapshot.Data.Run.Status != "cancelled" || !finalSnapshot.Data.Terminal {
		t.Fatalf("resumed stream final snapshot = %+v, want terminal cancelled", finalSnapshot.Data)
	}

	afterStream := getJSON[e2eTaskEventsResponse](t, fmt.Sprintf("%s/hecate/v1/tasks/%s/runs/%s/events?after_sequence=%d", baseURL, taskID, started.Data.ID, lastSequence))
	if len(afterStream.Data) != 0 {
		t.Fatalf("stream appended %d run events after cursor %d, want read-only stream", len(afterStream.Data), lastSequence)
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
	return e2eReadTerminalTaskRunSnapshotAfter(t, baseURL, taskID, runID, 0)
}

func e2eReadTerminalTaskRunSnapshotAfter(t *testing.T, baseURL, taskID, runID string, afterSequence int64) e2eTaskRunStreamResponse {
	t.Helper()
	streamURL := baseURL + "/hecate/v1/tasks/" + taskID + "/runs/" + runID + "/stream"
	if afterSequence > 0 {
		streamURL += "?after_sequence=" + strconv.FormatInt(afterSequence, 10)
	}
	resp, err := http.Get(streamURL) //nolint:noctx
	if err != nil {
		t.Fatalf("GET run stream: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("stream content-type = %q, want text/event-stream", ct)
	}
	events := readSSE(t, resp)
	var terminal e2eTaskRunStreamResponse
	var sawTerminal bool
	var sawDone bool
	for _, event := range events {
		var payload e2eTaskRunStreamResponse
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			continue
		}
		if payload.Data.Run.ID == "" {
			continue
		}
		if afterSequence > 0 {
			if payload.Data.Sequence != int(afterSequence) {
				t.Fatalf("stream sequence = %d, want cursor %d", payload.Data.Sequence, afterSequence)
			}
			if event.ID != strconv.FormatInt(afterSequence, 10) {
				t.Fatalf("stream id = %q, want %d", event.ID, afterSequence)
			}
		}
		if event.Event == "done" {
			sawDone = true
			if !payload.Data.Terminal {
				t.Fatalf("done stream terminal = false, want true")
			}
			if payload.Data.Sequence == 0 {
				t.Fatalf("done stream sequence = 0, want persisted sequence")
			}
			continue
		}
		if payload.Data.Terminal || payload.Data.Run.Status == "cancelled" || payload.Data.Run.Status == "failed" || payload.Data.Run.Status == "completed" {
			terminal = payload
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatalf("terminal stream snapshot not found in %d SSE payloads", len(events))
	}
	if !sawDone {
		t.Fatalf("terminal stream done event not found in %d SSE payloads", len(events))
	}
	return terminal
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
	Sequence int64          `json:"sequence,omitempty"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
}

type e2eTaskRunStreamResponse struct {
	Data struct {
		Sequence  int               `json:"sequence,omitempty"`
		Terminal  bool              `json:"terminal,omitempty"`
		EventType string            `json:"event_type,omitempty"`
		Run       e2eTaskRun        `json:"run"`
		Steps     []e2eTaskStep     `json:"steps,omitempty"`
		Approvals []e2eTaskApproval `json:"approvals,omitempty"`
	} `json:"data"`
}
