package acpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/acp-adapter-kit/acptest"
)

func TestAgentCreatesTaskAndMapsRuntimeEvents(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events: map[string][]RunEvent{
			"run_1": {
				{Sequence: 1, Type: "assistant.text_complete", Data: map[string]any{"text": "I will inspect the workspace."}},
				{Sequence: 2, Type: "assistant.tool_call_proposed", Data: map[string]any{"tool_call_id": "call_1", "tool_name": "shell_exec"}},
				{Sequence: 3, Type: "tool.started", Data: map[string]any{"tool_call_id": "call_1"}},
				{Sequence: 4, Type: "tool.completed", Data: map[string]any{"tool_call_id": "call_1"}},
				{Sequence: 5, Type: "assistant.final_answer", Data: map[string]any{"summary": "This must not be duplicated."}},
				{Sequence: 6, Type: "run.finished", Data: map[string]any{}},
			},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())

	initialize := client.Request("initialize", "initialize", map[string]any{"protocolVersion": 1}, time.Second)
	if len(initialize) != 1 || initialize[0].Error != nil {
		t.Fatalf("initialize responses = %#v", initialize)
	}
	var initialized struct {
		AgentCapabilities struct {
			PromptCapabilities struct {
				Image           bool `json:"image"`
				Audio           bool `json:"audio"`
				EmbeddedContext bool `json:"embeddedContext"`
			} `json:"promptCapabilities"`
			SessionCapabilities struct {
				Close map[string]any `json:"close"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	initialize[0].ResultInto(t, &initialized)
	if initialized.AgentCapabilities.PromptCapabilities.Image || initialized.AgentCapabilities.PromptCapabilities.Audio || initialized.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Fatalf("rich prompt capabilities = %#v, want all false", initialized.AgentCapabilities.PromptCapabilities)
	}
	if initialized.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Fatal("session close capability was not advertised")
	}

	sessionID := createTestSession(t, client, "/workspace/hecate")
	responses := client.PromptText("prompt", sessionID, "Inspect this repository", time.Second)
	assertPromptResponse(t, responses, "prompt", "end_turn")

	var notifications []acptest.Response
	for _, response := range responses {
		if response.Method == "session/update" {
			notifications = append(notifications, response)
		}
	}
	if len(notifications) != 4 {
		t.Fatalf("session/update count = %d, want 4: %#v", len(notifications), responses)
	}
	var firstUpdate struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	notifications[0].ParamsInto(t, &firstUpdate)
	if firstUpdate.SessionID != sessionID || firstUpdate.Update.SessionUpdate != "agent_message_chunk" || firstUpdate.Update.Content.Text != "I will inspect the workspace." {
		t.Fatalf("first ACP update = %#v", firstUpdate)
	}
	if strings.Contains(string(notifications[0].Params), "This must not be duplicated") {
		t.Fatalf("final summary was duplicated in ACP update: %s", notifications[0].Params)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.created) != 1 {
		t.Fatalf("created tasks = %#v, want one", runtime.created)
	}
	created := runtime.created[0]
	if created.Title != "ACP session" || created.Prompt != "Inspect this repository" || created.WorkingDirectory != "/workspace/hecate" {
		t.Fatalf("created task = %#v", created)
	}
}

func TestAgentExplainsWhenHecateAwaitsApproval(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{events: map[string][]RunEvent{
		"run_1": {
			{Sequence: 1, Type: "approval.requested", Data: map[string]any{}},
			{Sequence: 2, Type: "run.awaiting_approval", Data: map[string]any{}},
			{Sequence: 3, Type: "assistant.text_complete", Data: map[string]any{"text": "approved work completed"}},
			{Sequence: 4, Type: "run.finished", Data: map[string]any{}},
		},
	}}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	responses := client.PromptText("prompt", sessionID, "inspect", time.Second)
	assertPromptResponse(t, responses, "prompt", "end_turn")
	approvalUpdates := 0
	for _, response := range responses {
		if response.Method != "session/update" || !strings.Contains(string(response.Params), approvalWaitMessage) {
			continue
		}
		approvalUpdates++
	}
	if approvalUpdates != 1 {
		t.Fatalf("approval guidance updates = %d, want one; responses=%#v", approvalUpdates, responses)
	}
}

func TestAgentRejectsUnsupportedEditorInputsWithoutLeakingResourceURI(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events: map[string][]RunEvent{
			"run_1": {
				{Sequence: 1, Type: "run.finished", Data: map[string]any{}},
			},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)

	relative := client.Request("relative", "session/new", map[string]any{"cwd": "relative", "mcpServers": []any{}}, time.Second)
	if len(relative) != 1 || relative[0].Error == nil || relative[0].Error.Code != -32602 {
		t.Fatalf("relative cwd responses = %#v", relative)
	}
	mcp := client.Request("mcp", "session/new", map[string]any{"cwd": "/workspace", "mcpServers": []map[string]any{{"name": "tool"}}}, time.Second)
	if len(mcp) != 1 || mcp[0].Error == nil || !strings.Contains(mcp[0].Error.Message, "unsupported") {
		t.Fatalf("MCP session responses = %#v", mcp)
	}

	sessionID := createTestSession(t, client, "/workspace")
	image := client.Request("image", "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "image", "data": "abc", "mimeType": "image/png"}},
	}, time.Second)
	if len(image) != 1 || image[0].Error == nil || image[0].Error.Code != -32602 {
		t.Fatalf("image prompt responses = %#v", image)
	}

	const secretURI = "file:///private/secret-plan.txt"
	resources := client.Request("resource", "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "resource_link",
			"name": "secret plan",
			"uri":  secretURI,
		}},
	}, time.Second)
	assertPromptResponse(t, resources, "resource", "end_turn")

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.created) != 1 {
		t.Fatalf("created tasks = %#v, want one", runtime.created)
	}
	if strings.Contains(runtime.created[0].Prompt, secretURI) {
		t.Fatalf("resource URI leaked into Hecate task prompt: %q", runtime.created[0].Prompt)
	}
	if !strings.Contains(runtime.created[0].Prompt, "secret plan") {
		t.Fatalf("opaque resource label missing from task prompt: %q", runtime.created[0].Prompt)
	}
}

func TestAgentCancellationUsesFreshContextAndSuppressesLateOutput(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events:         map[string][]RunEvent{},
		cancelContexts: make(chan error, 8),
		blockEventPoll: true,
		pollStarted:    make(chan struct{}, 1),
		cancelled: map[string][]RunEvent{
			"run_1": {
				{Sequence: 1, Type: "assistant.text_complete", Data: map[string]any{"text": "late output"}},
				{Sequence: 2, Type: "run.cancelled", Data: map[string]any{}},
			},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(map[string]any{
		"jsonrpc": "2.0",
		"id":      "prompt",
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "keep working"}},
		},
	})
	select {
	case <-runtime.pollStarted:
	case <-time.After(time.Second):
		t.Fatal("prompt did not enter the event poll")
	}
	client.Notify("session/cancel", map[string]any{"sessionId": sessionID})
	responses := client.CollectUntilResponse("prompt", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "cancelled")
	for _, response := range responses {
		if strings.Contains(string(response.Params), "late output") {
			t.Fatalf("cancelled turn forwarded late output: %#v", responses)
		}
	}

	select {
	case cancelledContext := <-runtime.cancelContexts:
		if cancelledContext != nil {
			t.Fatalf("CancelRun received a cancelled context: %v", cancelledContext)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelRun was not called")
	}
}

func TestAgentRequestCancellationCancelsNativeRun(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events:         map[string][]RunEvent{},
		blockEventPoll: true,
		pollStarted:    make(chan struct{}, 1),
		cancelContexts: make(chan error, 1),
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 1, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(promptEnvelope("prompt", sessionID, "keep working"))
	select {
	case <-runtime.pollStarted:
	case <-time.After(time.Second):
		t.Fatal("prompt did not enter the event poll")
	}
	client.Notify("$/cancel_request", map[string]any{"requestId": "prompt"})

	responses := client.CollectUntilResponse("prompt", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "cancelled")
	select {
	case cancelledContext := <-runtime.cancelContexts:
		if cancelledContext != nil {
			t.Fatalf("CancelRun received a cancelled context: %v", cancelledContext)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not cancel the active Hecate run")
	}
}

func TestAgentRetriesTransientNativeCancellation(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events:  map[string][]RunEvent{},
		started: make(chan struct{}, 1),
		cancelErrors: []error{
			errors.New("temporary local runtime failure"),
		},
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 1, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(promptEnvelope("prompt", sessionID, "keep working"))
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start a Hecate run")
	}
	client.Notify("session/cancel", map[string]any{"sessionId": sessionID})
	responses := client.CollectUntilResponse("prompt", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "cancelled")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		cancelCalls := runtime.cancelCalls
		runtime.mu.Unlock()
		if cancelCalls >= 2 {
			session := agent.lookupSession(sessionID)
			if session != nil {
				session.mu.Lock()
				active := session.active
				session.mu.Unlock()
				if active == nil {
					return
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	runtime.mu.Lock()
	cancelCalls := runtime.cancelCalls
	runtime.mu.Unlock()
	t.Fatalf("native cancellation calls = %d and active turn did not settle; want retry then terminal settlement", cancelCalls)
}

func TestAgentRetiresSessionWhenNativeCancellationCannotBeConfirmed(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events:    map[string][]RunEvent{},
		started:   make(chan struct{}, 1),
		cancelErr: errors.New("runtime remains unavailable"),
	}
	agent, err := NewAgent(runtime, Config{
		Version:        "test",
		PollInterval:   time.Millisecond,
		RequestTimeout: time.Second,
		CancelTimeout:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(promptEnvelope("prompt", sessionID, "keep working"))
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start a Hecate run")
	}
	client.Notify("session/cancel", map[string]any{"sessionId": sessionID})
	responses := client.CollectUntilResponse("prompt", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "cancelled")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if agent.lookupSession(sessionID) == nil {
			runtime.mu.Lock()
			cancelCalls := runtime.cancelCalls
			runtime.mu.Unlock()
			if cancelCalls < 2 {
				t.Fatalf("native cancellation calls = %d, want retries before session retirement", cancelCalls)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("unconfirmed native cancellation did not retire the ACP session")
}

func TestAgentCloseCancelsActiveRun(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{
		events:         map[string][]RunEvent{},
		started:        make(chan struct{}, 1),
		cancelContexts: make(chan error, 8),
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 1, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(map[string]any{
		"jsonrpc": "2.0",
		"id":      "prompt",
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "keep working"}},
		},
	})
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start a Hecate run")
	}
	closed := client.Request("close", "session/close", map[string]any{"sessionId": sessionID}, time.Second)
	closeResponse := responseForID(closed, "close")
	if closeResponse == nil || closeResponse.Error != nil {
		t.Fatalf("close response = %#v", closed)
	}

	select {
	case <-runtime.cancelContexts:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel active Hecate run")
	}
}

func TestAgentCancellationDuringTaskCreationDoesNotStartRun(t *testing.T) {
	t.Parallel()

	releaseCreate := make(chan struct{})
	runtime := &fakeRuntime{
		createStarted: make(chan struct{}, 1),
		releaseCreate: releaseCreate,
		started:       make(chan struct{}, 1),
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(promptEnvelope("prompt", sessionID, "create then stop"))
	select {
	case <-runtime.createStarted:
	case <-time.After(time.Second):
		t.Fatal("prompt did not enter CreateTask")
	}
	client.Notify("session/cancel", map[string]any{"sessionId": sessionID})
	waitForActiveCancellation(t, agent, sessionID)
	close(releaseCreate)

	responses := client.CollectUntilResponse("prompt", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "cancelled")
	select {
	case <-runtime.started:
		t.Fatal("cancelled prompt started a Hecate run after CreateTask returned")
	case <-time.After(40 * time.Millisecond):
	}
}

func TestAgentCancellationDuringTaskStartCancelsReturnedRun(t *testing.T) {
	t.Parallel()

	releaseStart := make(chan struct{})
	runtime := &fakeRuntime{
		startStarted:   make(chan struct{}, 1),
		releaseStart:   releaseStart,
		cancelContexts: make(chan error, 1),
		cancelled: map[string][]RunEvent{
			"run_1": {{Sequence: 1, Type: "run.cancelled", Data: map[string]any{}}},
		},
	}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	client.Write(promptEnvelope("prompt", sessionID, "start then stop"))
	select {
	case <-runtime.startStarted:
	case <-time.After(time.Second):
		t.Fatal("prompt did not enter StartTask")
	}
	client.Notify("session/cancel", map[string]any{"sessionId": sessionID})
	waitForActiveCancellation(t, agent, sessionID)
	close(releaseStart)

	responses := client.CollectUntilResponse("prompt", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "cancelled")
	select {
	case cancelledContext := <-runtime.cancelContexts:
		if cancelledContext != nil {
			t.Fatalf("CancelRun received a cancelled context: %v", cancelledContext)
		}
	case <-time.After(time.Second):
		t.Fatal("returned run was not cancelled after an in-flight StartTask")
	}
}

func TestAgentRejectsDifferentPromptAfterInitialStartFailure(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{startErr: errors.New("runtime temporarily unavailable")}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	first := client.PromptText("first", sessionID, "first prompt", time.Second)
	firstResponse := responseForID(first, "first")
	if firstResponse == nil || firstResponse.Error == nil {
		t.Fatalf("first prompt response = %#v, want runtime error", first)
	}
	second := client.PromptText("second", sessionID, "different prompt", time.Second)
	secondResponse := responseForID(second, "second")
	if secondResponse == nil || secondResponse.Error == nil || !strings.Contains(secondResponse.Error.Message, "start Hecate task run") {
		t.Fatalf("second prompt response = %#v, want safe start error", second)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.created) != 1 || runtime.created[0].Prompt != "first prompt" {
		t.Fatalf("created tasks = %#v, want only the original prompt", runtime.created)
	}
	if runtime.startCalls != 1 {
		t.Fatalf("StartTask calls = %d, want one; a different prompt must not start the stale task", runtime.startCalls)
	}
}

func TestAgentBoundsModelDerivedToolUpdates(t *testing.T) {
	t.Parallel()

	longID := strings.Repeat("tool-id-", maxToolFieldBytes)
	longName := strings.Repeat("tool-name-", maxToolFieldBytes)
	runtime := &fakeRuntime{events: map[string][]RunEvent{
		"run_1": {
			{Sequence: 1, Type: "assistant.tool_call_proposed", Data: map[string]any{"tool_call_id": longID, "tool_name": longName}},
			{Sequence: 2, Type: "run.finished", Data: map[string]any{}},
		},
	}}
	agent := newTestAgent(t, runtime)
	client := acptest.NewLiveClient(t, agent.Server())
	initializeTestAgent(t, client)
	sessionID := createTestSession(t, client, "/workspace")

	responses := client.PromptText("prompt", sessionID, "inspect", 2*time.Second)
	assertPromptResponse(t, responses, "prompt", "end_turn")
	var update struct {
		Update struct {
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
		} `json:"update"`
	}
	for _, response := range responses {
		if response.Method == "session/update" {
			response.ParamsInto(t, &update)
			break
		}
	}
	if !strings.HasPrefix(update.Update.ToolCallID, "hecate_tool_") || len(update.Update.ToolCallID) > maxToolFieldBytes {
		t.Fatalf("bounded tool call id = %q", update.Update.ToolCallID)
	}
	if len(update.Update.Title) > maxToolFieldBytes {
		t.Fatalf("bounded tool title length = %d, max %d", len(update.Update.Title), maxToolFieldBytes)
	}
}

func TestAgentBoundsClientControlledErrorDetails(t *testing.T) {
	t.Parallel()

	secret := strings.Repeat("\x00private", maxRPCErrorDataBytes)
	unknown := unknownSession(secret)
	if unknown.Data != nil {
		t.Fatalf("unknown session error reflected client identifier: %#v", unknown.Data)
	}

	unsupported, err := promptText([]promptBlock{{Type: secret}})
	if err == nil || unsupported != "" {
		t.Fatalf("promptText unsupported type = %q, %v", unsupported, err)
	}
	if strings.Contains(err.Error(), "private") || len(err.Error()) > maxRPCErrorDataBytes {
		t.Fatalf("unsupported-type error leaked or exceeded bound: %q", err)
	}

	unsupported, err = promptText([]promptBlock{{Type: "image" + strings.Repeat(" private", maxRPCErrorDataBytes)}})
	if err == nil || unsupported != "" {
		t.Fatalf("promptText padded media type = %q, %v", unsupported, err)
	}
	if strings.Contains(err.Error(), "private") || len(err.Error()) > maxRPCErrorDataBytes {
		t.Fatalf("media-type error leaked or exceeded bound: %q", err)
	}

	rpcErr := invalidParams(errors.New(secret))
	detail, _ := rpcErr.Data.(string)
	if len(detail) > maxRPCErrorDataBytes {
		t.Fatalf("invalid-params data length = %d, want <= %d", len(detail), maxRPCErrorDataBytes)
	}
}

func TestAgentDoesNotCancelTurnAfterTerminalEvent(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{cancelContexts: make(chan error, 1)}
	agent := newTestAgent(t, runtime)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active := &activeTurn{
		taskID: "task_1",
		runID:  "run_1",
		ctx:    ctx,
		cancel: cancel,
	}
	session := &session{id: "session_1", active: active}

	// A terminal event removes the active turn before a request-context watcher
	// may observe the same request being cancelled. That late observation must
	// not create a detached native cancellation controller for a finished run.
	agent.finishTurn(session, active)
	agent.cancelTurn(session, active, "late ACP request cancellation")

	if active.cancelled() {
		t.Fatal("late cancellation cancelled a terminal turn")
	}
	runtime.mu.Lock()
	cancelCalls := runtime.cancelCalls
	runtime.mu.Unlock()
	if cancelCalls != 0 {
		t.Fatalf("CancelRun calls = %d, want 0 for a terminal turn", cancelCalls)
	}
}

func promptEnvelope(id, sessionID, text string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": text}},
		},
	}
}

func waitForActiveCancellation(t testing.TB, agent *Agent, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session := agent.lookupSession(sessionID)
		if session != nil {
			session.mu.Lock()
			active := session.active
			cancelled := active != nil && active.cancelled()
			session.mu.Unlock()
			if cancelled {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("session/cancel was not applied to the active turn")
}

func newTestAgent(t testing.TB, runtime Runtime) *Agent {
	t.Helper()
	agent, err := NewAgent(runtime, Config{
		Version:        "test",
		PollInterval:   2 * time.Millisecond,
		RequestTimeout: time.Second,
		CancelTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return agent
}

func initializeTestAgent(t testing.TB, client *acptest.LiveClient) {
	t.Helper()
	responses := client.Request("initialize", "initialize", map[string]any{"protocolVersion": 1}, time.Second)
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("initialize response = %#v", responses)
	}
}

func createTestSession(t testing.TB, client *acptest.LiveClient, cwd string) string {
	t.Helper()
	responses := client.Request("new", "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, time.Second)
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("new session response = %#v", responses)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	responses[0].ResultInto(t, &result)
	if result.SessionID == "" {
		t.Fatal("session/new returned empty sessionId")
	}
	return result.SessionID
}

func assertPromptResponse(t testing.TB, responses []acptest.Response, id, wantStopReason string) {
	t.Helper()
	response := responseForID(responses, id)
	if response == nil {
		t.Fatalf("no prompt response for %q in %#v", id, responses)
	}
	if response.Error != nil {
		t.Fatalf("prompt response error = %#v", response.Error)
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	response.ResultInto(t, &result)
	if result.StopReason != wantStopReason {
		t.Fatalf("stopReason = %q, want %q; responses=%#v", result.StopReason, wantStopReason, responses)
	}
}

func responseForID(responses []acptest.Response, id string) *acptest.Response {
	for index := range responses {
		response := &responses[index]
		if response.Method == "" && string(response.ID) == `"`+id+`"` {
			return response
		}
	}
	return nil
}

type fakeRuntime struct {
	mu sync.Mutex

	created []CreateTaskRequest

	events    map[string][]RunEvent
	cancelled map[string][]RunEvent

	cancelledRuns  map[string]bool
	cancelContexts chan error
	cancelErrors   []error
	cancelErr      error
	cancelCalls    int
	cancelStarted  chan struct{}
	releaseCancel  <-chan struct{}
	started        chan struct{}
	createStarted  chan struct{}
	startStarted   chan struct{}
	releaseCreate  <-chan struct{}
	releaseStart   <-chan struct{}
	createErr      error
	startErr       error
	startCalls     int
	blockEventPoll bool
	pollStarted    chan struct{}
}

func (r *fakeRuntime) EnsureReady(context.Context) error {
	return nil
}

func (r *fakeRuntime) CreateTask(_ context.Context, request CreateTaskRequest) (Task, error) {
	r.mu.Lock()
	r.created = append(r.created, request)
	err := r.createErr
	release := r.releaseCreate
	started := r.createStarted
	r.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return Task{}, err
	}
	return Task{ID: "task_1"}, nil
}

func (r *fakeRuntime) StartTask(context.Context, string) (Run, error) {
	r.mu.Lock()
	r.startCalls++
	err := r.startErr
	release := r.releaseStart
	startStarted := r.startStarted
	r.mu.Unlock()
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if startStarted != nil {
		select {
		case startStarted <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return Run{}, err
	}
	return Run{ID: "run_1", Status: "queued"}, nil
}

func (r *fakeRuntime) ContinueTask(context.Context, string, string, string) (Run, error) {
	return Run{ID: "run_2", Status: "queued"}, nil
}

func (r *fakeRuntime) CancelRun(ctx context.Context, _, runID, _ string) error {
	r.mu.Lock()
	r.cancelCalls++
	var cancelErr error
	if len(r.cancelErrors) > 0 {
		cancelErr = r.cancelErrors[0]
		r.cancelErrors = r.cancelErrors[1:]
	} else if r.cancelErr != nil {
		cancelErr = r.cancelErr
	}
	cancelContexts := r.cancelContexts
	cancelStarted := r.cancelStarted
	releaseCancel := r.releaseCancel
	r.mu.Unlock()
	if cancelContexts != nil {
		select {
		case cancelContexts <- ctx.Err():
		default:
		}
	}
	if cancelStarted != nil {
		select {
		case cancelStarted <- struct{}{}:
		default:
		}
	}
	if releaseCancel != nil {
		<-releaseCancel
	}
	if cancelErr != nil {
		return cancelErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancelledRuns == nil {
		r.cancelledRuns = make(map[string]bool)
	}
	r.cancelledRuns[runID] = true
	return nil
}

func (r *fakeRuntime) ListRunEvents(ctx context.Context, _, runID string, afterSequence int64) ([]RunEvent, error) {
	r.mu.Lock()
	block := r.blockEventPoll && !r.cancelledRuns[runID]
	if block && r.pollStarted != nil {
		select {
		case r.pollStarted <- struct{}{}:
		default:
		}
	}
	if block {
		r.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	defer r.mu.Unlock()
	events := r.events[runID]
	if r.cancelledRuns[runID] {
		if cancelled := r.cancelled[runID]; cancelled != nil {
			events = cancelled
		}
	}
	out := make([]RunEvent, 0, len(events))
	for _, event := range events {
		if event.Sequence > afterSequence {
			out = append(out, event)
		}
	}
	return out, nil
}

func TestSplitTextPreservesUTF8Boundaries(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("é", 4)
	chunks := splitText(input, 5)
	if got := strings.Join(chunks, ""); got != input {
		t.Fatalf("splitText reassembled %q, want %q", got, input)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("invalid UTF-8 chunk %q", chunk)
		}
	}
}

func TestNewSessionIDIsOpaqueAndUnique(t *testing.T) {
	t.Parallel()

	first, err := newSessionID()
	if err != nil {
		t.Fatalf("first session ID: %v", err)
	}
	second, err := newSessionID()
	if err != nil {
		t.Fatalf("second session ID: %v", err)
	}
	if first == second || !strings.HasPrefix(first, "acp_") {
		t.Fatalf("session ids = %q, %q", first, second)
	}
}

var _ Runtime = (*fakeRuntime)(nil)
