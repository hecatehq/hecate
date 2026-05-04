//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestApprovalReconcilePersistsAndFlipsAcrossRestart is the binary-
// level smoke for slice 1C's startup reconcile. It boots the real
// hecate binary with GATEWAY_CHAT_SESSIONS_BACKEND=sqlite, inserts a
// pending agent-chat approval directly into the SQLite db (simulating
// a process that crashed mid-RequestPermission), kills the binary,
// restarts it, and asserts via the HTTP API that the surviving
// pending row was reconciled to status=timed_out, path=startup_reconcile.
//
// This exercises three contracts at once:
//   - cmd/hecate actually CALLS ReconcilePending on startup.
//   - The reconcile pass runs BEFORE the gateway accepts requests
//     (we never see the row in pending state from the API).
//   - The wire shape includes the path/status fields the operator UI
//     will key off.
func TestApprovalReconcilePersistsAndFlipsAcrossRestart(t *testing.T) {
	t.Parallel()

	bin := hecateBinary(t)
	workDir := t.TempDir()
	dataDir := filepath.Join(workDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	dbPath := filepath.Join(dataDir, "hecate.db")

	commonEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workDir,
		"GATEWAY_DATA_DIR=" + dataDir,
		"GATEWAY_CHAT_SESSIONS_BACKEND=sqlite",
		"GATEWAY_SQLITE_PATH=" + dbPath,
		// Start with auto so the approval coordinator boots cleanly even
		// without any adapter activity. Mode doesn't affect reconcile,
		// which runs before the gateway accepts traffic.
		"GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto",
	}

	// ── First start: bring the gateway up to materialize the schema.
	addr1 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd1 := exec.Command(bin)
	cmd1.Dir = workDir
	cmd1.Env = append([]string{"GATEWAY_ADDRESS=" + addr1}, commonEnv...)
	cmd1.Stdout = io.Discard
	cmd1.Stderr = io.Discard
	if err := cmd1.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	waitHealthy(t, "http://"+addr1, 10*time.Second)

	// Create an agent-chat session via the API so the row we insert
	// below references a valid session_id (the API list endpoint
	// gates on session existence).
	sessionID := mustCreateAgentChatSession(t, "http://"+addr1)

	if err := cmd1.Process.Kill(); err != nil {
		t.Fatalf("kill first run: %v", err)
	}
	_ = cmd1.Wait()

	// ── Inject a pending approval directly into the SQLite db, as if
	// a previous process had registered a waiter and died before the
	// operator resolved it.
	approvalID := injectPendingApproval(t, dbPath, sessionID)

	// ── Second start: reconcile must fire BEFORE the gateway accepts
	// requests. By the time /healthz responds, the row is already
	// flipped — we never observe it in pending state via the API.
	addr2 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd2 := exec.Command(bin)
	cmd2.Dir = workDir
	cmd2.Env = append([]string{"GATEWAY_ADDRESS=" + addr2}, commonEnv...)
	cmd2.Stdout = io.Discard
	cmd2.Stderr = io.Discard
	if err := cmd2.Start(); err != nil {
		t.Fatalf("second start: %v", err)
	}
	t.Cleanup(func() { _ = cmd2.Process.Kill(); _ = cmd2.Wait() })
	waitHealthy(t, "http://"+addr2, 10*time.Second)

	// Fetch the row through the public API. It must be terminal, with
	// the path label that distinguishes reconciled rows from regular
	// timeouts.
	got := mustGetApproval(t, "http://"+addr2, sessionID, approvalID)
	if got.Status != "timed_out" {
		t.Fatalf("status = %q, want timed_out", got.Status)
	}
	if got.Path != "startup_reconcile" {
		t.Fatalf("path = %q, want startup_reconcile", got.Path)
	}
	if got.ResolvedAt == nil {
		t.Fatal("resolved_at must be set after reconcile")
	}
	if got.DecisionNote == "" {
		t.Fatal("decision_note must explain the disposition")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

type apiApproval struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"session_id"`
	Status       string     `json:"status"`
	Path         string     `json:"path"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`
}

func mustCreateAgentChatSession(t *testing.T, baseURL string) string {
	t.Helper()
	body := strings.NewReader(`{"title":"reconcile smoke","adapter_id":"codex","workspace":"/tmp"}`)
	resp, err := http.Post(baseURL+"/v1/agent-chat/sessions", "application/json", body)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create session status = %d; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if env.Data.ID == "" {
		t.Fatal("session id missing")
	}
	return env.Data.ID
}

func mustGetApproval(t *testing.T, baseURL, sessionID, approvalID string) apiApproval {
	t.Helper()
	url := fmt.Sprintf("%s/v1/agent-chat/sessions/%s/approvals/%s", baseURL, sessionID, approvalID)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET approval: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET approval status = %d; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Data apiApproval `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode approval: %v", err)
	}
	return env.Data
}

// injectPendingApproval writes a pending approval row directly into
// the SQLite database, bypassing the API. Simulates the state a
// previous process would have left behind if it crashed during a
// prompt-mode RequestPermission. The row is wired to the supplied
// session id so the API GET succeeds after restart.
func injectPendingApproval(t *testing.T, dbPath, sessionID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	id := "appr_e2e_" + fmt.Sprint(time.Now().UnixNano())
	now := time.Now().UTC().Add(-10 * time.Minute)
	expires := now.Add(time.Hour)
	options := `[{"option_id":"allow_once_id","kind":"allow_once","name":"Allow once"}]`
	scopes := `["once","session","workspace_tool","adapter_tool"]`
	payload := `{"sessionId":"` + sessionID + `","options":[]}`

	// Default GATEWAY_SQLITE_TABLE_PREFIX is "hecate"; combined with
	// agent_chat_approvals → hecate_agent_chat_approvals.
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO hecate_agent_chat_approvals (
			id, session_id, adapter_id, workspace, tool_kind, tool_name, status,
			acp_payload, acp_options, scope_choices,
			selected_option, scope, decision, path, decision_note,
			created_at, resolved_at, expires_at
		) VALUES (?,?,?,?,?,?,?, ?,?,?, ?,?,?,?,?, ?,?,?)`,
		id, sessionID, "codex", "/tmp", "file_write", "Write file", "pending",
		[]byte(payload), []byte(options), []byte(scopes),
		"", "", "", "", "",
		now, nil, expires,
	)
	if err != nil {
		t.Fatalf("inject pending approval: %v", err)
	}
	return id
}
