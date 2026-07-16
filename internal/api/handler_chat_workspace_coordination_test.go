package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/internal/workspacecoord"
	"github.com/hecatehq/hecate/pkg/types"
)

type chatWorkspaceRevertFixture struct {
	handler   *Handler
	client    apiTestClient
	store     *chat.MemoryStore
	sessionID string
	workspace string
	nested    string
	sibling   string
	filePath  string
	revision  string
}

type workspaceCoordinatedFakeAgentChatRunner struct {
	fakeAgentChatRunner
	coordinator *workspacecoord.Registry
}

func (runner *workspaceCoordinatedFakeAgentChatRunner) SetWorkspaceCoordinator(coordinator *workspacecoord.Registry) {
	runner.coordinator = coordinator
}

type retainedWorkspaceWriterAgentChatRunner struct {
	workspaceCoordinatedFakeAgentChatRunner
	workspaceLease *workspacecoord.WriterLease
}

func (runner *retainedWorkspaceWriterAgentChatRunner) Run(ctx context.Context, req agentadapters.RunRequest) (agentadapters.RunResult, error) {
	if runner.coordinator == nil {
		return agentadapters.RunResult{}, errors.New("workspace coordinator is not configured")
	}
	lease, err := runner.coordinator.AcquireWriter(ctx, req.Workspace)
	if err != nil {
		return agentadapters.RunResult{}, err
	}
	runner.workspaceLease = lease
	return runner.fakeAgentChatRunner.Run(ctx, req)
}

func (runner *retainedWorkspaceWriterAgentChatRunner) releaseWorkspaceWriter() {
	if runner.workspaceLease != nil {
		runner.workspaceLease.Release()
		runner.workspaceLease = nil
	}
}

func TestSetAgentChatRunnerWiresSharedWorkspaceCoordinator(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = handler.Shutdown(ctx)
	})

	replacement := &workspaceCoordinatedFakeAgentChatRunner{}
	handler.SetAgentChatRunner(replacement)
	if replacement.coordinator == nil || replacement.coordinator != handler.workspaceCoordinator {
		t.Fatalf("replacement workspace coordinator = %p, want handler registry %p", replacement.coordinator, handler.workspaceCoordinator)
	}
}

func TestRevertChatWorkspaceFilesRemainsBlockedAfterExternalTurnRetainsWriter(t *testing.T) {
	fixture := newChatWorkspaceRevertFixture(t)
	if _, err := fixture.store.UpdateSession(t.Context(), fixture.sessionID, func(session *chat.Session) {
		session.AgentID = "codex"
		session.DriverKind = agentadapters.DriverKindACP
	}); err != nil {
		t.Fatalf("UpdateSession(external): %v", err)
	}
	runner := &retainedWorkspaceWriterAgentChatRunner{
		workspaceCoordinatedFakeAgentChatRunner: workspaceCoordinatedFakeAgentChatRunner{
			fakeAgentChatRunner: fakeAgentChatRunner{output: "external turn complete"},
		},
	}
	fixture.handler.SetAgentChatRunner(runner)
	t.Cleanup(runner.releaseWorkspaceWriter)

	fixture.client.mustRequestStatus(http.StatusOK, http.MethodPost, "/hecate/v1/chat/sessions/"+fixture.sessionID+"/messages", `{"content":"work","execution_mode":"external_agent"}`)
	conflict := fixture.client.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	if !strings.Contains(conflict.Body.String(), `"status":"workspace_active"`) {
		t.Fatalf("revert with retained external writer body = %s, want workspace_active conflict", conflict.Body.String())
	}
	if content, err := os.ReadFile(fixture.filePath); err != nil || string(content) != "modified\n" {
		t.Fatalf("file while external writer retained = %q, err=%v", content, err)
	}

	runner.releaseWorkspaceWriter()
	response := mustRequestJSON[ChatWorkspaceDiffResponse](fixture.client, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	if response.Data.HasChanges {
		t.Fatalf("post-drain workspace diff = %+v, want clean workspace", response.Data)
	}
}

func newChatWorkspaceRevertFixture(t *testing.T) chatWorkspaceRevertFixture {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	nested := filepath.Join(workspace, "nested")
	sibling := filepath.Join(root, "sibling")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(nested): %v", err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("MkdirAll(sibling): %v", err)
	}
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	filePath := filepath.Join(workspace, "notes.md")
	if err := os.WriteFile(filePath, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".keep"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatalf("write nested marker: %v", err)
	}
	runTestGit(t, workspace, "add", ".")
	runTestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(filePath, []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	store := chat.NewMemoryStore()
	const sessionID = "chat_workspace_coordination"
	if _, err := store.Create(t.Context(), chat.Session{
		ID:        sessionID,
		Title:     "Workspace coordination",
		AgentID:   chat.DefaultAgentID,
		Workspace: workspace,
		Status:    "completed",
	}); err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	handler.SetAgentChatStore(store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = handler.Shutdown(ctx)
	})
	client := newAPITestClient(t, NewServer(logger, handler))
	reviewed := mustRequestJSON[ChatWorkspaceDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
	return chatWorkspaceRevertFixture{
		handler:   handler,
		client:    client,
		store:     store,
		sessionID: sessionID,
		workspace: workspace,
		nested:    nested,
		sibling:   sibling,
		filePath:  filePath,
		revision:  reviewed.Data.Revision,
	}
}

func (fixture chatWorkspaceRevertFixture) revertBody() string {
	return fmt.Sprintf(`{"paths":["notes.md"],"expected_revision":%q}`, fixture.revision)
}

func (fixture chatWorkspaceRevertFixture) revertPath() string {
	return "/hecate/v1/chat/sessions/" + fixture.sessionID + "/workspace-diff/revert"
}

func TestRevertChatWorkspaceFilesCoordinatesOverlappingWriters(t *testing.T) {
	fixture := newChatWorkspaceRevertFixture(t)

	writer, err := fixture.handler.workspaceCoordinator.AcquireWriter(t.Context(), fixture.nested)
	if err != nil {
		t.Fatalf("AcquireWriter(nested): %v", err)
	}
	conflict := fixture.client.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	writer.Release()
	if !strings.Contains(conflict.Body.String(), `"status":"workspace_active"`) {
		t.Fatalf("overlapping writer body = %s, want workspace_active conflict", conflict.Body.String())
	}
	if content, err := os.ReadFile(fixture.filePath); err != nil || string(content) != "modified\n" {
		t.Fatalf("file after overlapping conflict = %q, err=%v", content, err)
	}

	siblingWriter, err := fixture.handler.workspaceCoordinator.AcquireWriter(t.Context(), fixture.sibling)
	if err != nil {
		t.Fatalf("AcquireWriter(sibling): %v", err)
	}
	defer siblingWriter.Release()
	response := mustRequestJSON[ChatWorkspaceDiffResponse](fixture.client, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	if response.Data.HasChanges {
		t.Fatalf("post-revert diff = %+v, want clean workspace", response.Data)
	}
	if content, err := os.ReadFile(fixture.filePath); err != nil || string(content) != "original\n" {
		t.Fatalf("file after sibling-safe revert = %q, err=%v", content, err)
	}
}

func TestChatWorkspaceDurableOwnerFindsOverlappingOwnersAcrossPages(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("Mkdir(nested): %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	store := chat.NewMemoryStore()
	handler.SetAgentChatStore(store)
	for i := 0; i < workspaceOwnerScanPageSize; i++ {
		if _, err := handler.taskStore.CreateRun(t.Context(), types.TaskRun{
			ID:            fmt.Sprintf("run-%03d", i),
			TaskID:        "terminal-history",
			Status:        "completed",
			WorkspacePath: root,
		}); err != nil {
			t.Fatalf("CreateRun(%d): %v", i, err)
		}
	}
	activeRun, err := handler.taskStore.CreateRun(t.Context(), types.TaskRun{
		ID:            "run-999",
		TaskID:        "active-task",
		Status:        "awaiting_approval",
		WorkspacePath: nested,
	})
	if err != nil {
		t.Fatalf("CreateRun(active): %v", err)
	}
	workspaceKey, err := workspacecoord.CanonicalWorkspace(root)
	if err != nil {
		t.Fatalf("CanonicalWorkspace: %v", err)
	}
	status, active, err := handler.chatWorkspaceDurableOwner(t.Context(), workspaceKey, "current-chat")
	if err != nil || !active || status != "awaiting_approval" {
		t.Fatalf("task durable owner = status %q active %v err %v", status, active, err)
	}

	activeRun.Status = "completed"
	if _, err := handler.taskStore.UpdateRun(t.Context(), activeRun); err != nil {
		t.Fatalf("UpdateRun(active): %v", err)
	}
	if _, err := store.Create(t.Context(), chat.Session{
		ID:        "other-chat",
		AgentID:   "codex",
		Workspace: nested,
		Status:    "running",
	}); err != nil {
		t.Fatalf("Create(other chat): %v", err)
	}
	status, active, err = handler.chatWorkspaceDurableOwner(t.Context(), workspaceKey, "current-chat")
	if err != nil || !active || status != "running" {
		t.Fatalf("chat durable owner = status %q active %v err %v", status, active, err)
	}
}

type overlappingEditChatWorkspaceGitRunner struct {
	chatWorkspaceGitRunner
	path string
	once sync.Once
}

type stagingAfterReviewChatWorkspaceGitRunner struct {
	chatWorkspaceGitRunner
	local *gitrunner.LocalRunner
	path  string
	once  sync.Once
}

func (runner *stagingAfterReviewChatWorkspaceGitRunner) ReverseApplySnapshot(ctx context.Context, workspace string, snapshot gitrunner.DiffSnapshot, paths []string) (gitrunner.Result, error) {
	var stageErr error
	runner.once.Do(func() {
		result, err := runner.local.Run(ctx, workspace, "add", "--", runner.path)
		if err != nil {
			stageErr = fmt.Errorf("stage after review: %w: %s", err, result.Stderr)
		}
	})
	if stageErr != nil {
		return gitrunner.Result{ExitCode: -1}, stageErr
	}
	return runner.chatWorkspaceGitRunner.ReverseApplySnapshot(ctx, workspace, snapshot, paths)
}

func (runner *overlappingEditChatWorkspaceGitRunner) ReverseApplySnapshot(ctx context.Context, workspace string, snapshot gitrunner.DiffSnapshot, paths []string) (gitrunner.Result, error) {
	var writeErr error
	runner.once.Do(func() {
		writeErr = os.WriteFile(runner.path, []byte("concurrent\n"), 0o644)
	})
	if writeErr != nil {
		return gitrunner.Result{ExitCode: -1}, writeErr
	}
	return runner.chatWorkspaceGitRunner.ReverseApplySnapshot(ctx, workspace, snapshot, paths)
}

func TestRevertChatWorkspaceFilesRejectsOverlappingExternalEdit(t *testing.T) {
	fixture := newChatWorkspaceRevertFixture(t)
	fixture.handler.chatWorkspaceGit = &overlappingEditChatWorkspaceGitRunner{
		chatWorkspaceGitRunner: gitrunner.NewLocalRunner(),
		path:                   fixture.filePath,
	}

	conflict := fixture.client.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	if !strings.Contains(conflict.Body.String(), "workspace diff changed after it was reviewed") {
		t.Fatalf("conditional reverse-apply body = %s, want reviewed-diff conflict", conflict.Body.String())
	}
	content, err := os.ReadFile(fixture.filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "concurrent\n" {
		t.Fatalf("file after failed conditional reverse apply = %q, want concurrent edit preserved", content)
	}
}

func TestRevertChatWorkspaceFilesRejectsStagingAfterFinalSnapshot(t *testing.T) {
	fixture := newChatWorkspaceRevertFixture(t)
	local := gitrunner.NewLocalRunner()
	fixture.handler.chatWorkspaceGit = &stagingAfterReviewChatWorkspaceGitRunner{
		chatWorkspaceGitRunner: local,
		local:                  local,
		path:                   "notes.md",
	}

	conflict := fixture.client.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	if !strings.Contains(conflict.Body.String(), "workspace diff changed after it was reviewed") {
		t.Fatalf("post-review staging body = %s, want reviewed-diff conflict", conflict.Body.String())
	}
	content, err := os.ReadFile(fixture.filePath)
	if err != nil || string(content) != "modified\n" {
		t.Fatalf("file after post-review staging conflict = %q, err=%v", content, err)
	}
	status := runTestGit(t, fixture.workspace, "status", "--porcelain=v1", "--", "notes.md")
	if !strings.HasPrefix(status, "M ") {
		t.Fatalf("status after post-review staging conflict = %q, want staged modification preserved", status)
	}
}

func TestChatWorkspaceDiffFailsClosedForStagedChanges(t *testing.T) {
	fixture := newChatWorkspaceRevertFixture(t)
	runTestGit(t, fixture.workspace, "add", "notes.md")

	getConflict := fixture.client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodGet, "/hecate/v1/chat/sessions/"+fixture.sessionID+"/workspace-diff", "")
	if !strings.Contains(getConflict.Body.String(), "Unstage the changes") {
		t.Fatalf("staged GET body = %s, want unstage guidance", getConflict.Body.String())
	}
	revertConflict := fixture.client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, fixture.revertPath(), fixture.revertBody())
	if !strings.Contains(revertConflict.Body.String(), "staged Git changes") {
		t.Fatalf("staged revert body = %s, want staged-change refusal", revertConflict.Body.String())
	}
	content, err := os.ReadFile(fixture.filePath)
	if err != nil || string(content) != "modified\n" {
		t.Fatalf("file after staged refusal = %q, err=%v", content, err)
	}
	status := runTestGit(t, fixture.workspace, "status", "--porcelain=v1", "--", "notes.md")
	if !strings.HasPrefix(status, "M ") {
		t.Fatalf("status after refusal = %q, want staged modification preserved", status)
	}
}

func TestChatWorkspaceDiffRejectsParserPathMismatch(t *testing.T) {
	store := chat.NewMemoryStore()
	const sessionID = "chat_workspace_path_mismatch"
	if _, err := store.Create(t.Context(), chat.Session{
		ID: sessionID, AgentID: chat.DefaultAgentID, Workspace: t.TempDir(), Status: "completed",
	}); err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	diff := "diff --git a/reviewed.txt b/reviewed.txt\n--- a/reviewed.txt\n+++ b/reviewed.txt\n@@ -1 +1 @@\n-old\n+new\n"
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	handler.SetAgentChatStore(store)
	handler.chatWorkspaceGit = &fixedChatWorkspaceGitRunner{snapshot: gitrunner.DiffSnapshot{
		Diff:     diff,
		Revision: gitrunner.DiffRevision(diff),
		Paths:    []string{"different.txt"},
	}}
	client := newAPITestClient(t, NewServer(logger, handler))

	response := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
	if !strings.Contains(response.Body.String(), "cannot be represented safely") {
		t.Fatalf("body = %s, want fail-closed path mismatch", response.Body.String())
	}
}

func TestChatWorkspaceDiffRejectsNonUTF8GitPath(t *testing.T) {
	store := chat.NewMemoryStore()
	const sessionID = "chat_workspace_non_utf8_path"
	if _, err := store.Create(t.Context(), chat.Session{
		ID: sessionID, AgentID: chat.DefaultAgentID, Workspace: t.TempDir(), Status: "completed",
	}); err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	diff := "diff --git \"a/bad\\377.txt\" \"b/bad\\377.txt\"\n" +
		"--- \"a/bad\\377.txt\"\n" +
		"+++ \"b/bad\\377.txt\"\n" +
		"@@ -1 +1 @@\n-old\n+new\n"
	invalidPath := "bad" + string([]byte{0xff}) + ".txt"
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	handler.SetAgentChatStore(store)
	handler.chatWorkspaceGit = &fixedChatWorkspaceGitRunner{snapshot: gitrunner.DiffSnapshot{
		Diff:     diff,
		Revision: gitrunner.DiffRevision(diff),
		Paths:    []string{invalidPath},
	}}
	client := newAPITestClient(t, NewServer(logger, handler))

	response := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
	if !strings.Contains(response.Body.String(), `"type":"invalid_request"`) || !strings.Contains(response.Body.String(), "cannot be represented safely") {
		t.Fatalf("body = %s, want safe invalid-request refusal", response.Body.String())
	}
}

func TestRevertChatWorkspaceFilesPreservesExactUnusualPaths(t *testing.T) {
	workspace := t.TempDir()
	spacePath := "foo b/bar.txt"
	controlPath := "line\nname\t.txt"
	whitespacePath := " "
	paths := []string{spacePath, controlPath, whitespacePath}
	if err := os.MkdirAll(filepath.Join(workspace, "foo b"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(workspace, filepath.FromSlash(path)), []byte("original\n"), 0o644); err != nil {
			t.Fatalf("write original %q: %v", path, err)
		}
	}
	runTestGit(t, workspace, "add", ".")
	runTestGit(t, workspace, "commit", "-m", "initial")
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(workspace, filepath.FromSlash(path)), []byte("modified\n"), 0o644); err != nil {
			t.Fatalf("modify %q: %v", path, err)
		}
	}

	store := chat.NewMemoryStore()
	const sessionID = "chat_workspace_unusual_paths"
	if _, err := store.Create(t.Context(), chat.Session{
		ID: sessionID, AgentID: chat.DefaultAgentID, Workspace: workspace, Status: "completed",
	}); err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	handler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, handler))
	reviewed := mustRequestJSON[ChatWorkspaceDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
	for _, path := range paths {
		if _, ok := findChatChangedFile(reviewed.Data.Files, path); !ok {
			t.Fatalf("reviewed files = %#v, want exact path %q", reviewed.Data.Files, path)
		}
	}
	workspaceFiles := mustRequestJSON[ChatWorkspaceFilesResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-files", "")
	if file := chatWorkspaceFileByPath(workspaceFiles.Data.Files, whitespacePath); file == nil || file.Status != "modified" {
		t.Fatalf("whitespace-only workspace file = %#v, want exact modified path", file)
	}
	body, err := json.Marshal(RevertChatWorkspaceFilesRequest{
		Paths:            paths,
		ExpectedRevision: reviewed.Data.Revision,
	})
	if err != nil {
		t.Fatalf("Marshal revert request: %v", err)
	}
	response := mustRequestJSON[ChatWorkspaceDiffResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff/revert", string(body))
	if response.Data.HasChanges {
		t.Fatalf("post-revert diff = %+v, want clean workspace", response.Data)
	}
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(path)))
		if err != nil || string(content) != "original\n" {
			t.Fatalf("file %q after revert = %q, err=%v", path, content, err)
		}
	}
}

func TestExternalAgentTurnConflictsWithWorkspaceClosure(t *testing.T) {
	workspace := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	runner := &fakeAgentChatRunner{output: "unexpected"}
	handler.SetAgentChatRunner(runner)
	const sessionID = "external_workspace_closed"
	if _, err := handler.agentChat.Create(t.Context(), chat.Session{
		ID:         sessionID,
		Title:      "External workspace gate",
		AgentID:    "codex",
		DriverKind: agentadapters.DriverKindACP,
		Workspace:  workspace,
		Status:     "idle",
	}); err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	closure, err := handler.workspaceCoordinator.TryClose(t.Context(), workspace)
	if err != nil {
		t.Fatalf("TryClose: %v", err)
	}
	defer closure.Release()

	recorder := performRequest(t, NewServer(logger, handler), http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/messages", `{"content":"hello","execution_mode":"external_agent"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "workspace is temporarily closed") {
		t.Fatalf("body = %s, want workspace closure conflict", recorder.Body.String())
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("external runner requests = %d, want zero", len(runner.runRequests))
	}
	stored, ok, err := handler.agentChat.Get(t.Context(), sessionID)
	if err != nil || !ok {
		t.Fatalf("Get chat: ok=%v err=%v", ok, err)
	}
	if len(stored.Messages) != 0 {
		t.Fatalf("messages after rejected turn = %#v, want none", stored.Messages)
	}
}

func TestTaskPatchRevertConflictsWithOverlappingWorkspaceClosure(t *testing.T) {
	handler, tasks, fixture := newTaskPatchRevertFixture(t, "applied\n", "applied")
	workspace := filepath.Dir(filepath.Dir(fixture.absPath))
	closure, err := handler.workspaceCoordinator.TryClose(t.Context(), filepath.Dir(workspace))
	if err != nil {
		t.Fatalf("TryClose(parent): %v", err)
	}
	defer closure.Release()

	conflict := tasks.mustRequestStatus(http.StatusConflict, http.MethodPost, fixture.revertPath(), "")
	if !strings.Contains(conflict.Body.String(), "workspace is temporarily closed") {
		t.Fatalf("body = %s, want workspace closure conflict", conflict.Body.String())
	}
	if content := string(readTaskPatchFixtureFile(t, fixture)); content != "applied\n" {
		t.Fatalf("patch file after conflict = %q, want unchanged", content)
	}
}
