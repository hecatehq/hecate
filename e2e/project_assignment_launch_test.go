//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectAssignmentPreflightStartLaunchPlanE2E(t *testing.T) {
	workDir := t.TempDir()
	canonicalWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("canonicalize temp dir: %v", err)
	}
	upstream, _ := fakeAgentLoopToolCallingUpstream(t)
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
		"PROVIDER_FAKE_MODELS="+agentLoopE2EModel,
	)

	project := postJSONDecodeStatus[e2eProjectResponse](t, baseURL+"/hecate/v1/projects", e2eProjectLaunchJSON(t, map[string]any{
		"name":                   "Project launch plan e2e",
		"workspace_path":         canonicalWorkDir,
		"workspace_kind":         "git",
		"default_provider":       "fake",
		"default_model":          agentLoopE2EModel,
		"default_workspace_mode": "in_place",
	}), http.StatusCreated)
	projectID := project.Data.ID
	if projectID == "" {
		t.Fatal("created project id is empty")
	}
	postJSONDecodeStatus[e2eProjectWorkRoleResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/roles", `{
		"id": "role_launch",
		"name": "Launch engineer",
		"default_driver_kind": "hecate_task"
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkItemResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items", `{
		"id": "work_launch",
		"title": "Exercise launch plan",
		"brief": "Start a project assignment through the real API."
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkAssignmentResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_launch/assignments", `{
		"id": "asgn_launch",
		"role_id": "role_launch",
		"driver_kind": "hecate_task"
	}`, http.StatusCreated)

	preflight := getJSON[e2eProjectLaunchContextResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_launch/assignments/asgn_launch/preflight")
	if preflight.Data.ExecutionMode != "hecate_task" {
		t.Fatalf("preflight execution_mode = %q, want hecate_task", preflight.Data.ExecutionMode)
	}

	started := postJSONDecode[e2eProjectWorkAssignmentLaunchResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_launch/assignments/asgn_launch/start", `{}`)
	ref := started.Data.ExecutionRef
	if ref.Kind != "task_run" || ref.TaskID == "" || ref.RunID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("execution_ref = %+v, want task/run/context links", ref)
	}

	run := waitForE2ETaskRunTerminal(t, baseURL, ref.TaskID, ref.RunID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}
	if run.Provider != "fake" || run.ProviderKind != "local" || run.Model != agentLoopE2EModel {
		t.Fatalf("run route = provider %q kind %q model %q, want fake/local/%s", run.Provider, run.ProviderKind, run.Model, agentLoopE2EModel)
	}

	context := getJSON[e2eProjectLaunchContextResponse](t, baseURL+"/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/context")
	if context.Data.ID != ref.ContextSnapshotID {
		t.Fatalf("context id = %q, want execution_ref context %q", context.Data.ID, ref.ContextSnapshotID)
	}
	assertProjectLaunchContextMatches(t, preflight.Data, context.Data)
	if context.Data.Refs == nil || context.Data.Refs.ProjectID != projectID || context.Data.Refs.WorkItemID != "work_launch" || context.Data.Refs.AssignmentID != "asgn_launch" || context.Data.Refs.TaskID != ref.TaskID || context.Data.Refs.RunID != ref.RunID {
		t.Fatalf("context refs = %+v, want project/work/assignment/task/run refs", context.Data.Refs)
	}

	assignmentContext := getJSON[e2eProjectLaunchContextResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_launch/assignments/asgn_launch/context")
	if assignmentContext.Data.ID != ref.ContextSnapshotID {
		t.Fatalf("assignment context id = %q, want %q", assignmentContext.Data.ID, ref.ContextSnapshotID)
	}
}

func e2eProjectLaunchJSON(t *testing.T, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(raw)
}

func assertProjectLaunchContextMatches(t *testing.T, preflight, started e2eProjectLaunchContext) {
	t.Helper()
	if preflight.ExecutionMode != started.ExecutionMode ||
		preflight.Provider != started.Provider ||
		preflight.Model != started.Model ||
		preflight.ExecutionProfile != started.ExecutionProfile ||
		preflight.Workspace != started.Workspace {
		t.Fatalf("preflight launch shape = mode/provider/model/profile/workspace %q/%q/%q/%q/%q, started = %q/%q/%q/%q/%q",
			preflight.ExecutionMode, preflight.Provider, preflight.Model, preflight.ExecutionProfile, preflight.Workspace,
			started.ExecutionMode, started.Provider, started.Model, started.ExecutionProfile, started.Workspace)
	}
}

type e2eProjectWorkAssignmentLaunchResponse struct {
	Data struct {
		ID           string                        `json:"id"`
		Status       string                        `json:"status"`
		ExecutionRef e2eProjectAssignmentExecution `json:"execution_ref"`
	} `json:"data"`
}

type e2eProjectAssignmentExecution struct {
	Kind              string `json:"kind"`
	TaskID            string `json:"task_id,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	ContextSnapshotID string `json:"context_snapshot_id,omitempty"`
	Status            string `json:"status,omitempty"`
}

type e2eProjectLaunchContextResponse struct {
	Object string                  `json:"object"`
	Data   e2eProjectLaunchContext `json:"data"`
}

type e2eProjectLaunchContext struct {
	ID               string                       `json:"id"`
	ExecutionMode    string                       `json:"execution_mode"`
	Provider         string                       `json:"provider,omitempty"`
	Model            string                       `json:"model,omitempty"`
	ExecutionProfile string                       `json:"execution_profile,omitempty"`
	Workspace        string                       `json:"workspace,omitempty"`
	Refs             *e2eProjectLaunchContextRefs `json:"refs,omitempty"`
}

type e2eProjectLaunchContextRefs struct {
	ProjectID    string `json:"project_id,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
}
