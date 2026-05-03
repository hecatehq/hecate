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
	cancelled    []string
	cancelErr    error
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

func (f *fakeGateway) CancelRun(_ context.Context, taskID, runID, reason string) error {
	f.cancelled = append(f.cancelled, taskID+"/"+runID+"/"+reason)
	return f.cancelErr
}

func (f *fakeGateway) ResolveApproval(_ context.Context, _, _, _ string, _ ApprovalDecision) error {
	return nil
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
