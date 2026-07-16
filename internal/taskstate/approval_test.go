package taskstate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestTaskStore_UpdatePendingApprovalOnlyTransitionsPending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		store func(t *testing.T) Store
	}{
		{
			name: "memory",
			store: func(t *testing.T) Store {
				t.Helper()
				return NewMemoryStore()
			},
		},
		{
			name: "sqlite",
			store: func(t *testing.T) Store {
				t.Helper()
				return newSQLiteTestStore(t)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tt.store(t)
			createdAt := time.Now().UTC()

			if _, err := store.CreateTask(ctx, types.Task{ID: "task-ap", Status: "awaiting_approval"}); err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			pending := types.TaskApproval{
				ID:          "approval-1",
				TaskID:      "task-ap",
				RunID:       "run-ap",
				Kind:        "shell_command",
				Status:      "pending",
				RequestedBy: "agent",
				CreatedAt:   createdAt,
			}
			if _, err := store.CreateApproval(ctx, pending); err != nil {
				t.Fatalf("CreateApproval: %v", err)
			}

			pending.Status = "approved"
			pending.ResolvedBy = "operator"
			pending.ResolvedAt = createdAt.Add(time.Second)
			updated, ok, err := store.UpdatePendingApproval(ctx, pending)
			if err != nil || !ok {
				t.Fatalf("UpdatePendingApproval pending: ok=%v err=%v", ok, err)
			}
			if updated.Status != "approved" {
				t.Fatalf("updated status = %q, want approved", updated.Status)
			}

			stale := updated
			stale.Status = "cancelled"
			stale.ResolvedBy = "system"
			stale.ResolutionNote = "run cancelled"
			if _, ok, err := store.UpdatePendingApproval(ctx, stale); err != nil || ok {
				t.Fatalf("UpdatePendingApproval resolved: ok=%v err=%v, want ok=false err=nil", ok, err)
			}
			got, found, err := store.GetApproval(ctx, "task-ap", "approval-1")
			if err != nil || !found {
				t.Fatalf("GetApproval: found=%v err=%v", found, err)
			}
			if got.Status != "approved" || got.ResolvedBy != "operator" {
				t.Fatalf("resolved approval was clobbered: %+v", got)
			}
		})
	}
}

func TestApprovalResolutionTransitionValidationEnforcesDerivedContract(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	approved := PendingApprovalResolution{
		ApprovalID: "approval-approved", Status: "approved", ResolvedAt: now,
		RequestID: "request-resolution", TraceID: "trace-resolution",
	}
	rejected := PendingApprovalResolution{
		ApprovalID: "approval-rejected", Status: "rejected", ResolvedAt: now,
		RequestID: "request-resolution", TraceID: "trace-resolution",
	}
	taskCandidate := types.Task{ID: "task", LatestRequestID: "request-resolution", LatestTraceID: "trace-resolution"}
	runCandidate := types.TaskRun{ID: "run", TaskID: "task", RequestID: "request-resolution", TraceID: "trace-resolution"}

	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{
			name: "approved resolution forbids caller events",
			err: validateRunStateTransition(RunStateTransition{
				Task:                func() types.Task { candidate := taskCandidate; candidate.Status = "queued"; return candidate }(),
				Run:                 func() types.TaskRun { candidate := runCandidate; candidate.Status = "queued"; return candidate }(),
				ExpectedRunStatuses: []string{"awaiting_approval"}, ApprovalResolution: &approved,
				Events: []RunEventSpec{{EventType: "approval.resolved"}},
			}),
			wantErr: "events are store-derived",
		},
		{
			name: "rejected resolution forbids caller events",
			err: validateTerminalTransition(TerminalRunTransition{
				Task:               func() types.Task { candidate := taskCandidate; candidate.Status = "cancelled"; return candidate }(),
				Run:                func() types.TaskRun { candidate := runCandidate; candidate.Status = "cancelled"; return candidate }(),
				ApprovalResolution: &rejected,
				TerminalEvent:      &RunEventSpec{EventType: "approval.resolved"},
			}),
			wantErr: "events are store-derived",
		},
		{
			name: "rejected resolution forbids stale task projection",
			err: validateTerminalTransition(TerminalRunTransition{
				Task:                   func() types.Task { candidate := taskCandidate; candidate.Status = "cancelled"; return candidate }(),
				Run:                    func() types.TaskRun { candidate := runCandidate; candidate.Status = "cancelled"; return candidate }(),
				ApprovalResolution:     &rejected,
				PreserveTaskProjection: true,
			}),
			wantErr: "cannot preserve",
		},
		{
			name: "resolution requires audit timestamp",
			err: validateRunStateTransition(RunStateTransition{
				Task:                func() types.Task { candidate := taskCandidate; candidate.Status = "queued"; return candidate }(),
				Run:                 func() types.TaskRun { candidate := runCandidate; candidate.Status = "queued"; return candidate }(),
				ExpectedRunStatuses: []string{"awaiting_approval"},
				ApprovalResolution: &PendingApprovalResolution{
					ApprovalID: "approval", Status: "approved", RequestID: "request-resolution", TraceID: "trace-resolution",
				},
			}),
			wantErr: "resolution time is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.err == nil || !strings.Contains(tt.err.Error(), tt.wantErr) {
				t.Fatalf("validation error = %v, want %q", tt.err, tt.wantErr)
			}
		})
	}
}

func TestRunStateEventFromSpecCanonicalSnapshotCannotBeOverridden(t *testing.T) {
	t.Parallel()

	run := types.TaskRun{ID: "run", TaskID: "task", Status: "queued"}
	steps := []types.TaskStep{{ID: "step", RunID: run.ID, Status: "awaiting_approval"}}
	artifacts := []types.TaskArtifact{{ID: "artifact", RunID: run.ID, Status: "ready"}}
	event := runStateEventFromSpec(RunEventSpec{
		EventType: "run.queued",
		Data: map[string]any{
			"run":       types.TaskRun{Status: "failed"},
			"steps":     []types.TaskStep{{Status: "failed"}},
			"artifacts": []types.TaskArtifact{{Status: "failed"}},
			"resume":    true,
		},
		IncludeRunSnapshot: true,
	}, run.TaskID, run, steps, artifacts, time.Now().UTC())

	if got, ok := event.Data["run"].(types.TaskRun); !ok || got.Status != "queued" {
		t.Fatalf("event run snapshot = %#v, want authoritative queued run", event.Data["run"])
	}
	if got, ok := event.Data["steps"].([]types.TaskStep); !ok || len(got) != 1 || got[0].Status != "awaiting_approval" {
		t.Fatalf("event step snapshot = %#v, want authoritative step", event.Data["steps"])
	}
	if got, ok := event.Data["artifacts"].([]types.TaskArtifact); !ok || len(got) != 1 || got[0].Status != "ready" {
		t.Fatalf("event artifact snapshot = %#v, want authoritative artifact", event.Data["artifacts"])
	}
	if event.Data["resume"] != true {
		t.Fatalf("event custom data = %#v, want resume preserved", event.Data)
	}
}

func TestTaskStore_UpdatePendingApprovalValidatesIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		store func(t *testing.T) Store
	}{
		{
			name: "memory",
			store: func(t *testing.T) Store {
				t.Helper()
				return NewMemoryStore()
			},
		},
		{
			name: "sqlite",
			store: func(t *testing.T) Store {
				t.Helper()
				return newSQLiteTestStore(t)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tt.store(t)

			cases := []struct {
				name     string
				approval types.TaskApproval
				wantErr  string
			}{
				{
					name:     "empty id",
					approval: types.TaskApproval{TaskID: "task-ap", Status: "approved"},
					wantErr:  "approval id is required",
				},
				{
					name:     "empty task id",
					approval: types.TaskApproval{ID: "approval-1", Status: "approved"},
					wantErr:  "approval task id is required",
				},
			}

			for _, tc := range cases {
				if _, ok, err := store.UpdatePendingApproval(ctx, tc.approval); err == nil || ok {
					t.Fatalf("%s: UpdatePendingApproval ok=%v err=%v, want validation error", tc.name, ok, err)
				} else if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("%s: error = %q, want %q", tc.name, err.Error(), tc.wantErr)
				}
			}
		})
	}
}
