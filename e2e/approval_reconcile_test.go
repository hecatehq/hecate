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
// level smoke for startup reconcile. It boots the real
// hecate binary with HECATE_BACKEND=sqlite, inserts a
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
		"HECATE_DATA_DIR=" + dataDir,
		"HECATE_BACKEND=sqlite",
		"HECATE_SQLITE_PATH=" + dbPath,
		// Start with auto so the approval coordinator boots cleanly even
		// without any adapter activity. Mode doesn't affect reconcile,
		// which runs before the gateway accepts traffic.
		"HECATE_AGENT_ADAPTER_APPROVAL_MODE=auto",
	}

	// ── First start: bring the gateway up to materialize the schema.
	addr1 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd1 := exec.Command(bin, "serve")
	cmd1.Dir = workDir
	cmd1.Env = append([]string{"HECATE_ADDRESS=" + addr1}, commonEnv...)
	cmd1.Stdout = io.Discard
	cmd1.Stderr = io.Discard
	if err := cmd1.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	waitHealthy(t, "http://"+addr1, gatewayStartupTimeout)

	// Create a persisted agent-chat session directly so the row we
	// insert below references a valid session_id. The approval
	// reconcile contract doesn't need a live ACP subprocess, and CI
	// runners intentionally do not carry operator auth for Codex.
	sessionID := injectChatSession(t, dbPath)

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
	cmd2 := exec.Command(bin, "serve")
	cmd2.Dir = workDir
	cmd2.Env = append([]string{"HECATE_ADDRESS=" + addr2}, commonEnv...)
	cmd2.Stdout = io.Discard
	cmd2.Stderr = io.Discard
	if err := cmd2.Start(); err != nil {
		t.Fatalf("second start: %v", err)
	}
	t.Cleanup(func() { _ = cmd2.Process.Kill(); _ = cmd2.Wait() })
	waitHealthy(t, "http://"+addr2, gatewayStartupTimeout)

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

// TestApprovalGrantPersistsAcrossRestart is the binary-level smoke for
// durable external-adapter approval grants. It creates a real agent-chat
// session, injects a pending approval into SQLite, resolves it through
// the public HTTP API with scope=session, restarts the binary, and
// verifies the grant is still listed. Unit tests cover store parity;
// this pins the cmd/hecate wiring and public route together.
func TestApprovalGrantPersistsAcrossRestart(t *testing.T) {
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
		"HECATE_DATA_DIR=" + dataDir,
		"HECATE_BACKEND=sqlite",
		"HECATE_SQLITE_PATH=" + dbPath,
		"HECATE_AGENT_ADAPTER_APPROVAL_MODE=prompt",
	}

	addr1 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd1 := exec.Command(bin, "serve")
	cmd1.Dir = workDir
	cmd1.Env = append([]string{"HECATE_ADDRESS=" + addr1}, commonEnv...)
	cmd1.Stdout = io.Discard
	cmd1.Stderr = io.Discard
	if err := cmd1.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	waitHealthy(t, "http://"+addr1, gatewayStartupTimeout)

	base1 := "http://" + addr1
	sessionID := injectChatSession(t, dbPath)
	approvalID := injectPendingApproval(t, dbPath, sessionID)
	mustResolveApproval(t, base1, sessionID, approvalID, `{"decision":"approve","scope":"session"}`)

	grants := mustListGrants(t, base1)
	if len(grants) != 1 {
		t.Fatalf("grants before restart = %d, want 1", len(grants))
	}
	if grants[0].Scope != "session" || grants[0].SessionID != sessionID || grants[0].Decision != "approve" {
		t.Fatalf("grant before restart malformed: %+v", grants[0])
	}

	if err := cmd1.Process.Kill(); err != nil {
		t.Fatalf("kill first run: %v", err)
	}
	_ = cmd1.Wait()

	addr2 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd2 := exec.Command(bin, "serve")
	cmd2.Dir = workDir
	cmd2.Env = append([]string{"HECATE_ADDRESS=" + addr2}, commonEnv...)
	cmd2.Stdout = io.Discard
	cmd2.Stderr = io.Discard
	if err := cmd2.Start(); err != nil {
		t.Fatalf("second start: %v", err)
	}
	t.Cleanup(func() { _ = cmd2.Process.Kill(); _ = cmd2.Wait() })
	waitHealthy(t, "http://"+addr2, gatewayStartupTimeout)

	grants = mustListGrants(t, "http://"+addr2)
	if len(grants) != 1 {
		t.Fatalf("grants after restart = %d, want 1", len(grants))
	}
	if grants[0].Scope != "session" || grants[0].SessionID != sessionID || grants[0].Decision != "approve" {
		t.Fatalf("grant after restart malformed: %+v", grants[0])
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

type apiGrant struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	AdapterID string `json:"adapter_id"`
	ToolKind  string `json:"tool_kind"`
	SessionID string `json:"session_id,omitempty"`
	Decision  string `json:"decision"`
}

func injectChatSession(t *testing.T, dbPath string) string {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	sessionID := "chat_e2e_" + fmt.Sprintf("%d", now.UnixNano())
	_, err = db.Exec(
		`INSERT INTO hecate_chat_sessions (
			id, title, agent_id, driver_kind, native_session_id, workspace, workspace_branch,
			status, task_id, latest_run_id, provider, model, capabilities, config_options, turns_used, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		"reconcile smoke",
		"codex",
		"acp",
		"native_e2e",
		"/tmp",
		"",
		"idle",
		"",
		"",
		"",
		"",
		"{}",
		"[]",
		0,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert agent chat session: %v", err)
	}
	return sessionID
}

func mustResolveApproval(t *testing.T, baseURL, sessionID, approvalID, body string) {
	t.Helper()
	url := fmt.Sprintf("%s/hecate/v1/chat/sessions/%s/approvals/%s/resolve", baseURL, sessionID, approvalID)
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("resolve approval status = %d; body=%s", resp.StatusCode, raw)
	}
}

func mustListGrants(t *testing.T, baseURL string) []apiGrant {
	t.Helper()
	resp, err := http.Get(baseURL + "/hecate/v1/chat/grants")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("list grants status = %d; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Data []apiGrant `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode grants: %v", err)
	}
	return env.Data
}

func mustGetApproval(t *testing.T, baseURL, sessionID, approvalID string) apiApproval {
	t.Helper()
	url := fmt.Sprintf("%s/hecate/v1/chat/sessions/%s/approvals/%s", baseURL, sessionID, approvalID)
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

	// Default HECATE_SQLITE_TABLE_PREFIX is "hecate"; combined with
	// agent_chat_approvals → hecate_chat_approvals.
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO hecate_chat_approvals (
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
