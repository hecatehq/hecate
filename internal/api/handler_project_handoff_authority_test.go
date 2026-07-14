package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectHandoffAuthorityForwardsRevisionsAndAcceptsWithAtomicFollowUp(t *testing.T) {
	handler, server := newProjectWorkCairnlineCollaborationAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Handoff authority",
	}))
	projectID := project.Data.ID

	mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                  "role_follow_up",
		"name":                "Follow-up owner",
		"default_driver_kind": projectwork.AssignmentDriverHecateTask,
	}))
	mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_source",
		"title":         "Source work",
		"owner_role_id": "role_follow_up",
	}))
	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_source/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_follow_up",
		"target_role_id":          "role_follow_up",
		"title":                   "Continue the work",
		"summary":                 "Carry the reviewed context into a follow-up.",
		"recommended_next_action": "Create a focused follow-up.",
	}))
	path := "/hecate/v1/projects/" + projectID + "/work-items/work_source/handoffs/handoff_follow_up"

	patched := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusOK, http.MethodPatch, path, projectJourneyJSON(t, map[string]any{
		"expected_updated_at": handoff.Data.UpdatedAt,
		"summary":             "Carry the current reviewed context into a follow-up.",
	}))
	if patched.Data.Summary != "Carry the current reviewed context into a follow-up." {
		t.Fatalf("patched handoff = %+v, want current summary", patched.Data)
	}

	staleCases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "patch",
			method: http.MethodPatch,
			path:   path,
			body: projectJourneyJSON(t, map[string]any{
				"expected_updated_at": handoff.Data.UpdatedAt,
				"summary":             "A stale editor must not win.",
			}),
		},
		{
			name:   "status",
			method: http.MethodPost,
			path:   path + "/status",
			body: projectJourneyJSON(t, map[string]any{
				"expected_updated_at": handoff.Data.UpdatedAt,
				"status":              projectwork.HandoffStatusAccepted,
			}),
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   path,
			body: projectJourneyJSON(t, map[string]any{
				"expected_updated_at": handoff.Data.UpdatedAt,
			}),
		},
		{
			name:   "atomic follow-up",
			method: http.MethodPost,
			path:   path + "/accept-with-follow-up",
			body: projectJourneyJSON(t, map[string]any{
				"expected_updated_at": handoff.Data.UpdatedAt,
				"idempotency_key":     "stale-operator-action",
				"intent":              cairnline.HandoffFollowUpIntentAcceptAndEnsure,
			}),
		},
	}
	for _, test := range staleCases {
		t.Run("stale "+test.name, func(t *testing.T) {
			response := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusConflict, test.method, test.path, test.body)
			if response.Error.Type != errCodeConflict {
				t.Fatalf("error = %+v, want conflict", response.Error)
			}
		})
	}

	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: projectID, WorkItemID: "work_source"})
	if err != nil {
		t.Fatalf("ListAssignments(before atomic success): %v", err)
	}
	if len(assignments) != 0 {
		t.Fatalf("assignments after stale atomic request = %+v, want no fabricated follow-up", assignments)
	}
	authoritative := getMirroredCairnlineHandoffForTest(t, handler, projectID, "work_source", "handoff_follow_up")
	if authoritative.Body != patched.Data.Summary || authoritative.Status != cairnline.HandoffStatusOpen {
		t.Fatalf("authoritative handoff after stale requests = %+v, want patched open record", authoritative)
	}

	request := projectJourneyJSON(t, map[string]any{
		"expected_updated_at": patched.Data.UpdatedAt,
		"idempotency_key":     "operator-action-1",
		"intent":              cairnline.HandoffFollowUpIntentAcceptAndEnsure,
	})
	accepted := mustRequestJSONStatus[ProjectHandoffFollowUpEnvelope](client, http.StatusOK, http.MethodPost, path+"/accept-with-follow-up", request)
	if accepted.Object != "project_handoff_follow_up" || accepted.Data.Outcome != cairnline.HandoffFollowUpCreated || accepted.Data.Replayed {
		t.Fatalf("follow-up response = %+v, want fresh created outcome", accepted)
	}
	if accepted.Data.Handoff.Status != projectwork.HandoffStatusAccepted || accepted.Data.Handoff.TargetAssignmentID != accepted.Data.Assignment.ID {
		t.Fatalf("follow-up records = %+v, want accepted handoff linked to returned assignment", accepted.Data)
	}
	if accepted.Data.Assignment.WorkItemID != "work_source" || accepted.Data.Assignment.RoleID != "role_follow_up" || accepted.Data.Assignment.Status != projectwork.AssignmentStatusQueued || accepted.Data.Assignment.ExecutionRef != nil {
		t.Fatalf("follow-up assignment = %+v, want queued unlaunched assignment", accepted.Data.Assignment)
	}
	assertHecateShadowHandoffStatusForTest(t, handler, projectID, "work_source", "handoff_follow_up", projectwork.HandoffStatusAccepted)

	replayed := mustRequestJSONStatus[ProjectHandoffFollowUpEnvelope](client, http.StatusOK, http.MethodPost, path+"/accept-with-follow-up", request)
	if !replayed.Data.Replayed || replayed.Data.Outcome != accepted.Data.Outcome || replayed.Data.Assignment.ID != accepted.Data.Assignment.ID || replayed.Data.Handoff.TargetAssignmentID != accepted.Data.Assignment.ID {
		t.Fatalf("replayed follow-up = %+v, want same authoritative records", replayed.Data)
	}
	mismatchedReplay := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusConflict, http.MethodPost, path+"/accept-with-follow-up", projectJourneyJSON(t, map[string]any{
		"expected_updated_at": replayed.Data.Handoff.UpdatedAt,
		"idempotency_key":     "operator-action-1",
		"intent":              cairnline.HandoffFollowUpIntentAcceptAndEnsure,
	}))
	if mismatchedReplay.Error.Type != errCodeConflict {
		t.Fatalf("mismatched replay error = %+v, want conflict", mismatchedReplay.Error)
	}

	portableAssignment := getMirroredCairnlineAssignmentForTest(t, handler, projectID, accepted.Data.Assignment.ID)
	if portableAssignment.WorkItemID != "work_source" || portableAssignment.Status != cairnline.AssignmentQueued {
		t.Fatalf("Cairnline assignment = %+v, want queued authoritative follow-up", portableAssignment)
	}
	shadowAssignment := getStoredProjectWorkAssignmentForTest(t, handler, projectID, "work_source", accepted.Data.Assignment.ID)
	if shadowAssignment.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("Hecate assignment shadow = %+v, want returned queued record", shadowAssignment)
	}
	assertHecateShadowHandoffStatusForTest(t, handler, projectID, "work_source", "handoff_follow_up", projectwork.HandoffStatusAccepted)

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, path, projectJourneyJSON(t, map[string]any{
		"expected_updated_at": replayed.Data.Handoff.UpdatedAt,
	}))
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open embedded Cairnline after delete: %v", err)
	}
	defer store.Close()
	if _, err := service.GetHandoff(t.Context(), projectID, "work_source", "handoff_follow_up"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetHandoff(after delete) error = %v, want ErrNotFound", err)
	}
}

func TestProjectHandoffAuthorityRequiresRevisionTokens(t *testing.T) {
	_, server := newProjectWorkCairnlineCollaborationAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	base := "/hecate/v1/projects/project/work-items/work/handoffs/handoff"
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "patch", method: http.MethodPatch, path: base, body: `{"summary":"missing revision"}`},
		{name: "status", method: http.MethodPost, path: base + "/status", body: `{"status":"accepted"}`},
		{name: "delete", method: http.MethodDelete, path: base, body: `{}`},
		{name: "atomic follow-up", method: http.MethodPost, path: base + "/accept-with-follow-up", body: `{"idempotency_key":"action","intent":"accept_and_ensure_follow_up"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusBadRequest, test.method, test.path, test.body)
			if response.Error.Type != errCodeInvalidRequest || response.Error.Message != "expected_updated_at is required" {
				t.Fatalf("error = %+v, want required revision", response.Error)
			}
		})
	}
}

func TestProjectHandoffMutationContractRequiresRevisionWithoutCairnlineAuthority(t *testing.T) {
	_, server := newProjectWorkTestServer()
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Legacy coordination project",
	}))
	projectID := project.Data.ID
	mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":    "work_revision_contract",
		"title": "Keep one mutation contract",
	}))
	mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_revision_contract/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_revision_contract",
		"title":                   "Revision contract",
		"summary":                 "Legacy storage still validates the public request contract.",
		"recommended_next_action": "Refresh before mutating.",
	}))
	path := "/hecate/v1/projects/" + projectID + "/work-items/work_revision_contract/handoffs/handoff_revision_contract"
	for _, test := range []struct {
		name   string
		method string
		body   string
	}{
		{name: "patch", method: http.MethodPatch, body: `{"summary":"missing revision"}`},
		{name: "status", method: http.MethodPost, body: `{"status":"accepted"}`},
		{name: "delete", method: http.MethodDelete, body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := path
			if test.name == "status" {
				target += "/status"
			}
			response := mustRequestJSONStatus[projectWorkErrorResponse](client, http.StatusBadRequest, test.method, target, test.body)
			if response.Error.Type != errCodeInvalidRequest || response.Error.Message != "expected_updated_at is required" {
				t.Fatalf("error = %+v, want required revision", response.Error)
			}
		})
	}
}
