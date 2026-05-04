package agentadapters

import (
	"context"
	"errors"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// ─── Coordinator.Resolve ─────────────────────────────────────────────────────

func TestCoordinatorResolveApproveScopeOnce(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})

	// Seed a pending row directly via the store (avoiding the prompt
	// blocking path; the row is what Resolve operates on). Single
	// allow_once option so Resolve doesn't hit the ambiguity path.
	row := pendingRow(t, store, "sess1", "codex", "/tmp/w", unambiguousAllowOnceRequest())

	resolved, err := c.Resolve(context.Background(), row.ID, ResolveRequest{
		Decision: ApprovalDecisionApprove,
		Scope:    ApprovalScopeOnce,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != ApprovalStatusApproved {
		t.Fatalf("status = %q, want approved", resolved.Status)
	}
	if resolved.SelectedOption != "allow_once_id" {
		t.Fatalf("selected_option = %q, want allow_once_id (only allow_once)", resolved.SelectedOption)
	}
	if resolved.Path != PathOperator {
		t.Fatalf("path = %q, want operator", resolved.Path)
	}
	if resolved.Scope != ApprovalScopeOnce {
		t.Fatalf("scope = %q, want once", resolved.Scope)
	}

	// Scope=once must NOT create a grant.
	grants, _ := c.ListGrants(context.Background(), GrantFilter{})
	if len(grants) != 0 {
		t.Fatalf("got %d grants, want 0 for scope=once", len(grants))
	}
}

func TestCoordinatorResolveCreatesGrantPerScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		scope       ApprovalScope
		expectField func(t *testing.T, g Grant)
	}{
		{
			name:  "session scope",
			scope: ApprovalScopeSession,
			expectField: func(t *testing.T, g Grant) {
				if g.SessionID != "sess1" {
					t.Fatalf("session-scope grant must carry session_id; got %q", g.SessionID)
				}
				if g.Workspace != "" {
					t.Fatalf("session-scope grant must not carry workspace; got %q", g.Workspace)
				}
			},
		},
		{
			name:  "workspace_tool scope",
			scope: ApprovalScopeWorkspaceTool,
			expectField: func(t *testing.T, g Grant) {
				if g.Workspace != "/tmp/w" {
					t.Fatalf("workspace_tool grant must carry workspace; got %q", g.Workspace)
				}
				if g.SessionID != "" {
					t.Fatalf("workspace_tool grant must not carry session_id; got %q", g.SessionID)
				}
			},
		},
		{
			name:  "adapter_tool scope",
			scope: ApprovalScopeAdapterTool,
			expectField: func(t *testing.T, g Grant) {
				if g.Workspace != "" || g.SessionID != "" {
					t.Fatalf("adapter_tool grant must carry neither workspace nor session_id; got w=%q s=%q", g.Workspace, g.SessionID)
				}
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := NewMemoryApprovalStore()
			c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})
			row := pendingRow(t, store, "sess1", "codex", "/tmp/w", unambiguousAllowOnceRequest())

			_, err := c.Resolve(context.Background(), row.ID, ResolveRequest{
				Decision:  ApprovalDecisionApprove,
				Scope:     tc.scope,
				GrantedBy: "test-operator",
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			grants, _ := c.ListGrants(context.Background(), GrantFilter{})
			if len(grants) != 1 {
				t.Fatalf("got %d grants, want 1", len(grants))
			}
			g := grants[0]
			if g.AdapterID != "codex" || g.ToolKind != ToolKindFileWrite {
				t.Fatalf("grant adapter/tool wrong: %+v", g)
			}
			if g.Decision != ApprovalDecisionApprove {
				t.Fatalf("grant decision = %q, want approve", g.Decision)
			}
			if g.GrantedBy != "test-operator" {
				t.Fatalf("granted_by = %q, want test-operator", g.GrantedBy)
			}
			tc.expectField(t, g)
		})
	}
}

func TestCoordinatorResolveAlreadyResolvedReturns409(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})
	row := pendingRow(t, store, "s", "codex", "/tmp/w", unambiguousAllowOnceRequest())
	_, _ = c.Resolve(context.Background(), row.ID, ResolveRequest{Decision: ApprovalDecisionApprove, Scope: ApprovalScopeOnce})

	_, err := c.Resolve(context.Background(), row.ID, ResolveRequest{Decision: ApprovalDecisionDeny, Scope: ApprovalScopeOnce})
	if !errors.Is(err, ErrApprovalAlreadyResolved) {
		t.Fatalf("got %v, want ErrApprovalAlreadyResolved", err)
	}
}

func TestCoordinatorResolveUnknownIdReturns404(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})
	_, err := c.Resolve(context.Background(), "missing", ResolveRequest{Decision: ApprovalDecisionApprove, Scope: ApprovalScopeOnce})
	if !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("got %v, want ErrApprovalNotFound", err)
	}
}

func TestCoordinatorResolveInvalidDecision(t *testing.T) {
	t.Parallel()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: NewMemoryApprovalStore()})
	_, err := c.Resolve(context.Background(), "any", ResolveRequest{Decision: "maybe", Scope: ApprovalScopeOnce})
	if !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("got %v, want ErrInvalidDecision", err)
	}
}

func TestCoordinatorResolveInvalidScope(t *testing.T) {
	t.Parallel()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: NewMemoryApprovalStore()})
	_, err := c.Resolve(context.Background(), "any", ResolveRequest{Decision: ApprovalDecisionApprove, Scope: "forever"})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("got %v, want ErrInvalidScope", err)
	}
}

func TestCoordinatorResolveAmbiguousOptionReturnsErr(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})

	// Two allow_* options — operator must disambiguate.
	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "a1", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
			{OptionId: "a2", Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow always"},
			{OptionId: "r1", Kind: acp.PermissionOptionKindRejectOnce, Name: "Deny"},
		},
		ToolCall: acp.ToolCallUpdate{ToolCallId: "c"},
	}
	row := pendingRow(t, store, "s", "codex", "/tmp/w", req)
	_, err := c.Resolve(context.Background(), row.ID, ResolveRequest{Decision: ApprovalDecisionApprove, Scope: ApprovalScopeOnce})
	var ambiguous *AmbiguousOptionError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want *AmbiguousOptionError", err)
	}
	if len(ambiguous.Options) != 2 {
		t.Fatalf("ambiguous options len = %d, want 2", len(ambiguous.Options))
	}
}

func TestCoordinatorResolveExplicitSelectedOptionDisambiguates(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})

	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "a1", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
			{OptionId: "a2", Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow always"},
		},
	}
	row := pendingRow(t, store, "s", "codex", "/tmp/w", req)

	resolved, err := c.Resolve(context.Background(), row.ID, ResolveRequest{
		Decision:       ApprovalDecisionApprove,
		Scope:          ApprovalScopeOnce,
		SelectedOption: "a2",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.SelectedOption != "a2" {
		t.Fatalf("selected_option = %q, want a2", resolved.SelectedOption)
	}
}

func TestCoordinatorResolveUnknownSelectedOption(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})

	row := pendingRow(t, store, "s", "codex", "/tmp/w", unambiguousAllowOnceRequest())
	_, err := c.Resolve(context.Background(), row.ID, ResolveRequest{
		Decision:       ApprovalDecisionApprove,
		Scope:          ApprovalScopeOnce,
		SelectedOption: "phantom_id",
	})
	if !errors.Is(err, ErrUnknownOption) {
		t.Fatalf("got %v, want ErrUnknownOption", err)
	}
}

func TestCoordinatorResolveDenyWithoutRejectOption(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})

	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "a1", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
		},
	}
	row := pendingRow(t, store, "s", "codex", "/tmp/w", req)
	_, err := c.Resolve(context.Background(), row.ID, ResolveRequest{Decision: ApprovalDecisionDeny, Scope: ApprovalScopeOnce})
	if !errors.Is(err, ErrNoMatchingOption) {
		t.Fatalf("got %v, want ErrNoMatchingOption", err)
	}
}

// ─── Coordinator.Cancel ──────────────────────────────────────────────────────

func TestCoordinatorCancelMarksCancelledAndReturnsACPCancelled(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})
	row := pendingRow(t, store, "s", "codex", "/tmp/w", unambiguousAllowOnceRequest())

	resolved, err := c.Cancel(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resolved.Status != ApprovalStatusCancelled {
		t.Fatalf("status = %q, want cancelled", resolved.Status)
	}
	if resolved.Path != PathOperator {
		t.Fatalf("path = %q, want operator", resolved.Path)
	}
}

func TestCoordinatorCancelOnAlreadyResolvedReturns409(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})
	row := pendingRow(t, store, "s", "codex", "/tmp/w", unambiguousAllowOnceRequest())
	_, _ = c.Resolve(context.Background(), row.ID, ResolveRequest{Decision: ApprovalDecisionApprove, Scope: ApprovalScopeOnce})

	_, err := c.Cancel(context.Background(), row.ID)
	if !errors.Is(err, ErrApprovalAlreadyResolved) {
		t.Fatalf("got %v, want ErrApprovalAlreadyResolved", err)
	}
}

// ─── Prompt-mode blocking + waiter wakeup ────────────────────────────────────

func TestPromptModeBlocksUntilOperatorApproves(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{
		Mode:    ModePrompt,
		Store:   store,
		Timeout: 5 * time.Second, // long enough to ensure operator wins
	})

	// Drive RequestPermission in a goroutine so the test can call
	// Resolve while the prompt is blocked. The resolve must return
	// the operator's chosen ACP option, not Cancelled.
	respCh := make(chan acp.RequestPermissionResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := c.RequestPermission(
			context.Background(),
			RecordingContext{SessionID: "s", AdapterID: "codex", Workspace: "/tmp/w"},
			unambiguousAllowOnceRequest(),
		)
		respCh <- resp
		errCh <- err
	}()

	// Poll until the pending row appears (the goroutine raced to
	// register before we Resolve).
	var pending Approval
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := store.ListApprovals(context.Background(), "s", ApprovalStatusPending)
		if len(rows) == 1 {
			pending = rows[0]
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("pending row never appeared; goroutine never reached prompt block")
	}

	resolved, err := c.Resolve(context.Background(), pending.ID, ResolveRequest{
		Decision: ApprovalDecisionApprove, Scope: ApprovalScopeOnce,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != ApprovalStatusApproved {
		t.Fatalf("resolved status = %q, want approved", resolved.Status)
	}

	select {
	case resp := <-respCh:
		if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow_once_id" {
			t.Fatalf("blocked RequestPermission returned wrong outcome: %+v", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked RequestPermission to wake")
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RequestPermission err: %v", err)
	}
}

func TestPromptModeResolveDuringOnRequestedReturnsResolvedOutcome(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	var c *ApprovalCoordinator
	c = NewApprovalCoordinator(CoordinatorOptions{
		Mode: ModePrompt, Store: store, Timeout: 5 * time.Second,
		Hooks: CoordinatorHooks{
			OnRequested: func(a Approval) {
				_, err := c.Resolve(context.Background(), a.ID, ResolveRequest{
					Decision: ApprovalDecisionApprove,
					Scope:    ApprovalScopeOnce,
				})
				if err != nil {
					t.Errorf("Resolve from OnRequested: %v", err)
				}
			},
		},
	})

	resp, err := c.RequestPermission(
		context.Background(),
		RecordingContext{SessionID: "s", AdapterID: "codex", Workspace: "/tmp/w"},
		unambiguousAllowOnceRequest(),
	)
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow_once_id" {
		t.Fatalf("RequestPermission returned %+v, want selected allow_once_id", resp.Outcome)
	}
	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if len(rows) != 1 || rows[0].Status != ApprovalStatusApproved {
		t.Fatalf("row = %+v, want approved", rows)
	}
}

func TestPromptModeBlocksUntilOperatorCancels(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{
		Mode: ModePrompt, Store: store, Timeout: 5 * time.Second,
	})

	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := c.RequestPermission(
			context.Background(),
			RecordingContext{SessionID: "s", AdapterID: "codex"},
			unambiguousAllowOnceRequest(),
		)
		respCh <- resp
	}()

	var pending Approval
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := store.ListApprovals(context.Background(), "s", ApprovalStatusPending)
		if len(rows) == 1 {
			pending = rows[0]
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("pending row never appeared")
	}

	if _, err := c.Cancel(context.Background(), pending.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	select {
	case resp := <-respCh:
		if resp.Outcome.Cancelled == nil {
			t.Fatalf("expected ACP Cancelled outcome on operator cancel; got %+v", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancel to wake the waiter")
	}
}

func TestPromptModeContextCancelWakesAdapter(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{
		Mode: ModePrompt, Store: store, Timeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())

	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := c.RequestPermission(
			ctx, RecordingContext{SessionID: "s", AdapterID: "codex"},
			unambiguousAllowOnceRequest(),
		)
		respCh <- resp
	}()

	// Wait for the row to land, then cancel the request context. The
	// adapter receives Cancelled; the row gets path=operator,
	// status=cancelled.
	deadline := time.Now().Add(2 * time.Second)
	var pending Approval
	for time.Now().Before(deadline) {
		rows, _ := store.ListApprovals(context.Background(), "s", ApprovalStatusPending)
		if len(rows) == 1 {
			pending = rows[0]
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("pending row never appeared")
	}
	cancel()

	select {
	case resp := <-respCh:
		if resp.Outcome.Cancelled == nil {
			t.Fatalf("ctx cancel should yield ACP Cancelled; got %+v", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ctx cancel to unblock RequestPermission")
	}

	got, _ := store.GetApproval(context.Background(), pending.ID)
	if got.Status != ApprovalStatusCancelled {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}
}

func TestPromptModeTimeoutWakesAdapter(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{
		Mode: ModePrompt, Store: store, Timeout: 20 * time.Millisecond,
	})

	resp, err := c.RequestPermission(
		context.Background(),
		RecordingContext{SessionID: "s", AdapterID: "codex"},
		unambiguousAllowOnceRequest(),
	)
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("timeout should yield ACP Cancelled; got %+v", resp.Outcome)
	}
	rows, _ := store.ListApprovals(context.Background(), "s", "")
	if len(rows) != 1 || rows[0].Status != ApprovalStatusTimedOut || rows[0].Path != PathTimeout {
		t.Fatalf("row malformed after timeout: %+v", rows[0])
	}
}

// ─── ListApprovals / ListGrants / DeleteGrant ────────────────────────────────

func TestCoordinatorListGrantsFiltersByAdapterAndScope(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})

	now := time.Now().UTC()
	_, _ = store.CreateGrant(context.Background(), Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "file_write",
		Decision: ApprovalDecisionApprove, GrantedAt: now,
	})
	_, _ = store.CreateGrant(context.Background(), Grant{
		Scope: ApprovalScopeWorkspaceTool, AdapterID: "claude_code", ToolKind: "file_write",
		Workspace: "/tmp/w", Decision: ApprovalDecisionDeny, GrantedAt: now,
	})

	all, _ := c.ListGrants(context.Background(), GrantFilter{})
	if len(all) != 2 {
		t.Fatalf("got %d, want 2", len(all))
	}

	codex, _ := c.ListGrants(context.Background(), GrantFilter{AdapterID: "codex"})
	if len(codex) != 1 || codex[0].AdapterID != "codex" {
		t.Fatalf("filter by adapter failed: %+v", codex)
	}

	wsScope, _ := c.ListGrants(context.Background(), GrantFilter{Scope: ApprovalScopeWorkspaceTool})
	if len(wsScope) != 1 || wsScope[0].Scope != ApprovalScopeWorkspaceTool {
		t.Fatalf("filter by scope failed: %+v", wsScope)
	}
}

func TestCoordinatorDeleteGrant(t *testing.T) {
	t.Parallel()
	store := NewMemoryApprovalStore()
	c := NewApprovalCoordinator(CoordinatorOptions{Mode: ModePrompt, Store: store})
	g, _ := store.CreateGrant(context.Background(), Grant{
		Scope: ApprovalScopeAdapterTool, AdapterID: "codex", ToolKind: "file_write",
		Decision: ApprovalDecisionApprove, GrantedAt: time.Now().UTC(),
	})

	if err := c.DeleteGrant(context.Background(), g.ID); err != nil {
		t.Fatalf("DeleteGrant: %v", err)
	}
	if err := c.DeleteGrant(context.Background(), g.ID); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("second delete err = %v, want ErrApprovalNotFound", err)
	}
	rest, _ := c.ListGrants(context.Background(), GrantFilter{})
	if len(rest) != 0 {
		t.Fatalf("got %d grants after delete, want 0", len(rest))
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// unambiguousAllowOnceRequest is a permission request with a single
// allow_once option, so Coordinator.Resolve(approve) picks it without
// hitting the ambiguity path. The ToolCall has Kind=Edit so the
// recorded approval lands with tool_kind=file_write.
func unambiguousAllowOnceRequest() acp.RequestPermissionRequest {
	k := acp.ToolKindEdit
	title := "Edit foo.go"
	return acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "allow_once_id", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once"},
			{OptionId: "reject_once_id", Kind: acp.PermissionOptionKindRejectOnce, Name: "Deny"},
		},
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "call_1",
			Kind:       &k,
			Title:      &title,
		},
	}
}

// pendingRow seeds a pending approval row in the store, mirroring
// what RequestPermission would record. Lets tests cover Resolve/Cancel
// without spinning the prompt-block goroutine.
func pendingRow(t *testing.T, store *MemoryApprovalStore, sessionID, adapterID, workspace string, req acp.RequestPermissionRequest) Approval {
	t.Helper()
	now := time.Now().UTC()
	options := normalizeACPOptions(req.Options)
	row, err := store.CreateApproval(context.Background(), Approval{
		SessionID:    sessionID,
		AdapterID:    adapterID,
		Workspace:    workspace,
		ToolKind:     extractToolKind(req.ToolCall),
		ToolName:     extractToolName(req.ToolCall),
		Status:       ApprovalStatusPending,
		ACPOptions:   options,
		ScopeChoices: defaultScopeChoices(),
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed pendingRow: %v", err)
	}
	return row
}
