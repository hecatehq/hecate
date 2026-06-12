//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestProjectAssistantProposeApplyWorkItemE2E(t *testing.T) {
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite")
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	proposalID := "pa_e2e_" + suffix
	workItemID := "work_assistant_" + suffix

	project := postJSONDecodeStatus[e2eProjectResponse](t, baseURL+"/hecate/v1/projects", fmt.Sprintf(`{
		"name": "project assistant e2e %s"
	}`, suffix), http.StatusCreated)
	projectID := project.Data.ID
	if projectID == "" {
		t.Fatal("created project id is empty")
	}

	proposalBody := e2eProjectLaunchJSON(t, map[string]any{
		"id":      proposalID,
		"title":   "Create assistant work",
		"summary": "Exercise Project Assistant through the real HTTP API.",
		"actions": []map[string]any{{
			"kind":   "create_work_item",
			"target": map[string]string{"project_id": projectID},
			"patch": map[string]any{
				"project_id": projectID,
				"id":         workItemID,
				"title":      "Assistant-created work",
				"brief":      "Created by Project Assistant e2e.",
				"status":     "ready",
				"priority":   "normal",
			},
			"reason": "Create reviewable work without starting execution.",
		}},
	})
	proposal := postJSONDecode[e2eProjectAssistantProposalResponse](t, baseURL+"/hecate/v1/project-assistant/propose", proposalBody)
	if proposal.Object != "project_assistant.proposal" || proposal.Data.ID != proposalID || len(proposal.Data.Actions) != 1 {
		t.Fatalf("proposal = %+v, want one project assistant action", proposal)
	}

	applyBody := e2eProjectLaunchJSON(t, map[string]any{
		"proposal": proposal.Data,
		"confirm":  true,
	})
	applied := postJSONDecode[e2eProjectAssistantApplyResponse](t, baseURL+"/hecate/v1/project-assistant/apply", applyBody)
	if applied.Object != "project_assistant.apply_result" || !applied.Data.Applied || applied.Data.ProposalID != proposalID {
		t.Fatalf("apply result = %+v, want applied %s", applied, proposalID)
	}
	if len(applied.Data.Actions) != 1 || applied.Data.Actions[0].Kind != "create_work_item" || applied.Data.Actions[0].ID != workItemID {
		t.Fatalf("applied actions = %+v, want created work item", applied.Data.Actions)
	}

	items := getJSON[e2eProjectWorkItemsResponse](t, fmt.Sprintf("%s/hecate/v1/projects/%s/work-items", baseURL, projectID))
	if items.Object != "project_work_items" || len(items.Data) != 1 {
		t.Fatalf("work items = %+v, want one project work item", items)
	}
	item := items.Data[0]
	if item.ID != workItemID || item.Title != "Assistant-created work" || item.Status != "ready" {
		t.Fatalf("work item = %+v, want assistant-created ready item", item)
	}
}

type e2eProjectAssistantProposalResponse struct {
	Object string                      `json:"object"`
	Data   e2eProjectAssistantProposal `json:"data"`
}

type e2eProjectAssistantProposal struct {
	ID      string                      `json:"id"`
	Title   string                      `json:"title"`
	Actions []e2eProjectAssistantAction `json:"actions"`
	Raw     map[string]json.RawMessage  `json:"-"`
}

func (p *e2eProjectAssistantProposal) UnmarshalJSON(raw []byte) error {
	type alias e2eProjectAssistantProposal
	var decoded alias
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawFields); err != nil {
		return err
	}
	*p = e2eProjectAssistantProposal(decoded)
	p.Raw = rawFields
	return nil
}

func (p e2eProjectAssistantProposal) MarshalJSON() ([]byte, error) {
	if p.Raw != nil {
		return json.Marshal(p.Raw)
	}
	type alias e2eProjectAssistantProposal
	return json.Marshal(alias(p))
}

type e2eProjectAssistantAction struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

type e2eProjectAssistantApplyResponse struct {
	Object string `json:"object"`
	Data   struct {
		ProposalID string                      `json:"proposal_id"`
		Applied    bool                        `json:"applied"`
		Actions    []e2eProjectAssistantAction `json:"actions"`
	} `json:"data"`
}

type e2eProjectWorkItemsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"data"`
}
