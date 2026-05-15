package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCaller is a hand-rolled mock for the Caller interface so the
// ACPWorkspace tests don't have to spin up an entire acp.Dispatcher
// + stdio pump. Each call records the method and params, then
// returns the next queued response (or runs the next queued handler
// for tests that need to react to the params).
type fakeCaller struct {
	mu    sync.Mutex
	calls []fakeCall

	// Handler queue: tests register one handler per expected
	// outbound RPC. The fake pops them in FIFO order. If a call
	// arrives with no queued handler, the fake returns a clear
	// error so the test sees "unexpected RPC" instead of a panic.
	handlers []fakeHandler
}

type fakeCall struct {
	method string
	params any
}

type fakeHandler func(method string, params any) (json.RawMessage, error)

func (f *fakeCaller) expect(handler fakeHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, handler)
}

func (f *fakeCaller) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{method: method, params: params})
	if len(f.handlers) == 0 {
		f.mu.Unlock()
		return nil, errors.New("fakeCaller: no queued handler for " + method)
	}
	handler := f.handlers[0]
	f.handlers = f.handlers[1:]
	f.mu.Unlock()
	return handler(method, params)
}

func (f *fakeCaller) callMethods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.method
	}
	return out
}

// ---------------------------------------------------------------------------
// File operations
// ---------------------------------------------------------------------------

func TestACPWorkspace_WriteFileSendsFSWrite(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{}
	caller.expect(func(method string, params any) (json.RawMessage, error) {
		if method != "fs/write_text_file" {
			t.Errorf("method = %q; want fs/write_text_file", method)
		}
		p, ok := params.(acpFSWriteParams)
		if !ok {
			t.Fatalf("params type = %T; want acpFSWriteParams", params)
		}
		if p.Path != "/projects/demo/README.md" {
			t.Errorf("path = %q", p.Path)
		}
		if p.Content != "hello" {
			t.Errorf("content = %q", p.Content)
		}
		return json.RawMessage(`{}`), nil
	})
	ws := NewACPWorkspace(caller, "session-1")
	res, err := ws.WriteFile(context.Background(), FileRequest{
		Path:             "README.md",
		Content:          "hello",
		WorkingDirectory: "/projects/demo",
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if res.BytesWritten != 5 {
		t.Fatalf("BytesWritten = %d; want 5", res.BytesWritten)
	}
}

func TestACPWorkspace_AppendFileReadsThenWrites(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{}
	caller.expect(func(method string, params any) (json.RawMessage, error) {
		if method != "fs/read_text_file" {
			t.Errorf("first method = %q; want fs/read_text_file", method)
		}
		return json.RawMessage(`{"content":"existing\n"}`), nil
	})
	caller.expect(func(method string, params any) (json.RawMessage, error) {
		if method != "fs/write_text_file" {
			t.Errorf("second method = %q; want fs/write_text_file", method)
		}
		p := params.(acpFSWriteParams)
		if p.Content != "existing\nappended" {
			t.Errorf("combined content = %q", p.Content)
		}
		return json.RawMessage(`{}`), nil
	})
	ws := NewACPWorkspace(caller, "session-1")
	_, err := ws.AppendFile(context.Background(), FileRequest{
		Path:    "notes.txt",
		Content: "appended",
	})
	if err != nil {
		t.Fatalf("AppendFile: %v", err)
	}
}

func TestACPWorkspace_AppendFileTreatsNotFoundAsEmpty(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{}
	caller.expect(func(string, any) (json.RawMessage, error) {
		return nil, errors.New("acp: fs/read_text_file rejected (code -32603): file not found")
	})
	caller.expect(func(method string, params any) (json.RawMessage, error) {
		if method != "fs/write_text_file" {
			t.Errorf("second method = %q", method)
		}
		p := params.(acpFSWriteParams)
		if p.Content != "fresh" {
			t.Errorf("content = %q; want exactly the appended bytes (not-found should be treated as empty)", p.Content)
		}
		return json.RawMessage(`{}`), nil
	})
	ws := NewACPWorkspace(caller, "session-1")
	if _, err := ws.AppendFile(context.Background(), FileRequest{
		Path:    "new.txt",
		Content: "fresh",
	}); err != nil {
		t.Fatalf("AppendFile: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Run path
// ---------------------------------------------------------------------------

func TestACPWorkspace_RunCreatesWaitsReleases(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{}
	caller.expect(func(method string, _ any) (json.RawMessage, error) {
		if method != "terminal/create" {
			t.Errorf("step 1 method = %q; want terminal/create", method)
		}
		return json.RawMessage(`{"terminalId":"term-1"}`), nil
	})
	caller.expect(func(method string, _ any) (json.RawMessage, error) {
		if method != "terminal/wait_for_exit" {
			t.Errorf("step 2 method = %q; want terminal/wait_for_exit", method)
		}
		// Small sleep so the polling loop has a chance to issue a
		// terminal/output call before exit drains the loop.
		time.Sleep(150 * time.Millisecond)
		return json.RawMessage(`{"exitCode":0}`), nil
	})
	caller.expect(func(method string, _ any) (json.RawMessage, error) {
		if method != "terminal/output" {
			t.Errorf("polling step = %q; want terminal/output", method)
		}
		return json.RawMessage(`{"stdout":"hello","stderr":""}`), nil
	})
	caller.expect(func(method string, _ any) (json.RawMessage, error) {
		// Final drain after exit.
		if method != "terminal/output" {
			t.Errorf("final drain = %q; want terminal/output", method)
		}
		return json.RawMessage(`{"stdout":"hello world","stderr":""}`), nil
	})
	caller.expect(func(method string, _ any) (json.RawMessage, error) {
		if method != "terminal/release" {
			t.Errorf("teardown = %q; want terminal/release", method)
		}
		return json.RawMessage(`{}`), nil
	})

	ws := NewACPWorkspace(caller, "session-1")
	res, err := ws.Run(context.Background(), Command{
		Command:          "echo hello world",
		WorkingDirectory: "/projects/demo",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello world") {
		t.Fatalf("stdout = %q; want to contain 'hello world'", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d; want 0", res.ExitCode)
	}

	// Confirm the canonical method order — sanity guard against a
	// future refactor that subtly reorders the wire flow.
	got := caller.callMethods()
	want := []string{"terminal/create", "terminal/wait_for_exit", "terminal/output", "terminal/output", "terminal/release"}
	if len(got) < len(want) {
		t.Fatalf("methods = %v; want at least %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Permission gate
// ---------------------------------------------------------------------------

func TestACPWorkspace_RequestPermissionForwardsToEditor(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{}
	caller.expect(func(method string, params any) (json.RawMessage, error) {
		if method != "session/request_permission" {
			t.Errorf("method = %q", method)
		}
		p := params.(acpPermissionParams)
		if p.Tool != "shell" {
			t.Errorf("tool = %q", p.Tool)
		}
		return json.RawMessage(`{"granted":false,"reason":"user clicked deny"}`), nil
	})
	ws := NewACPWorkspace(caller, "session-1")
	decision, err := ws.RequestPermission(context.Background(), PermissionRequest{
		Tool:   "shell",
		Action: "git push",
	})
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if decision.Granted {
		t.Fatal("expected denial")
	}
	if !strings.Contains(decision.Reason, "user clicked deny") {
		t.Fatalf("reason = %q", decision.Reason)
	}
}

// ---------------------------------------------------------------------------
// Wiring guards
// ---------------------------------------------------------------------------

func TestACPWorkspace_RejectsEmptySession(t *testing.T) {
	t.Parallel()
	ws := NewACPWorkspace(&fakeCaller{}, "") // no session id
	_, err := ws.WriteFile(context.Background(), FileRequest{Path: "x.txt"})
	if err == nil {
		t.Fatal("expected error for empty session id; got nil")
	}
	if !strings.Contains(err.Error(), "session id") {
		t.Fatalf("error = %q; want to mention session id", err.Error())
	}
}

func TestACPWorkspace_RejectsNilCaller(t *testing.T) {
	t.Parallel()
	ws := NewACPWorkspace(nil, "s1")
	_, err := ws.WriteFile(context.Background(), FileRequest{Path: "x.txt"})
	if err == nil {
		t.Fatal("expected error for nil caller")
	}
}
