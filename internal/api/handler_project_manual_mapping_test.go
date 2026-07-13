package api

import (
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectWorkManualDriverCairnlineMapping(t *testing.T) {
	if got := projectWorkAssignmentDriverFromCairnline(cairnline.ExecutionManual); got != projectwork.AssignmentDriverManual {
		t.Fatalf("projectWorkAssignmentDriverFromCairnline(manual) = %q, want %q", got, projectwork.AssignmentDriverManual)
	}
	if got := projectWorkAssignmentDriverFromCairnline(cairnline.ExecutionMCPPull); got != projectwork.AssignmentDriverHecateTask {
		t.Fatalf("projectWorkAssignmentDriverFromCairnline(mcp_pull) = %q, want existing %q fallback", got, projectwork.AssignmentDriverHecateTask)
	}

	role := projectWorkRoleFromCairnline(cairnline.Role{
		ID:                   "operator",
		ProjectID:            "proj_manual",
		Name:                 "Operator",
		DefaultExecutionMode: cairnline.ExecutionManual,
	}, projectwork.AgentRoleProfile{})
	if role.DefaultDriverKind != projectwork.AssignmentDriverManual {
		t.Fatalf("manual role default driver = %q, want %q", role.DefaultDriverKind, projectwork.AssignmentDriverManual)
	}

	assignment := projectWorkAssignmentFromCairnline(cairnline.Assignment{
		ID:            "asgn_manual",
		ProjectID:     "proj_manual",
		WorkItemID:    "work_manual",
		RoleID:        role.ID,
		ExecutionMode: cairnline.ExecutionManual,
		Status:        cairnline.AssignmentQueued,
	})
	if assignment.DriverKind != projectwork.AssignmentDriverManual {
		t.Fatalf("manual assignment driver = %q, want %q", assignment.DriverKind, projectwork.AssignmentDriverManual)
	}
	if !validProjectAssignmentDriverKindForCairnlineAuthority(projectwork.AssignmentDriverManual) {
		t.Fatal("Cairnline assignment authority should accept manual driver")
	}
	if !validProjectRoleDefaultDriverForCairnlineAuthority(projectwork.AssignmentDriverManual) {
		t.Fatal("Cairnline role authority should accept manual default driver")
	}
}
