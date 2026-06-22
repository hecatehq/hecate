//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

func TestOperatorTerminalLifecycleE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}

	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_OPERATOR_TERMINALS=1",
	)
	workspace := t.TempDir()
	created := postJSONDecodeStatus[e2eTerminalResponse](t, baseURL+"/hecate/v1/terminals", fmt.Sprintf(`{
		"workspace": %q,
		"command": "sh",
		"args": ["-c", "printf operator-terminal-e2e; printf err 1>&2"]
	}`, workspace), http.StatusCreated)
	if created.Object != "terminal" || created.Data.ID == "" {
		t.Fatalf("created terminal = %+v, want terminal id", created)
	}

	wait := postJSONDecode[e2eTerminalResponse](t, baseURL+"/hecate/v1/terminals/"+created.Data.ID+"/wait", `{}`)
	if wait.Data.Running {
		t.Fatal("wait running = true, want false")
	}
	if wait.Data.ExitCode == nil || *wait.Data.ExitCode != 0 {
		t.Fatalf("wait exit code = %v, want 0", wait.Data.ExitCode)
	}
	if !strings.Contains(wait.Data.Output, "operator-terminal-e2e") || !strings.Contains(wait.Data.Output, "err") {
		t.Fatalf("wait output = %q, want stdout and stderr", wait.Data.Output)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, baseURL+"/hecate/v1/terminals/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest DELETE terminal: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE terminal: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE terminal status = %d, want 204; body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

type e2eTerminalResponse struct {
	Object string          `json:"object"`
	Data   e2eTerminalItem `json:"data"`
}

type e2eTerminalItem struct {
	ID       string `json:"id"`
	Output   string `json:"output"`
	Running  bool   `json:"running"`
	ExitCode *int   `json:"exit_code"`
}
