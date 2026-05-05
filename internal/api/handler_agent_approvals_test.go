package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/providers"
)

// approvalsHTTPFixture wires a test HTTP handler with an installed
// approval coordinator. Tests seed approvals via the coordinator's
// store and exercise the HTTP surface end-to-end.
type approvalsHTTPFixture struct {
	server   *httptest.Server
	handler  *Handler
	coord    *agentadapters.ApprovalCoordinator
	store    *agentadapters.MemoryApprovalStore
	chatMgmt *agentchat.MemoryStore
}

func newApprovalsHTTPFixture(t *testing.T) *approvalsHTTPFixture {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	provider := &fakeProvider{name: "openai"}
	apiHandler := newTestAPIHandlerWithControlPlane(logger, []providers.Provider{provider}, config.Config{}, nil)

	// Replace the auto-wired coordinator with one tied to our local
	// store so tests can seed/inspect rows directly. Reuse the
	// handler's existing approval hooks (telemetry + SSE bus) so
	// approval.* SSE events flow on /v1/agent-chat/sessions/{id}/stream.
	store := agentadapters.NewMemoryApprovalStore()
	coord := agentadapters.NewApprovalCoordinator(agentadapters.CoordinatorOptions{
		Mode:    agentadapters.ModePrompt,
		Store:   store,
		Timeout: 5 * time.Second,
		Hooks:   apiHandler.approvalConfig.hooks,
	})
	mgr, _ := apiHandler.agentChatRunner.(*agentadapters.SessionManager)
	if mgr == nil {
		t.Fatal("expected agentChatRunner to be a *SessionManager")
	}
	mgr.SetApprovalCoordinator(coord)

	chat := agentchat.NewMemoryStore()
	apiHandler.agentChat = chat

	srv := httptest.NewServer(NewServer(logger, apiHandler))
	t.Cleanup(srv.Close)
	return &approvalsHTTPFixture{server: srv, handler: apiHandler, coord: coord, store: store, chatMgmt: chat}
}

func (f *approvalsHTTPFixture) seedSession(t *testing.T, id string) {
	t.Helper()
	if _, err := f.chatMgmt.Create(context.Background(), agentchat.Session{
		ID:        id,
		Title:     "test session",
		AdapterID: "codex",
		Workspace: "/tmp/w",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func (f *approvalsHTTPFixture) seedPending(t *testing.T, sessionID string, options []acp.PermissionOption) agentadapters.Approval {
	t.Helper()
	now := time.Now().UTC()
	return mustCreateApproval(t, f.store, agentadapters.Approval{
		SessionID:    sessionID,
		AdapterID:    "codex",
		Workspace:    "/tmp/w",
		ToolKind:     "file_write",
		Status:       agentadapters.ApprovalStatusPending,
		ACPOptions:   normalizeOptionsForTest(options),
		ScopeChoices: []agentadapters.ApprovalScope{agentadapters.ApprovalScopeOnce, agentadapters.ApprovalScopeSession, agentadapters.ApprovalScopeWorkspaceTool, agentadapters.ApprovalScopeAdapterTool},
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
	})
}

func mustCreateApproval(t *testing.T, store *agentadapters.MemoryApprovalStore, row agentadapters.Approval) agentadapters.Approval {
	t.Helper()
	got, err := store.CreateApproval(context.Background(), row)
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	return got
}

func normalizeOptionsForTest(options []acp.PermissionOption) []agentadapters.ApprovalOption {
	out := make([]agentadapters.ApprovalOption, 0, len(options))
	for _, opt := range options {
		out = append(out, agentadapters.ApprovalOption{
			OptionID: string(opt.OptionId),
			Kind:     string(opt.Kind),
			Name:     opt.Name,
		})
	}
	return out
}

func defaultAllowDenyOptions() []acp.PermissionOption {
	return []acp.PermissionOption{
		{OptionId: "allow_once_id", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
		{OptionId: "reject_once_id", Kind: acp.PermissionOptionKindRejectOnce, Name: "Deny"},
	}
}

// ─── GET list ────────────────────────────────────────────────────────────────

func TestHTTPListApprovalsReturnsSessionRowsOldestFirst(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "sess1")

	t0 := time.Now().UTC()
	a1 := mustCreateApproval(t, f.store, agentadapters.Approval{SessionID: "sess1", AdapterID: "codex", Status: agentadapters.ApprovalStatusPending, CreatedAt: t0})
	a2 := mustCreateApproval(t, f.store, agentadapters.Approval{SessionID: "sess1", AdapterID: "codex", Status: agentadapters.ApprovalStatusPending, CreatedAt: t0.Add(time.Second)})
	mustCreateApproval(t, f.store, agentadapters.Approval{SessionID: "other", AdapterID: "codex", Status: agentadapters.ApprovalStatusPending, CreatedAt: t0})

	resp, err := http.Get(f.server.URL + "/v1/agent-chat/sessions/sess1/approvals")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Object string              `json:"object"`
		Data   []agentApprovalItem `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("got %d rows, want 2 (other-session row excluded)", len(body.Data))
	}
	if body.Data[0].ID != a1.ID || body.Data[1].ID != a2.ID {
		t.Fatalf("rows out of order; want %s,%s got %s,%s", a1.ID, a2.ID, body.Data[0].ID, body.Data[1].ID)
	}
}

func TestHTTPListApprovalsFilterByStatus(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")

	row := mustCreateApproval(t, f.store, agentadapters.Approval{SessionID: "s", Status: agentadapters.ApprovalStatusPending})
	mustCreateApproval(t, f.store, agentadapters.Approval{SessionID: "s", Status: agentadapters.ApprovalStatusPending})
	_, _ = f.store.ResolveApproval(context.Background(), row.ID, agentadapters.ApprovalStatusApproved, agentadapters.ApprovalDecisionApprove, "x", agentadapters.ApprovalScopeOnce, agentadapters.PathOperator, "", time.Now())

	resp, err := http.Get(f.server.URL + "/v1/agent-chat/sessions/s/approvals?status=pending")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Data []agentApprovalItem `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Data) != 1 {
		t.Fatalf("pending rows = %d, want 1", len(body.Data))
	}
}

func TestHTTPListApprovalsUnknownSessionReturns404(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	resp, err := http.Get(f.server.URL + "/v1/agent-chat/sessions/missing/approvals")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── GET single ──────────────────────────────────────────────────────────────

func TestHTTPGetApprovalHappyPath(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	resp, err := http.Get(f.server.URL + "/v1/agent-chat/sessions/s/approvals/" + row.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct{ Data agentApprovalItem }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Data.ID != row.ID || body.Data.Status != "pending" {
		t.Fatalf("got %+v", body.Data)
	}
}

func TestHTTPGetApprovalUnknownReturns404(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	resp, err := http.Get(f.server.URL + "/v1/agent-chat/sessions/s/approvals/missing")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHTTPGetApprovalWrongSessionReturns404(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s1")
	f.seedSession(t, "s2")
	row := f.seedPending(t, "s1", defaultAllowDenyOptions())

	resp, err := http.Get(f.server.URL + "/v1/agent-chat/sessions/s2/approvals/" + row.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── POST resolve ────────────────────────────────────────────────────────────

func TestHTTPResolveApproveScopeOnce(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve",
		`{"decision":"approve","scope":"once"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, readApprovalBody(t, resp))
	}
	var body struct{ Data agentApprovalItem }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Data.Status != "approved" || body.Data.SelectedOption != "allow_once_id" {
		t.Fatalf("resolved row wrong: %+v", body.Data)
	}
}

func TestHTTPResolveCreatesGrantOnSessionScope(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve",
		`{"decision":"approve","scope":"session"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	grants, _ := f.coord.ListGrants(context.Background(), agentadapters.GrantFilter{})
	if len(grants) != 1 {
		t.Fatalf("got %d grants, want 1", len(grants))
	}
	if grants[0].SessionID != "s" || grants[0].Decision != agentadapters.ApprovalDecisionApprove {
		t.Fatalf("grant malformed: %+v", grants[0])
	}
}

func TestHTTPResolveAlreadyResolvedReturns409(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	body := `{"decision":"approve","scope":"once"}`
	first := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve", body)
	first.Body.Close()

	second := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve", body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", second.StatusCode)
	}
}

func TestHTTPResolveAmbiguousOptionReturns409WithOptions(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	// Two allow_* options force ambiguity when no selected_option is provided.
	row := f.seedPending(t, "s", []acp.PermissionOption{
		{OptionId: "a1", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
		{OptionId: "a2", Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow always"},
		{OptionId: "r1", Kind: acp.PermissionOptionKindRejectOnce, Name: "Deny"},
	})

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve",
		`{"decision":"approve","scope":"once"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Type    string                    `json:"type"`
			Message string                    `json:"message"`
			Options []agentApprovalOptionItem `json:"options"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Type != "conflict" {
		t.Fatalf("error.type = %q, want conflict", body.Error.Type)
	}
	if len(body.Error.Options) != 2 {
		t.Fatalf("options len = %d, want 2 (the ambiguous candidates)", len(body.Error.Options))
	}
}

func TestHTTPResolveExplicitSelectedOptionDisambiguates(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", []acp.PermissionOption{
		{OptionId: "a1", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
		{OptionId: "a2", Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow always"},
	})

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve",
		`{"decision":"approve","scope":"once","selected_option":"a2"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, readApprovalBody(t, resp))
	}
	var body struct{ Data agentApprovalItem }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Data.SelectedOption != "a2" {
		t.Fatalf("selected_option = %q, want a2", body.Data.SelectedOption)
	}
}

func TestHTTPResolveUnknownSelectedOptionReturns400(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve",
		`{"decision":"approve","scope":"once","selected_option":"phantom"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPResolveSelectedOptionMustMatchDecision(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve",
		`{"decision":"deny","scope":"once","selected_option":"allow_once_id"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHTTPResolveWrongSessionReturns404(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s1")
	f.seedSession(t, "s2")
	row := f.seedPending(t, "s1", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s2/approvals/"+row.ID+"/resolve",
		`{"decision":"approve","scope":"once"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	stored, err := f.coord.GetApproval(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if stored.Status != agentadapters.ApprovalStatusPending {
		t.Fatalf("status = %q, want pending", stored.Status)
	}
}

// ─── POST cancel ─────────────────────────────────────────────────────────────

func TestHTTPCancelApprovalHappyPath(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/cancel", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct{ Data agentApprovalItem }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Data.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", body.Data.Status)
	}
}

func TestHTTPCancelOnAlreadyResolvedReturns409(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	row := f.seedPending(t, "s", defaultAllowDenyOptions())
	first := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/resolve", `{"decision":"approve","scope":"once"}`)
	first.Body.Close()

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+row.ID+"/cancel", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHTTPCancelWrongSessionReturns404(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s1")
	f.seedSession(t, "s2")
	row := f.seedPending(t, "s1", defaultAllowDenyOptions())

	resp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s2/approvals/"+row.ID+"/cancel", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	stored, err := f.coord.GetApproval(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if stored.Status != agentadapters.ApprovalStatusPending {
		t.Fatalf("status = %q, want pending", stored.Status)
	}
}

// ─── Grants ──────────────────────────────────────────────────────────────────

func TestHTTPListGrants(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	now := time.Now().UTC()
	_, _ = f.store.CreateGrant(context.Background(), agentadapters.Grant{
		Scope: agentadapters.ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "file_write",
		Decision: agentadapters.ApprovalDecisionApprove, GrantedAt: now,
	})
	_, _ = f.store.CreateGrant(context.Background(), agentadapters.Grant{
		Scope: agentadapters.ApprovalScopeWorkspaceTool, AdapterID: "claude_code", ToolKind: "shell_exec",
		Workspace: "/tmp/w", Decision: agentadapters.ApprovalDecisionDeny, GrantedAt: now,
	})

	resp, err := http.Get(f.server.URL + "/v1/agent-chat/grants?adapter_id=codex")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct{ Data []agentGrantItem }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Data) != 1 || body.Data[0].AdapterID != "codex" {
		t.Fatalf("filter failed; got %+v", body.Data)
	}
}

func TestHTTPDeleteGrantHappyPath(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	g, _ := f.store.CreateGrant(context.Background(), agentadapters.Grant{
		Scope: agentadapters.ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "file_write",
		Decision: agentadapters.ApprovalDecisionApprove, GrantedAt: time.Now().UTC(),
	})

	req, _ := http.NewRequest(http.MethodDelete, f.server.URL+"/v1/agent-chat/grants/"+g.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	rest, _ := f.coord.ListGrants(context.Background(), agentadapters.GrantFilter{})
	if len(rest) != 0 {
		t.Fatalf("got %d grants after delete, want 0", len(rest))
	}
}

func TestHTTPDeleteGrantUnknownReturns404(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	req, _ := http.NewRequest(http.MethodDelete, f.server.URL+"/v1/agent-chat/grants/missing", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── End-to-end: prompt-mode block + HTTP resolve ────────────────────────────

func TestHTTPResolveWakesBlockedPromptModeRequest(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")

	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex", Workspace: "/tmp/w"},
			acp.RequestPermissionRequest{
				Options:  defaultAllowDenyOptions(),
				ToolCall: acp.ToolCallUpdate{ToolCallId: "c"},
			},
		)
		respCh <- resp
	}()

	// Wait for the pending row, then resolve via HTTP.
	deadline := time.Now().Add(2 * time.Second)
	var pending agentadapters.Approval
	for time.Now().Before(deadline) {
		rows, _ := f.store.ListApprovals(context.Background(), "s", agentadapters.ApprovalStatusPending)
		if len(rows) == 1 {
			pending = rows[0]
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("blocked RequestPermission never registered a pending row")
	}

	httpResp := postJSONApprovalEndpoint(t, f.server.URL+"/v1/agent-chat/sessions/s/approvals/"+pending.ID+"/resolve",
		`{"decision":"approve","scope":"once"}`)
	httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP resolve status = %d, want 200", httpResp.StatusCode)
	}

	select {
	case resp := <-respCh:
		if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow_once_id" {
			t.Fatalf("blocked goroutine got wrong outcome: %+v", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP resolve did not wake the blocked RequestPermission")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func postJSONApprovalEndpoint(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func readApprovalBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
