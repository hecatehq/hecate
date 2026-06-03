package projectwork

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	AssignmentDriverHecateTask    = "hecate_task"
	AssignmentDriverExternalAgent = "external_agent"

	WorkItemStatusBacklog   = "backlog"
	WorkItemStatusReady     = "ready"
	WorkItemStatusRunning   = "running"
	WorkItemStatusReview    = "review"
	WorkItemStatusBlocked   = "blocked"
	WorkItemStatusDone      = "done"
	WorkItemStatusCancelled = "cancelled"

	AssignmentStatusQueued           = "queued"
	AssignmentStatusRunning          = "running"
	AssignmentStatusAwaitingApproval = "awaiting_approval"
	AssignmentStatusCompleted        = "completed"
	AssignmentStatusFailed           = "failed"
	AssignmentStatusCancelled        = "cancelled"

	ArtifactKindBrief        = "brief"
	ArtifactKindHandoff      = "handoff"
	ArtifactKindReview       = "review"
	ArtifactKindDecisionNote = "decision_note"
)

var (
	ErrNotFound      = errors.New("project work record not found")
	ErrInvalid       = errors.New("invalid project work record")
	ErrBuiltInRole   = errors.New("built-in role cannot be mutated")
	ErrDuplicateRole = errors.New("project role already exists")
	ErrDuplicate     = errors.New("project work record already exists")
)

type AgentRoleProfile struct {
	ID           string
	ProjectID    string
	Name         string
	Description  string
	Instructions string
	BuiltIn      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type WorkItem struct {
	ID              string
	ProjectID       string
	Title           string
	Brief           string
	Status          string
	Priority        string
	OwnerRoleID     string
	ReviewerRoleIDs []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Assignment struct {
	ID                string
	ProjectID         string
	WorkItemID        string
	RoleID            string
	DriverKind        string
	Status            string
	TaskID            string
	RunID             string
	ChatSessionID     string
	MessageID         string
	ContextSnapshotID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         time.Time
	CompletedAt       time.Time
}

type CollaborationArtifact struct {
	ID           string
	ProjectID    string
	WorkItemID   string
	AssignmentID string
	Kind         string
	Title        string
	Body         string
	AuthorRoleID string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AssignmentFilter struct {
	ProjectID  string
	WorkItemID string
}

type ArtifactFilter struct {
	ProjectID    string
	WorkItemID   string
	AssignmentID string
}

type Store interface {
	Backend() string
	ListRoles(ctx context.Context, projectID string) ([]AgentRoleProfile, error)
	CreateRole(ctx context.Context, role AgentRoleProfile) (AgentRoleProfile, error)
	UpdateRole(ctx context.Context, projectID, id string, update func(*AgentRoleProfile)) (AgentRoleProfile, error)
	DeleteRole(ctx context.Context, projectID, id string) error
	ListWorkItems(ctx context.Context, projectID string) ([]WorkItem, error)
	CreateWorkItem(ctx context.Context, item WorkItem) (WorkItem, error)
	GetWorkItem(ctx context.Context, projectID, id string) (WorkItem, bool, error)
	UpdateWorkItem(ctx context.Context, projectID, id string, update func(*WorkItem)) (WorkItem, error)
	DeleteWorkItem(ctx context.Context, projectID, id string) error
	ListAssignments(ctx context.Context, filter AssignmentFilter) ([]Assignment, error)
	CreateAssignment(ctx context.Context, assignment Assignment) (Assignment, error)
	UpdateAssignment(ctx context.Context, projectID, id string, update func(*Assignment)) (Assignment, error)
	ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]CollaborationArtifact, error)
	CreateArtifact(ctx context.Context, artifact CollaborationArtifact) (CollaborationArtifact, error)
	DeleteProject(ctx context.Context, projectID string) (int, error)
	Clear(ctx context.Context) (int, error)
}

type MemoryStore struct {
	mu          sync.Mutex
	roles       map[string]AgentRoleProfile
	workItems   map[string]WorkItem
	assignments map[string]Assignment
	artifacts   map[string]CollaborationArtifact
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		roles:       make(map[string]AgentRoleProfile),
		workItems:   make(map[string]WorkItem),
		assignments: make(map[string]Assignment),
		artifacts:   make(map[string]CollaborationArtifact),
	}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func BuiltInRoleProfiles(projectID string) []AgentRoleProfile {
	projectID = strings.TrimSpace(projectID)
	roles := []AgentRoleProfile{
		{ID: "product_manager", Name: "Product Manager", Description: "Shapes product intent, scope, and acceptance criteria."},
		{ID: "architect", Name: "Architect", Description: "Owns technical direction, boundaries, and system trade-offs."},
		{ID: "software_developer", Name: "Software Developer", Description: "Implements backend and shared application behavior."},
		{ID: "frontend_engineer", Name: "Frontend Engineer", Description: "Implements user-facing application surfaces."},
		{ID: "designer", Name: "Designer", Description: "Owns interaction, information architecture, and visual quality."},
		{ID: "sre", Name: "SRE", Description: "Owns deployability, reliability, observability, and operations."},
		{ID: "tech_writer", Name: "Technical Writer", Description: "Turns implementation and decisions into clear operator-facing docs."},
		{ID: "reviewer_qa", Name: "Reviewer QA", Description: "Reviews behavior, risks, regressions, and verification gaps."},
	}
	for idx := range roles {
		roles[idx].ProjectID = projectID
		roles[idx].BuiltIn = true
	}
	return roles
}

func IsBuiltInRoleID(id string) bool {
	id = strings.TrimSpace(id)
	for _, role := range BuiltInRoleProfiles("") {
		if role.ID == id {
			return true
		}
	}
	return false
}

func (s *MemoryStore) ListRoles(_ context.Context, projectID string) ([]AgentRoleProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	items := BuiltInRoleProfiles(projectID)
	for _, role := range s.roles {
		if role.ProjectID == projectID {
			items = append(items, cloneRole(role))
		}
	}
	sortRoles(items)
	return items, nil
}

func (s *MemoryStore) CreateRole(_ context.Context, role AgentRoleProfile) (AgentRoleProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	role = normalizeRole(role, time.Now().UTC())
	if IsBuiltInRoleID(role.ID) || role.BuiltIn {
		return AgentRoleProfile{}, ErrBuiltInRole
	}
	if err := validateRole(role); err != nil {
		return AgentRoleProfile{}, err
	}
	key := roleKey(role.ProjectID, role.ID)
	if _, exists := s.roles[key]; exists {
		return AgentRoleProfile{}, ErrDuplicateRole
	}
	s.roles[key] = cloneRole(role)
	return cloneRole(role), nil
}

func (s *MemoryStore) UpdateRole(_ context.Context, projectID, id string, update func(*AgentRoleProfile)) (AgentRoleProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	if IsBuiltInRoleID(id) {
		return AgentRoleProfile{}, ErrBuiltInRole
	}
	key := roleKey(projectID, id)
	role, ok := s.roles[key]
	if !ok {
		return AgentRoleProfile{}, ErrNotFound
	}
	role = cloneRole(role)
	originalID := role.ID
	originalProjectID := role.ProjectID
	originalCreatedAt := role.CreatedAt
	if update != nil {
		update(&role)
	}
	if strings.TrimSpace(role.ID) != originalID || strings.TrimSpace(role.ProjectID) != originalProjectID {
		return AgentRoleProfile{}, fmt.Errorf("%w: role id and project_id cannot be changed", ErrInvalid)
	}
	role.ID = originalID
	role.ProjectID = originalProjectID
	role.CreatedAt = originalCreatedAt
	role.UpdatedAt = time.Now().UTC()
	role = normalizeRole(role, role.UpdatedAt)
	if role.BuiltIn || IsBuiltInRoleID(role.ID) {
		return AgentRoleProfile{}, ErrBuiltInRole
	}
	if err := validateRole(role); err != nil {
		return AgentRoleProfile{}, err
	}
	s.roles[key] = cloneRole(role)
	return cloneRole(role), nil
}

func (s *MemoryStore) DeleteRole(_ context.Context, projectID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	if IsBuiltInRoleID(id) {
		return ErrBuiltInRole
	}
	key := roleKey(projectID, id)
	if _, ok := s.roles[key]; !ok {
		return ErrNotFound
	}
	delete(s.roles, key)
	return nil
}

func (s *MemoryStore) ListWorkItems(_ context.Context, projectID string) ([]WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	items := make([]WorkItem, 0, len(s.workItems))
	for _, item := range s.workItems {
		if item.ProjectID == projectID {
			items = append(items, cloneWorkItem(item))
		}
	}
	sortWorkItems(items)
	return items, nil
}

func (s *MemoryStore) CreateWorkItem(_ context.Context, item WorkItem) (WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item = normalizeWorkItem(item, time.Now().UTC())
	if err := validateWorkItem(item); err != nil {
		return WorkItem{}, err
	}
	key := workItemKey(item.ProjectID, item.ID)
	if _, exists := s.workItems[key]; exists {
		return WorkItem{}, ErrDuplicate
	}
	s.workItems[key] = cloneWorkItem(item)
	return cloneWorkItem(item), nil
}

func (s *MemoryStore) GetWorkItem(_ context.Context, projectID, id string) (WorkItem, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.workItems[workItemKey(projectID, id)]
	if !ok {
		return WorkItem{}, false, nil
	}
	return cloneWorkItem(item), true, nil
}

func (s *MemoryStore) UpdateWorkItem(_ context.Context, projectID, id string, update func(*WorkItem)) (WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workItemKey(projectID, id)
	item, ok := s.workItems[key]
	if !ok {
		return WorkItem{}, ErrNotFound
	}
	item = cloneWorkItem(item)
	originalID := item.ID
	originalProjectID := item.ProjectID
	originalCreatedAt := item.CreatedAt
	if update != nil {
		update(&item)
	}
	if strings.TrimSpace(item.ID) != originalID || strings.TrimSpace(item.ProjectID) != originalProjectID {
		return WorkItem{}, fmt.Errorf("%w: work item id and project_id cannot be changed", ErrInvalid)
	}
	item.ID = originalID
	item.ProjectID = originalProjectID
	item.CreatedAt = originalCreatedAt
	item.UpdatedAt = time.Now().UTC()
	item = normalizeWorkItem(item, item.UpdatedAt)
	if err := validateWorkItem(item); err != nil {
		return WorkItem{}, err
	}
	s.workItems[key] = cloneWorkItem(item)
	return cloneWorkItem(item), nil
}

func (s *MemoryStore) DeleteWorkItem(_ context.Context, projectID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	key := workItemKey(projectID, id)
	if _, ok := s.workItems[key]; !ok {
		return ErrNotFound
	}
	delete(s.workItems, key)
	for key, assignment := range s.assignments {
		if assignment.ProjectID == projectID && assignment.WorkItemID == id {
			delete(s.assignments, key)
		}
	}
	for key, artifact := range s.artifacts {
		if artifact.ProjectID == projectID && artifact.WorkItemID == id {
			delete(s.artifacts, key)
		}
	}
	return nil
}

func (s *MemoryStore) ListAssignments(_ context.Context, filter AssignmentFilter) ([]Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.WorkItemID = strings.TrimSpace(filter.WorkItemID)
	items := make([]Assignment, 0, len(s.assignments))
	for _, item := range s.assignments {
		if item.ProjectID != filter.ProjectID {
			continue
		}
		if filter.WorkItemID != "" && item.WorkItemID != filter.WorkItemID {
			continue
		}
		items = append(items, cloneAssignment(item))
	}
	sortAssignments(items)
	return items, nil
}

func (s *MemoryStore) CreateAssignment(_ context.Context, assignment Assignment) (Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment = normalizeAssignment(assignment, time.Now().UTC())
	if err := validateAssignment(assignment); err != nil {
		return Assignment{}, err
	}
	if _, ok := s.workItems[workItemKey(assignment.ProjectID, assignment.WorkItemID)]; !ok {
		return Assignment{}, fmt.Errorf("%w: work item not found", ErrNotFound)
	}
	key := assignmentKey(assignment.ProjectID, assignment.ID)
	if _, exists := s.assignments[key]; exists {
		return Assignment{}, ErrDuplicate
	}
	s.assignments[key] = cloneAssignment(assignment)
	return cloneAssignment(assignment), nil
}

func (s *MemoryStore) UpdateAssignment(_ context.Context, projectID, id string, update func(*Assignment)) (Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := assignmentKey(projectID, id)
	item, ok := s.assignments[key]
	if !ok {
		return Assignment{}, ErrNotFound
	}
	item = cloneAssignment(item)
	originalID := item.ID
	originalProjectID := item.ProjectID
	originalWorkItemID := item.WorkItemID
	originalCreatedAt := item.CreatedAt
	if update != nil {
		update(&item)
	}
	if strings.TrimSpace(item.ID) != originalID ||
		strings.TrimSpace(item.ProjectID) != originalProjectID ||
		strings.TrimSpace(item.WorkItemID) != originalWorkItemID {
		return Assignment{}, fmt.Errorf("%w: assignment id, project_id, and work_item_id cannot be changed", ErrInvalid)
	}
	item.ID = originalID
	item.ProjectID = originalProjectID
	item.WorkItemID = originalWorkItemID
	item.CreatedAt = originalCreatedAt
	item.UpdatedAt = time.Now().UTC()
	item = normalizeAssignment(item, item.UpdatedAt)
	if err := validateAssignment(item); err != nil {
		return Assignment{}, err
	}
	s.assignments[key] = cloneAssignment(item)
	return cloneAssignment(item), nil
}

func (s *MemoryStore) ListArtifacts(_ context.Context, filter ArtifactFilter) ([]CollaborationArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.WorkItemID = strings.TrimSpace(filter.WorkItemID)
	filter.AssignmentID = strings.TrimSpace(filter.AssignmentID)
	items := make([]CollaborationArtifact, 0, len(s.artifacts))
	for _, item := range s.artifacts {
		if item.ProjectID != filter.ProjectID {
			continue
		}
		if filter.WorkItemID != "" && item.WorkItemID != filter.WorkItemID {
			continue
		}
		if filter.AssignmentID != "" && item.AssignmentID != filter.AssignmentID {
			continue
		}
		items = append(items, cloneArtifact(item))
	}
	sortArtifacts(items)
	return items, nil
}

func (s *MemoryStore) CreateArtifact(_ context.Context, artifact CollaborationArtifact) (CollaborationArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact = normalizeArtifact(artifact, time.Now().UTC())
	if err := validateArtifact(artifact); err != nil {
		return CollaborationArtifact{}, err
	}
	if _, ok := s.workItems[workItemKey(artifact.ProjectID, artifact.WorkItemID)]; !ok {
		return CollaborationArtifact{}, fmt.Errorf("%w: work item not found", ErrNotFound)
	}
	if artifact.AssignmentID != "" {
		assignment, ok := s.assignments[assignmentKey(artifact.ProjectID, artifact.AssignmentID)]
		if !ok || assignment.WorkItemID != artifact.WorkItemID {
			return CollaborationArtifact{}, fmt.Errorf("%w: assignment not found", ErrNotFound)
		}
	}
	key := artifactKey(artifact.ProjectID, artifact.ID)
	if _, exists := s.artifacts[key]; exists {
		return CollaborationArtifact{}, ErrDuplicate
	}
	s.artifacts[key] = cloneArtifact(artifact)
	return cloneArtifact(artifact), nil
}

func (s *MemoryStore) DeleteProject(_ context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	deleted := 0
	for key, item := range s.roles {
		if item.ProjectID == projectID {
			delete(s.roles, key)
			deleted++
		}
	}
	for key, item := range s.workItems {
		if item.ProjectID == projectID {
			delete(s.workItems, key)
			deleted++
		}
	}
	for key, item := range s.assignments {
		if item.ProjectID == projectID {
			delete(s.assignments, key)
			deleted++
		}
	}
	for key, item := range s.artifacts {
		if item.ProjectID == projectID {
			delete(s.artifacts, key)
			deleted++
		}
	}
	return deleted, nil
}

func (s *MemoryStore) Clear(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := len(s.roles) + len(s.workItems) + len(s.assignments) + len(s.artifacts)
	s.roles = make(map[string]AgentRoleProfile)
	s.workItems = make(map[string]WorkItem)
	s.assignments = make(map[string]Assignment)
	s.artifacts = make(map[string]CollaborationArtifact)
	return deleted, nil
}

func normalizeRole(role AgentRoleProfile, now time.Time) AgentRoleProfile {
	role.ID = strings.TrimSpace(role.ID)
	role.ProjectID = strings.TrimSpace(role.ProjectID)
	role.Name = strings.TrimSpace(role.Name)
	role.Description = strings.TrimSpace(role.Description)
	role.Instructions = strings.TrimSpace(role.Instructions)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if role.CreatedAt.IsZero() {
		role.CreatedAt = now
	}
	if role.UpdatedAt.IsZero() {
		role.UpdatedAt = role.CreatedAt
	}
	return role
}

func normalizeWorkItem(item WorkItem, now time.Time) WorkItem {
	item.ID = strings.TrimSpace(item.ID)
	item.ProjectID = strings.TrimSpace(item.ProjectID)
	item.Title = strings.TrimSpace(item.Title)
	item.Brief = strings.TrimSpace(item.Brief)
	item.Status = strings.TrimSpace(item.Status)
	item.Priority = strings.TrimSpace(item.Priority)
	item.OwnerRoleID = strings.TrimSpace(item.OwnerRoleID)
	item.ReviewerRoleIDs = normalizeStringList(item.ReviewerRoleIDs)
	if item.Status == "" {
		item.Status = WorkItemStatusBacklog
	}
	if item.Priority == "" {
		item.Priority = "normal"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item
}

func normalizeAssignment(item Assignment, now time.Time) Assignment {
	item.ID = strings.TrimSpace(item.ID)
	item.ProjectID = strings.TrimSpace(item.ProjectID)
	item.WorkItemID = strings.TrimSpace(item.WorkItemID)
	item.RoleID = strings.TrimSpace(item.RoleID)
	item.DriverKind = strings.TrimSpace(item.DriverKind)
	item.Status = strings.TrimSpace(item.Status)
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.RunID = strings.TrimSpace(item.RunID)
	item.ChatSessionID = strings.TrimSpace(item.ChatSessionID)
	item.MessageID = strings.TrimSpace(item.MessageID)
	item.ContextSnapshotID = strings.TrimSpace(item.ContextSnapshotID)
	if item.Status == "" {
		item.Status = AssignmentStatusQueued
	}
	if item.DriverKind == "" {
		item.DriverKind = AssignmentDriverHecateTask
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item
}

func normalizeArtifact(item CollaborationArtifact, now time.Time) CollaborationArtifact {
	item.ID = strings.TrimSpace(item.ID)
	item.ProjectID = strings.TrimSpace(item.ProjectID)
	item.WorkItemID = strings.TrimSpace(item.WorkItemID)
	item.AssignmentID = strings.TrimSpace(item.AssignmentID)
	item.Kind = strings.TrimSpace(item.Kind)
	item.Title = strings.TrimSpace(item.Title)
	item.Body = strings.TrimSpace(item.Body)
	item.AuthorRoleID = strings.TrimSpace(item.AuthorRoleID)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item
}

func validateRole(role AgentRoleProfile) error {
	if role.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if role.ID == "" {
		return fmt.Errorf("%w: role id is required", ErrInvalid)
	}
	if role.Name == "" {
		return fmt.Errorf("%w: role name is required", ErrInvalid)
	}
	return nil
}

func validateWorkItem(item WorkItem) error {
	if item.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if item.ID == "" {
		return fmt.Errorf("%w: work item id is required", ErrInvalid)
	}
	if item.Title == "" {
		return fmt.Errorf("%w: work item title is required", ErrInvalid)
	}
	if !validWorkItemStatus(item.Status) {
		return fmt.Errorf("%w: unsupported work item status %q", ErrInvalid, item.Status)
	}
	if !validPriority(item.Priority) {
		return fmt.Errorf("%w: unsupported work item priority %q", ErrInvalid, item.Priority)
	}
	return nil
}

func validateAssignment(item Assignment) error {
	if item.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if item.ID == "" {
		return fmt.Errorf("%w: assignment id is required", ErrInvalid)
	}
	if item.WorkItemID == "" {
		return fmt.Errorf("%w: work_item_id is required", ErrInvalid)
	}
	if item.RoleID == "" {
		return fmt.Errorf("%w: role_id is required", ErrInvalid)
	}
	if !validAssignmentDriverKind(item.DriverKind) {
		return fmt.Errorf("%w: unsupported assignment driver_kind %q", ErrInvalid, item.DriverKind)
	}
	if !validAssignmentStatus(item.Status) {
		return fmt.Errorf("%w: unsupported assignment status %q", ErrInvalid, item.Status)
	}
	return nil
}

func validAssignmentDriverKind(kind string) bool {
	switch kind {
	case AssignmentDriverHecateTask, AssignmentDriverExternalAgent:
		return true
	default:
		return false
	}
}

func validateArtifact(item CollaborationArtifact) error {
	if item.ProjectID == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if item.ID == "" {
		return fmt.Errorf("%w: artifact id is required", ErrInvalid)
	}
	if item.WorkItemID == "" {
		return fmt.Errorf("%w: work_item_id is required", ErrInvalid)
	}
	if !validArtifactKind(item.Kind) {
		return fmt.Errorf("%w: unsupported collaboration artifact kind %q", ErrInvalid, item.Kind)
	}
	if item.Body == "" {
		return fmt.Errorf("%w: artifact body is required", ErrInvalid)
	}
	return nil
}

func validWorkItemStatus(status string) bool {
	switch status {
	case WorkItemStatusBacklog, WorkItemStatusReady, WorkItemStatusRunning, WorkItemStatusReview, WorkItemStatusBlocked, WorkItemStatusDone, WorkItemStatusCancelled:
		return true
	default:
		return false
	}
}

func validAssignmentStatus(status string) bool {
	switch status {
	case AssignmentStatusQueued, AssignmentStatusRunning, AssignmentStatusAwaitingApproval, AssignmentStatusCompleted, AssignmentStatusFailed, AssignmentStatusCancelled:
		return true
	default:
		return false
	}
}

func validArtifactKind(kind string) bool {
	switch kind {
	case ArtifactKindBrief, ArtifactKindHandoff, ArtifactKindReview, ArtifactKindDecisionNote:
		return true
	default:
		return false
	}
}

func validPriority(priority string) bool {
	switch priority {
	case "low", "normal", "high", "urgent":
		return true
	default:
		return false
	}
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func roleKey(projectID, id string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(id)
}

func workItemKey(projectID, id string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(id)
}

func assignmentKey(projectID, id string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(id)
}

func artifactKey(projectID, id string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(id)
}

func cloneRole(item AgentRoleProfile) AgentRoleProfile {
	return item
}

func cloneWorkItem(item WorkItem) WorkItem {
	item.ReviewerRoleIDs = append([]string(nil), item.ReviewerRoleIDs...)
	return item
}

func cloneAssignment(item Assignment) Assignment {
	return item
}

func cloneArtifact(item CollaborationArtifact) CollaborationArtifact {
	return item
}

func sortRoles(items []AgentRoleProfile) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].BuiltIn != items[j].BuiltIn {
			return items[i].BuiltIn
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
}

func sortWorkItems(items []WorkItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func sortAssignments(items []Assignment) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func sortArtifacts(items []CollaborationArtifact) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
}
