package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projectapp"
)

func TestProjectMutationGate_ReentrantLeaseRejectsSecondProjectKey(t *testing.T) {
	t.Parallel()
	var gate projectMutationGate
	ctx, release, err := gate.begin(t.Context(), "project_a")
	if err != nil {
		t.Fatalf("begin project_a: %v", err)
	}
	defer release()

	nestedCtx, releaseNested, err := gate.begin(ctx, " project_a ")
	if err != nil {
		t.Fatalf("nested begin project_a: %v", err)
	}
	releaseNested()
	if nestedCtx != ctx {
		t.Fatal("nested same-project begin replaced the active lease context")
	}
	if _, _, err := gate.begin(ctx, "project_b"); !errors.Is(err, errProjectMutationFenceOrder) {
		t.Fatalf("nested begin project_b error = %v, want errProjectMutationFenceOrder", err)
	}
}

func TestProjectMutationGate_MultiProjectLeaseAdmitsNestedSubsetsAndBlocksDelete(t *testing.T) {
	t.Parallel()
	var gate projectMutationGate
	ctx, release, err := gate.beginMany(t.Context(), []string{"project_b", "project_a", "project_b"})
	if err != nil {
		t.Fatalf("begin projects: %v", err)
	}
	defer release()
	for _, projectID := range []string{"project_a", "project_b"} {
		_, releaseNested, err := gate.begin(ctx, projectID)
		if err != nil {
			t.Fatalf("nested begin %s: %v", projectID, err)
		}
		releaseNested()
	}
	if _, _, err := gate.begin(ctx, "project_c"); !errors.Is(err, errProjectMutationFenceOrder) {
		t.Fatalf("nested expansion error = %v, want errProjectMutationFenceOrder", err)
	}

	type acquisition struct {
		release func()
		err     error
	}
	deleteDone := make(chan acquisition, 1)
	go func() {
		_, releaseDelete, err := gate.beginDestructive(context.Background(), "project_b")
		deleteDone <- acquisition{release: releaseDelete, err: err}
	}()
	waitForProjectMutationDestructive(t, &gate, "project_b")
	select {
	case result := <-deleteDone:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("delete acquired before multi-project lease released: %v", result.err)
	default:
	}
	release()
	select {
	case result := <-deleteDone:
		if result.err != nil {
			t.Fatalf("delete acquisition error: %v", result.err)
		}
		result.release()
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not acquire after multi-project lease released")
	}
}

func TestProjectMutationGate_MultiProjectAdmissionIsAtomicWhenOneKeyIsClosed(t *testing.T) {
	t.Parallel()
	var gate projectMutationGate
	_, releaseA, err := gate.beginDestructive(t.Context(), "project_a")
	if err != nil {
		t.Fatalf("close project_a: %v", err)
	}
	defer releaseA()
	if _, _, err := gate.beginMany(t.Context(), []string{"project_b", "project_a"}); !errors.Is(err, errProjectMutationClosed) {
		t.Fatalf("begin projects error = %v, want errProjectMutationClosed", err)
	}
	_, releaseB, err := gate.beginDestructive(t.Context(), "project_b")
	if err != nil {
		t.Fatalf("close project_b after rejected multi-project admission: %v", err)
	}
	releaseB()
}

func TestProjectMutationGate_DestructiveClosureWaitsForSameProjectMutations(t *testing.T) {
	t.Parallel()
	var gate projectMutationGate
	_, releaseA, err := gate.begin(t.Context(), "project_a")
	if err != nil {
		t.Fatalf("begin project_a owner: %v", err)
	}

	type acquisition struct {
		release func()
		err     error
	}
	destructiveDone := make(chan acquisition, 1)
	go func() {
		_, release, err := gate.beginDestructive(context.Background(), "project_a")
		destructiveDone <- acquisition{release: release, err: err}
	}()
	waitForProjectMutationDestructive(t, &gate, "project_a")
	if _, _, err := gate.begin(t.Context(), "project_a"); !errors.Is(err, errProjectMutationClosed) {
		t.Fatalf("begin project_a during destructive closure error = %v, want errProjectMutationClosed", err)
	}
	select {
	case result := <-destructiveDone:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("destructive closure acquired before active mutation released: %v", result.err)
	default:
	}

	_, releaseB, err := gate.begin(t.Context(), "project_b")
	if err != nil {
		t.Fatalf("begin project_b while project_a is held: %v", err)
	}
	releaseB()
	releaseA()

	select {
	case result := <-destructiveDone:
		if result.err != nil {
			t.Fatalf("destructive closure error: %v", result.err)
		}
		result.release()
	case <-time.After(5 * time.Second):
		t.Fatal("destructive closure did not acquire after active mutation released")
	}
}

func TestProjectMutationGate_CanceledDestructiveWaitReopensProject(t *testing.T) {
	t.Parallel()
	var gate projectMutationGate
	_, releaseMutation, err := gate.begin(t.Context(), "project_cancel_delete")
	if err != nil {
		t.Fatalf("begin mutation: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	destructiveDone := make(chan error, 1)
	go func() {
		_, _, err := gate.beginDestructive(ctx, "project_cancel_delete")
		destructiveDone <- err
	}()
	waitForProjectMutationDestructive(t, &gate, "project_cancel_delete")
	cancel()
	releaseMutation()

	select {
	case err := <-destructiveDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("destructive wait error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled destructive wait did not return")
	}
	_, releaseNext, err := gate.begin(t.Context(), "project_cancel_delete")
	if err != nil {
		t.Fatalf("project remained closed after canceled destructive wait: %v", err)
	}
	releaseNext()
}

func TestProjectAssignmentReconcile_DoesNotJoinActiveChatDeleteWaitCycle(t *testing.T) {
	t.Parallel()
	const (
		projectID = "project_active_chat_delete"
		sessionID = "chat_active_project_delete"
	)
	handler := &Handler{agentChatLive: newAgentChatLive(agentChatSnapshotConfig{})}
	snapshot := handler.agentChatLive.snapshotLifecycle(sessionID)
	defer snapshot.release()
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	if got := handler.agentChatLive.registerRun(snapshot, func() {
		cancelOnce.Do(func() { close(cancelled) })
	}); got != agentChatRunAccepted {
		t.Fatalf("registerRun = %v, want accepted", got)
	}

	releaseState, err := handler.stateMutationGate.beginDestructive(t.Context())
	if err != nil {
		t.Fatalf("begin destructive state mutation: %v", err)
	}
	defer releaseState()
	_, releaseProject, err := handler.projectMutationGate.beginDestructive(t.Context(), projectID)
	if err != nil {
		t.Fatalf("begin delete project mutation: %v", err)
	}
	defer releaseProject()

	waitDone := make(chan bool, 1)
	go func() {
		waitDone <- handler.agentChatLive.cancelRunAndWait(context.Background(), sessionID)
	}()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not cancel the active chat run")
	}

	reconcileDone := make(chan struct{})
	go func() {
		handler.reconcileProjectAssignmentsForChat(context.Background(), chat.Session{ID: sessionID, ProjectID: projectID})
		close(reconcileDone)
	}()
	select {
	case <-reconcileDone:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal reconciliation waited on the delete-owned project fence")
	}
	// The real terminal handler clears its run after this best-effort
	// reconciliation. Reaching clearRun proves delete can finish its wait.
	handler.agentChatLive.clearRun(sessionID)
	select {
	case settled := <-waitDone:
		if !settled {
			t.Fatal("delete did not observe the active chat run settle")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete remained stuck waiting for the active chat run")
	}
}

func TestProjectsAPI_ProjectScopedFacadeMutationsUseProjectFence(t *testing.T) {
	t.Parallel()
	const projectID = "project_fence_audit"
	handler := &Handler{}
	mux := http.NewServeMux()
	registerHecateProjectRoutes(mux, handler)
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "project", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID},
		{name: "root create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/roots"},
		{name: "root discovery", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/roots/discover"},
		{name: "worktree root", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/roots/worktrees"},
		{name: "root update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/roots/root"},
		{name: "root delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/roots/root"},
		{name: "context source create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/context-sources"},
		{name: "context source discovery", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/context-sources/discover"},
		{name: "context source update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/context-sources/source"},
		{name: "context source delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/context-sources/source"},
		{name: "skill discovery", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/skills/discover"},
		{name: "skill update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/skills/skill"},
		{name: "memory create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/memory"},
		{name: "memory candidate create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/memory/candidates"},
		{name: "memory candidate promote", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/memory/candidates/candidate/promote"},
		{name: "memory candidate reject", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/memory/candidates/candidate/reject"},
		{name: "memory update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/memory/memory"},
		{name: "memory delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/memory/memory"},
		{name: "role create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/roles"},
		{name: "role update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/roles/role"},
		{name: "role delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/roles/role"},
		{name: "work item create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items"},
		{name: "work item update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/work-items/work"},
		{name: "work item delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/work-items/work"},
		{name: "assignment create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items/work/assignments"},
		{name: "assignment update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/work-items/work/assignments/assignment"},
		{name: "assignment delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/work-items/work/assignments/assignment"},
		{name: "assignment start", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items/work/assignments/assignment/start"},
		{name: "artifact create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items/work/artifacts"},
		{name: "handoff create", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items/work/handoffs"},
		{name: "handoff update", method: http.MethodPatch, path: "/hecate/v1/projects/" + projectID + "/work-items/work/handoffs/handoff"},
		{name: "handoff status", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items/work/handoffs/handoff/status"},
		{name: "handoff accept", method: http.MethodPost, path: "/hecate/v1/projects/" + projectID + "/work-items/work/handoffs/handoff/accept-with-follow-up"},
		{name: "handoff delete", method: http.MethodDelete, path: "/hecate/v1/projects/" + projectID + "/work-items/work/handoffs/handoff"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, releaseOwner, err := handler.projectMutationGate.beginDestructive(t.Context(), projectID)
			if err != nil {
				t.Fatalf("begin destructive owner: %v", err)
			}
			defer releaseOwner()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, bytes.NewReader([]byte(`{}`)))
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s, want project deletion conflict", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreateTaskUsesProjectFence(t *testing.T) {
	t.Parallel()
	const projectID = "project_task_fence"
	handler := &Handler{}
	_, release, err := handler.projectMutationGate.beginDestructive(t.Context(), projectID)
	if err != nil {
		t.Fatalf("begin destructive owner: %v", err)
	}
	defer release()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/hecate/v1/tasks", bytes.NewReader([]byte(`{
		"title":"Fenced task",
		"prompt":"Do not create",
		"project_id":"project_task_fence"
	}`)))
	handler.HandleCreateTask(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want project deletion conflict", recorder.Code, recorder.Body.String())
	}
}

func TestProjectMutationFenceReadReconciliationErrorsUseConflict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		write func(http.ResponseWriter)
	}{
		{
			name: "project render",
			write: func(w http.ResponseWriter) {
				writeProjectReadRenderError(w, errProjectMutationClosed)
			},
		},
		{
			name: "project work render",
			write: func(w http.ResponseWriter) {
				writeProjectWorkError(w, errProjectMutationClosed)
			},
		},
		{
			name: "project delete",
			write: func(w http.ResponseWriter) {
				(&Handler{}).writeProjectDeleteResponse(w, projectapp.DeleteProjectResult{}, errProjectMutationFenceOrder)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.write(recorder)
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s, want conflict", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func waitForProjectMutationDestructive(t *testing.T, gate *projectMutationGate, projectID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gate.mu.Lock()
		state := gate.states[projectID]
		destructive := state != nil && state.destructive
		gate.mu.Unlock()
		if destructive {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("project fence did not enter destructive closure")
}
