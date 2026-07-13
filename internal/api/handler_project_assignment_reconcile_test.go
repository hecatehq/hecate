package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProjectWorkAPI_TaskReconciliationFollowsLatestOwnedRun(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)
	const projectID = "proj_task_reconcile_boundaries"
	base := time.Date(2026, 7, 13, 15, 0, 0, 0, time.UTC)

	type reconciliationCase struct {
		name                  string
		portableRunID         string
		latestRunID           string
		latestRunStatus       string
		taskAssignmentID      string
		latestRunAssignmentID string
		wantRunID             string
	}
	cases := []reconciliationCase{
		{
			name:                  "resumed",
			portableRunID:         "run_reconcile_resumed_old",
			latestRunID:           "run_reconcile_resumed_new",
			latestRunStatus:       "running",
			taskAssignmentID:      "asgn_reconcile_resumed",
			latestRunAssignmentID: "asgn_reconcile_resumed",
			wantRunID:             "run_reconcile_resumed_new",
		},
		{
			name:                  "task_mismatch",
			portableRunID:         "run_reconcile_task_mismatch",
			latestRunID:           "run_reconcile_task_mismatch",
			latestRunStatus:       "failed",
			taskAssignmentID:      "asgn_elsewhere",
			latestRunAssignmentID: "asgn_elsewhere",
			wantRunID:             "run_reconcile_task_mismatch",
		},
		{
			name:                  "run_mismatch",
			portableRunID:         "run_reconcile_run_mismatch",
			latestRunID:           "run_reconcile_run_mismatch",
			latestRunStatus:       "completed",
			taskAssignmentID:      "asgn_reconcile_run_mismatch",
			latestRunAssignmentID: "asgn_elsewhere",
			wantRunID:             "run_reconcile_run_mismatch",
		},
	}

	if err := handler.withCairnlineEmbeddedService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{ID: projectID, Name: "Task reconciliation boundaries"}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:                   "role_reconcile",
			ProjectID:            projectID,
			Name:                 "Task operator",
			DefaultExecutionMode: cairnline.ExecutionMCPPull,
		}); err != nil {
			return err
		}
		for _, tc := range cases {
			assignmentID := "asgn_reconcile_" + tc.name
			workItemID := "work_reconcile_" + tc.name
			taskID := "task_reconcile_" + tc.name
			if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
				ID:          workItemID,
				ProjectID:   projectID,
				Title:       "Reconcile " + tc.name,
				Status:      cairnline.WorkStatusReady,
				Priority:    cairnline.PriorityNormal,
				OwnerRoleID: "role_reconcile",
			}); err != nil {
				return err
			}
			if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
				ID:            assignmentID,
				ProjectID:     projectID,
				WorkItemID:    workItemID,
				RoleID:        "role_reconcile",
				ExecutionMode: cairnline.ExecutionMCPPull,
				DesiredAgent:  cairnline.DesiredAgent{Kind: "hecate"},
			}); err != nil {
				return err
			}
			if _, err := service.ClaimAssignment(t.Context(), projectID, assignmentID, projectAssignmentStartClaimedByHecate); err != nil {
				return err
			}
			ref := cairnline.ExecutionRef{
				Kind:   projectwork.AssignmentExecutionKindTaskRun,
				TaskID: taskID,
				RunID:  tc.portableRunID,
			}
			if _, err := service.PrepareAssignment(t.Context(), projectID, assignmentID, cairnline.AssignmentPreparation{
				ClaimedBy:         projectAssignmentStartClaimedByHecate,
				ExecutionRef:      ref,
				ContextSnapshotID: "ctx_reconcile_" + tc.name,
			}); err != nil {
				return err
			}
			if _, err := service.UpdateAssignmentStatus(t.Context(), projectID, assignmentID, cairnline.AssignmentRunning, ref); err != nil {
				return err
			}

			if _, err := handler.taskStore.CreateTask(t.Context(), types.Task{
				ID:           taskID,
				ProjectID:    projectID,
				WorkItemID:   workItemID,
				AssignmentID: tc.taskAssignmentID,
				Title:        tc.name,
				Status:       tc.latestRunStatus,
				LatestRunID:  tc.latestRunID,
				CreatedAt:    base,
				UpdatedAt:    base.Add(time.Minute),
			}); err != nil {
				return err
			}
			if tc.portableRunID != tc.latestRunID {
				if _, err := handler.taskStore.CreateRun(t.Context(), types.TaskRun{
					ID:           tc.portableRunID,
					TaskID:       taskID,
					ProjectID:    projectID,
					WorkItemID:   workItemID,
					AssignmentID: assignmentID,
					Status:       "failed",
					StartedAt:    base,
					FinishedAt:   base.Add(30 * time.Second),
				}); err != nil {
					return err
				}
			}
			if _, err := handler.taskStore.CreateRun(t.Context(), types.TaskRun{
				ID:           tc.latestRunID,
				TaskID:       taskID,
				ProjectID:    projectID,
				WorkItemID:   workItemID,
				AssignmentID: tc.latestRunAssignmentID,
				Status:       tc.latestRunStatus,
				StartedAt:    base.Add(time.Minute),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed task reconciliation boundaries: %v", err)
	}

	client := newAPITestClient(t, server)
	mustRequestJSONStatus[ProjectActivityEnvelope](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/"+projectID+"/activity", "")
	for _, tc := range cases {
		portable := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_reconcile_"+tc.name)
		if portable.Status != cairnline.AssignmentRunning || portable.ExecutionRef.RunID != tc.wantRunID {
			t.Fatalf("portable %s assignment = %+v, want running with run %q", tc.name, portable, tc.wantRunID)
		}
	}
}
