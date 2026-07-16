package chat

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/pkg/types"
)

func messageRequestTestFingerprint(value string) MessageRequestFingerprint {
	return sha256.Sum256([]byte(value))
}

type messageRequestNowSetter interface {
	setMessageRequestNow(func() time.Time)
}

type controlledMessageRequestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *controlledMessageRequestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *controlledMessageRequestClock) Set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

func runStoreMessageRequestIdempotency(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Create(ctx, Session{ID: "chat_idempotency", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fingerprint := messageRequestTestFingerprint("same payload")
	claim, err := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-1", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest: %v", err)
	}
	if claim.Replay || claim.Lease.Empty() {
		t.Fatalf("initial claim = %+v, want owned lease", claim)
	}

	type claimResult struct {
		claim MessageRequestClaim
		err   error
	}
	replayed := make(chan claimResult, 1)
	go func() {
		got, claimErr := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-1", fingerprint)
		replayed <- claimResult{claim: got, err: claimErr}
	}()
	select {
	case got := <-replayed:
		t.Fatalf("concurrent claim returned before commit: %+v", got)
	case <-time.After(40 * time.Millisecond):
	}
	if _, err := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-1", messageRequestTestFingerprint("changed while pending")); !errors.Is(err, ErrMessageRequestPayloadConflict) {
		t.Fatalf("pending mismatched ClaimMessageRequest error = %v, want payload conflict", err)
	}

	committed, err := store.CommitMessageRequest(ctx, claim.Lease, Message{
		ID:      "msg_user",
		Role:    "user",
		Content: "hello once",
	})
	if err != nil {
		t.Fatalf("CommitMessageRequest: %v", err)
	}
	if len(committed.Messages) != 1 || committed.Messages[0].ID != "msg_user" {
		t.Fatalf("committed messages = %+v, want one authoritative user row", committed.Messages)
	}
	select {
	case got := <-replayed:
		if got.err != nil {
			t.Fatalf("concurrent ClaimMessageRequest: %v", got.err)
		}
		if !got.claim.Replay || !got.claim.Lease.Empty() || got.claim.CommittedMessageID != "msg_user" || len(got.claim.Session.Messages) != 1 {
			t.Fatalf("concurrent replay = %+v, want committed authoritative session", got.claim)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent claim did not return after commit")
	}

	// Retrying the owner's commit is also safe after an ambiguous database
	// response; it returns the current session without appending again.
	committedAgain, err := store.CommitMessageRequest(ctx, claim.Lease, Message{
		ID:      "msg_duplicate",
		Role:    "user",
		Content: "must not append",
	})
	if err != nil {
		t.Fatalf("CommitMessageRequest retry: %v", err)
	}
	if len(committedAgain.Messages) != 1 || committedAgain.Messages[0].ID != "msg_user" {
		t.Fatalf("commit retry messages = %+v, want original message only", committedAgain.Messages)
	}
	displacedLease := claim.Lease
	displacedLease.OwnerToken = "displaced-owner-token"
	if _, err := store.CommitMessageRequest(ctx, displacedLease, Message{ID: "msg_displaced", Role: "user", Content: "must not append"}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("displaced committed owner error = %v, want invalid lease", err)
	}

	if _, err := store.AppendMessage(ctx, "chat_idempotency", Message{ID: "msg_assistant", Role: "assistant", Content: "done", Status: "completed"}); err != nil {
		t.Fatalf("AppendMessage assistant: %v", err)
	}
	latest, err := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-1", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest replay: %v", err)
	}
	if !latest.Replay || latest.CommittedMessageID != "msg_user" || len(latest.Session.Messages) != 2 || latest.Session.Messages[1].ID != "msg_assistant" {
		t.Fatalf("latest replay = %+v, want current authoritative session", latest)
	}

	if _, err := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-1", messageRequestTestFingerprint("changed payload")); !errors.Is(err, ErrMessageRequestPayloadConflict) {
		t.Fatalf("mismatched ClaimMessageRequest error = %v, want payload conflict", err)
	}

	releasable, err := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-release", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest releasable: %v", err)
	}
	if err := store.ReleaseMessageRequest(ctx, releasable.Lease); err != nil {
		t.Fatalf("ReleaseMessageRequest: %v", err)
	}
	reclaimed, err := store.ClaimMessageRequest(ctx, "chat_idempotency", "queued-chat-release", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest after release: %v", err)
	}
	if reclaimed.Replay || reclaimed.Lease.Empty() || reclaimed.Lease.OwnerToken == releasable.Lease.OwnerToken {
		t.Fatalf("reclaimed claim = %+v, want fresh owned lease", reclaimed)
	}
	if err := store.ReleaseMessageRequest(ctx, reclaimed.Lease); err != nil {
		t.Fatalf("ReleaseMessageRequest reclaimed: %v", err)
	}
}

func runStoreMessageRequestLeaseRenewal(t *testing.T, store Store) {
	t.Helper()
	setter, ok := store.(messageRequestNowSetter)
	if !ok {
		t.Fatalf("%T does not expose the message-request test clock", store)
	}
	base := time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)
	clock := &controlledMessageRequestClock{now: base}
	setter.setMessageRequestNow(clock.Now)

	ctx := context.Background()
	if _, err := store.Create(ctx, Session{ID: "chat_lease_renewal", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fingerprint := messageRequestTestFingerprint("lease payload")
	claim, err := store.ClaimMessageRequest(ctx, "chat_lease_renewal", "queued-renewal", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest: %v", err)
	}

	tamperedOwner := claim.Lease
	tamperedOwner.OwnerToken = "not-the-owner"
	if err := store.RenewMessageRequest(ctx, RenewMessageRequestRequest{Lease: tamperedOwner}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("tampered-owner RenewMessageRequest error = %v, want invalid lease", err)
	}
	tamperedFingerprint := claim.Lease
	tamperedFingerprint.Fingerprint = messageRequestTestFingerprint("different payload")
	if err := store.RenewMessageRequest(ctx, RenewMessageRequestRequest{Lease: tamperedFingerprint}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("tampered-fingerprint RenewMessageRequest error = %v, want invalid lease", err)
	}

	ttl := store.MessageRequestLeaseTTL()
	renewedAt := base.Add(ttl - time.Second)
	clock.Set(renewedAt)
	if err := store.RenewMessageRequest(ctx, RenewMessageRequestRequest{Lease: claim.Lease}); err != nil {
		t.Fatalf("RenewMessageRequest: %v", err)
	}

	// This instant is stale relative to the original claim but fresh relative
	// to the controlled renewal, so a competing owner must still wait.
	clock.Set(base.Add(ttl + time.Second))
	waitCtx, cancelWait := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = store.ClaimMessageRequest(waitCtx, "chat_lease_renewal", "queued-renewal", fingerprint)
	cancelWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claim after renewal error = %v, want deadline without takeover", err)
	}

	clock.Set(renewedAt.Add(ttl + time.Second))
	reclaimed, err := store.ClaimMessageRequest(ctx, "chat_lease_renewal", "queued-renewal", fingerprint)
	if err != nil {
		t.Fatalf("ClaimMessageRequest after renewed lease expiry: %v", err)
	}
	if reclaimed.Lease.Empty() || reclaimed.Lease.OwnerToken == claim.Lease.OwnerToken {
		t.Fatalf("reclaimed lease = %+v, want a fresh owner", reclaimed.Lease)
	}
	if err := store.RenewMessageRequest(ctx, RenewMessageRequestRequest{Lease: claim.Lease}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("displaced RenewMessageRequest error = %v, want invalid lease", err)
	}
	if _, err := store.CommitMessageRequest(ctx, claim.Lease, Message{ID: "msg_displaced", Role: "user"}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("displaced CommitMessageRequest error = %v, want invalid lease", err)
	}
	committed, err := store.CommitMessageRequest(ctx, reclaimed.Lease, Message{ID: "msg_owner", Role: "user", Content: "once"})
	if err != nil {
		t.Fatalf("reclaimed CommitMessageRequest: %v", err)
	}
	if len(committed.Messages) != 1 || committed.Messages[0].ID != "msg_owner" {
		t.Fatalf("committed messages = %+v, want reclaimed owner only", committed.Messages)
	}
	if err := store.RenewMessageRequest(ctx, RenewMessageRequestRequest{Lease: reclaimed.Lease}); !errors.Is(err, ErrMessageRequestLeaseInvalid) {
		t.Fatalf("committed RenewMessageRequest error = %v, want invalid lease", err)
	}
}

func TestMemoryStoreConformance(t *testing.T) {
	RunConformanceTests(t, "MemoryStore", func(*testing.T) Store { return NewMemoryStore() })
}

func TestMemoryStoreRoundTripsProviderInstance(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	if _, err := store.Create(context.Background(), Session{ID: "chat_provider_instance", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	initial := types.ProviderInstanceIdentity{ID: "runtime-initial", Kind: types.ProviderInstanceIdentityRuntime}
	if _, err := store.AppendMessage(context.Background(), "chat_provider_instance", Message{
		ID:               "msg_provider_instance",
		Role:             "user",
		ProviderInstance: initial,
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	updatedIdentity := types.ProviderInstanceIdentity{ID: "configuration-updated", Kind: types.ProviderInstanceIdentityConfiguration}
	if _, err := store.UpdateMessage(context.Background(), "chat_provider_instance", "msg_provider_instance", func(message *Message) {
		message.ProviderInstance = updatedIdentity
	}); err != nil {
		t.Fatalf("UpdateMessage() error = %v", err)
	}
	got, ok, err := store.Get(context.Background(), "chat_provider_instance")
	if err != nil || !ok {
		t.Fatalf("Get() = found %v, error %v", ok, err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ProviderInstance != updatedIdentity {
		t.Fatalf("provider instance round trip = %+v, want %+v", got.Messages, updatedIdentity)
	}
}

func TestContextPacketEmptyConsidersItems(t *testing.T) {
	packet := ContextPacket{
		Items: []ContextItem{{
			Kind:       "transcript",
			TrustLevel: "runtime_state",
			Origin:     "chat.transcript",
			Title:      "Chat transcript",
			Included:   true,
		}},
	}

	if packet.Empty() {
		t.Fatal("ContextPacket.Empty() = true for itemized packet, want false")
	}
}

func runStoreMessageAttachmentsRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Create(ctx, Session{ID: "chat_attachments", AgentID: DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	createdAt := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	attachment := MessageAttachment{
		ID:        "att_1",
		Filename:  "diagram.png",
		MediaType: "image/png",
		SizeBytes: 123,
		SHA256:    "abc123",
		CreatedAt: createdAt,
	}
	if _, err := store.AppendMessage(ctx, "chat_attachments", Message{
		ID:          "msg_1",
		Role:        "user",
		Content:     "Review this diagram",
		Attachments: []MessageAttachment{attachment},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	got, ok, err := store.Get(ctx, "chat_attachments")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].Attachments) != 1 || got.Messages[0].Attachments[0] != attachment {
		t.Fatalf("attachments = %+v, want %+v", got.Messages, attachment)
	}
	got.Messages[0].Attachments[0].Filename = "mutated.png"
	again, ok, err := store.Get(ctx, "chat_attachments")
	if err != nil || !ok {
		t.Fatalf("Get again: ok=%v err=%v", ok, err)
	}
	if again.Messages[0].Attachments[0].Filename != "diagram.png" {
		t.Fatalf("stored filename = %q, want immutable metadata copy", again.Messages[0].Attachments[0].Filename)
	}
}

func runStoreActivityOnlyUpdateDoesNotReprojectSessionStatus(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Create(ctx, Session{ID: "chat_activity_status", AgentID: "codex"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, "chat_activity_status", Message{
		ID: "msg_origin", Role: "assistant", Status: "completed",
	}); err != nil {
		t.Fatalf("AppendMessage origin: %v", err)
	}
	if _, err := store.AppendMessage(ctx, "chat_activity_status", Message{
		ID: "msg_current", Role: "assistant", Status: "running",
	}); err != nil {
		t.Fatalf("AppendMessage current: %v", err)
	}

	updated, err := store.UpdateMessage(ctx, "chat_activity_status", "msg_origin", func(message *Message) {
		message.Activities = append(message.Activities, Activity{ID: "terminal:origin", Type: "terminal", Status: "completed"})
	})
	if err != nil {
		t.Fatalf("UpdateMessage activity only: %v", err)
	}
	if updated.Status != "running" {
		t.Fatalf("session status after old activity update = %q, want running", updated.Status)
	}
	if len(updated.Messages) != 2 || len(updated.Messages[0].Activities) != 1 {
		t.Fatalf("messages after old activity update = %+v, want activity retained", updated.Messages)
	}

	updated, err = store.UpdateMessage(ctx, "chat_activity_status", "msg_origin", func(message *Message) {
		message.Status = "failed"
	})
	if err != nil {
		t.Fatalf("UpdateMessage status transition: %v", err)
	}
	if updated.Status != "failed" {
		t.Fatalf("session status after explicit message transition = %q, want failed", updated.Status)
	}
}

func runStoreLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if store.Backend() == "" {
		t.Fatal("Backend() is empty")
	}

	created, err := store.Create(ctx, Session{
		ID:              "chat_1",
		Title:           "Review diff",
		ProjectID:       "proj_hecate",
		AgentID:         DefaultAgentID,
		TaskID:          "task_chat_1",
		LatestRunID:     "run_chat_1",
		Provider:        "openai",
		Model:           "gpt-4o-mini",
		Capabilities:    types.ModelCapabilities{ToolCalling: "basic", Streaming: true, MaxContextTokens: 128000, Source: "provider"},
		Workspace:       "/tmp/hecate",
		WorkspaceBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != "idle" {
		t.Fatalf("created status = %q, want idle", created.Status)
	}
	if created.WorkspaceBranch != "main" {
		t.Fatalf("created workspace branch = %q, want main", created.WorkspaceBranch)
	}
	if created.ProjectID != "proj_hecate" {
		t.Fatalf("created project_id = %q, want proj_hecate", created.ProjectID)
	}
	if created.AgentID != DefaultAgentID || created.TaskID != "task_chat_1" || created.LatestRunID != "run_chat_1" {
		t.Fatalf("created linkage = agent %q task %q run %q", created.AgentID, created.TaskID, created.LatestRunID)
	}
	if created.Provider != "openai" || created.Model != "gpt-4o-mini" || created.Capabilities.ToolCalling != "basic" {
		t.Fatalf("created model snapshot = provider %q model %q caps %+v", created.Provider, created.Model, created.Capabilities)
	}

	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_user",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "task:task_chat_1",
		TaskID:        "task_chat_1",
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Role:          "user",
		Content:       "review this",
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	startedAt := time.Now().UTC().Add(-2 * time.Second)
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_assistant",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "task:task_chat_1",
		TaskID:        "task_chat_1",
		RunID:         "agent_run_1",
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Capabilities:  types.ModelCapabilities{ToolCalling: "basic", Streaming: true, Source: "provider"},
		Role:          "assistant",
		Content:       "running",
		AgentID:       DefaultAgentID,
		AgentName:     "Hecate",
		Status:        "running",
		CostMode:      "hecate",
		Workspace:     "/tmp/hecate",
		Context: ContextPacket{
			Version:              "chat.context.v1",
			ExecutionMode:        ExecutionModeHecateTask,
			Provider:             "openai",
			Model:                "gpt-4o-mini",
			Workspace:            "/tmp/hecate",
			SystemPromptIncluded: true,
			MessageCount:         2,
			Sources: []ContextSource{
				{
					Kind:   "workspace",
					Label:  "Workspace",
					Detail: "/tmp/hecate",
					Trust:  "workspace",
				},
				{
					Kind:   "task_runtime",
					Label:  "Hecate task runtime",
					Detail: "Continuing the existing task-backed agent loop",
					Trust:  "runtime",
				},
			},
			Items: []ContextItem{
				{
					Kind:            "workspace",
					TrustLevel:      "workspace_guidance",
					Origin:          "/tmp/hecate",
					Title:           "Workspace",
					BodyRef:         "/tmp/hecate",
					Included:        true,
					InclusionReason: "Workspace path selected for this task-backed turn",
				},
				{
					Kind:            "task_runtime",
					TrustLevel:      "runtime_state",
					Origin:          "hecate.task_runtime",
					Title:           "Hecate task runtime",
					Body:            "Continuing the existing task-backed agent loop",
					Included:        true,
					InclusionReason: "Task-backed Hecate Chat turn",
				},
			},
		},
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	updated, err := store.UpdateMessage(ctx, created.ID, "msg_assistant", func(message *Message) {
		message.Content = "done"
		message.RawOutput = `{"type":"message","content":"done"}`
		message.RequestID = "req_agent"
		message.TraceID = "trace_agent"
		message.SpanID = "span_agent"
		message.Status = "completed"
		message.ExitCode = 0
		message.DiffStat = "1 file changed"
		message.Diff = "diff --git a/a b/a"
		message.StartedAt = startedAt
		message.CompletedAt = startedAt.Add(1500 * time.Millisecond)
		message.Usage = Usage{
			ContextSize:          200_000,
			ContextUsed:          42_000,
			ReportedCostAmount:   "0.1234",
			ReportedCostCurrency: "USD",
		}
		message.Timing = Timing{
			TotalMS:      1500,
			QueueMS:      20,
			ModelMS:      900,
			ToolMS:       200,
			OverheadMS:   380,
			TurnCount:    1,
			ToolCount:    1,
			Bottleneck:   "model",
			BottleneckMS: 900,
		}
		message.Activities = []Activity{
			{Type: "started", Status: "completed", Title: "Started external agent", CreatedAt: startedAt},
			{Type: "files_changed", Status: "completed", Title: "Files changed", Detail: "1 file changed", CreatedAt: startedAt.Add(time.Second)},
		}
	})
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	if updated.Status != "completed" {
		t.Fatalf("updated session status = %q, want completed", updated.Status)
	}
	if len(updated.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(updated.Messages))
	}
	if got := updated.Messages[1]; got.Content != "done" || got.RawOutput == "" || got.TraceID != "trace_agent" || got.DiffStat != "1 file changed" || got.RunID != "agent_run_1" || got.CompletedAt.IsZero() || len(got.Activities) != 2 {
		t.Fatalf("assistant message not updated: %+v", got)
	}

	got, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Messages[1].Content != "done" {
		t.Fatalf("persisted assistant content = %q, want done", got.Messages[1].Content)
	}
	if got.WorkspaceBranch != "main" {
		t.Fatalf("persisted workspace branch = %q, want main", got.WorkspaceBranch)
	}
	if got.ProjectID != "proj_hecate" {
		t.Fatalf("persisted project_id = %q, want proj_hecate", got.ProjectID)
	}
	if got.AgentID != DefaultAgentID || got.TaskID != "task_chat_1" || got.LatestRunID != "run_chat_1" {
		t.Fatalf("persisted linkage = agent %q task %q run %q", got.AgentID, got.TaskID, got.LatestRunID)
	}
	if got.Provider != "openai" || got.Model != "gpt-4o-mini" || got.Capabilities.ToolCalling != "basic" || got.Capabilities.Source != "provider" {
		t.Fatalf("persisted model snapshot = provider %q model %q caps %+v", got.Provider, got.Model, got.Capabilities)
	}
	if got.Messages[1].RawOutput == "" || got.Messages[1].TraceID != "trace_agent" || len(got.Messages[1].Activities) != 2 {
		t.Fatalf("persisted diagnostics missing: %+v", got.Messages[1])
	}
	if got.Messages[1].Usage.ContextSize != 200_000 || got.Messages[1].Usage.ContextUsed != 42_000 {
		t.Fatalf("persisted usage = %+v, want 42000/200000", got.Messages[1].Usage)
	}
	if got.Messages[1].Timing.Bottleneck != "model" || got.Messages[1].Timing.ModelMS != 900 || got.Messages[1].Timing.TurnCount != 1 {
		t.Fatalf("persisted timing = %+v, want model bottleneck", got.Messages[1].Timing)
	}
	if got.Messages[1].ExecutionMode != ExecutionModeHecateTask || got.Messages[1].SegmentID != "task:task_chat_1" || got.Messages[1].TaskID != "task_chat_1" {
		t.Fatalf("persisted message execution = mode %q segment %q task %q", got.Messages[1].ExecutionMode, got.Messages[1].SegmentID, got.Messages[1].TaskID)
	}
	if got.Messages[1].Provider != "openai" || got.Messages[1].Model != "gpt-4o-mini" || got.Messages[1].Capabilities.ToolCalling != "basic" {
		t.Fatalf("persisted message model snapshot = provider %q model %q caps %+v", got.Messages[1].Provider, got.Messages[1].Model, got.Messages[1].Capabilities)
	}
	if got.Messages[1].Context.Version != "chat.context.v1" || got.Messages[1].Context.MessageCount != 2 || len(got.Messages[1].Context.Sources) != 2 || len(got.Messages[1].Context.Items) != 2 {
		t.Fatalf("persisted context packet = %+v, want version/count/sources/items", got.Messages[1].Context)
	}
	got.Messages[1].Context.Sources[0].Detail = "mutated"
	got.Messages[1].Context.Items[0].Origin = "mutated"
	got, ok, err = store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("Get after context mutation: ok=%v err=%v", ok, err)
	}
	if got.Messages[1].Context.Sources[0].Detail != "/tmp/hecate" {
		t.Fatalf("context packet source mutated through get snapshot: %+v", got.Messages[1].Context.Sources[0])
	}
	if got.Messages[1].Context.Items[0].Origin != "/tmp/hecate" {
		t.Fatalf("context packet item mutated through get snapshot: %+v", got.Messages[1].Context.Items[0])
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List = %+v, want created session", list)
	}
	if list[0].WorkspaceBranch != "main" || len(list[0].Messages) != 2 || list[0].AgentID != DefaultAgentID || list[0].TaskID != "task_chat_1" {
		t.Fatalf("List summary = %+v, want cached branch and message count", list[0])
	}
	if list[0].ProjectID != "proj_hecate" {
		t.Fatalf("List summary project_id = %q, want proj_hecate", list[0].ProjectID)
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok, err = store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Fatal("Get after delete: ok = true, want false")
	}
}

func runStoreToolsEnabledRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:      "chat_tools_enabled",
		AgentID: DefaultAgentID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two messages with explicit, opposite ToolsEnabled values to
	// verify the round-trip preserves each independently and the
	// boolean isn't being collapsed to a per-session signal.
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_tools_on",
		ExecutionMode: ExecutionModeHecateTask,
		ToolsEnabled:  true,
		Role:          "user",
		Content:       "with tools",
	}); err != nil {
		t.Fatalf("AppendMessage(tools_on): %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_tools_off",
		ExecutionMode: ExecutionModeHecateTask,
		ToolsEnabled:  false,
		Role:          "user",
		Content:       "no tools",
	}); err != nil {
		t.Fatalf("AppendMessage(tools_off): %v", err)
	}

	session, ok, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: ok = false")
	}
	byID := make(map[string]Message, len(session.Messages))
	for _, m := range session.Messages {
		byID[m.ID] = m
	}
	if got := byID["msg_tools_on"].ToolsEnabled; !got {
		t.Errorf("msg_tools_on.ToolsEnabled = false, want true")
	}
	if got := byID["msg_tools_off"].ToolsEnabled; got {
		t.Errorf("msg_tools_off.ToolsEnabled = true, want false")
	}

	// UpdateMessage flips the flag — verifies the write path preserves
	// the column on UPDATE, not just INSERT.
	if _, err := store.UpdateMessage(ctx, created.ID, "msg_tools_off", func(m *Message) {
		m.ToolsEnabled = true
	}); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	session, _, err = store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	for _, m := range session.Messages {
		if m.ID == "msg_tools_off" && !m.ToolsEnabled {
			t.Errorf("msg_tools_off.ToolsEnabled after update = false, want true")
		}
	}
}

func runStoreDeepCopiesConfigOptions(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:      "chat_config_options",
		AgentID: "codex",
		ConfigOptions: []agentcontrols.ConfigOption{
			{
				ID:           "model",
				Name:         "Model",
				Type:         agentcontrols.ConfigOptionTypeSelect,
				CurrentValue: "fast",
				Options: []agentcontrols.ConfigSelectOption{
					{Value: "fast", Name: "Fast"},
					{Value: "smart", Name: "Smart"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.ConfigOptions[0].CurrentValue = "mutated"
	created.ConfigOptions[0].Options[0].Name = "Mutated"

	got, ok, err := store.Get(ctx, "chat_config_options")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.ConfigOptions[0].CurrentValue != "fast" || got.ConfigOptions[0].Options[0].Name != "Fast" {
		t.Fatalf("stored options mutated through create snapshot: %#v", got.ConfigOptions)
	}
	got.ConfigOptions[0].CurrentValue = "again"
	got.ConfigOptions[0].Options[1].Name = "Again"

	got, ok, err = store.Get(ctx, "chat_config_options")
	if err != nil || !ok {
		t.Fatalf("Get after mutation: ok=%v err=%v", ok, err)
	}
	if got.ConfigOptions[0].CurrentValue != "fast" || got.ConfigOptions[0].Options[1].Name != "Smart" {
		t.Fatalf("stored options mutated through get snapshot: %#v", got.ConfigOptions)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	list[0].ConfigOptions[0].Options[0].Name = "Listed"
	got, ok, err = store.Get(ctx, "chat_config_options")
	if err != nil || !ok {
		t.Fatalf("Get after list mutation: ok=%v err=%v", ok, err)
	}
	if got.ConfigOptions[0].Options[0].Name != "Fast" {
		t.Fatalf("stored options mutated through list snapshot: %#v", got.ConfigOptions)
	}
}

func runStoreMCPServersRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:        "chat_mcp_servers",
		AgentID:   "codex",
		Workspace: "/tmp/hecate",
		MCPServers: []types.MCPServerConfig{
			{
				Name:    "weather",
				URL:     "https://example.com/mcp",
				Headers: map[string]string{"Authorization": "$MCP_TOKEN"},
			},
			{
				Name:           "fs",
				Command:        "node",
				Args:           []string{"server.js"},
				Env:            map[string]string{"DEBUG": "1"},
				ApprovalPolicy: types.MCPApprovalRequireApproval,
			},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.MCPServers[0].Headers["Authorization"] = "mutated"
	created.MCPServers[1].Args[0] = "mutated.js"

	got, ok, err := store.Get(ctx, "chat_mcp_servers")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.MCPServers[0].Headers["Authorization"] != "$MCP_TOKEN" || got.MCPServers[1].Args[0] != "server.js" {
		t.Fatalf("stored MCP servers mutated through create snapshot: %#v", got.MCPServers)
	}
	got.MCPServers[1].Env["DEBUG"] = "0"
	got.MCPServers[1].Args[0] = "again.js"

	got, ok, err = store.Get(ctx, "chat_mcp_servers")
	if err != nil || !ok {
		t.Fatalf("Get after mutation: ok=%v err=%v", ok, err)
	}
	if got.MCPServers[1].Env["DEBUG"] != "1" || got.MCPServers[1].Args[0] != "server.js" {
		t.Fatalf("stored MCP servers mutated through get snapshot: %#v", got.MCPServers)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	list[0].MCPServers[0].URL = "https://mutated.invalid/mcp"
	got, ok, err = store.Get(ctx, "chat_mcp_servers")
	if err != nil || !ok {
		t.Fatalf("Get after list mutation: ok=%v err=%v", ok, err)
	}
	if got.MCPServers[0].URL != "https://example.com/mcp" {
		t.Fatalf("stored MCP servers mutated through list snapshot: %#v", got.MCPServers)
	}
}

func runStoreAvailableCommandsRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:        "chat_commands",
		Title:     "Commands",
		AgentID:   "codex",
		Workspace: "/tmp/hecate",
		AvailableCommands: []agentcontrols.Command{
			{Name: "web", Description: "Search the web", InputHint: "query"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.AvailableCommands[0].Name = "mutated"
	got, ok, err := store.Get(ctx, "chat_commands")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if len(got.AvailableCommands) != 1 || got.AvailableCommands[0].Name != "web" || got.AvailableCommands[0].InputHint != "query" {
		t.Fatalf("stored commands = %#v, want web command", got.AvailableCommands)
	}
	got.AvailableCommands[0].Description = "mutated"
	again, _, err := store.Get(ctx, "chat_commands")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.AvailableCommands[0].Description != "Search the web" {
		t.Fatalf("stored command mutated through get snapshot: %#v", again.AvailableCommands)
	}
	updated, err := store.UpdateSession(ctx, "chat_commands", func(item *Session) {
		item.AvailableCommands = []agentcontrols.Command{}
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.AvailableCommands == nil || len(updated.AvailableCommands) != 0 {
		t.Fatalf("updated commands = %#v, want non-nil empty slice", updated.AvailableCommands)
	}
}

func runStoreAgentInfoRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:              "chat_agent_info",
		Title:           "Agent info",
		AgentID:         "codex",
		DriverKind:      "acp",
		NativeSessionID: "native_agent_info",
		Workspace:       "/tmp/hecate",
		AgentInfo: &agentcontrols.ImplementationInfo{
			Name:    "codex-acp-adapter",
			Title:   "Codex ACP Adapter",
			Version: "0.1.0-alpha.28",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.AgentInfo.Title = "mutated"

	got, ok, err := store.Get(ctx, "chat_agent_info")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.AgentInfo == nil || got.AgentInfo.Title != "Codex ACP Adapter" || got.AgentInfo.Version != "0.1.0-alpha.28" {
		t.Fatalf("session agent info = %#v, want stored adapter metadata", got.AgentInfo)
	}
	got.AgentInfo.Version = "mutated"

	withMessage, err := store.AppendMessage(ctx, "chat_agent_info", Message{
		ID:      "msg_agent_info",
		Role:    "assistant",
		Content: "hello",
		Status:  "completed",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	var appended Message
	for _, message := range withMessage.Messages {
		if message.ID == "msg_agent_info" {
			appended = message
			break
		}
	}
	if appended.AgentInfo == nil || appended.AgentInfo.Name != "codex-acp-adapter" {
		t.Fatalf("hydrated message agent info = %#v, want session metadata", appended.AgentInfo)
	}

	if _, err := store.UpdateMessage(ctx, "chat_agent_info", "msg_agent_info", func(message *Message) {
		message.AgentInfo = &agentcontrols.ImplementationInfo{
			Name:    "claude-code-acp-adapter",
			Title:   "Claude Code ACP Adapter",
			Version: "0.1.0-alpha.29",
		}
	}); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	again, ok, err := store.Get(ctx, "chat_agent_info")
	if err != nil || !ok {
		t.Fatalf("Get after update: ok=%v err=%v", ok, err)
	}
	if again.AgentInfo == nil || again.AgentInfo.Version != "0.1.0-alpha.28" {
		t.Fatalf("session agent info after mutation/update = %#v, want original session metadata", again.AgentInfo)
	}
	if len(again.Messages) != 1 || again.Messages[0].AgentInfo == nil || again.Messages[0].AgentInfo.Name != "claude-code-acp-adapter" {
		t.Fatalf("message agent info after update = %#v, want updated message metadata", again.Messages)
	}
}

func runStoreContextSummaryRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, Session{
		ID:      "chat_context_summary",
		Title:   "Context summary",
		AgentID: DefaultAgentID,
		ContextSummary: ContextSummary{
			Content:          "- User: first request",
			MessageCount:     1,
			ThroughMessageID: "msg_user_1",
			Strategy:         ContextSummaryStrategySemantic,
			CompactedAt:      createdAt,
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := store.Get(ctx, "chat_context_summary")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.ContextSummary.Content != "- User: first request" ||
		got.ContextSummary.MessageCount != 1 ||
		got.ContextSummary.ThroughMessageID != "msg_user_1" ||
		got.ContextSummary.Strategy != ContextSummaryStrategySemantic ||
		!got.ContextSummary.CompactedAt.Equal(createdAt) {
		t.Fatalf("stored context summary = %+v", got.ContextSummary)
	}
	got.ContextSummary.Content = "mutated"
	again, _, err := store.Get(ctx, "chat_context_summary")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.ContextSummary.Content != "- User: first request" {
		t.Fatalf("context summary mutated through get snapshot: %+v", again.ContextSummary)
	}

	updated, err := store.UpdateSession(ctx, "chat_context_summary", func(item *Session) {
		item.ContextSummary = ContextSummary{
			Content:          "- Assistant: answer",
			MessageCount:     2,
			ThroughMessageID: "msg_assistant_1",
			Strategy:         ContextSummaryStrategyDeterministic,
			CompactedAt:      createdAt.Add(time.Minute),
		}
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.ContextSummary.MessageCount != 2 ||
		updated.ContextSummary.ThroughMessageID != "msg_assistant_1" ||
		updated.ContextSummary.Strategy != ContextSummaryStrategyDeterministic {
		t.Fatalf("updated context summary = %+v", updated.ContextSummary)
	}
}

func runStoreDeleteByProjectID(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	seed := []Session{
		{ID: "chat_project_a", ProjectID: "proj_delete", AgentID: DefaultAgentID},
		{ID: "chat_project_b", ProjectID: "proj_delete", AgentID: "codex"},
		{ID: "chat_other", ProjectID: "proj_other", AgentID: DefaultAgentID},
		{ID: "chat_unprojected", AgentID: DefaultAgentID},
	}
	for _, session := range seed {
		if _, err := store.Create(ctx, session); err != nil {
			t.Fatalf("Create(%s): %v", session.ID, err)
		}
		if _, err := store.AppendMessage(ctx, session.ID, Message{
			ID:      "msg_" + session.ID,
			Role:    "user",
			Content: "hello",
		}); err != nil {
			t.Fatalf("AppendMessage(%s): %v", session.ID, err)
		}
	}

	if err := store.DeleteByProjectID(ctx, "proj_delete"); err != nil {
		t.Fatalf("DeleteByProjectID: %v", err)
	}
	for _, id := range []string{"chat_project_a", "chat_project_b"} {
		if _, ok, err := store.Get(ctx, id); err != nil || ok {
			t.Fatalf("Get(%s) after project delete: ok=%v err=%v, want missing", id, ok, err)
		}
	}
	for _, id := range []string{"chat_other", "chat_unprojected"} {
		got, ok, err := store.Get(ctx, id)
		if err != nil || !ok {
			t.Fatalf("Get(%s) after project delete: ok=%v err=%v, want retained", id, ok, err)
		}
		if len(got.Messages) != 1 {
			t.Fatalf("Get(%s) messages = %d, want retained message", id, len(got.Messages))
		}
	}
}

func runStoreDoesNotHydrateTaskIDForAnonymousAgentSegment(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:          "chat_1",
		AgentID:     DefaultAgentID,
		TaskID:      "task_previous",
		LatestRunID: "run_previous",
		Provider:    "openai",
		Model:       "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_new_segment",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "segment_pending_new_task",
		Role:          "user",
		Content:       "tools again",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if got := updated.Messages[len(updated.Messages)-1]; got.TaskID != "" {
		t.Fatalf("anonymous agent segment task_id = %q, want empty until new task is assigned", got.TaskID)
	}

	updated, err = store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_existing_task",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "task:task_previous",
		Role:          "assistant",
		Content:       "continuing previous task",
	})
	if err != nil {
		t.Fatalf("AppendMessage(existing task): %v", err)
	}
	if got := updated.Messages[len(updated.Messages)-1]; got.TaskID != "task_previous" {
		t.Fatalf("task segment task_id = %q, want hydrated previous task", got.TaskID)
	}
}

func runStoreReconcileInterruptedRuns(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:        "chat_interrupted",
		Title:     "Interrupted",
		AgentID:   "codex",
		Workspace: "/tmp/hecate",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:        "msg_user",
		Role:      "user",
		Content:   "keep going",
		CreatedAt: time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_assistant",
		ExecutionMode: ExecutionModeExternalAgent,
		RunID:         "agent_run_interrupted",
		Role:          "assistant",
		Content:       "partial answer",
		AgentID:       "codex",
		AgentName:     "Codex",
		Status:        "running",
		CostMode:      "external",
		Workspace:     "/tmp/hecate",
		StartedAt:     time.Now().UTC().Add(-time.Minute),
		Activities: []Activity{
			{Type: "running", Status: "running", Title: "Running"},
		},
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}

	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	count, err := ReconcileInterruptedRuns(ctx, store, now)
	if err != nil {
		t.Fatalf("ReconcileInterruptedRuns: %v", err)
	}
	if count != 1 {
		t.Fatalf("reconciled count = %d, want 1", count)
	}

	got, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Status != "cancelled" {
		t.Fatalf("session status = %q, want cancelled", got.Status)
	}
	assistant := got.Messages[1]
	if assistant.Status != "cancelled" || assistant.Error != "interrupted by Hecate restart" || !assistant.CompletedAt.Equal(now) {
		t.Fatalf("assistant after reconcile = %+v", assistant)
	}
	if assistant.Content != "partial answer" {
		t.Fatalf("assistant content = %q, want preserved partial answer", assistant.Content)
	}
	if !activityTypeExists(assistant.Activities, "interrupted") {
		t.Fatalf("activities = %+v, want interrupted activity", assistant.Activities)
	}

	count, err = ReconcileInterruptedRuns(ctx, store, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ReconcileInterruptedRuns second call: %v", err)
	}
	if count != 0 {
		t.Fatalf("second reconciled count = %d, want 0", count)
	}

	orphaned, err := store.Create(ctx, Session{
		ID:              "chat_orphaned_external",
		Title:           "Orphaned external run",
		AgentID:         "grok_build",
		DriverKind:      "acp",
		NativeSessionID: "native_orphaned",
		Workspace:       "/tmp/hecate",
		Status:          "running",
	})
	if err != nil {
		t.Fatalf("Create(orphaned): %v", err)
	}
	count, err = ReconcileInterruptedRuns(ctx, store, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ReconcileInterruptedRuns orphaned: %v", err)
	}
	if count != 1 {
		t.Fatalf("orphaned reconciled count = %d, want 1", count)
	}
	got, ok, err = store.Get(ctx, orphaned.ID)
	if err != nil || !ok {
		t.Fatalf("Get(orphaned): ok=%v err=%v", ok, err)
	}
	if got.Status != "cancelled" {
		t.Fatalf("orphaned session status = %q, want cancelled", got.Status)
	}
}
