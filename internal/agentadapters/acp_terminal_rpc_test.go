package agentadapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestAcpChatClientTerminalRPCsDisabledByDefault(t *testing.T) {
	t.Parallel()

	client := &acpChatClient{
		sessionID: "chat_test",
		adapterID: "codex",
		workspace: t.TempDir(),
	}
	_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{Command: "true"})
	if err == nil {
		t.Fatal("CreateTerminal succeeded while terminal support disabled; want method not found")
	}
	var rpcErr *acp.RequestError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("CreateTerminal error = %T %v, want JSON-RPC method not found", err, err)
	}
}

func TestAcpChatClientTerminalRPCLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)

	ctx := context.Background()
	resp, err := client.CreateTerminal(ctx, acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'hello '; printf \"$ACP_TEST_VALUE\"; printf ' err' 1>&2"},
		Cwd:     &workspace,
		Env:     []acp.EnvVariable{{Name: "ACP_TEST_VALUE", Value: "world"}},
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	if resp.TerminalId == "" {
		t.Fatal("CreateTerminal returned empty terminal id")
	}
	t.Cleanup(func() {
		_, _ = client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: resp.TerminalId})
	})

	wait, err := client.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId})
	if err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}
	if wait.ExitCode == nil || *wait.ExitCode != 0 {
		t.Fatalf("WaitForTerminalExit exit = %v, want 0", wait.ExitCode)
	}
	output, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{TerminalId: resp.TerminalId})
	if err != nil {
		t.Fatalf("TerminalOutput: %v", err)
	}
	if !strings.Contains(output.Output, "hello world") {
		t.Fatalf("TerminalOutput output = %q, want stdout", output.Output)
	}
	if !strings.Contains(output.Output, "err") {
		t.Fatalf("TerminalOutput output = %q, want stderr", output.Output)
	}
	if output.ExitStatus == nil || output.ExitStatus.ExitCode == nil || *output.ExitStatus.ExitCode != 0 {
		t.Fatalf("TerminalOutput exit status = %+v, want exit code 0", output.ExitStatus)
	}
	if _, err := client.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("ReleaseTerminal: %v", err)
	}
	if _, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{TerminalId: resp.TerminalId}); err == nil {
		t.Fatal("TerminalOutput after release succeeded; want not found")
	}
}

func TestAcpChatClientTerminalRPCOutputTruncatesFromBeginning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	limit := 8
	resp, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command:         "sh",
		Args:            []string{"-c", "printf 0123456789"},
		Cwd:             &workspace,
		OutputByteLimit: &limit,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: resp.TerminalId})
	})
	if _, err := client.WaitForTerminalExit(context.Background(), acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}
	output, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: resp.TerminalId})
	if err != nil {
		t.Fatalf("TerminalOutput: %v", err)
	}
	if output.Output != "3456789\n" {
		t.Fatalf("TerminalOutput output = %q, want retained tail", output.Output)
	}
	if !output.Truncated {
		t.Fatal("TerminalOutput truncated = false, want true")
	}
}

func TestAcpChatClientTerminalRPCKillKeepsTerminalReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix signal semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	resp, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'started\n'; exec sleep 60"},
		Cwd:     &workspace,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: resp.TerminalId})
	})
	waitForTerminalOutput(t, client, resp.TerminalId, "started")
	if _, err := client.KillTerminal(context.Background(), acp.KillTerminalRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("KillTerminal: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("WaitForTerminalExit after kill: %v", err)
	}
	output, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: resp.TerminalId})
	if err != nil {
		t.Fatalf("TerminalOutput after kill: %v", err)
	}
	if !strings.Contains(output.Output, "started") {
		t.Fatalf("TerminalOutput output = %q, want retained output after kill", output.Output)
	}
}

func waitForTerminalOutput(t *testing.T, client *acpChatClient, terminalID string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: terminalID})
		if err == nil && strings.Contains(output.Output, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	output, _ := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: terminalID})
	t.Fatalf("terminal output = %q, want %q before deadline", output.Output, want)
}

func TestAcpChatClientTerminalRPCRejectsWorkspaceEscape(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "true",
		Cwd:     &outside,
	})
	if err == nil {
		t.Fatal("CreateTerminal succeeded outside workspace; want sandbox rejection")
	}
	if !strings.Contains(err.Error(), "escapes allowed root") {
		t.Fatalf("CreateTerminal error = %v, want workspace escape rejection", err)
	}
}

func TestAcpChatClientCloseTerminalsReleasesRunningChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix signal semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	resp, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "sleep",
		Args:    []string{"60"},
		Cwd:     &workspace,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	if err := client.closeTerminals(context.Background()); err != nil {
		t.Fatalf("closeTerminals: %v", err)
	}
	if _, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: resp.TerminalId}); err == nil {
		t.Fatal("TerminalOutput after closeTerminals succeeded; want not found")
	}
}

func TestAcpChatClientTerminalRPCRejectsBeforeSpawn(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	client, store := newTerminalTestClient(workspace, ModeDeny)
	_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf should-not-run > denied.txt"},
		Cwd:     &workspace,
		Env:     []acp.EnvVariable{{Name: "SECRET_VALUE", Value: "super-secret"}},
	})
	if err == nil {
		t.Fatal("CreateTerminal succeeded with deny-mode coordinator; want cancellation")
	}
	var rpcErr *acp.RequestError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32800 {
		t.Fatalf("CreateTerminal error = %T %v, want JSON-RPC request cancelled", err, err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "denied.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("denied command output stat error = %v, want file not created", statErr)
	}
	rows, err := store.ListApprovals(context.Background(), "chat_test", "")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("approvals = %d, want 1", len(rows))
	}
	if rows[0].ToolKind != ToolKindShellExec || rows[0].Status != ApprovalStatusDenied {
		t.Fatalf("approval = %+v, want denied shell_exec", rows[0])
	}
	payload := string(rows[0].ACPPayload)
	if !strings.Contains(payload, "SECRET_VALUE") {
		t.Fatalf("approval payload = %s, want env name for operator context", payload)
	}
	if strings.Contains(payload, "super-secret") {
		t.Fatalf("approval payload leaked env value: %s", payload)
	}
}

func TestAcpChatClientTerminalRPCRequiresApprovalCoordinator(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	client := &acpChatClient{
		sessionID:        "chat_test",
		adapterID:        "codex",
		workspace:        workspace,
		terminalsEnabled: true,
	}
	_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "true",
		Cwd:     &workspace,
	})
	if err == nil {
		t.Fatal("CreateTerminal succeeded without approval coordinator; want cancellation")
	}
	var rpcErr *acp.RequestError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32800 {
		t.Fatalf("CreateTerminal error = %T %v, want JSON-RPC request cancelled", err, err)
	}
}

func TestAcpChatClientReadTextFileRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	client := &acpChatClient{
		sessionID: "chat_test",
		adapterID: "codex",
		workspace: workspace,
	}

	_, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: "linked/secret.txt"})
	if err == nil {
		t.Fatal("ReadTextFile() error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("ReadTextFile() error = %v, want symlink rejection", err)
	}
}

func newTerminalTestClient(workspace string, mode ApprovalMode) (*acpChatClient, *MemoryApprovalStore) {
	store := NewMemoryApprovalStore()
	return &acpChatClient{
		sessionID:        "chat_test",
		adapterID:        "codex",
		workspace:        workspace,
		terminalsEnabled: true,
		coordinator: NewApprovalCoordinator(CoordinatorOptions{
			Mode:  mode,
			Store: store,
		}),
	}, store
}

func TestAcpChatClientWriteTextFileRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	client := &acpChatClient{
		sessionID: "chat_test",
		adapterID: "codex",
		workspace: workspace,
	}

	_, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:    "linked/escape.txt",
		Content: "nope",
	})
	if err == nil {
		t.Fatal("WriteTextFile() error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("WriteTextFile() error = %v, want symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file stat error = %v, want not exist", statErr)
	}
}
