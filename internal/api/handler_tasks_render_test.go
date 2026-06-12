package api

import (
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestRenderTaskRunUsesParentTaskProjectLinkage(t *testing.T) {
	t.Parallel()

	item := renderTaskRun(types.TaskRun{
		ID:     "run_1",
		TaskID: "task_1",
		Status: "running",
	}, types.Task{
		ID:           "task_1",
		ProjectID:    "proj_1",
		WorkItemID:   "work_1",
		AssignmentID: "asgn_1",
	})
	if item.ProjectID != "proj_1" || item.WorkItemID != "work_1" || item.AssignmentID != "asgn_1" {
		t.Fatalf("run linkage = project %q work %q assignment %q, want proj_1/work_1/asgn_1", item.ProjectID, item.WorkItemID, item.AssignmentID)
	}
}
