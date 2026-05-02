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

// TestSandboxShellExecRunsInline is the highest-fidelity smoke test for the
// inline-sandbox model: the gateway spawns the shell directly with no helper
// binary. A simple echo task must round-trip through the runtime and produce
// stdout in the artifacts.
func TestSandboxShellExecRunsInline(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		// Disable the shell_exec approval gate so the task runs without
		// a human approval step in the test loop.
		"GATEWAY_TASK_APPROVAL_POLICIES=",
	)

	taskID := sbCreateShellTask(t, baseURL, `echo "inline-sandbox-ok"`, workDir)
	sbStartTask(t, baseURL, taskID)

	runID, status := sbWaitTerminal(t, baseURL, taskID, 20*time.Second)
	if status != "completed" {
		t.Fatalf("task %s: status = %q, want completed (run %s)", taskID, status, runID)
	}
	stdout := sbStdout(t, baseURL, taskID, runID)
	if !strings.Contains(stdout, "inline-sandbox-ok") {
		t.Errorf("stdout = %q, want to contain inline-sandbox-ok", stdout)
	}
}

// TestSandboxPolicyDeniesNetwork verifies that commands attempting network
// access are rejected at the policy layer when sandbox_network=false (default).
// The task must fail — not time out — confirming enforcement is static and
// fast, not a network-level block.
func TestSandboxPolicyDeniesNetwork(t *testing.T) {
	workDir := t.TempDir()

	baseURL := gatewayServer(t,
		"GATEWAY_TASK_APPROVAL_POLICIES=",
	)

	taskID := sbCreateShellTask(t, baseURL, `curl https://example.com`, workDir)
	sbStartTask(t, baseURL, taskID)

	_, status := sbWaitTerminal(t, baseURL, taskID, 20*time.Second)
	if status != "failed" {
		t.Fatalf("task %s: status = %q, want failed (network policy denial)", taskID, status)
	}
}

// TestSandboxReadOnlyPolicyDeniesWrite verifies that the read-only policy
// gate blocks write operations before the command runs.
func TestSandboxReadOnlyPolicyDeniesWrite(t *testing.T) {
	workDir := t.TempDir()

	baseURL := gatewayServer(t,
		"GATEWAY_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"execution_kind": "shell",
		"shell_command": "touch %s/blocked.txt",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"sandbox_read_only": true,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir, workDir)

	taskID := sbPostTask(t, baseURL, body)
	sbStartTask(t, baseURL, taskID)

	_, status := sbWaitTerminal(t, baseURL, taskID, 20*time.Second)
	if status != "failed" {
		t.Fatalf("task %s: status = %q, want failed (read-only policy denial)", taskID, status)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func sbCreateShellTask(t *testing.T, baseURL, command, workDir string) string {
	t.Helper()
	return sbPostTask(t, baseURL, fmt.Sprintf(`{
		"execution_kind": "shell",
		"shell_command": %q,
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, command, workDir, workDir))
}

func sbPostTask(t *testing.T, baseURL, body string) string {
	t.Helper()
	resp := postJSON(t, baseURL+"/v1/tasks", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/tasks status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode task response: %v", err)
	}
	if result.Data.ID == "" {
		t.Fatal("task ID is empty in response")
	}
	return result.Data.ID
}

func sbStartTask(t *testing.T, baseURL, taskID string) {
	t.Helper()
	resp := postJSON(t, baseURL+"/v1/tasks/"+taskID+"/start", `{}`, nil)
	body := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/tasks/%s/start status = %d, body = %s", taskID, resp.StatusCode, body)
	}
}

func sbWaitTerminal(t *testing.T, baseURL, taskID string, timeout time.Duration) (runID, status string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", baseURL+"/v1/tasks/"+taskID+"/runs", nil)
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		var runs struct {
			Data []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&runs)
		resp.Body.Close()
		for _, run := range runs.Data {
			switch run.Status {
			case "completed", "failed", "cancelled":
				return run.ID, run.Status
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal state within %v", taskID, timeout)
	return "", ""
}

func sbStdout(t *testing.T, baseURL, taskID, runID string) string {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", baseURL+"/v1/tasks/"+taskID+"/runs/"+runID+"/artifacts", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET artifacts: %v", err)
	}
	defer resp.Body.Close()
	var arts struct {
		Data []struct {
			Name        string `json:"name"`
			ContentText string `json:"content_text"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&arts); err != nil {
		t.Fatalf("decode artifacts response: %v", err)
	}
	for _, a := range arts.Data {
		if a.Name == "stdout.txt" {
			return a.ContentText
		}
	}
	t.Logf("artifacts in response: %+v", arts.Data)
	t.Fatal("stdout.txt artifact not found")
	return ""
}
