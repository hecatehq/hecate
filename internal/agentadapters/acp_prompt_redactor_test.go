package agentadapters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/agentcontrols"
)

func TestACPPromptRedactorCoversSplitOutputRawAndActivities(t *testing.T) {
	turn, stage, path, _ := newACPPromptRedactionFixture(t)
	redactor := turn.redactor()
	split := len(path) / 2
	if split < minACPPromptAliasFragmentBytes || len(path)-split < minACPPromptAliasFragmentBytes {
		t.Fatalf("staged path is too short for split test: %q", path)
	}

	var outputs []string
	turn.onOutput = func(value string) { outputs = append(outputs, value) }
	messageID := "00000000-0000-0000-0000-000000000001"
	turn.appendAgentMessageChunk(&acp.SessionUpdateAgentMessageChunk{
		Content:       acp.TextBlock("reading " + path[:split]),
		MessageId:     &messageID,
		SessionUpdate: "agent_message_chunk",
	})
	turn.appendAgentMessageChunk(&acp.SessionUpdateAgentMessageChunk{
		Content:       acp.TextBlock(path[split:]),
		MessageId:     &messageID,
		SessionUpdate: "agent_message_chunk",
	})
	if len(outputs) != 2 {
		t.Fatalf("output callback count = %d, want 2", len(outputs))
	}
	if strings.Contains(outputs[0], path[:split]) || strings.Contains(outputs[1], path) {
		t.Fatalf("split staged path escaped output callbacks: %#v", outputs)
	}

	// A message reset discards the accumulated prefix. The remaining suffix
	// must still be recognized as a leading alias fragment.
	secondID := "00000000-0000-0000-0000-000000000002"
	turn.appendAgentMessageChunk(&acp.SessionUpdateAgentMessageChunk{
		Content:       acp.TextBlock(path[split:]),
		MessageId:     &secondID,
		SessionUpdate: "agent_message_chunk",
	})
	if strings.Contains(outputs[len(outputs)-1], path[split:]) {
		t.Fatalf("stage suffix escaped reset output callback: %q", outputs[len(outputs)-1])
	}

	turn.recordUpdate(acp.SessionNotification{
		SessionId: "session",
		Update:    acp.UpdateAgentMessageText(path[:split]),
	})
	turn.recordUpdate(acp.SessionNotification{
		SessionId: "session",
		Update:    acp.UpdateAgentMessageText(path[split:]),
	})
	_, raw, _ := turn.snapshot()
	if raw != privateACPRawOutputWithheld {
		t.Fatalf("raw output = %q, want fail-closed marker", raw)
	}

	var activities []Activity
	turn.setActivityCallback(func(activity Activity) { activities = append(activities, activity) })
	prefix := path[:len(path)-1]
	turn.emitActivity(Activity{
		ID:              "tool:" + prefix,
		Type:            "tool_call",
		Status:          "running",
		Kind:            "execute",
		Title:           "Read " + prefix,
		Detail:          prefix,
		ArtifactPreview: prefix,
	})
	if len(activities) != 1 {
		t.Fatalf("activity count = %d, want 1", len(activities))
	}
	activityJSON, _ := json.Marshal(activities[0])
	if strings.Contains(string(activityJSON), prefix) {
		t.Fatalf("partial staged path escaped activity: %s", activityJSON)
	}
	if activities[0].Type != "tool_call" || activities[0].Status != "running" || activities[0].Kind != "execute" {
		t.Fatalf("ordinary activity protocol fields were corrupted: %#v", activities[0])
	}

	thoughtID := "00000000-0000-0000-0000-000000000003"
	turn.appendAgentThoughtChunk(&acp.SessionUpdateAgentThoughtChunk{
		Content:       acp.TextBlock(prefix),
		MessageId:     &thoughtID,
		SessionUpdate: "agent_thought_chunk",
	})
	if strings.Contains(activities[len(activities)-1].Detail, prefix) {
		t.Fatalf("partial staged path escaped thought activity: %#v", activities[len(activities)-1])
	}

	turn.clearPromptFiles()
	if turn.promptFiles != nil || turn.redactor() != redactor {
		t.Fatal("clearing prompt bodies discarded or retained the wrong turn state")
	}
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestACPPromptRedactorCoversOneByteAccumulatedMessageStream(t *testing.T) {
	turn, stage, path, _ := newACPPromptRedactionFixture(t)
	messageID := "00000000-0000-0000-0000-000000000004"
	var outputs []string
	turn.onOutput = func(value string) { outputs = append(outputs, value) }

	for index := range len(path) {
		turn.appendAgentMessageChunk(&acp.SessionUpdateAgentMessageChunk{
			Content:       acp.TextBlock(path[index : index+1]),
			MessageId:     &messageID,
			SessionUpdate: "agent_message_chunk",
		})
	}
	if len(outputs) != len(path) {
		t.Fatalf("output callback count = %d, want %d", len(outputs), len(path))
	}
	final := outputs[len(outputs)-1]
	if strings.Contains(final, path) || !strings.Contains(final, privateACPPromptInputRedaction) {
		t.Fatalf("one-byte accumulated stream escaped final redaction: %q", final)
	}
	output, _, _ := turn.snapshot()
	if strings.Contains(output, path) || !strings.Contains(output, privateACPPromptInputRedaction) {
		t.Fatalf("one-byte accumulated stream escaped snapshot redaction: %q", output)
	}

	turn.clearPromptFiles()
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestACPPromptRedactorPreservesOrdinaryOutputAndActivityWithoutStage(t *testing.T) {
	var activities []Activity
	turn := newACPTurn(4096, nil)
	turn.setActivityCallback(func(activity Activity) { activities = append(activities, activity) })
	ordinary := Activity{
		ID:              "tool:terminal-1",
		Type:            "tool_call",
		Status:          "completed",
		Kind:            "execute",
		Title:           "Run tests",
		Detail:          "type and status remain diagnostic",
		ArtifactPreview: "all tests passed",
	}
	turn.emitActivity(ordinary)
	turn.recordUpdate(acp.SessionNotification{SessionId: "session", Update: acp.UpdateAgentMessageText("diagnostic output")})
	output, raw, _ := turn.snapshot()
	if len(activities) != 1 || activities[0] != ordinary {
		t.Fatalf("ordinary activity changed: %#v", activities)
	}
	if output != "diagnostic output" || !strings.Contains(raw, "diagnostic output") {
		t.Fatalf("ordinary output/raw changed: output=%q raw=%q", output, raw)
	}
}

func TestACPPromptRedactorSanitizesApprovalBeforePersistence(t *testing.T) {
	turn, stage, path, uri := newACPPromptRedactionFixture(t)
	store := NewMemoryApprovalStore()
	coordinator := NewApprovalCoordinator(CoordinatorOptions{
		Mode:    ModeAuto,
		Store:   store,
		Timeout: time.Minute,
	})
	client := &acpChatClient{
		sessionID:   "redaction-session",
		adapterID:   "test-adapter",
		workspace:   t.TempDir(),
		coordinator: coordinator,
	}
	client.setTurn(turn)
	defer client.clearTurn(turn)

	kind := acp.ToolKindRead
	status := acp.ToolCallStatusPending
	title := "Read " + path
	request := acp.RequestPermissionRequest{
		SessionId: "native-session",
		Meta:      map[string]any{"private": uri, path: "key"},
		Options: []acp.PermissionOption{{
			OptionId: "allow-once",
			Kind:     acp.PermissionOptionKindAllowOnce,
			Name:     "Allow " + path,
		}},
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "tool-call",
			Kind:       &kind,
			Status:     &status,
			Title:      &title,
			Locations:  []acp.ToolCallLocation{{Path: path}},
			RawInput:   map[string]any{"path": path, "uri": uri},
			RawOutput:  "preview " + path,
		},
	}
	response, err := client.RequestPermission(context.Background(), request)
	if err != nil || response.Outcome.Selected == nil || response.Outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("RequestPermission = %#v, %v", response, err)
	}
	rows, err := store.ListApprovals(context.Background(), "redaction-session", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListApprovals = %#v, %v", rows, err)
	}
	rowJSON, _ := json.Marshal(rows[0])
	assertNoACPPromptAlias(t, turn.redactor(), string(rowJSON))
	if rows[0].ToolName != "Read "+privateACPPromptInputRedaction {
		t.Fatalf("stored tool name = %q", rows[0].ToolName)
	}
	if len(rows[0].ACPOptions) != 1 || strings.Contains(rows[0].ACPOptions[0].Name, path) {
		t.Fatalf("stored approval options = %#v", rows[0].ACPOptions)
	}
	var stored acp.RequestPermissionRequest
	if err := json.Unmarshal(rows[0].ACPPayload, &stored); err != nil || stored.Validate() != nil || stored.ToolCall.Validate() != nil {
		t.Fatalf("stored sanitized ACP payload is not reconstructable: %#v, %v", stored, err)
	}

	request.Options[0].OptionId = acp.PermissionOptionId(path)
	if _, err := client.RequestPermission(context.Background(), request); err == nil {
		t.Fatal("RequestPermission accepted a private path in a protocol identifier")
	}
	rows, _ = store.ListApprovals(context.Background(), "redaction-session", "")
	if len(rows) != 1 {
		t.Fatalf("fail-closed approval was persisted: %#v", rows)
	}

	turn.clearPromptFiles()
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestACPChatClientRedactsDelayedApprovalWithRetainedStageAliases(t *testing.T) {
	turn, stage, path, uri := newACPPromptRedactionFixture(t)
	store := NewMemoryApprovalStore()
	coordinator := NewApprovalCoordinator(CoordinatorOptions{
		Mode:    ModeAuto,
		Store:   store,
		Timeout: time.Minute,
	})
	client := &acpChatClient{
		sessionID:   "delayed-redaction-session",
		adapterID:   "test-adapter",
		workspace:   t.TempDir(),
		coordinator: coordinator,
	}
	client.setTurn(turn)
	client.registerPromptStageNamespace(stage, turn.redactor())
	client.clearTurn(turn)
	turn.clearPromptFiles()
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// A later text-only turn has no turn-local redactor. The session-owned
	// historical set must still sanitize a delayed request before the approval
	// coordinator persists it.
	later := newACPTurn(1<<20, nil)
	client.setTurn(later)
	defer client.clearTurn(later)
	kind := acp.ToolKindRead
	status := acp.ToolCallStatusPending
	title := "Read " + path
	response, err := client.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "native-session",
		Meta:      map[string]any{"private": uri},
		Options: []acp.PermissionOption{{
			OptionId: "allow-once",
			Kind:     acp.PermissionOptionKindAllowOnce,
			Name:     "Allow " + path,
		}},
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "delayed-tool-call",
			Kind:       &kind,
			Status:     &status,
			Title:      &title,
			Locations:  []acp.ToolCallLocation{{Path: path}},
			RawInput:   map[string]any{"path": path, "uri": uri},
		},
	})
	if err != nil || response.Outcome.Selected == nil || response.Outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("RequestPermission = %#v, %v", response, err)
	}
	rows, err := store.ListApprovals(context.Background(), "delayed-redaction-session", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListApprovals = %#v, %v", rows, err)
	}
	rowJSON, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatalf("marshal approval: %v", err)
	}
	assertNoACPPromptAlias(t, turn.redactor(), string(rowJSON))
	if !strings.Contains(string(rowJSON), privateACPPromptInputRedaction) {
		t.Fatalf("delayed approval did not retain a redaction marker: %s", rowJSON)
	}
}

func TestACPPromptRedactorSurvivesLateTerminalWithoutBodies(t *testing.T) {
	turn, stage, path, _ := newACPPromptRedactionFixture(t)
	var activities []Activity
	var closed []string
	turn.setTerminalActivityCallback(func(activity Activity) { activities = append(activities, activity) })
	turn.setTerminalClosedCallback(func(id string) { closed = append(closed, id) })
	activitySink, doneSink, redactor := turn.terminalCallbacks()
	turn.clearPromptFiles()
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if turn.promptFiles != nil || redactor == nil {
		t.Fatal("terminal callback retained bodies or lost its redactor")
	}
	prefix := path[:len(path)-1]
	activitySink(Activity{
		ID:              "terminal:" + path,
		Type:            "terminal",
		Status:          "failed",
		Kind:            "execute",
		Title:           "Run " + prefix,
		Detail:          errors.New("failed at " + path).Error(),
		ArtifactPreview: path,
	})
	doneSink(prefix)
	activityJSON, _ := json.Marshal(activities)
	assertNoACPPromptAlias(t, redactor, string(activityJSON))
	if len(closed) != 1 || strings.Contains(closed[0], prefix) {
		t.Fatalf("late terminal id was not redacted: %#v", closed)
	}

	item := &acpTerminal{output: newACPTerminalOutputBuffer(len(path) / 2), redactor: redactor}
	item.output.append(path)
	preview := terminalOutputPreview(item)
	if strings.Contains(preview, path[len(path)/2:]) {
		t.Fatalf("ring-buffer stage suffix escaped terminal preview: %q", preview)
	}
	item.output = newACPTerminalOutputBuffer(toolOutputPreviewMaxBytes * 2)
	item.output.append(strings.Repeat("x", toolOutputPreviewMaxBytes-len(path)/2) + path)
	preview = terminalOutputPreview(item)
	assertNoACPPromptAlias(t, redactor, preview)
}

func TestACPPromptRedactorSanitizesErrorsAndTruncatedFinalOutput(t *testing.T) {
	turn, stage, path, _ := newACPPromptRedactionFixture(t)
	redactor := turn.redactor()
	prefix := path[:len(path)-1]
	gotErr := redactor.redactError(errors.New("adapter failed at " + prefix))
	if strings.Contains(gotErr.Error(), prefix) || !strings.Contains(gotErr.Error(), privateACPPromptInputRedaction) {
		t.Fatalf("redacted error = %q", gotErr)
	}

	turn.output.limit = int64(len(prefix))
	turn.appendAgentMessageChunk(&acp.SessionUpdateAgentMessageChunk{
		Content:       acp.TextBlock(prefix),
		SessionUpdate: "agent_message_chunk",
	})
	output, _, _ := turn.snapshot()
	if strings.Contains(output, prefix) {
		t.Fatalf("truncated final output exposed staged path prefix: %q", output)
	}
	turn.clearPromptFiles()
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestACPPromptStageNamespaceBlocksWorkspaceFallback(t *testing.T) {
	turn, stage, path, uri := newACPPromptRedactionFixture(t)
	workspace := filepath.Dir(stage.dir)
	clientWorkspace := workspace
	workspaceAlias := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, workspaceAlias); err == nil {
		clientWorkspace = workspaceAlias
	}
	client := &acpChatClient{workspace: clientWorkspace}
	client.setTurn(turn)
	defer client.clearTurn(turn)

	read, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: uri})
	if err != nil || read.Content != "private body" {
		t.Fatalf("exact staged read = %#v, %v", read, err)
	}
	sibling := filepath.Join(stage.dir, "unlisted.txt")
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: sibling}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("unlisted stage sibling did not hit namespace rejection: %v", err)
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: path, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("stage write did not hit namespace rejection: %v", err)
	}
	turn.clearPromptFiles()
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: path}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("cleared exact stage read did not hit namespace rejection: %v", err)
	}
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestACPPromptStageNamespaceSurvivesTurnClearUntilCleanupProof(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonical workspace: %v", err)
	}
	t.Setenv("TMPDIR", workspace)
	t.Setenv("TMP", workspace)
	t.Setenv("TEMP", workspace)
	file := promptTestFile("private.txt", "text/plain", []byte("private body"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	turn := newACPTurn(1<<20, nil)
	if err := turn.setPromptFiles(stage.files); err != nil {
		t.Fatalf("setPromptFiles: %v", err)
	}
	client := &acpChatClient{workspace: workspace}
	client.setTurn(turn)
	client.registerPromptStageNamespace(stage, turn.redactor())
	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	relativePath, err := filepath.Rel(workspace, path)
	if err != nil {
		t.Fatalf("relative staged path: %v", err)
	}

	stage.quarantineStage = func(string, *acpPromptStageIdentity) error {
		return errors.New("forced quarantine failure")
	}
	stage.waitForRetry = func(time.Duration) {}
	if err := stage.cleanup(); err == nil {
		t.Fatal("forced cleanup unexpectedly succeeded")
	}
	turn.clearPromptFiles()
	client.clearTurn(turn)
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: path}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("retained stage read escaped namespace fence: %v", err)
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: path, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("retained stage write escaped namespace fence: %v", err)
	}
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: relativePath}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("relative retained stage read escaped namespace fence: %v", err)
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: relativePath, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("relative retained stage write escaped namespace fence: %v", err)
	}

	stage.quarantineStage = nil
	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup proof: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create replacement stage directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("ordinary workspace file"), 0o600); err != nil {
		t.Fatalf("create replacement workspace file: %v", err)
	}
	read, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: path})
	if err != nil || read.Content != "ordinary workspace file" {
		t.Fatalf("post-proof workspace read = %#v, %v", read, err)
	}
	read, err = client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: relativePath})
	if err != nil || read.Content != "ordinary workspace file" {
		t.Fatalf("post-proof relative workspace read = %#v, %v", read, err)
	}
}

func TestACPPromptStageNamespaceFencesQuarantineDuringConcurrentCleanup(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonical workspace: %v", err)
	}
	t.Setenv("TMPDIR", workspace)
	t.Setenv("TMP", workspace)
	t.Setenv("TEMP", workspace)
	file := promptTestFile("private.txt", "text/plain", []byte("private body"))
	_, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	turn := newACPTurn(1<<20, nil)
	if err := turn.setPromptFiles(stage.files); err != nil {
		t.Fatalf("setPromptFiles: %v", err)
	}
	var originalPath string
	for path := range stage.files {
		originalPath = path
	}
	if originalPath == "" {
		t.Fatal("staged prompt path is empty")
	}
	clientWorkspace := workspace
	workspaceAlias := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, workspaceAlias); err == nil {
		clientWorkspace = workspaceAlias
	}
	client := &acpChatClient{workspace: clientWorkspace}
	client.setTurn(turn)
	client.registerPromptStageNamespace(stage, turn.redactor())
	stage.prepareStage = func(*acpPromptStageIdentity) error {
		return errors.New("forced post-quarantine failure")
	}
	stage.waitForRetry = func(time.Duration) {}
	if err := stage.cleanup(); err == nil {
		t.Fatal("forced quarantined cleanup unexpectedly succeeded")
	}
	quarantineDir := currentPrivateACPPromptStageDirectory(stage.identity)
	if quarantineDir == "" || filepath.Base(quarantineDir) == filepath.Base(stage.dir) {
		t.Fatalf("stage was not quarantined: original=%q current=%q", stage.dir, quarantineDir)
	}
	quarantinePath := filepath.Join(quarantineDir, stage.fileNames[0])
	relativeQuarantinePath, err := filepath.Rel(workspace, quarantinePath)
	if err != nil {
		t.Fatalf("relative quarantine path: %v", err)
	}
	turn.clearPromptFiles()
	client.clearTurn(turn)
	originalRelativePath, err := filepath.Rel(workspace, originalPath)
	if err != nil {
		t.Fatalf("relative original stage path: %v", err)
	}
	originalAliasPath := filepath.Join(clientWorkspace, originalRelativePath)
	for _, alias := range []string{originalAliasPath, stagedFileURI(originalAliasPath)} {
		if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: alias}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
			t.Fatalf("quarantined original alias read escaped namespace fence for %q: %v", alias, err)
		}
		if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: alias, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("quarantined original alias write escaped namespace fence for %q: %v", alias, err)
		}
	}
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: quarantinePath}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("quarantined stage read escaped namespace fence: %v", err)
	}
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: relativeQuarantinePath}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("relative quarantined stage read escaped namespace fence: %v", err)
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: relativeQuarantinePath, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("relative quarantined stage write escaped namespace fence: %v", err)
	}

	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				_ = client.containsPromptStagePath(quarantinePath)
			}
		}
	}()
	<-started
	stage.prepareStage = nil
	if err := stage.cleanup(); err != nil {
		close(stop)
		<-done
		t.Fatalf("cleanup proof: %v", err)
	}
	close(stop)
	<-done

	if err := os.MkdirAll(quarantineDir, 0o700); err != nil {
		t.Fatalf("create replacement quarantine directory: %v", err)
	}
	if err := os.WriteFile(quarantinePath, []byte("ordinary workspace file"), 0o600); err != nil {
		t.Fatalf("create replacement quarantine file: %v", err)
	}
	read, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: relativeQuarantinePath})
	if err != nil || read.Content != "ordinary workspace file" {
		t.Fatalf("post-proof quarantine read = %#v, %v", read, err)
	}
}

func TestACPPromptStageNamespaceSerializesRegistrationWithWorkspaceFallback(t *testing.T) {
	workspace := t.TempDir()
	client := &acpChatClient{workspace: workspace}
	stage := &acpPromptStage{dir: filepath.Join(workspace, ".hecate-acp-stage-existing")}
	client.registerPromptStageNamespace(stage, nil)
	if stage.namespace == nil {
		t.Fatal("prompt stage namespace was not registered")
	}

	quarantineDir := filepath.Join(workspace, ".hecate-acp-cleanup-pending")
	quarantinePath := filepath.Join(quarantineDir, "input-1-private.txt")
	fallbackStarted := make(chan struct{})
	fallbackRelease := make(chan struct{})
	fallbackDone := make(chan error, 1)
	go func() {
		fallbackDone <- client.withPromptStageWorkspaceFallback(quarantinePath, "denied", func() error {
			close(fallbackStarted)
			<-fallbackRelease
			return nil
		})
	}()
	select {
	case <-fallbackStarted:
	case <-time.After(time.Second):
		t.Fatal("workspace fallback did not start")
	}

	registrationStarted := make(chan struct{})
	registrationDone := make(chan struct{})
	go func() {
		close(registrationStarted)
		stage.namespace.registerDirectory(quarantineDir)
		close(registrationDone)
	}()
	<-registrationStarted
	select {
	case <-registrationDone:
		close(fallbackRelease)
		t.Fatal("quarantine alias crossed an in-flight workspace fallback")
	case <-time.After(100 * time.Millisecond):
	}
	close(fallbackRelease)
	select {
	case err := <-fallbackDone:
		if err != nil {
			t.Fatalf("workspace fallback: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace fallback did not finish")
	}
	select {
	case <-registrationDone:
	case <-time.After(time.Second):
		t.Fatal("quarantine alias registration did not finish")
	}

	if !client.containsPromptStagePath(quarantinePath) {
		t.Fatal("registered quarantine alias is not denied")
	}
	if _, err := client.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: quarantinePath}); err == nil || !strings.Contains(err.Error(), "staged prompt input is not available") {
		t.Fatalf("read crossed registered quarantine alias: %v", err)
	}
	if _, err := client.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: quarantinePath, Content: "overwrite"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("write crossed registered quarantine alias: %v", err)
	}
}

func TestACPChatClientRedactsTypedControlUpdatesAcrossRetainedStage(t *testing.T) {
	turn, stage, path, uri := newACPPromptRedactionFixture(t)
	var commands []agentcontrols.Command
	var options []agentcontrols.ConfigOption
	commandUpdates := 0
	configUpdates := 0
	client := &acpChatClient{
		onAvailableCommands: func(update []agentcontrols.Command) {
			commandUpdates++
			commands = update
		},
		onConfigOptions: func(update []agentcontrols.ConfigOption) {
			configUpdates++
			options = update
		},
	}
	client.setTurn(turn)
	client.registerPromptStageNamespace(stage, turn.redactor())

	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: acp.SessionId("session"),
		Update: acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update",
			AvailableCommands: []acp.AvailableCommand{
				{
					Name:        "inspect",
					Description: "Inspect " + path,
					Input: &acp.AvailableCommandInput{Unstructured: &acp.UnstructuredCommandInput{
						Hint: "source " + uri,
					}},
				},
				{Name: path, Description: "private identifier"},
			},
		}},
	}); err != nil {
		t.Fatalf("command SessionUpdate: %v", err)
	}
	if commandUpdates != 1 || len(commands) != 1 || commands[0].Name != "inspect" {
		t.Fatalf("command updates = %d, %#v", commandUpdates, commands)
	}
	if strings.Contains(commands[0].Description, path) || strings.Contains(commands[0].InputHint, uri) ||
		!strings.Contains(commands[0].Description, privateACPPromptInputRedaction) ||
		!strings.Contains(commands[0].InputHint, privateACPPromptInputRedaction) {
		t.Fatalf("typed command metadata was not redacted: %#v", commands[0])
	}

	// A retained namespace must keep protecting durable metadata after the
	// originating turn is gone, including while a later turn could be active.
	client.clearTurn(turn)
	description := "Select from " + uri
	itemDescription := "Reads " + path
	values := acp.SessionConfigSelectOptionsUngrouped{
		{Value: acp.SessionConfigValueId("safe"), Name: "Safe " + path, Description: &itemDescription},
	}
	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: acp.SessionId("session"),
		Update: acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: []acp.SessionConfigOption{
				{Select: &acp.SessionConfigOptionSelect{
					Id:           acp.SessionConfigId("mode"),
					Name:         "Mode " + path,
					Description:  &description,
					CurrentValue: acp.SessionConfigValueId("safe"),
					Options:      acp.SessionConfigSelectOptions{Ungrouped: &values},
				}},
				{Select: &acp.SessionConfigOptionSelect{
					Id:           acp.SessionConfigId(uri),
					Name:         "private identifier",
					CurrentValue: acp.SessionConfigValueId("safe"),
					Options:      acp.SessionConfigSelectOptions{Ungrouped: &values},
				}},
			},
		}},
	}); err != nil {
		t.Fatalf("config SessionUpdate: %v", err)
	}
	if configUpdates != 1 || len(options) != 1 || options[0].ID != "mode" || options[0].CurrentValue != "safe" ||
		len(options[0].Options) != 1 || options[0].Options[0].Value != "safe" {
		t.Fatalf("config updates = %d, %#v", configUpdates, options)
	}
	encoded, err := json.Marshal(options[0])
	if err != nil {
		t.Fatalf("marshal config option: %v", err)
	}
	if strings.Contains(string(encoded), path) || strings.Contains(string(encoded), uri) || !strings.Contains(string(encoded), privateACPPromptInputRedaction) {
		t.Fatalf("typed config metadata was not redacted: %s", encoded)
	}

	// If every item has an unsafe identifier, publish the authoritative filtered
	// empty list rather than retaining stale metadata the peer no longer advertises.
	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: acp.SessionId("session"),
		Update: acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			SessionUpdate:     "available_commands_update",
			AvailableCommands: []acp.AvailableCommand{{Name: uri}},
		}},
	}); err != nil {
		t.Fatalf("unsafe command SessionUpdate: %v", err)
	}
	if commandUpdates != 2 || commands == nil || len(commands) != 0 {
		t.Fatalf("all-dropped command update = %d, %#v", commandUpdates, commands)
	}

	unsafeValues := acp.SessionConfigSelectOptionsUngrouped{
		{Value: acp.SessionConfigValueId(uri), Name: "private identifier"},
	}
	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: acp.SessionId("session"),
		Update: acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
				Id:           acp.SessionConfigId("unsafe-mode"),
				Name:         "Unsafe mode",
				CurrentValue: acp.SessionConfigValueId(uri),
				Options:      acp.SessionConfigSelectOptions{Ungrouped: &unsafeValues},
			}}},
		}},
	}); err != nil {
		t.Fatalf("unsafe config SessionUpdate: %v", err)
	}
	if configUpdates != 2 || options == nil || len(options) != 0 {
		t.Fatalf("all-dropped config update = %d, %#v", configUpdates, options)
	}

	if err := stage.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if client.containsPromptStagePath(path) {
		t.Fatal("cleanup proof retained the filesystem namespace")
	}
	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: acp.SessionId("session"),
		Update: acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update",
			AvailableCommands: []acp.AvailableCommand{{
				Name:        "delayed",
				Description: "Delayed " + path,
			}},
		}},
	}); err != nil {
		t.Fatalf("post-cleanup command SessionUpdate: %v", err)
	}
	if commandUpdates != 3 || len(commands) != 1 || commands[0].Name != "delayed" ||
		strings.Contains(commands[0].Description, path) || !strings.Contains(commands[0].Description, privateACPPromptInputRedaction) {
		t.Fatalf("post-cleanup command update = %d, %#v", commandUpdates, commands)
	}

	later := newACPTurn(1<<20, nil)
	client.setTurn(later)
	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: acp.SessionId("session"),
		Update: acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
				Id:           acp.SessionConfigId("mode"),
				Name:         "Delayed " + uri,
				CurrentValue: acp.SessionConfigValueId("safe"),
				Options:      acp.SessionConfigSelectOptions{Ungrouped: &values},
			}}},
		}},
	}); err != nil {
		t.Fatalf("later-turn config SessionUpdate: %v", err)
	}
	if configUpdates != 3 || len(options) != 1 || options[0].ID != "mode" || strings.Contains(options[0].Name, uri) {
		t.Fatalf("later-turn config update = %d, %#v", configUpdates, options)
	}
	_, raw, _ := later.snapshot()
	if raw != "" {
		t.Fatalf("later turn retained unsanitized typed control wire data: %q", raw)
	}
	client.clearTurn(later)
}

func TestACPPromptStageCleanupDoesNotMutateSubstitution(t *testing.T) {
	turn, stage, _, _ := newACPPromptRedactionFixture(t)
	turn.clearPromptFiles()
	allowACPPromptStageSubstitutionForTest(t, stage)
	originalPath := stage.dir
	movedPath := originalPath + "-moved"
	if err := os.Rename(originalPath, movedPath); err != nil {
		t.Fatalf("rename original stage: %v", err)
	}
	if err := os.Mkdir(originalPath, 0o755); err != nil {
		t.Fatalf("create replacement stage: %v", err)
	}
	sentinel := filepath.Join(originalPath, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("replacement"), 0o644); err != nil {
		t.Fatalf("write replacement sentinel: %v", err)
	}
	replacementInfo, err := os.Stat(originalPath)
	if err != nil {
		t.Fatalf("stat replacement stage: %v", err)
	}
	if err := stage.cleanup(); err == nil {
		t.Fatal("cleanup accepted substituted stage path")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("cleanup mutated replacement sentinel: %q, %v", data, err)
	}
	info, err := os.Stat(originalPath)
	if err != nil || info.Mode().Perm() != replacementInfo.Mode().Perm() {
		t.Fatalf("cleanup mutated replacement mode: %#v, %v", info, err)
	}
	if stage.identity == nil {
		t.Fatal("failed cleanup discarded retryable protected identity")
	}
	if err := os.RemoveAll(originalPath); err != nil {
		t.Fatalf("remove replacement: %v", err)
	}
	if err := os.Rename(movedPath, originalPath); err != nil {
		t.Fatalf("restore original stage: %v", err)
	}
	if err := stage.cleanup(); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
}

func newACPPromptRedactionFixture(t *testing.T) (*acpTurn, *acpPromptStage, string, string) {
	t.Helper()
	file := promptTestFile("private.txt", "text/plain", []byte("private body"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err != nil {
		t.Fatalf("buildACPPrompt: %v", err)
	}
	if stage == nil || len(blocks) != 1 || blocks[0].ResourceLink == nil {
		t.Fatalf("staged prompt fixture = %#v, %#v", blocks, stage)
	}
	turn := newACPTurn(1<<20, nil)
	if err := turn.setPromptFiles(stage.files); err != nil {
		_ = stage.cleanup()
		t.Fatalf("setPromptFiles: %v", err)
	}
	t.Cleanup(func() {
		turn.clearPromptFiles()
		_ = stage.cleanup()
	})
	path := cleanACPReadPath(blocks[0].ResourceLink.Uri)
	return turn, stage, path, blocks[0].ResourceLink.Uri
}

func assertNoACPPromptAlias(t *testing.T, redactor *acpPromptRedactor, value string) {
	t.Helper()
	if safe := redactor.redact(value); safe != value {
		t.Fatalf("value contains private prompt-stage alias:\n%s", value)
	}
}
