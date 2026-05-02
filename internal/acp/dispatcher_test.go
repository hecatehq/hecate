package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeGateway struct {
	models    []string
	modelsErr error
}

func (f *fakeGateway) ListModels(_ context.Context) ([]string, error) {
	return f.models, f.modelsErr
}

func (f *fakeGateway) CreateAgentLoopTask(_ context.Context, _ CreateTaskRequest) (CreateTaskResult, error) {
	return CreateTaskResult{}, errors.New("not used in this test")
}

func (f *fakeGateway) ResumeTask(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not used")
}

func (f *fakeGateway) CancelRun(_ context.Context, _, _ string) error { return nil }

func (f *fakeGateway) ResolveApproval(_ context.Context, _, _, _ string, _ ApprovalDecision) error {
	return nil
}

func (f *fakeGateway) StreamRunEvents(_ context.Context, _, _ string) (<-chan RunEvent, error) {
	return nil, errors.New("not used")
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

func TestSessionStore_CreateAndGet(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("m", "/tmp")
	if sess.ID == "" {
		t.Fatal("empty ID")
	}
	if got := s.Get(sess.ID); got != sess {
		t.Errorf("Get returned %p, want %p", got, sess)
	}
	s.Delete(sess.ID)
	if got := s.Get(sess.ID); got != nil {
		t.Errorf("Get after Delete returned %p, want nil", got)
	}
}
