package agentadapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacecoord"
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

func TestTerminalCommandLineQuotesDisplayArgs(t *testing.T) {
	t.Parallel()

	got := terminalCommandLine("my cmd", []string{"-c", "printf 'hello world'", ""})
	want := `'my cmd' -c 'printf '\''hello world'\''' ''`
	if got != want {
		t.Fatalf("terminalCommandLine = %q, want %q", got, want)
	}
}

func TestAcpChatClientTerminalRPCLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	activities := attachTerminalActivityCapture(client)

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
	running := findTerminalActivity(activities.snapshot(), resp.TerminalId, "running")
	if running == nil {
		t.Fatalf("terminal activities = %+v, want running activity", activities.snapshot())
	}
	if running.Type != "terminal" || running.Kind != "execute" || running.Title != "Terminal command" {
		t.Fatalf("running terminal activity = %+v, want terminal execute title", *running)
	}
	if !strings.Contains(running.Detail, "cwd "+workspace) || !strings.Contains(running.Detail, "sh -c") {
		t.Fatalf("running terminal detail = %q, want command and cwd", running.Detail)
	}
	completed := findTerminalActivity(activities.snapshot(), resp.TerminalId, "completed")
	if completed == nil {
		t.Fatalf("terminal activities = %+v, want completed activity", activities.snapshot())
	}
	if !strings.Contains(completed.Detail, "exit code 0") {
		t.Fatalf("completed terminal detail = %q, want exit code", completed.Detail)
	}
	if !strings.Contains(completed.ArtifactPreview, "hello world") || !strings.Contains(completed.ArtifactPreview, "err") {
		t.Fatalf("completed terminal preview = %q, want retained output", completed.ArtifactPreview)
	}
	if _, err := client.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("ReleaseTerminal: %v", err)
	}
	if got := countTerminalActivities(activities.snapshot(), resp.TerminalId, "completed"); got != 1 {
		t.Fatalf("completed terminal activity count = %d, want 1 after wait+release", got)
	}
	if got := activities.closedSnapshot(); len(got) != 1 || got[0] != resp.TerminalId {
		t.Fatalf("terminal closed callbacks = %v, want exactly [%s]", got, resp.TerminalId)
	}
	if _, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{TerminalId: resp.TerminalId}); err == nil {
		t.Fatal("TerminalOutput after release succeeded; want not found")
	}
}

func TestAcpChatClientTerminalExitUsesOriginatingTurnActivitySink(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	client, _ := newTerminalTestClient(workspacePath, ModeAuto)
	term := newControlledWorkspaceTerminal("term_origin_turn")
	client.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}

	originStream := &terminalActivityCapture{}
	originTerminal := &terminalActivityCapture{}
	firstTurn := newACPTurn(64*1024, nil)
	firstTurn.setActivityCallback(originStream.record)
	firstTurn.setTerminalActivityCallback(originTerminal.record)
	client.setTurn(firstTurn)

	resp, err := client.CreateTerminal(t.Context(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "sleep 1"},
		Cwd:     &workspacePath,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	if findTerminalActivity(originTerminal.snapshot(), resp.TerminalId, "running") == nil {
		t.Fatalf("origin terminal activities = %+v, want running", originTerminal.snapshot())
	}
	if got := originStream.snapshot(); len(got) != 0 {
		t.Fatalf("origin stream activities = %+v, want terminal activity on durable sink", got)
	}

	client.clearTurn(firstTurn)
	later := &terminalActivityCapture{}
	secondTurn := newACPTurn(64*1024, nil)
	secondTurn.setActivityCallback(later.record)
	secondTurn.setTerminalActivityCallback(later.record)
	client.setTurn(secondTurn)

	term.finish()
	waitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := client.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}
	if findTerminalActivity(originTerminal.snapshot(), resp.TerminalId, "completed") == nil {
		t.Fatalf("origin terminal activities = %+v, want completed", originTerminal.snapshot())
	}
	if got := later.snapshot(); len(got) != 0 {
		t.Fatalf("later-turn activities = %+v, want no activity from originating terminal", got)
	}
}

func TestAcpChatClientTerminalDoesNotRetainRunScopedActivityFallback(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	client, _ := newTerminalTestClient(workspacePath, ModeAuto)
	term := newControlledWorkspaceTerminal("term_no_run_fallback")
	client.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}

	runScoped := &terminalActivityCapture{}
	turn := newACPTurn(64*1024, nil)
	turn.setActivityCallback(runScoped.record)
	client.setTurn(turn)
	resp, err := client.CreateTerminal(t.Context(), acp.CreateTerminalRequest{
		Command: "true",
		Cwd:     &workspacePath,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	client.clearTurn(turn)
	term.finish()
	waitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := client.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}
	if got := runScoped.snapshot(); len(got) != 0 {
		t.Fatalf("Run-scoped activities = %+v, want no retained terminal callbacks", got)
	}
}

func TestAcpChatClientTerminalPublishesRunningBeforeImmediateExit(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	client, _ := newTerminalTestClient(workspacePath, ModeAuto)
	term := newControlledWorkspaceTerminal("term_immediate_exit")
	term.finish()
	client.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}
	activities := attachTerminalActivityCapture(client)

	resp, err := client.CreateTerminal(t.Context(), acp.CreateTerminalRequest{
		Command: "true",
		Cwd:     &workspacePath,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := client.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}

	var statuses []string
	for _, activity := range activities.snapshot() {
		if activity.ID == "terminal:"+resp.TerminalId {
			statuses = append(statuses, activity.Status)
		}
	}
	if len(statuses) != 2 || statuses[0] != "running" || statuses[1] != "completed" {
		t.Fatalf("terminal status order = %v, want [running completed]", statuses)
	}
}

func TestAcpChatClientTerminalKillActivityNeverRunsAfterSettlement(t *testing.T) {
	t.Parallel()

	for i := 0; i < 100; i++ {
		client := &acpChatClient{}
		capture := &terminalActivityCapture{}
		item := &acpTerminal{
			id:           "term_kill_settlement",
			activitySink: capture.record,
			activityDone: capture.recordClosed,
		}
		code := 0
		item.exitCode = &code
		start := make(chan struct{})
		var calls sync.WaitGroup
		calls.Add(2)
		go func() {
			defer calls.Done()
			<-start
			client.emitTransientTerminalActivity(item, "cancelled", "killed", "")
		}()
		go func() {
			defer calls.Done()
			<-start
			client.emitTerminalExitActivity(item)
		}()
		close(start)
		calls.Wait()

		events := capture.eventSnapshot()
		if len(events) == 0 || events[len(events)-1] != "closed:"+item.id {
			t.Fatalf("iteration %d terminal events = %v, want closed last", i, events)
		}
		if got := capture.closedSnapshot(); len(got) != 1 {
			t.Fatalf("iteration %d terminal closed callbacks = %v, want exactly one", i, got)
		}
	}
}

func TestAcpChatClientTerminalShutdownAfterTurnUsesOriginatingActivitySink(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	client, _ := newTerminalTestClient(workspacePath, ModeAuto)
	term := newControlledWorkspaceTerminal("term_origin_shutdown")
	term.closeCompletes = true
	client.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}

	origin := &terminalActivityCapture{}
	turn := newACPTurn(64*1024, nil)
	turn.setTerminalActivityCallback(origin.record)
	client.setTurn(turn)
	resp, err := client.CreateTerminal(t.Context(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "sleep 1"},
		Cwd:     &workspacePath,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	client.clearTurn(turn)

	if err := client.closeTerminals(t.Context()); err != nil {
		t.Fatalf("closeTerminals: %v", err)
	}
	cancelled := findTerminalActivity(origin.snapshot(), resp.TerminalId, "cancelled")
	if cancelled == nil || !strings.Contains(cancelled.Detail, "killed") {
		t.Fatalf("origin terminal activities = %+v, want shutdown cancellation", origin.snapshot())
	}
}

func TestAcpChatClientReleaseMarksCancellationBeforeWatcherWins(t *testing.T) {
	t.Parallel()

	term := newControlledWorkspaceTerminal("term_release_watcher")
	term.closeCompletes = true
	term.closeRelease = make(chan struct{})
	client := &acpChatClient{terminalsEnabled: true}
	activities := &terminalActivityCapture{}
	item := &acpTerminal{
		id:           term.id,
		term:         term,
		output:       newACPTerminalOutputBuffer(1024),
		done:         make(chan struct{}),
		activitySink: activities.record,
		activityDone: activities.recordClosed,
		onExit:       client.emitTerminalExitActivity,
	}
	client.storeTerminal(item)
	go item.watch()

	releaseDone := make(chan error, 1)
	go func() {
		_, err := client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: term.id})
		releaseDone <- err
	}()
	select {
	case <-term.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("ReleaseTerminal did not call terminal Close")
	}
	select {
	case <-item.done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not settle while terminal Close was blocked")
	}
	if completed := findTerminalActivity(activities.snapshot(), term.id, "completed"); completed != nil {
		t.Fatalf("watcher activity = %+v, want released terminal not completed", completed)
	}
	if cancelled := findTerminalActivity(activities.snapshot(), term.id, "cancelled"); cancelled == nil {
		t.Fatalf("watcher activities = %+v, want authoritative cancellation", activities.snapshot())
	}
	close(term.closeRelease)
	if err := <-releaseDone; err != nil {
		t.Fatalf("ReleaseTerminal: %v", err)
	}
	if got := activities.closedSnapshot(); len(got) != 1 {
		t.Fatalf("terminal closed callbacks = %v, want exactly one", got)
	}
}

func TestAcpChatClientTerminalAcquireFailsBeforeSpawnWhileWorkspaceClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	registry := workspacecoord.NewRegistry()
	client.workspaceCoordinator = registry
	closure, err := registry.TryClose(t.Context(), workspace)
	if err != nil {
		t.Fatalf("TryClose: %v", err)
	}
	defer closure.Release()

	marker := filepath.Join(workspace, "terminal-started.txt")
	_, err = client.CreateTerminal(t.Context(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf started > terminal-started.txt"},
		Cwd:     &workspace,
	})
	if !errors.Is(err, workspacecoord.ErrClosed) {
		t.Fatalf("CreateTerminal error = %v, want workspacecoord.ErrClosed", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("terminal marker stat error = %v, want process not spawned", statErr)
	}
}

func TestAcpChatClientTerminalWorkspaceLeaseReleasesAfterWatcherDrain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}
	t.Parallel()

	workspace := t.TempDir()
	client, _ := newTerminalTestClient(workspace, ModeAuto)
	registry := workspacecoord.NewRegistry()
	client.workspaceCoordinator = registry
	resp, err := client.CreateTerminal(t.Context(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf watcher-drained"},
		Cwd:     &workspace,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: resp.TerminalId})
	})
	if _, err := client.WaitForTerminalExit(t.Context(), acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId}); err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}
	output, err := client.TerminalOutput(t.Context(), acp.TerminalOutputRequest{TerminalId: resp.TerminalId})
	if err != nil || !strings.Contains(output.Output, "watcher-drained") {
		t.Fatalf("TerminalOutput after watcher drain = %q, err=%v", output.Output, err)
	}
	closure, err := registry.TryClose(t.Context(), workspace)
	if err != nil {
		t.Fatalf("TryClose after watcher drain: %v", err)
	}
	closure.Release()
}

func TestAcpChatClientTerminalToolOutputPreviewSurvivesRemoval(t *testing.T) {
	t.Parallel()

	client := &acpChatClient{}
	item := &acpTerminal{
		id:     "term_123",
		output: newACPTerminalOutputBuffer(1024),
	}
	item.output.append("terminal output\n")
	client.storeTerminal(item)

	preview, ok := client.terminalToolOutputPreview("term_123")
	if !ok || preview != "terminal output" {
		t.Fatalf("terminalToolOutputPreview(active) = %q, %v; want retained active output", preview, ok)
	}
	if _, err := client.removeTerminal("term_123"); err != nil {
		t.Fatalf("removeTerminal: %v", err)
	}

	preview, ok = client.terminalToolOutputPreview("term_123")
	if !ok || preview != "terminal output" {
		t.Fatalf("terminalToolOutputPreview(removed) = %q, %v; want retained removed output", preview, ok)
	}
}

func TestAcpChatClientReleaseTerminalKeepsHandleWhenCloseFails(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	term := &fakeWorkspaceTerminal{
		id:       "term_close_error",
		outputCh: make(chan workspace.OutputChunk),
		closeErr: closeErr,
	}
	client := &acpChatClient{terminalsEnabled: true}
	item := &acpTerminal{
		id:          term.id,
		commandLine: "sh -c 'sleep 60'",
		cwd:         t.TempDir(),
		term:        term,
		output:      newACPTerminalOutputBuffer(1024),
		done:        make(chan struct{}),
	}
	item.output.append("still readable\n")
	client.storeTerminal(item)

	if _, err := client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: term.id}); !errors.Is(err, closeErr) {
		t.Fatalf("ReleaseTerminal error = %v, want close failure", err)
	}
	output, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: term.id})
	if err != nil {
		t.Fatalf("TerminalOutput after failed release: %v", err)
	}
	if !strings.Contains(output.Output, "still readable") {
		t.Fatalf("TerminalOutput after failed release = %q, want retained active output", output.Output)
	}
	if preview, ok := client.terminalToolOutputPreview(term.id); !ok || !strings.Contains(preview, "still readable") {
		t.Fatalf("terminalToolOutputPreview after failed release = %q, %v; want retained active output", preview, ok)
	}

	term.closeErr = nil
	if _, err := client.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{TerminalId: term.id}); err != nil {
		t.Fatalf("ReleaseTerminal retry: %v", err)
	}
	if _, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: term.id}); err == nil {
		t.Fatal("TerminalOutput after successful release succeeded; want not found")
	}
	if preview, ok := client.terminalToolOutputPreview(term.id); !ok || !strings.Contains(preview, "still readable") {
		t.Fatalf("terminalToolOutputPreview after successful release = %q, %v; want retained removed output", preview, ok)
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
	activities := attachTerminalActivityCapture(client)
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
	cancelled := findTerminalActivity(activities.snapshot(), resp.TerminalId, "cancelled")
	if cancelled == nil {
		t.Fatalf("terminal activities = %+v, want cancelled activity", activities.snapshot())
	}
	if !strings.Contains(cancelled.Detail, "killed") {
		t.Fatalf("cancelled terminal detail = %q, want kill reason", cancelled.Detail)
	}
}

type terminalActivityCapture struct {
	mu         sync.Mutex
	activities []Activity
	closed     []string
	events     []string
}

func attachTerminalActivityCapture(client *acpChatClient) *terminalActivityCapture {
	capture := &terminalActivityCapture{}
	turn := newACPTurn(64*1024, nil)
	turn.setTerminalActivityCallback(capture.record)
	turn.setTerminalClosedCallback(capture.recordClosed)
	client.setTurn(turn)
	return capture
}

func (c *terminalActivityCapture) record(activity Activity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activities = append(c.activities, activity)
	c.events = append(c.events, activity.Status+":"+activity.ID)
}

func (c *terminalActivityCapture) recordClosed(terminalID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = append(c.closed, terminalID)
	c.events = append(c.events, "closed:"+terminalID)
}

func (c *terminalActivityCapture) snapshot() []Activity {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Activity, len(c.activities))
	copy(out, c.activities)
	return out
}

func (c *terminalActivityCapture) closedSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.closed...)
}

func (c *terminalActivityCapture) eventSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

func findTerminalActivity(activities []Activity, terminalID, status string) *Activity {
	id := "terminal:" + terminalID
	for i := len(activities) - 1; i >= 0; i-- {
		activity := activities[i]
		if activity.ID == id && activity.Status == status {
			return &activity
		}
	}
	return nil
}

func countTerminalActivities(activities []Activity, terminalID, status string) int {
	id := "terminal:" + terminalID
	var count int
	for _, activity := range activities {
		if activity.ID == id && activity.Status == status {
			count++
		}
	}
	return count
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
	activities := attachTerminalActivityCapture(client)
	resp, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'started\n'; exec sleep 60"},
		Cwd:     &workspace,
	})
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	waitForTerminalOutput(t, client, resp.TerminalId, "started")
	if err := client.closeTerminals(context.Background()); err != nil {
		t.Fatalf("closeTerminals: %v", err)
	}
	if _, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: resp.TerminalId}); err == nil {
		t.Fatal("TerminalOutput after closeTerminals succeeded; want not found")
	}
	cancelled := findTerminalActivity(activities.snapshot(), resp.TerminalId, "cancelled")
	if cancelled == nil {
		t.Fatalf("terminal activities = %+v, want cancelled activity", activities.snapshot())
	}
	if !strings.Contains(cancelled.Detail, "killed") {
		t.Fatalf("cancelled terminal detail = %q, want killed reason", cancelled.Detail)
	}
	if !strings.Contains(cancelled.ArtifactPreview, "started") {
		t.Fatalf("cancelled terminal preview = %q, want retained output", cancelled.ArtifactPreview)
	}
	preview, ok := client.terminalToolOutputPreview(resp.TerminalId)
	if !ok || !strings.Contains(preview, "started") {
		t.Fatalf("terminalToolOutputPreview after closeTerminals = %q, %v; want retained output", preview, ok)
	}
}

func TestAcpChatClientCloseTerminalsWaitsForCreateAndRollsBackSpawn(t *testing.T) {
	workspacePath := t.TempDir()
	client, _ := newTerminalTestClient(workspacePath, ModeAuto)
	registry := client.workspaceCoordinator
	term := newControlledWorkspaceTerminal("term_create_during_close")
	term.closeCompletes = true
	spawned := make(chan struct{})
	releaseSpawn := make(chan struct{})
	var spawnOnce sync.Once
	openCalls := 0
	client.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		openCalls++
		spawnOnce.Do(func() { close(spawned) })
		<-releaseSpawn
		return term, nil
	}

	createDone := make(chan error, 1)
	go func() {
		_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
			Command: "sh",
			Args:    []string{"-c", "sleep 60"},
			Cwd:     &workspacePath,
		})
		createDone <- err
	}()
	select {
	case <-spawned:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal spawn")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- client.closeTerminals(context.Background())
	}()
	waitForTerminalAdmissionClosed(t, client)
	select {
	case err := <-closeDone:
		t.Fatalf("closeTerminals returned before in-flight create completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseSpawn)
	select {
	case err := <-createDone:
		if !errors.Is(err, errACPTerminalsClosed) {
			t.Fatalf("CreateTerminal error = %v, want errACPTerminalsClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight CreateTerminal rollback")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("closeTerminals: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for closeTerminals")
	}
	select {
	case <-term.closeCalled:
	default:
		t.Fatal("spawned terminal was not closed during admission rollback")
	}

	_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "true",
		Cwd:     &workspacePath,
	})
	if !errors.Is(err, errACPTerminalsClosed) {
		t.Fatalf("CreateTerminal after close error = %v, want errACPTerminalsClosed", err)
	}
	if openCalls != 1 {
		t.Fatalf("OpenTerminal calls = %d, want 1", openCalls)
	}
	waitForWorkspaceWriterRelease(t, registry, workspacePath)
}

func TestAcpChatClientCloseTerminalsTimeoutDetachesTranscriptCallbacks(t *testing.T) {
	term := newControlledWorkspaceTerminal("term_timeout")
	term.closeWithContextError = true
	term.result.ExitCode = 23
	client := &acpChatClient{terminalsEnabled: true}
	activities := attachTerminalActivityCapture(client)
	item := &acpTerminal{
		id:           term.id,
		commandLine:  "sh -c 'sleep 60'",
		cwd:          t.TempDir(),
		term:         term,
		output:       newACPTerminalOutputBuffer(1024),
		done:         make(chan struct{}),
		activitySink: activities.record,
		activityDone: activities.recordClosed,
		onExit:       client.emitTerminalExitActivity,
	}
	client.storeTerminal(item)
	go item.watch()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.closeTerminals(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("closeTerminals error = %v, want context.Canceled", err)
	}
	if !item.exitReported.Load() {
		t.Fatal("closeTerminals did not make deadline cancellation authoritative")
	}
	if got := countTerminalActivities(activities.snapshot(), term.id, "cancelled"); got != 1 {
		t.Fatalf("cancelled activity count = %d, want 1", got)
	}
	if got := activities.closedSnapshot(); len(got) != 1 || got[0] != term.id {
		t.Fatalf("terminal closed callbacks = %v, want [%s]", got, term.id)
	}

	term.finish()
	select {
	case <-item.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal watcher completion")
	}
	if got := countTerminalActivities(activities.snapshot(), term.id, "cancelled"); got != 1 {
		t.Fatalf("cancelled activity count after watcher completion = %d, want no callback re-entry", got)
	}
	if got := activities.closedSnapshot(); len(got) != 1 {
		t.Fatalf("terminal closed callbacks after watcher completion = %v, want exactly one", got)
	}
	events := activities.eventSnapshot()
	if len(events) != 2 || !strings.HasPrefix(events[0], "cancelled:") || events[1] != "closed:"+term.id {
		t.Fatalf("terminal settlement order = %v, want cancelled then closed", events)
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
		sessionID:            "chat_test",
		adapterID:            "codex",
		workspace:            workspace,
		terminalsEnabled:     true,
		workspaceCoordinator: workspacecoord.NewRegistry(),
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

func TestAcpChatClientTerminalRPCRequiresWorkspaceCoordinator(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	client := &acpChatClient{
		sessionID:        "chat_test",
		adapterID:        "codex",
		workspace:        workspace,
		terminalsEnabled: true,
		coordinator:      NewApprovalCoordinator(CoordinatorOptions{Mode: ModeAuto}),
	}
	_, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "true",
		Cwd:     &workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace coordination is required") {
		t.Fatalf("CreateTerminal error = %v, want missing workspace coordination failure", err)
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
		sessionID:            "chat_test",
		adapterID:            "codex",
		workspace:            workspace,
		terminalsEnabled:     true,
		workspaceCoordinator: workspacecoord.NewRegistry(),
		coordinator: NewApprovalCoordinator(CoordinatorOptions{
			Mode:  mode,
			Store: store,
		}),
	}, store
}

type fakeWorkspaceTerminal struct {
	id       string
	outputCh chan workspace.OutputChunk
	closeErr error
}

func (t *fakeWorkspaceTerminal) ID() string {
	return t.id
}

func (t *fakeWorkspaceTerminal) Output() <-chan workspace.OutputChunk {
	return t.outputCh
}

func (t *fakeWorkspaceTerminal) Write(context.Context, string) error {
	return nil
}

func (t *fakeWorkspaceTerminal) WaitForExit(context.Context) (workspace.Result, error) {
	return workspace.Result{ExitCode: 0}, nil
}

func (t *fakeWorkspaceTerminal) Kill(context.Context) error {
	return nil
}

func (t *fakeWorkspaceTerminal) Close(context.Context) error {
	return t.closeErr
}

type controlledWorkspaceTerminal struct {
	id                    string
	outputCh              chan workspace.OutputChunk
	exitCh                chan struct{}
	closeCalled           chan struct{}
	closeOnce             sync.Once
	finishOnce            sync.Once
	closeCompletes        bool
	closeWithContextError bool
	closeRelease          chan struct{}
	result                workspace.Result
	waitErr               error
}

func newControlledWorkspaceTerminal(id string) *controlledWorkspaceTerminal {
	return &controlledWorkspaceTerminal{
		id:          id,
		outputCh:    make(chan workspace.OutputChunk),
		exitCh:      make(chan struct{}),
		closeCalled: make(chan struct{}),
	}
}

func (t *controlledWorkspaceTerminal) ID() string {
	return t.id
}

func (t *controlledWorkspaceTerminal) Output() <-chan workspace.OutputChunk {
	return t.outputCh
}

func (t *controlledWorkspaceTerminal) Write(context.Context, string) error {
	return nil
}

func (t *controlledWorkspaceTerminal) WaitForExit(ctx context.Context) (workspace.Result, error) {
	select {
	case <-t.exitCh:
		return t.result, t.waitErr
	case <-ctx.Done():
		return workspace.Result{}, ctx.Err()
	}
}

func (t *controlledWorkspaceTerminal) Kill(context.Context) error {
	return nil
}

func (t *controlledWorkspaceTerminal) Close(ctx context.Context) error {
	t.closeOnce.Do(func() { close(t.closeCalled) })
	if t.closeCompletes {
		t.finish()
	}
	if t.closeRelease != nil {
		<-t.closeRelease
	}
	if t.closeWithContextError && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (t *controlledWorkspaceTerminal) finish() {
	t.finishOnce.Do(func() {
		close(t.outputCh)
		close(t.exitCh)
	})
}

func waitForTerminalAdmissionClosed(t *testing.T, client *acpChatClient) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client.terminalMu.Lock()
		closed := client.terminalsClosed
		client.terminalMu.Unlock()
		if closed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for terminal admission to close")
}

func waitForWorkspaceWriterRelease(t *testing.T, registry *workspacecoord.Registry, workspacePath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		closure, err := registry.TryClose(context.Background(), workspacePath)
		if err == nil {
			closure.Release()
			return
		}
		if !errors.Is(err, workspacecoord.ErrBusy) {
			t.Fatalf("TryClose after terminal rollback: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for terminal watcher to release workspace writer")
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
