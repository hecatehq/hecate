package projectapp

import (
	"context"
	"errors"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestApplication_DeleteProjectCleansProjectScopedStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectStore := projects.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	skillStore := projectskills.NewMemoryStore()
	memoryStore := memory.NewMemoryStore()
	projectID := "proj_delete"
	otherProjectID := "proj_other"

	if _, err := projectStore.Create(ctx, projects.Project{ID: projectID, Name: "Delete me"}); err != nil {
		t.Fatalf("Create(project): %v", err)
	}
	if _, err := projectStore.Create(ctx, projects.Project{ID: otherProjectID, Name: "Keep me"}); err != nil {
		t.Fatalf("Create(other project): %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_delete", ProjectID: projectID, AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create(project chat): %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_keep", ProjectID: otherProjectID, AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create(other chat): %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_no_project", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create(no-project chat): %v", err)
	}
	if _, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{ID: "work_delete", ProjectID: projectID, Title: "Cleanup"}); err != nil {
		t.Fatalf("CreateWorkItem(project): %v", err)
	}
	if _, err := workStore.CreateAssignment(ctx, projectwork.Assignment{ID: "asgn_delete", ProjectID: projectID, WorkItemID: "work_delete", RoleID: "software_developer"}); err != nil {
		t.Fatalf("CreateAssignment(project): %v", err)
	}
	if _, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{ID: "work_keep", ProjectID: otherProjectID, Title: "Keep"}); err != nil {
		t.Fatalf("CreateWorkItem(other): %v", err)
	}
	if _, err := skillStore.UpsertDiscovered(ctx, projectID, []projectskills.Skill{{ID: "skill_delete", Title: "Delete", Path: "SKILL.md"}}); err != nil {
		t.Fatalf("UpsertDiscovered(project): %v", err)
	}
	if _, err := skillStore.UpsertDiscovered(ctx, otherProjectID, []projectskills.Skill{{ID: "skill_keep", Title: "Keep", Path: "SKILL.md"}}); err != nil {
		t.Fatalf("UpsertDiscovered(other): %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{ID: "mem_delete", Scope: memory.ScopeProject, ProjectID: projectID, Title: "Delete", Body: "Delete"}); err != nil {
		t.Fatalf("Create(memory): %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{ID: "mem_keep", Scope: memory.ScopeProject, ProjectID: otherProjectID, Title: "Keep", Body: "Keep"}); err != nil {
		t.Fatalf("Create(other memory): %v", err)
	}
	if _, err := memoryStore.CreateCandidate(ctx, memory.Candidate{ID: "cand_delete", ProjectID: projectID, Title: "Delete", Body: "Delete"}); err != nil {
		t.Fatalf("CreateCandidate(project): %v", err)
	}
	if _, err := memoryStore.CreateCandidate(ctx, memory.Candidate{ID: "cand_keep", ProjectID: otherProjectID, Title: "Keep", Body: "Keep"}); err != nil {
		t.Fatalf("CreateCandidate(other): %v", err)
	}

	deletedChats := make([]string, 0, 1)
	app := New(Options{
		Projects:         projectStore,
		Chats:            chatStore,
		DeleteChat:       deleteChatFromStore(chatStore, &deletedChats, false),
		ProjectWork:      workStore,
		ProjectSkills:    skillStore,
		Memory:           memoryStore,
		MemoryCandidates: memoryStore,
	})
	result, err := app.DeleteProject(ctx, projectID)
	if err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if result.Project.ID != projectID || result.ChatSessionsDeleted != 1 || result.ProjectWorkRowsDeleted != 2 ||
		result.ProjectSkillsDeleted != 1 || result.MemoryEntriesDeleted != 1 || result.MemoryCandidatesDeleted != 1 {
		t.Fatalf("DeleteProject() result = %+v, want project and scoped delete counts", result)
	}
	if len(deletedChats) != 1 || deletedChats[0] != "chat_delete" {
		t.Fatalf("deleted chats = %#v, want only project chat", deletedChats)
	}
	if _, ok, err := projectStore.Get(ctx, projectID); err != nil || ok {
		t.Fatalf("Get(deleted project) ok=%v err=%v, want missing", ok, err)
	}
	if _, ok, err := projectStore.Get(ctx, otherProjectID); err != nil || !ok {
		t.Fatalf("Get(other project) ok=%v err=%v, want present", ok, err)
	}
	assertProjectAppListCount(t, "project work", func() (int, error) {
		items, err := workStore.ListWorkItems(ctx, projectID)
		return len(items), err
	}, 0)
	assertProjectAppListCount(t, "other project work", func() (int, error) {
		items, err := workStore.ListWorkItems(ctx, otherProjectID)
		return len(items), err
	}, 1)
	assertProjectAppListCount(t, "project skills", func() (int, error) {
		items, err := skillStore.List(ctx, projectID)
		return len(items), err
	}, 0)
	assertProjectAppListCount(t, "other project skills", func() (int, error) {
		items, err := skillStore.List(ctx, otherProjectID)
		return len(items), err
	}, 1)
	assertProjectAppListCount(t, "project memory", func() (int, error) {
		items, err := memoryStore.List(ctx, memory.Filter{ProjectID: projectID, IncludeDisabled: true})
		return len(items), err
	}, 0)
	assertProjectAppListCount(t, "other project memory", func() (int, error) {
		items, err := memoryStore.List(ctx, memory.Filter{ProjectID: otherProjectID, IncludeDisabled: true})
		return len(items), err
	}, 1)
	assertProjectAppListCount(t, "project memory candidates", func() (int, error) {
		items, err := memoryStore.ListCandidates(ctx, memory.CandidateFilter{ProjectID: projectID})
		return len(items), err
	}, 0)
	assertProjectAppListCount(t, "other project memory candidates", func() (int, error) {
		items, err := memoryStore.ListCandidates(ctx, memory.CandidateFilter{ProjectID: otherProjectID})
		return len(items), err
	}, 1)
	assertProjectAppListCount(t, "remaining chats", func() (int, error) {
		items, err := chatStore.List(ctx)
		return len(items), err
	}, 2)
}

func TestApplication_DeleteProjectStopsBeforeCleanupWhenProjectChatIsStopping(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectStore := projects.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	memoryStore := memory.NewMemoryStore()
	projectID := "proj_stopping"
	if _, err := projectStore.Create(ctx, projects.Project{ID: projectID, Name: "Stopping"}); err != nil {
		t.Fatalf("Create(project): %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_stopping", ProjectID: projectID, AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create(chat): %v", err)
	}
	if _, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{ID: "work_stopping", ProjectID: projectID, Title: "Cleanup"}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{ID: "mem_stopping", Scope: memory.ScopeProject, ProjectID: projectID, Title: "Keep", Body: "Keep"}); err != nil {
		t.Fatalf("Create(memory): %v", err)
	}

	deletedChats := make([]string, 0, 1)
	app := New(Options{
		Projects:         projectStore,
		Chats:            chatStore,
		DeleteChat:       deleteChatFromStore(chatStore, &deletedChats, true),
		ProjectWork:      workStore,
		Memory:           memoryStore,
		MemoryCandidates: memoryStore,
	})
	result, err := app.DeleteProject(ctx, projectID)
	if !errors.Is(err, ErrProjectDeleteConflict) {
		t.Fatalf("DeleteProject() error = %v, want ErrProjectDeleteConflict", err)
	}
	if result.Project.ID != projectID || result.ChatSessionsDeleted != 0 || result.ProjectWorkRowsDeleted != 0 ||
		result.MemoryEntriesDeleted != 0 {
		t.Fatalf("DeleteProject() result = %+v, want no cleanup counts after stopping chat", result)
	}
	if len(deletedChats) != 0 {
		t.Fatalf("deleted chats = %#v, want none when chat is still stopping", deletedChats)
	}
	if _, ok, err := projectStore.Get(ctx, projectID); err != nil || !ok {
		t.Fatalf("Get(project) ok=%v err=%v, want still present", ok, err)
	}
	assertProjectAppListCount(t, "project work", func() (int, error) {
		items, err := workStore.ListWorkItems(ctx, projectID)
		return len(items), err
	}, 1)
	assertProjectAppListCount(t, "project memory", func() (int, error) {
		items, err := memoryStore.List(ctx, memory.Filter{ProjectID: projectID, IncludeDisabled: true})
		return len(items), err
	}, 1)
	assertProjectAppListCount(t, "project chats", func() (int, error) {
		items, err := chatStore.List(ctx)
		return len(items), err
	}, 1)
}

func TestApplication_DeleteProjectKeepsProjectWhenLaterCleanupFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectStore := projects.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	memoryStore := memory.NewMemoryStore()
	projectID := "proj_retry"
	if _, err := projectStore.Create(ctx, projects.Project{ID: projectID, Name: "Retry"}); err != nil {
		t.Fatalf("Create(project): %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_retry", ProjectID: projectID, AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create(chat): %v", err)
	}
	if _, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{ID: "work_retry", ProjectID: projectID, Title: "Cleanup"}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{ID: "mem_retry", Scope: memory.ScopeProject, ProjectID: projectID, Title: "Retry", Body: "Retry"}); err != nil {
		t.Fatalf("Create(memory): %v", err)
	}

	skillErr := errors.New("skill cleanup failed")
	deletedChats := make([]string, 0, 1)
	app := New(Options{
		Projects:      projectStore,
		Chats:         chatStore,
		DeleteChat:    deleteChatFromStore(chatStore, &deletedChats, false),
		ProjectWork:   workStore,
		ProjectSkills: failingProjectSkillStore{err: skillErr},
		Memory:        memoryStore,
	})
	result, err := app.DeleteProject(ctx, projectID)
	if !errors.Is(err, skillErr) {
		t.Fatalf("DeleteProject() error = %v, want skill cleanup failure", err)
	}
	if result.ChatSessionsDeleted != 1 || result.ProjectWorkRowsDeleted != 1 || result.MemoryEntriesDeleted != 0 {
		t.Fatalf("DeleteProject() result = %+v, want committed counts before skill failure", result)
	}
	if _, ok, err := projectStore.Get(ctx, projectID); err != nil || !ok {
		t.Fatalf("Get(project) ok=%v err=%v, want project retained for retry", ok, err)
	}
	assertProjectAppListCount(t, "project chats", func() (int, error) {
		items, err := chatStore.List(ctx)
		return len(items), err
	}, 0)
	assertProjectAppListCount(t, "project work", func() (int, error) {
		items, err := workStore.ListWorkItems(ctx, projectID)
		return len(items), err
	}, 0)
	assertProjectAppListCount(t, "project memory", func() (int, error) {
		items, err := memoryStore.List(ctx, memory.Filter{ProjectID: projectID, IncludeDisabled: true})
		return len(items), err
	}, 1)
}

func TestApplication_DeleteProjectMapsMissingProject(t *testing.T) {
	t.Parallel()

	app := New(Options{Projects: projects.NewMemoryStore()})
	_, err := app.DeleteProject(context.Background(), "proj_missing")
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("DeleteProject() error = %v, want ErrProjectNotFound", err)
	}
}

type failingProjectSkillStore struct {
	err error
}

func (s failingProjectSkillStore) DeleteProject(context.Context, string) (int, error) {
	return 0, s.err
}

func deleteChatFromStore(store *chat.MemoryStore, deleted *[]string, stopping bool) ChatDeleteFunc {
	return func(ctx context.Context, session chat.Session) (bool, error) {
		if stopping {
			return true, nil
		}
		*deleted = append(*deleted, session.ID)
		return false, store.Delete(ctx, session.ID)
	}
}

func assertProjectAppListCount(t *testing.T, label string, list func() (int, error), want int) {
	t.Helper()
	got, err := list()
	if err != nil {
		t.Fatalf("%s list error = %v", label, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", label, got, want)
	}
}
