package chatapp

import (
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

func TestApplicationAuthorizeNativeSessionReplacement(t *testing.T) {
	t.Parallel()
	failed := chat.Message{
		Role:    "assistant",
		Status:  "failed",
		Content: "Claude Code error: prompt command failed",
		Error:   "Claude Code error: prompt command failed",
		Activities: []chat.Activity{
			{Type: "running", Status: "running"},
			{ID: "tool:prompt-command-1", Type: "tool_call", Kind: "execute", Status: "failed"},
			{Type: "failed", Status: "failed"},
		},
	}
	failed.RawOutput = `{"code":-32000,"message":"prompt command failed","data":{"error":"process command not found: claude"}}`
	cancelled := chat.Message{Role: "assistant", Status: "cancelled", RawOutput: "context canceled"}
	commandBridgeFailure := failed
	commandBridgeFailure.RawOutput = strings.Join([]string{
		`{"sessionId":"native_stale","update":{"sessionUpdate":"tool_call","toolCallId":"prompt-command-1","title":"Run claude","kind":"execute","status":"in_progress"}}`,
		`{"sessionId":"native_stale","update":{"sessionUpdate":"tool_call_update","toolCallId":"prompt-command-1","title":"Run claude","kind":"execute","status":"failed"}}`,
	}, "\n")
	withheldFailure := failed
	withheldFailure.RawOutput = "[ACP raw output withheld: private prompt inputs active]"

	tests := []struct {
		name    string
		session chat.Session
		want    bool
	}{
		{name: "fresh prepared session", session: chat.Session{NativeSessionID: "native_stale"}, want: true},
		{name: "user turn", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "user", Content: "prompt"}}}, want: true},
		{name: "failed pre-output turns", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{failed, failed}}, want: true},
		{name: "command bridge lifecycle", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{commandBridgeFailure}}, want: true},
		{name: "withheld command bridge lifecycle", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{withheldFailure}}, want: true},
		{name: "empty cancellation", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{cancelled}}, want: true},
		{name: "missing native id", session: chat.Session{}, want: false},
		{name: "unknown role", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "tool"}}}, want: false},
		{name: "empty role", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Content: "legacy"}}}, want: false},
		{name: "compacted history", session: chat.Session{NativeSessionID: "native_stale", ContextSummary: chat.ContextSummary{Content: "history"}}, want: false},
		{name: "completed assistant", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "completed"}}}, want: false},
		{name: "legacy assistant status", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant"}}}, want: false},
		{name: "failed without error", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure"}}}, want: false},
		{name: "failed after output", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "partial\n\nfailure", Error: "failure"}}}, want: false},
		{name: "failed with provider raw output", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", RawOutput: `{"method":"session/update"}`}}}, want: false},
		{name: "failed with unrelated rpc error", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", RawOutput: `{"code":-32603,"message":"Internal error","data":{"error":"provider failed"}}`}}}, want: false},
		{name: "process command missing path", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{messageWithRaw(failed, `{"code":-32000,"message":"prompt command failed","data":{"error":"process command not found: /usr/bin/claude"}}`)}}, want: false},
		{name: "process command missing whitespace", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{messageWithRaw(failed, `{"code":-32000,"message":"prompt command failed","data":{"error":"process command not found: claude code"}}`)}}, want: false},
		{name: "process command missing empty command", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{messageWithRaw(failed, `{"code":-32000,"message":"prompt command failed","data":{"error":"process command not found: "}}`)}}, want: false},
		{name: "process command missing wrong detail", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{messageWithRaw(failed, `{"code":-32000,"message":"prompt command failed","data":{"error":"claude"}}`)}}, want: false},
		{name: "process command missing wrong code", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{messageWithRaw(failed, `{"code":-32603,"message":"prompt command failed","data":{"error":"process command not found: claude"}}`)}}, want: false},
		{name: "process command missing wrong message", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{messageWithRaw(failed, `{"code":-32000,"message":"Internal error","data":{"error":"process command not found: claude"}}`)}}, want: false},
		{name: "failed with diff", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", Diff: "patch"}}}, want: false},
		{name: "completed tool", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", Activities: []chat.Activity{{Type: "tool_call", Status: "completed"}}}}}, want: false},
		{name: "unknown tool status", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", RawOutput: commandBridgeFailure.RawOutput, Activities: []chat.Activity{{ID: "tool:prompt-command-1", Type: "tool_call", Kind: "execute", Status: "succeeded"}}}}}, want: false},
		{name: "empty tool status", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", RawOutput: commandBridgeFailure.RawOutput, Activities: []chat.Activity{{ID: "tool:prompt-command-1", Type: "tool_call", Kind: "execute"}}}}}, want: false},
		{name: "unknown activity", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "failed", Content: "failure", Error: "failure", Activities: []chat.Activity{{Type: "mystery"}}}}}, want: false},
		{name: "cancelled after output", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "cancelled", Content: "partial"}}}, want: false},
		{name: "cancelled with provider raw output", session: chat.Session{NativeSessionID: "native_stale", Messages: []chat.Message{{Role: "assistant", Status: "cancelled", RawOutput: `{"method":"session/update"}`}}}, want: false},
	}
	app := &Application{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := app.AuthorizeNativeSessionReplacement(test.session); got != test.want {
				t.Fatalf("AuthorizeNativeSessionReplacement() = %v, want %v", got, test.want)
			}
		})
	}
}

func messageWithRaw(message chat.Message, raw string) chat.Message {
	message.RawOutput = raw
	return message
}
