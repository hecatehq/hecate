package agentadapters

import (
	"context"
	"errors"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// fixedClock returns t for every call. Lets us assert deterministic
// timestamps and grant-expiry math.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func samplePermissionRequest(toolKind acp.ToolKind, title string) acp.RequestPermissionRequest {
	k := toolKind
	t := title
	return acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: acp.PermissionOptionId("allow_once_id"), Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
			{OptionId: acp.PermissionOptionId("allow_always_id"), Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow always"},
			{OptionId: acp.PermissionOptionId("reject_once_id"), Kind: acp.PermissionOptionKindRejectOnce, Name: "Deny"},
		},
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "call_1",
			Kind:       &k,
			Title:      &t,
		},
	}
}

// ─── Memory store ────────────────────────────────────────────────────────────

func TestMemoryStoreCreateAndGet(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()

	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	got, err := s.CreateApproval(ctx, Approval{SessionID: "sess1", AdapterID: "codex", ToolKind: "file_write", CreatedAt: now})
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	if got.ID == "" {
		t.Fatal("expected store to assign id")
	}
	if got.Status != ApprovalStatusPending {
		t.Fatalf("Status = %q, want %q", got.Status, ApprovalStatusPending)
	}
	got2, err := s.GetApproval(ctx, got.ID)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if got2.SessionID != "sess1" || got2.AdapterID != "codex" {
		t.Fatalf("got back %+v", got2)
	}
}

func TestMemoryStoreGetMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	if _, err := s.GetApproval(context.Background(), "missing"); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("got %v, want ErrApprovalNotFound", err)
	}
}

func TestMemoryStoreResolveTransitions(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()
	now := time.Now().UTC()

	created, _ := s.CreateApproval(ctx, Approval{SessionID: "s", AdapterID: "codex", Status: ApprovalStatusPending, CreatedAt: now})
	resolved, err := s.ResolveApproval(ctx, created.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "allow_once_id", ApprovalScopeOnce, PathOperator, "ok", now.Add(time.Second))
	if err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if resolved.Status != ApprovalStatusApproved || resolved.Decision != ApprovalDecisionApprove {
		t.Fatalf("unexpected resolved row: %+v", resolved)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set")
	}

	// Second resolve must fail — append-only state machine.
	_, err = s.ResolveApproval(ctx, created.ID, ApprovalStatusDenied, ApprovalDecisionDeny, "", ApprovalScopeOnce, PathOperator, "", now)
	if !errors.Is(err, ErrApprovalAlreadyResolved) {
		t.Fatalf("got %v, want ErrApprovalAlreadyResolved", err)
	}
}

func TestMemoryStoreListSortsOldestFirst(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()
	t0 := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		_, _ = s.CreateApproval(ctx, Approval{
			SessionID: "sess", AdapterID: "codex",
			Status:    ApprovalStatusPending,
			CreatedAt: t0.Add(time.Duration(i) * time.Second),
		})
	}
	rows, err := s.ListApprovals(ctx, "sess", "")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if !rows[i-1].CreatedAt.Before(rows[i].CreatedAt) {
			t.Fatalf("rows not sorted oldest-first: %+v", rows)
		}
	}
}

func TestMemoryStoreListFiltersByStatus(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()

	a, _ := s.CreateApproval(ctx, Approval{SessionID: "sess", Status: ApprovalStatusPending})
	_, _ = s.CreateApproval(ctx, Approval{SessionID: "sess", Status: ApprovalStatusPending})
	_, _ = s.ResolveApproval(ctx, a.ID, ApprovalStatusApproved, ApprovalDecisionApprove, "x", ApprovalScopeOnce, PathOperator, "", time.Now())

	pending, _ := s.ListApprovals(ctx, "sess", ApprovalStatusPending)
	if len(pending) != 1 {
		t.Fatalf("pending=%d, want 1", len(pending))
	}
	approved, _ := s.ListApprovals(ctx, "sess", ApprovalStatusApproved)
	if len(approved) != 1 {
		t.Fatalf("approved=%d, want 1", len(approved))
	}
}

// ─── Grant lookup ────────────────────────────────────────────────────────────

func TestFindMatchingGrantSessionWinsOverWorkspaceTool(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()
	now := time.Now().UTC()

	_, _ = s.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeWorkspaceTool, AdapterID: "codex", ToolKind: "file_write",
		Workspace: "/tmp/w", Decision: ApprovalDecisionDeny, GrantedAt: now,
	})
	_, _ = s.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeSession, AdapterID: "codex", ToolKind: "file_write",
		SessionID: "sess1", Decision: ApprovalDecisionApprove, GrantedAt: now,
	})

	got, ok, err := s.FindMatchingGrant(ctx, "sess1", "/tmp/w", "codex", "file_write", now)
	if err != nil || !ok {
		t.Fatalf("expected match, got ok=%v err=%v", ok, err)
	}
	if got.Decision != ApprovalDecisionApprove {
		t.Fatalf("session-scope grant should win; got decision %q", got.Decision)
	}
}

func TestFindMatchingGrantWorkspaceToolWinsOverAdapterTool(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()
	now := time.Now().UTC()

	_, _ = s.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "shell_exec",
		Decision: ApprovalDecisionApprove, GrantedAt: now,
	})
	_, _ = s.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeWorkspaceTool, AdapterID: "codex", ToolKind: "shell_exec",
		Workspace: "/tmp/w", Decision: ApprovalDecisionDeny, GrantedAt: now,
	})

	got, ok, _ := s.FindMatchingGrant(ctx, "any-session", "/tmp/w", "codex", "shell_exec", now)
	if !ok {
		t.Fatal("expected match")
	}
	if got.Decision != ApprovalDecisionDeny {
		t.Fatalf("workspace_tool should win; got %q", got.Decision)
	}
}

func TestFindMatchingGrantIgnoresExpired(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)

	_, _ = s.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "shell_exec",
		Decision: ApprovalDecisionApprove, GrantedAt: now.Add(-2 * time.Hour), ExpiresAt: &past,
	})

	if _, ok, _ := s.FindMatchingGrant(ctx, "s", "", "codex", "shell_exec", now); ok {
		t.Fatal("expired grant must not match")
	}
}

func TestFindMatchingGrantIgnoresWrongAdapterOrTool(t *testing.T) {
	t.Parallel()
	s := NewMemoryApprovalStore()
	ctx := context.Background()
	now := time.Now().UTC()

	_, _ = s.CreateGrant(ctx, Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "file_write",
		Decision: ApprovalDecisionApprove, GrantedAt: now,
	})
	if _, ok, _ := s.FindMatchingGrant(ctx, "s", "", "claude_code", "file_write", now); ok {
		t.Fatal("wrong adapter must not match")
	}
	if _, ok, _ := s.FindMatchingGrant(ctx, "s", "", "codex", "shell_exec", now); ok {
		t.Fatal("wrong tool_kind must not match")
	}
}

// ─── Coordinator ─────────────────────────────────────────────────────────────

func TestCoordinatorModeAutoApprovesAndRecords(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	var requested, resolved int
	c := NewApprovalCoordinator(CoordinatorOptions{
		Mode: ModeAuto, Store: store, NowFunc: fixedClock(now),
		Hooks: CoordinatorHooks{
			OnRequested: func(Approval) { requested++ },
			OnResolved:  func(Approval, int64) { resolved++ },
		},
	})

	resp, err := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex", Workspace: "/tmp/w"},
		samplePermissionRequest(acp.ToolKindEdit, "Edit foo.go"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Outcome.Selected == nil {
		t.Fatalf("expected approve outcome; got %+v", resp.Outcome)
	}
	if resp.Outcome.Selected.OptionId != "allow_once_id" {
		t.Fatalf("expected first allow_once option; got %q", resp.Outcome.Selected.OptionId)
	}
	if requested != 1 || resolved != 1 {
		t.Fatalf("hooks fired %d/%d, want 1/1", requested, resolved)
	}

	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.Status != ApprovalStatusApproved || got.Decision != ApprovalDecisionApprove {
		t.Fatalf("recorded approval not in approved/approve state: %+v", got)
	}
	if got.Path != PathDefaultMode {
		t.Fatalf("path = %q, want %q (default-mode auto-resolution)", got.Path, PathDefaultMode)
	}
	if got.ToolKind != ToolKindFileWrite {
		t.Fatalf("tool_kind = %q, want %q", got.ToolKind, ToolKindFileWrite)
	}
	if got.SelectedOption != "allow_once_id" {
		t.Fatalf("selected_option = %q, want allow_once_id", got.SelectedOption)
	}
}

func TestCoordinatorModeDenyRecordsAndRejects(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModeDeny, Store: store})

	resp, err := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex"},
		samplePermissionRequest(acp.ToolKindExecute, "Run tests"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Outcome.Selected == nil {
		t.Fatal("expected deny outcome to select reject option")
	}
	if resp.Outcome.Selected.OptionId != "reject_once_id" {
		t.Fatalf("got option %q, want reject_once_id", resp.Outcome.Selected.OptionId)
	}

	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if rows[0].Status != ApprovalStatusDenied || rows[0].Path != PathDefaultMode {
		t.Fatalf("denied row malformed: %+v", rows[0])
	}
}

func TestCoordinatorModePromptCancelsWithoutUI(t *testing.T) {
	t.Parallel()
	// Sanity check: prompt mode falls back to a Cancelled outcome
	// with status=timed_out when no operator decision arrives before
	// the configured timeout fires. The "real blocking" path is
	// covered by the resolve/cancel tests below.
	store := NewMemoryApprovalStore()
	var timedOut int
	timeout := 5 * time.Millisecond
	c := NewApprovalCoordinator(CoordinatorOptions{
		Mode: ModePrompt, Store: store, Timeout: timeout,
		Hooks: CoordinatorHooks{
			OnTimedOut: func(_ Approval, durationMS int64) {
				timedOut++
				// Real wall-clock between createdAt and timeout fire;
				// must be >= configured timeout but the upper bound is
				// scheduler-dependent.
				if durationMS < timeout.Milliseconds() {
					t.Fatalf("durationMS = %d, want >= %d", durationMS, timeout.Milliseconds())
				}
			},
		},
	})

	started := time.Now()
	resp, err := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex"},
		samplePermissionRequest(acp.ToolKindEdit, "Edit foo.go"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if elapsed := time.Since(started); elapsed < timeout {
		t.Fatalf("prompt mode returned before timeout: elapsed=%s timeout=%s", elapsed, timeout)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("expected cancelled outcome in prompt mode; got %+v", resp.Outcome)
	}
	if timedOut != 1 {
		t.Fatalf("OnTimedOut hook fired %d times, want 1", timedOut)
	}

	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if len(rows) != 1 || rows[0].Status != ApprovalStatusTimedOut || rows[0].Path != PathTimeout {
		t.Fatalf("row not in timed_out/timeout state: %+v", rows[0])
	}
}

func TestCoordinatorGrantShortCircuitsAuto(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	now := time.Now().UTC()
	_, _ = store.CreateGrant(context.Background(), Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "shell_exec",
		Decision: ApprovalDecisionDeny, GrantedAt: now,
	})

	// Mode is `auto` (would otherwise approve), but the grant says deny.
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModeAuto, Store: store, NowFunc: fixedClock(now)})

	resp, err := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex"},
		samplePermissionRequest(acp.ToolKindExecute, "Run rm -rf /"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "reject_once_id" {
		t.Fatalf("grant deny should drive reject option; got %+v", resp.Outcome)
	}

	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if rows[0].Path != PathGrant {
		t.Fatalf("path = %q, want %q (grant short-circuit)", rows[0].Path, PathGrant)
	}
	if rows[0].Scope != ApprovalScopeAdapterTool {
		t.Fatalf("scope = %q, want %q (carried over from grant)", rows[0].Scope, ApprovalScopeAdapterTool)
	}
}

func TestCoordinatorGrantApproveWithoutAllowOptionCancels(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	now := time.Now().UTC()
	_, _ = store.CreateGrant(context.Background(), Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: ToolKindOther,
		Decision: ApprovalDecisionApprove, GrantedAt: now,
	})
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModeDeny, Store: store, NowFunc: fixedClock(now)})

	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: acp.PermissionOptionId("custom_proceed"), Kind: "custom", Name: "Proceed"},
			{OptionId: acp.PermissionOptionId("custom_stop"), Kind: "custom_stop", Name: "Stop"},
		},
		ToolCall: acp.ToolCallUpdate{ToolCallId: "c"},
	}
	resp, err := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex"}, req)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("grant approve without normalized allow option must cancel; got %+v", resp.Outcome)
	}

	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Path != PathGrant || rows[0].Status != ApprovalStatusCancelled {
		t.Fatalf("grant row = %+v, want path=grant status=cancelled", rows[0])
	}
}

func TestCoordinatorApproveWithoutAllowOptionFallsBackToFirst(t *testing.T) {
	t.Parallel()
	// Defensive: an adapter sends only custom-named options. We keep
	// the legacy "first option" fallback for the auto path so we don't
	// degrade to Cancelled silently.
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModeAuto, Store: store})

	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: acp.PermissionOptionId("custom_proceed"), Kind: "custom", Name: "Proceed"},
			{OptionId: acp.PermissionOptionId("custom_stop"), Kind: "custom_stop", Name: "Stop"},
		},
		ToolCall: acp.ToolCallUpdate{ToolCallId: "c"},
	}
	resp, _ := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex"}, req)
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "custom_proceed" {
		t.Fatalf("expected fallback to first option; got %+v", resp.Outcome)
	}
}

func TestCoordinatorDenyWithoutRejectOptionCancels(t *testing.T) {
	t.Parallel()
	// If the adapter offers no reject option, the only honest answer
	// for ModeDeny is ACP Cancelled. Picking a custom option as a
	// stand-in could approve an action by accident.
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModeDeny, Store: store})

	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: acp.PermissionOptionId("allow_once"), Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow"},
		},
	}
	resp, _ := c.RequestPermission(context.Background(), RecordingContext{SessionID: "s", AdapterID: "codex"}, req)
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("expected cancelled outcome when no deny option present; got %+v", resp.Outcome)
	}

	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if rows[0].Status != ApprovalStatusCancelled {
		t.Fatalf("status = %q, want cancelled", rows[0].Status)
	}
}

func TestCoordinatorDefaultsAreSafe(t *testing.T) {
	t.Parallel()
	c := NewApprovalCoordinator(CoordinatorOptions{})
	if c.Mode() != ModeAuto {
		t.Fatalf("zero-value mode = %q, want auto", c.Mode())
	}
	if c.Timeout() != 5*time.Minute {
		t.Fatalf("zero-value timeout = %s, want 5m", c.Timeout())
	}
	if c.Store() == nil {
		t.Fatal("expected default in-memory store")
	}
}
