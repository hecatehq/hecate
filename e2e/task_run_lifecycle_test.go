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
