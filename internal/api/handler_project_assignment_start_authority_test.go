package api

import (
	"net/http"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectWorkAPI_AssignmentStartUsesPortableSkillSnapshot(t *testing.T) {
	t.Parallel()
	const projectID = "proj_start_skill_snapshot"
	handler, server := newProjectWorkCairnlineAssignmentAuthorityTestServer(t)
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:   projectID,
		Name: "Assignment skill snapshot",
	}, []cairnline.Role{{
		ID:                   "operator",
		ProjectID:            projectID,
		Name:                 "Operator",
		DefaultExecutionMode: cairnline.ExecutionManual,
		DefaultSkillIDs:      []string{"current-role-skill", "reordered-role-skill"},
	}}, []cairnline.WorkItem{{
		ID:        "work_snapshot",
		ProjectID: projectID,
		Title:     "Start snapshotted work",
		Status:    cairnline.WorkStatusReady,
		Priority:  cairnline.PriorityNormal,
	}}, []cairnline.Assignment{
		{
			ID:            "asgn_manual_snapshot",
			ProjectID:     projectID,
			WorkItemID:    "work_snapshot",
			RoleID:        "operator",
			ExecutionMode: cairnline.ExecutionManual,
			DesiredAgent: cairnline.DesiredAgent{
				Kind:     "human",
				SkillIDs: []string{"assignment-snapshot-skill"},
			},
		},
		{
			ID:            "asgn_task_snapshot",
			ProjectID:     projectID,
			WorkItemID:    "work_snapshot",
			RoleID:        "operator",
			ExecutionMode: cairnline.ExecutionMCPPull,
			DesiredAgent: cairnline.DesiredAgent{
				Kind:     "hecate",
				SkillIDs: []string{"assignment-snapshot-skill"},
			},
		},
	})
	handler.config.Projects.CairnlineReadSource = "embedded"
	handler.projects = nil

	client := newAPITestClient(t, server)
	started := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](
		client,
		http.StatusOK,
		http.MethodPost,
		"/hecate/v1/projects/"+projectID+"/work-items/work_snapshot/assignments/asgn_manual_snapshot/start",
		projectJourneyJSON(t, map[string]any{"driver_kind": projectwork.AssignmentDriverManual}),
	)
	if started.Data.Status != projectwork.AssignmentStatusRunning {
		t.Fatalf("manual assignment start = %+v, want running after role skill change", started.Data)
	}

	inputs, err := handler.cairnlineProjectAssignmentLaunchInputs(
		t.Context(),
		projectID,
		"work_snapshot",
		"asgn_task_snapshot",
	)
	if err != nil {
		t.Fatalf("load task assignment launch inputs: %v", err)
	}
	if len(inputs.Role.SkillIDs) != 1 || inputs.Role.SkillIDs[0] != "assignment-snapshot-skill" {
		t.Fatalf("launch role skills = %+v, want portable assignment snapshot", inputs.Role.SkillIDs)
	}

	portableTask := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_task_snapshot")
	expected := portableTask.Coordination()
	claimed, err := handler.claimStrictEmbeddedCairnlineAssignment(
		t.Context(),
		projectWorkAssignmentFromCairnline(portableTask),
		&expected,
	)
	if err != nil {
		t.Fatalf("claim strict task assignment with snapshotted skills: %v", err)
	}
	if claimed.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("strict claimed assignment = %+v, want queued facade state", claimed)
	}
	claimedPortable := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_task_snapshot")
	if claimedPortable.Status != cairnline.AssignmentClaimed || len(claimedPortable.DesiredAgent.SkillIDs) != 1 || claimedPortable.DesiredAgent.SkillIDs[0] != "assignment-snapshot-skill" {
		t.Fatalf("strict portable claim = %+v, want original assignment skill snapshot", claimedPortable)
	}
}
