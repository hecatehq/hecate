//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestProjectActivityProjectionE2E(t *testing.T) {
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite")

	project := postJSONDecodeStatus[e2eProjectResponse](t, baseURL+"/hecate/v1/projects", `{
		"name": "project activity e2e"
	}`, http.StatusCreated)
	projectID := project.Data.ID
	if projectID == "" {
		t.Fatal("created project id is empty")
	}

	postJSONDecodeStatus[e2eProjectWorkRoleResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/roles", `{
		"id": "role_activity",
		"name": "Activity reviewer"
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkItemResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items", `{
		"id": "work_activity",
		"title": "Review activity projection",
		"status": "running",
		"priority": "high"
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkAssignmentResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_activity/assignments", `{
		"id": "asgn_activity",
		"role_id": "role_activity",
		"status": "completed"
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkArtifactResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_activity/artifacts", `{
		"id": "artifact_activity",
		"assignment_id": "asgn_activity",
		"kind": "brief",
		"title": "E2E artifact",
		"body": "Activity projection reached the real server."
	}`, http.StatusCreated)

	activity := getJSON[e2eProjectActivityResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/activity")
	if activity.Object != "project_activity" || activity.Data.ProjectID != projectID {
		t.Fatalf("activity envelope = %+v, want project_activity for project %q", activity, projectID)
	}
	if activity.Data.Summary.WorkItemCount != 1 || activity.Data.Summary.AssignmentCount != 1 || activity.Data.Summary.CompletedCount != 1 || activity.Data.Summary.RecentCount != 1 {
		t.Fatalf("activity summary = %+v, want one completed assignment", activity.Data.Summary)
	}
	if len(activity.Data.Buckets.Completed) != 1 || len(activity.Data.Buckets.Recent) != 1 {
		t.Fatalf("activity buckets = %+v, want one completed and one recent item", activity.Data.Buckets)
	}
	item := activity.Data.Buckets.Completed[0]
	if item.ID != "asgn_activity" || item.BlockingSignal != "completed" || item.StatusSummary != "completed with 1 artifact" {
		t.Fatalf("completed activity item = %+v, want completed artifact summary", item)
	}
	if item.ArtifactSummary.Count != 1 || item.ArtifactSummary.LatestTitle != "E2E artifact" || item.ArtifactSummary.AssignmentID != "asgn_activity" {
		t.Fatalf("artifact summary = %+v, want linked artifact signal", item.ArtifactSummary)
	}
	if activity.Data.Buckets.Recent[0].ID != "asgn_activity" {
		t.Fatalf("recent item id = %q, want asgn_activity", activity.Data.Buckets.Recent[0].ID)
	}
}

func postJSONDecodeStatus[T any](t *testing.T, url, body string, status int) T {
	t.Helper()
	resp := postJSON(t, url, body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("POST %s: HTTP %d, want %d -- body: %s", url, resp.StatusCode, status, readBody(t, resp))
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode POST %s: %v", url, err)
	}
	return out
}

type e2eProjectResponse struct {
	Object string `json:"object"`
	Data   struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eProjectWorkRoleResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eProjectWorkItemResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eProjectWorkAssignmentResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eProjectWorkArtifactResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eProjectActivityResponse struct {
	Object string `json:"object"`
	Data   struct {
		ProjectID string `json:"project_id"`
		Summary   struct {
			WorkItemCount   int `json:"work_item_count"`
			AssignmentCount int `json:"assignment_count"`
			CompletedCount  int `json:"completed_count"`
			RecentCount     int `json:"recent_count"`
		} `json:"summary"`
		Buckets struct {
			Completed []e2eProjectActivityItem `json:"completed"`
			Recent    []e2eProjectActivityItem `json:"recent"`
		} `json:"buckets"`
	} `json:"data"`
}

type e2eProjectActivityItem struct {
	ID              string `json:"id"`
	BlockingSignal  string `json:"blocking_signal"`
	StatusSummary   string `json:"status_summary"`
	ArtifactSummary struct {
		Count        int    `json:"count"`
		LatestTitle  string `json:"latest_title"`
		AssignmentID string `json:"assignment_id"`
	} `json:"artifact_summary"`
}
