package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/sandbox"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

func TestCommandTimeoutDefaultsTo5000ms(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want int
	}{
		{"zero falls back", 0, 5000},
		{"negative falls back", -1, 5000},
		{"positive passes through", 1234, 1234},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := commandTimeout(types.Task{TimeoutMS: tc.in})
			if got != tc.want {
				t.Errorf("commandTimeout(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestCommandWorkingDirectoryDefaultsToDot(t *testing.T) {
	if got := commandWorkingDirectory(types.Task{}); got != "." {
		t.Errorf("commandWorkingDirectory(empty) = %q, want %q", got, ".")
	}
	if got := commandWorkingDirectory(types.Task{WorkingDirectory: "/srv/work"}); got != "/srv/work" {
		t.Errorf("commandWorkingDirectory(/srv/work) = %q, want %q", got, "/srv/work")
	}
}

func TestFileOperationDefaultsToWrite(t *testing.T) {
	if got := fileOperation(types.Task{}); got != "write" {
		t.Errorf("fileOperation(empty) = %q, want write", got)
	}
	if got := fileOperation(types.Task{FileOperation: "append"}); got != "append" {
		t.Errorf("fileOperation(append) = %q, want append", got)
	}
}

func TestExecutionStatusFromError(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantStatus     string
		wantResult     string
		wantOTelStatus string
	}{
		{"nil → completed", nil, "completed", telemetry.ResultSuccess, "ok"},
		{"context.Canceled → cancelled", context.Canceled, "cancelled", telemetry.ResultError, "error"},
		{"other error → failed", errors.New("kaboom"), "failed", telemetry.ResultError, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, result, _, otelCode, _ := executionStatus(tc.err)
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if result != tc.wantResult {
				t.Errorf("result = %q, want %q", result, tc.wantResult)
			}
			if otelCode != tc.wantOTelStatus {
				t.Errorf("otelStatusCode = %q, want %q", otelCode, tc.wantOTelStatus)
			}
		})
	}
}

func TestCommandErrorKindClassification(t *testing.T) {
	cases := []struct {
		name             string
		err              error
		timeoutErrorKind string
		defaultErrorKind string
		want             string
	}{
		{"nil → empty", nil, "timeout", "default", ""},
		{"cancelled → run_cancelled", context.Canceled, "timeout", "default", "run_cancelled"},
		{"policy denied wins over default", &sandbox.PolicyError{Reason: "no exec"}, "timeout", "default", "sandbox_policy_denied"},
		{"deadline exceeded → caller-supplied timeout kind", context.DeadlineExceeded, "shell_timeout", "default", "shell_timeout"},
		{"other error → caller-supplied default kind", errors.New("other"), "timeout", "shell_failed", "shell_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := commandErrorKind(tc.err, tc.timeoutErrorKind, tc.defaultErrorKind)
			if got != tc.want {
				t.Errorf("commandErrorKind(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestFileErrorKindClassification(t *testing.T) {
	if got := fileErrorKind(nil); got != "" {
		t.Errorf("fileErrorKind(nil) = %q, want empty", got)
	}
	if got := fileErrorKind(&sandbox.PolicyError{Reason: "no write"}); got != "sandbox_policy_denied" {
		t.Errorf("fileErrorKind(policy) = %q, want sandbox_policy_denied", got)
	}
	if got := fileErrorKind(errors.New("other")); got != "file_operation_failed" {
		t.Errorf("fileErrorKind(other) = %q, want file_operation_failed", got)
	}
}

func TestFileExecutorCreatesPatchArtifactAndEvent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var events []capturedRunEvent
	exec := NewFileExecutor(sandbox.NewLocalExecutor())
	result, err := exec.Execute(context.Background(), ExecutionSpec{
		Task: types.Task{
			ID:                 "task-1",
			ExecutionKind:      "file",
			FileOperation:      "write",
			FilePath:           "note.txt",
			FileContent:        "new\n",
			WorkingDirectory:   dir,
			SandboxAllowedRoot: dir,
		},
		Run:       types.TaskRun{ID: "run-1"},
		StartedAt: time.Now().UTC(),
		NewID:     deterministicIDGenerator(),
		EmitRunEvent: func(eventType string, data map[string]any) {
			events = append(events, capturedRunEvent{eventType: eventType, data: data})
		},
		ToolCallID: "call-file-1",
		ToolName:   "file_write",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	var patch types.TaskArtifact
	for _, artifact := range result.Artifacts {
		if artifact.Kind == "patch" {
			patch = artifact
			break
		}
	}
	if patch.ID == "" {
		t.Fatalf("patch artifact missing; artifacts=%+v", result.Artifacts)
	}
	if patch.MimeType != "text/x-diff" {
		t.Fatalf("patch mime = %q, want text/x-diff", patch.MimeType)
	}
	if patch.Status != "applied" {
		t.Fatalf("patch status = %q, want applied", patch.Status)
	}
	for _, want := range []string{"--- a/note.txt", "+++ b/note.txt", "-old", "+new"} {
		if !strings.Contains(patch.ContentText, want) {
			t.Fatalf("patch missing %q:\n%s", want, patch.ContentText)
		}
	}
	if patch.SHA256 == "" {
		t.Fatal("patch SHA256 is empty")
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %+v", len(events), events)
	}
	if events[0].eventType != "tool.file.patch" {
		t.Fatalf("event type = %q, want tool.file.patch", events[0].eventType)
	}
	if got := events[0].data["artifact_id"]; got != patch.ID {
		t.Fatalf("artifact_id = %v, want %s", got, patch.ID)
	}
	if got := events[0].data["tool_call_id"]; got != "call-file-1" {
		t.Fatalf("tool_call_id = %v, want call-file-1", got)
	}
	if got := events[0].data["before_existed"]; got != true {
		t.Fatalf("before_existed = %v, want true", got)
	}
}

func TestTaskPolicyMaterializesSandboxFields(t *testing.T) {
	spec := ExecutionSpec{
		Task: types.Task{
			SandboxAllowedRoot: "/srv/run",
			SandboxReadOnly:    true,
			SandboxNetwork:     true,
		},
		ShellNetworkAllowedHosts:    []string{"github.com"},
		ShellNetworkAllowPrivateIPs: true,
	}
	got := taskPolicy(spec)
	if got.AllowedRoot != "/srv/run" {
		t.Errorf("AllowedRoot = %q, want /srv/run", got.AllowedRoot)
	}
	if !got.ReadOnly {
		t.Error("ReadOnly should be true")
	}
	if !got.Network {
		t.Error("Network should be true")
	}
	// The shell-network refinement fields flow through from the
	// runner's ShellNetworkPolicy via ExecutionSpec.
	if len(got.AllowedHosts) != 1 || got.AllowedHosts[0] != "github.com" {
		t.Errorf("AllowedHosts = %v, want [github.com]", got.AllowedHosts)
	}
	if !got.AllowPrivateIPs {
		t.Error("AllowPrivateIPs should be true")
	}
}

func TestShellExecutorEmitsTypedEventProtocolShellEvents(t *testing.T) {
	exec := &fakeStreamingSandbox{
		result: sandbox.Result{Stdout: "hello\n", Stderr: "warn\n", ExitCode: 0},
		chunks: []sandbox.OutputChunk{
			{Stream: "stdout", Text: "hello\n"},
			{Stream: "stderr", Text: "warn\n"},
		},
	}
	var events []capturedRunEvent
	shell := NewShellExecutor(exec)
	result, err := shell.Execute(context.Background(), ExecutionSpec{
		Task: types.Task{
			ID:               "task-1",
			ExecutionKind:    "shell",
			ShellCommand:     "printf hello",
			WorkingDirectory: ".",
			TimeoutMS:        1000,
		},
		Run:       types.TaskRun{ID: "run-1"},
		StartedAt: time.Now().UTC(),
		NewID:     deterministicIDGenerator(),
		EmitRunEvent: func(eventType string, data map[string]any) {
			events = append(events, capturedRunEvent{eventType: eventType, data: data})
		},
		ToolCallID: "call-shell-1",
		ToolName:   "shell_exec",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	wantTypes := []string{
		"tool.invoked",
		"tool.started",
		"tool.shell.command",
		"tool.shell.output_chunk",
		"tool.shell.output_chunk",
		"tool.shell.exited",
		"tool.completed",
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].eventType != want {
			t.Fatalf("events[%d] = %q, want %q", i, events[i].eventType, want)
		}
	}
	if got := events[0].data["tool_call_id"]; got != "call-shell-1" {
		t.Fatalf("tool_call_id = %v, want call-shell-1", got)
	}
	if got := events[0].data["tool_name"]; got != "shell_exec" {
		t.Fatalf("tool_name = %v, want shell_exec", got)
	}
	if got := events[3].data["byte_offset"]; got != 0 {
		t.Fatalf("first byte_offset = %v, want 0", got)
	}
	if got := events[5].data["exit_code"]; got != 0 {
		t.Fatalf("exit_code = %v, want 0", got)
	}
}

func TestShellExecutorEmitsTypedTimeoutEvent(t *testing.T) {
	exec := &fakeStreamingSandbox{
		result: sandbox.Result{Stdout: "partial", ExitCode: -1},
		chunks: []sandbox.OutputChunk{{Stream: "stdout", Text: "partial"}},
		err:    context.DeadlineExceeded,
	}
	var events []capturedRunEvent
	shell := NewShellExecutor(exec)
	result, err := shell.Execute(context.Background(), ExecutionSpec{
		Task: types.Task{
			ID:               "task-1",
			ExecutionKind:    "shell",
			ShellCommand:     "sleep 10",
			WorkingDirectory: ".",
			TimeoutMS:        250,
		},
		Run:       types.TaskRun{ID: "run-1"},
		StartedAt: time.Now().UTC(),
		NewID:     deterministicIDGenerator(),
		EmitRunEvent: func(eventType string, data map[string]any) {
			events = append(events, capturedRunEvent{eventType: eventType, data: data})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	last := events[len(events)-1]
	if last.eventType != "tool.timed_out" {
		t.Fatalf("last event = %q, want tool.timed_out", last.eventType)
	}
	if got := last.data["after_ms"]; got != 250 {
		t.Fatalf("after_ms = %v, want 250", got)
	}
}

type capturedRunEvent struct {
	eventType string
	data      map[string]any
}

type fakeStreamingSandbox struct {
	result sandbox.Result
	chunks []sandbox.OutputChunk
	err    error
}

func (f *fakeStreamingSandbox) Run(ctx context.Context, command sandbox.Command) (sandbox.Result, error) {
	return f.RunStreaming(ctx, command, nil)
}

func (f *fakeStreamingSandbox) RunStreaming(_ context.Context, _ sandbox.Command, onChunk func(sandbox.OutputChunk)) (sandbox.Result, error) {
	for _, chunk := range f.chunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	return f.result, f.err
}

func (f *fakeStreamingSandbox) WriteFile(_ context.Context, _ sandbox.FileRequest) (sandbox.FileResult, error) {
	return sandbox.FileResult{}, nil
}

func (f *fakeStreamingSandbox) AppendFile(_ context.Context, _ sandbox.FileRequest) (sandbox.FileResult, error) {
	return sandbox.FileResult{}, nil
}

func deterministicIDGenerator() func(string) string {
	counters := make(map[string]int)
	return func(prefix string) string {
		counters[prefix]++
		return fmt.Sprintf("%s-test-%d", prefix, counters[prefix])
	}
}
