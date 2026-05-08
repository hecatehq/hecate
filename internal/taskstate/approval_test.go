package taskstate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
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
