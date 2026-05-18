package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeGateway struct {
	models       []string
	modelsErr    error
	createReqs   []CreateTaskRequest
	createErr    error
	continued    []string
	continueErr  error
	cancelled    []string
	cancelErr    error
	resolved     []string
	resolveErr   error
	streamEvents chan RunEvent
}

func (f *fakeGateway) ListModels(_ context.Context) ([]string, error) {
	return f.models, f.modelsErr
}

func (f *fakeGateway) CreateAgentLoopTask(_ context.Context, req CreateTaskRequest) (CreateTaskResult, error) {
	f.createReqs = append(f.createReqs, req)
	if f.createErr != nil {
		return CreateTaskResult{}, f.createErr
	}
	return CreateTaskResult{TaskID: "task-1", RunID: "run-1"}, nil
}

func (f *fakeGateway) ContinueAgentLoopTask(_ context.Context, taskID, runID, prompt string) (string, error) {
	f.continued = append(f.continued, taskID+"/"+runID+"/"+prompt)
	if f.continueErr != nil {
		return "", f.continueErr
	}
	return "run-2", nil
}

func (f *fakeGateway) CancelRun(_ context.Context, taskID, runID, reason string) error {
	f.cancelled = append(f.cancelled, taskID+"/"+runID+"/"+reason)
	return f.cancelErr
}

func (f *fakeGateway) ResolveApproval(_ context.Context, taskID, runID, approvalID string, decision ApprovalDecision) error {
	f.resolved = append(f.resolved, taskID+"/"+runID+"/"+approvalID+"/"+string(decision))
	return f.resolveErr
}

func (f *fakeGateway) StreamRunEvents(_ context.Context, _, _ string) (<-chan RunEvent, error) {
	if f.streamEvents == nil {
		ch := make(chan RunEvent)
		close(ch)
		return ch, nil
	}
	return f.streamEvents, nil
}

func makeID(t *testing.T, id int) *json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal id: %v", err)
	}
	rm := json.RawMessage(raw)
	return &rm
}

func initParams(t *testing.T, perms bool) json.RawMessage {
	t.Helper()
	p := InitializeParams{ProtocolVersion: DeclaredProtocolVersion}
	if perms {
		p.ClientCaps.Permissions = &PermissionCapability{}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func initParamsWithTerminalAuth(t *testing.T) json.RawMessage {
	t.Helper()
	p := InitializeParams{ProtocolVersion: DeclaredProtocolVersion}
	p.ClientCaps.Permissions = &PermissionCapability{}
	p.ClientCaps.Auth = &AuthCapabilities{Terminal: true}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestInitialize_Happy(t *testing.T) {
	gw := &fakeGateway{models: []string{"claude-sonnet-4-5", "gpt-4o"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{AgentName: "Hecate", AgentVersion: "v0.42.0", ApprovalRoute: "editor"})

	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true),
	})
	if resp == nil {
		t.Fatal("Handle returned nil")
	}
	if resp.Error != nil {
		t.Fatalf("Error: %d %q", resp.Error.Code, resp.Error.Message)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.ProtocolVersion != DeclaredProtocolVersion {
		t.Errorf("protocolVersion = %q", result.ProtocolVersion)
	}
	if len(result.AvailableModels) != 2 {
		t.Errorf("models len = %d, want 2", len(result.AvailableModels))
	}
	if !result.AgentCaps.Permissions {
		t.Error("agentCaps.Permissions = false, want true with ApprovalRoute=editor")
	}
}

func TestInitialize_AdvertisesTerminalAuthWhenSupported(t *testing.T) {
	gw := &fakeGateway{models: []string{"claude-sonnet-4-5"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{AgentName: "Hecate", AgentVersion: "v0.42.0", ApprovalRoute: "editor"})

	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParamsWithTerminalAuth(t),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.AuthMethods) != 1 {
		t.Fatalf("authMethods len = %d, want 1", len(result.AuthMethods))
	}
	method := result.AuthMethods[0]
	if method.ID != "hecate-setup" || method.Type != "terminal" || strings.Join(method.Args, " ") != "auth setup" {
		t.Fatalf("auth method = %+v", method)
	}
}

func TestInitialize_OmitsTerminalAuthWhenUnsupported(t *testing.T) {
	gw := &fakeGateway{models: []string{"claude-sonnet-4-5"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{AgentName: "Hecate", AgentVersion: "v0.42.0", ApprovalRoute: "editor"})

	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.AuthMethods) != 0 {
		t.Fatalf("authMethods = %+v, want omitted", result.AuthMethods)
	}
}

func TestInitialize_NoPermissions(t *testing.T) {
	gw := &fakeGateway{models: []string{"x"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, false),
	})
	if resp.Error == nil || resp.Error.Code != ErrorInvalidRequest {
		t.Fatalf("expected ErrorInvalidRequest, got %v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "permissions") {
		t.Errorf("message = %q", resp.Error.Message)
	}
}

func TestInitialize_NoPermissionsAllowedWhenApprovalsStayWithOperator(t *testing.T) {
	gw := &fakeGateway{models: []string{"x"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "operator"})
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, false),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.AgentCaps.Permissions {
		t.Fatal("agentCaps.Permissions = true, want false when ApprovalRoute=operator")
	}
}

func TestInitialize_GatewayUnreachable(t *testing.T) {
	gw := &fakeGateway{modelsErr: errors.New("connection refused")}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true),
	})
	if resp.Error == nil || resp.Error.Code != ErrorGatewayUnreachable {
		t.Fatalf("expected ErrorGatewayUnreachable, got %v", resp.Error)
	}
}

func TestInitialize_EditorOwnedModeRequiresClientCaps(t *testing.T) {
	gw := &fakeGateway{models: []string{"m"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{
		ApprovalRoute: "editor",
		WorkspaceMode: WorkspaceModeEditorOwned,
	})
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true), // permissions only — no fs/terminal
	})
	if resp.Error == nil || resp.Error.Code != ErrorInvalidRequest {
		t.Fatalf("expected ErrorInvalidRequest, got %v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "editor-owned") {
		t.Fatalf("message = %q; want to mention editor-owned", resp.Error.Message)
	}
	if d.WorkspaceMode() != WorkspaceModeEditorOwned {
		t.Fatalf("WorkspaceMode() = %q after failed init; want the configured value to remain (%q)", d.WorkspaceMode(), WorkspaceModeEditorOwned)
	}
}

func TestInitialize_DoesNotCommitWorkspaceModeWhenGatewayUnreachable(t *testing.T) {
	// ResolveWorkspaceMode would succeed (auto + permissions-only caps
	// → hecate-owned), but ListModels fails. WorkspaceMode() must
	// still report the configured value, not the value that was almost
	// negotiated — initialize did not succeed.
	gw := &fakeGateway{modelsErr: errors.New("connection refused")}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor", WorkspaceMode: WorkspaceModeHecateOwned})
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true),
	})
	if resp.Error == nil || resp.Error.Code != ErrorGatewayUnreachable {
		t.Fatalf("expected ErrorGatewayUnreachable, got %v", resp.Error)
	}
	if d.WorkspaceMode() != WorkspaceModeHecateOwned {
		t.Fatalf("WorkspaceMode() = %q; want configured value %q after failed init", d.WorkspaceMode(), WorkspaceModeHecateOwned)
	}
}

func TestInitialize_AutoFallsBackToHecateOwnedWhenCapsMissing(t *testing.T) {
	gw := &fakeGateway{models: []string{"m"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"}) // WorkspaceMode empty → auto
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	if d.WorkspaceMode() != WorkspaceModeHecateOwned {
		t.Fatalf("WorkspaceMode() = %q; want %q after auto with no fs/terminal caps", d.WorkspaceMode(), WorkspaceModeHecateOwned)
	}
}

func TestInitialize_AutoChoosesEditorOwnedWhenCapsPresent(t *testing.T) {
	gw := &fakeGateway{models: []string{"m"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	params := InitializeParams{
		ProtocolVersion: DeclaredProtocolVersion,
		ClientCaps: ClientCapabilities{
			Permissions: &PermissionCapability{},
			FS:          &FSCapability{ReadTextFile: true, WriteTextFile: true},
			Terminal:    &TerminalCapability{},
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  raw,
	})
	if resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	if d.WorkspaceMode() != WorkspaceModeEditorOwned {
		t.Fatalf("WorkspaceMode() = %q; want %q with full client caps", d.WorkspaceMode(), WorkspaceModeEditorOwned)
	}
}

func TestInitialize_TwiceRejects(t *testing.T) {
	gw := &fakeGateway{models: []string{}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	req := &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  MethodInitialize,
		Params:  initParams(t, true),
	}
	if r := d.Handle(context.Background(), req); r.Error != nil {
		t.Fatalf("first init unexpectedly errored: %v", r.Error)
	}
	resp := d.Handle(context.Background(), req)
	if resp.Error == nil || resp.Error.Code != ErrorInvalidRequest {
		t.Fatalf("expected error on second init, got %v", resp.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	d := NewDispatcher(&fakeGateway{}, NewSessionStore(), Config{ApprovalRoute: "editor"})
	resp := d.Handle(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		ID:      makeID(t, 1),
		Method:  "x/some-future",
	})
	if resp.Error == nil || resp.Error.Code != ErrorMethodNotFound {
		t.Fatalf("expected ErrorMethodNotFound, got %v", resp.Error)
	}
}

func TestHandleNilRequestReturnsInvalidRequest(t *testing.T) {
	d := NewDispatcher(&fakeGateway{}, NewSessionStore(), Config{ApprovalRoute: "editor"})
	resp := d.Handle(context.Background(), nil)
	if resp == nil {
		t.Fatal("Handle(nil) returned nil")
	}
	if resp.Error == nil || resp.Error.Code != ErrorInvalidRequest {
		t.Fatalf("expected ErrorInvalidRequest, got %v", resp.Error)
	}
	if resp.ID != nil {
		t.Fatalf("response ID = %s, want nil for invalid request", string(*resp.ID))
	}
}

func TestNotificationDropped(t *testing.T) {
	d := NewDispatcher(&fakeGateway{}, NewSessionStore(), Config{ApprovalRoute: "editor"})
	if r := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, Method: "x"}); r != nil {
		t.Errorf("non-nil response for notification: %+v", r)
	}
}

func TestSessionNewAndPromptStartsTask(t *testing.T) {
	gw := &fakeGateway{models: []string{"gpt-4o-mini"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	updates := make(chan *Request, 4)
	d.SetEmitter(func(req *Request) { updates <- req })
	if resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 1), Method: MethodInitialize, Params: initParams(t, true)}); resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	newParams := json.RawMessage(`{"model":"gpt-4o-mini","cwd":"/repo"}`)
	resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 2), Method: MethodSessionNew, Params: newParams})
	if resp.Error != nil {
		t.Fatalf("session/new error = %v", resp.Error)
	}
	var created SessionNewResult
	if err := json.Unmarshal(resp.Result, &created); err != nil {
		t.Fatal(err)
	}
	promptRaw, _ := json.Marshal(SessionPromptParams{SessionID: created.SessionID, Prompt: "fix tests"})
	resp = d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 3), Method: MethodSessionPrompt, Params: promptRaw})
	if resp.Error != nil {
		t.Fatalf("session/prompt error = %v", resp.Error)
	}
	if len(gw.createReqs) != 1 {
		t.Fatalf("CreateAgentLoopTask calls = %d, want 1", len(gw.createReqs))
	}
	if got := gw.createReqs[0]; got.Model != "gpt-4o-mini" || got.WorkingDirectory != "/repo" || got.Prompt != "fix tests" {
		t.Fatalf("CreateAgentLoopTask req = %+v", got)
	}
	select {
	case update := <-updates:
		if update.Method != MethodSessionUpdate {
			t.Fatalf("update method = %q", update.Method)
		}
	default:
		t.Fatal("missing session/update notification")
	}
}

func TestSessionPromptContinuesExistingTask(t *testing.T) {
	gw := &fakeGateway{models: []string{"gpt-4o-mini"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	if resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 1), Method: MethodInitialize, Params: initParams(t, true)}); resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 2), Method: MethodSessionNew, Params: json.RawMessage(`{"model":"gpt-4o-mini","cwd":"/repo"}`)})
	if resp.Error != nil {
		t.Fatalf("session/new error = %v", resp.Error)
	}
	var created SessionNewResult
	if err := json.Unmarshal(resp.Result, &created); err != nil {
		t.Fatal(err)
	}
	firstPrompt, _ := json.Marshal(SessionPromptParams{SessionID: created.SessionID, Prompt: "first"})
	if resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 3), Method: MethodSessionPrompt, Params: firstPrompt}); resp.Error != nil {
		t.Fatalf("first session/prompt error = %v", resp.Error)
	}
	secondPrompt, _ := json.Marshal(SessionPromptParams{SessionID: created.SessionID, Prompt: "second"})
	resp = d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 4), Method: MethodSessionPrompt, Params: secondPrompt})
	if resp.Error != nil {
		t.Fatalf("second session/prompt error = %v", resp.Error)
	}
	if len(gw.createReqs) != 1 {
		t.Fatalf("CreateAgentLoopTask calls = %d, want 1", len(gw.createReqs))
	}
	if strings.Join(gw.continued, ",") != "task-1/run-1/second" {
		t.Fatalf("continued = %#v", gw.continued)
	}
	var prompted SessionPromptResult
	if err := json.Unmarshal(resp.Result, &prompted); err != nil {
		t.Fatal(err)
	}
	if prompted.TaskID != "task-1" || prompted.RunID != "run-2" {
		t.Fatalf("prompt result = %+v, want same task with new run", prompted)
	}
}

func TestSessionCancelCancelsCurrentRun(t *testing.T) {
	gw := &fakeGateway{models: []string{"gpt-4o-mini"}}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	if resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 1), Method: MethodInitialize, Params: initParams(t, true)}); resp.Error != nil {
		t.Fatalf("initialize error = %v", resp.Error)
	}
	resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 2), Method: MethodSessionNew, Params: json.RawMessage(`{}`)})
	if resp.Error != nil {
		t.Fatalf("session/new error = %v", resp.Error)
	}
	var created SessionNewResult
	if err := json.Unmarshal(resp.Result, &created); err != nil {
		t.Fatal(err)
	}
	promptRaw, _ := json.Marshal(SessionPromptParams{SessionID: created.SessionID, Prompt: "run"})
	if resp := d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 3), Method: MethodSessionPrompt, Params: promptRaw}); resp.Error != nil {
		t.Fatalf("session/prompt error = %v", resp.Error)
	}
	cancelRaw, _ := json.Marshal(SessionCancelParams{SessionID: created.SessionID, Reason: "user"})
	resp = d.Handle(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: makeID(t, 4), Method: MethodSessionCancel, Params: cancelRaw})
	if resp.Error != nil {
		t.Fatalf("session/cancel error = %v", resp.Error)
	}
	if strings.Join(gw.cancelled, ",") != "task-1/run-1/user" {
		t.Fatalf("cancelled = %#v", gw.cancelled)
	}
}

func TestRunEventToSessionUpdateMapsTerminalFailure(t *testing.T) {
	update := RunEventToSessionUpdate("s", "t", "r", RunEvent{
		Type: "run.failed",
		Data: []byte(`{"error":"boom"}`),
	})
	if update.Kind != "error" || update.Status != "failed" || !update.Terminal || update.Message != "boom" {
		t.Fatalf("update = %+v", update)
	}
}

func TestRunEventToSessionUpdateMapsAssistantTextEvents(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      string
		wantMsg   string
	}{
		{
			name:      "delta",
			eventType: "assistant.text_delta",
			data:      `{"delta":"hello"}`,
			wantMsg:   "hello",
		},
		{
			name:      "complete",
			eventType: "assistant.text_complete",
			data:      `{"text":"hello world"}`,
			wantMsg:   "hello world",
		},
		{
			name:      "final answer",
			eventType: "assistant.final_answer",
			data:      `{"summary":"done"}`,
			wantMsg:   "done",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := RunEventToSessionUpdate("s", "t", "r", RunEvent{
				Type: tt.eventType,
				Data: []byte(tt.data),
			})
			if update.Kind != "text" {
				t.Fatalf("Kind = %q, want text", update.Kind)
			}
			if update.Message != tt.wantMsg {
				t.Fatalf("Message = %q, want %q", update.Message, tt.wantMsg)
			}
		})
	}
}

func TestRunEventToSessionUpdateUsesToolMetadataMessage(t *testing.T) {
	update := RunEventToSessionUpdate("s", "t", "r", RunEvent{
		Type: "tool.started",
		Data: []byte(`{"tool_name":"shell_exec","status":"running"}`),
	})
	if update.Kind != "tool_call" {
		t.Fatalf("Kind = %q, want tool_call", update.Kind)
	}
	if update.Message != "shell_exec" {
		t.Fatalf("Message = %q, want shell_exec", update.Message)
	}
}

func TestDispatcherPermissionResponseResolvesGatewayApproval(t *testing.T) {
	gw := &fakeGateway{}
	d := NewDispatcher(gw, NewSessionStore(), Config{ApprovalRoute: "editor"})
	d.pendingPermissions["permission-1"] = pendingPermission{
		SessionID:  "session-1",
		TaskID:     "task-1",
		RunID:      "run-1",
		ApprovalID: "approval-1",
	}
	id := json.RawMessage(`"permission-1"`)
	result := json.RawMessage(`{"decision":"allow","note":"ok"}`)

	d.HandleResponse(context.Background(), &Response{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Result:  result,
	})

	if strings.Join(gw.resolved, ",") != "task-1/run-1/approval-1/allow" {
		t.Fatalf("resolved = %#v", gw.resolved)
	}
}

func TestPendingPermissionFromSessionUpdate(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "snapshot approvals array",
			data: `{"event_type":"approval.requested","approvals":[{"id":"approval-1","kind":"shell_command","reason":"run command?","status":"pending"}]}`,
		},
		{
			name: "direct approval event",
			data: `{"approval_id":"approval-1","kind":"shell_command","status":"pending","policy_reason":"run command?"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := RunEventToSessionUpdate("session-1", "task-1", "run-1", RunEvent{
				Type: "approval.requested",
				Data: []byte(tt.data),
			})
			params, ok := PendingPermissionFromSessionUpdate(update)
			if !ok {
				t.Fatal("PendingPermissionFromSessionUpdate returned false")
			}
			if params.SessionID != "session-1" || params.TaskID != "task-1" || params.RunID != "run-1" || params.ApprovalID != "approval-1" {
				t.Fatalf("params = %+v", params)
			}
			if params.Kind != "shell_command" || params.Message != "run command?" {
				t.Fatalf("params = %+v", params)
			}
		})
	}
}

func TestDispatcherDeduplicatesPermissionRequests(t *testing.T) {
	d := NewDispatcher(&fakeGateway{}, NewSessionStore(), Config{ApprovalRoute: "editor"})
	update := SessionUpdateParams{
		SessionID: "session-1",
		TaskID:    "task-1",
		RunID:     "run-1",
		EventType: "approval.requested",
		Data: map[string]any{
			"approvals": []map[string]any{
				{"id": "approval-1", "kind": "shell_command", "reason": "run command?", "status": "pending"},
			},
		},
	}

	firstID, first := d.trackPendingPermission(mustPendingPermission(t, update))
	if !first || firstID == "" {
		t.Fatalf("first track = (%q, %v), want new id", firstID, first)
	}
	if _, ok := d.takePendingPermission(firstID); !ok {
		t.Fatalf("takePendingPermission(%q) = false", firstID)
	}
	secondID, second := d.trackPendingPermission(mustPendingPermission(t, update))
	if second || secondID != firstID {
		t.Fatalf("second track = (%q, %v), want existing id and no emit", secondID, second)
	}
}

func mustPendingPermission(t *testing.T, update SessionUpdateParams) PermissionRequestParams {
	t.Helper()
	params, ok := PendingPermissionFromSessionUpdate(update)
	if !ok {
		t.Fatal("expected pending permission")
	}
	return params
}

func TestSessionStore_CreateAndGet(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("m", "/tmp")
	if sess.ID == "" {
		t.Fatal("empty ID")
	}
	if got := s.Get(sess.ID); got == nil || got.ID != sess.ID || got.Model != "m" || got.CWD != "/tmp" {
		t.Errorf("Get returned %+v, want session %+v", got, sess)
	}
	s.Delete(sess.ID)
	if got := s.Get(sess.ID); got != nil {
		t.Errorf("Get after Delete returned %p, want nil", got)
	}
}
