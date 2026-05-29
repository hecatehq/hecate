//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

func TestTaskRunQueuedLifecycleE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "queued lifecycle e2e",
		"prompt": "complete normally",
		"execution_kind": "shell",
		"shell_command": "printf 'ok\n'",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
}

func TestTaskRunQueueCoordinatorDrainsMultipleTasksE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
		"HECATE_TASK_QUEUE_WORKERS=1",
	)

	type startedTask struct {
		taskID string
		runID  string
	}
	startedTasks := make([]startedTask, 0, 2)
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{
			"title": "queue coordinator e2e %d",
			"prompt": "complete normally",
			"execution_kind": "shell",
			"shell_command": "printf 'task-%d\n'",
			"working_directory": %q,
			"sandbox_allowed_root": %q,
			"workspace_mode": "in_place",
			"timeout_ms": 10000
		}`, i, i, workDir, workDir)
		created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
		started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)
		startedTasks = append(startedTasks, startedTask{taskID: created.Data.ID, runID: started.Data.ID})
	}

	for _, task := range startedTasks {
		run := waitForE2ETaskRunTerminal(t, baseURL, task.taskID, task.runID, 10*time.Second)
		if run.Status != "completed" {
			t.Fatalf("run %s status = %q last_error=%q, want completed", task.runID, run.Status, run.LastError)
		}
		events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+task.taskID+"/runs/"+task.runID+"/events")
		assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
	}
}

func TestTaskRunClaimedProcessorFinalizesFailureE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "claimed processor failure e2e",
		"prompt": "fail normally",
		"execution_kind": "shell",
		"shell_command": "exit 7",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "failed" {
		t.Fatalf("run status = %q last_error=%q, want failed", run.Status, run.LastError)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.failed"})
}
