package projectwork

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

type SQLiteStore struct {
	mu             sync.Mutex
	db             *sql.DB
	rolesTbl       string
	workItemsTbl   string
	reviewersTbl   string
	assignmentsTbl string
	artifactsTbl   string
	handoffsTbl    string
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteStore{
		db:             client.DB(),
		rolesTbl:       client.QualifiedTable("project_work_roles"),
		workItemsTbl:   client.QualifiedTable("project_work_items"),
		reviewersTbl:   client.QualifiedTable("project_work_item_reviewers"),
		assignmentsTbl: client.QualifiedTable("project_work_assignments"),
		artifactsTbl:   client.QualifiedTable("project_work_artifacts"),
		handoffsTbl:    client.QualifiedTable("project_handoffs"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return "sqlite"
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	instructions TEXT NOT NULL DEFAULT '',
	default_driver_kind TEXT NOT NULL DEFAULT '',
	default_provider TEXT NOT NULL DEFAULT '',
	default_model TEXT NOT NULL DEFAULT '',
	default_agent_profile TEXT NOT NULL DEFAULT '',
	skill_ids TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(project_id, id)
)`, s.rolesTbl)); err != nil {
		return fmt.Errorf("create project work roles table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	title TEXT NOT NULL,
	brief TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	priority TEXT NOT NULL,
	owner_role_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(project_id, id)
)`, s.workItemsTbl)); err != nil {
		return fmt.Errorf("create project work items table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	project_id TEXT NOT NULL,
	work_item_id TEXT NOT NULL,
	role_id TEXT NOT NULL,
	PRIMARY KEY(project_id, work_item_id, role_id)
)`, s.reviewersTbl)); err != nil {
		return fmt.Errorf("create project work item reviewers table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	work_item_id TEXT NOT NULL,
	role_id TEXT NOT NULL,
	driver_kind TEXT NOT NULL DEFAULT 'hecate_task',
	status TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	chat_session_id TEXT NOT NULL DEFAULT '',
	message_id TEXT NOT NULL DEFAULT '',
	context_snapshot_id TEXT NOT NULL DEFAULT '',
	context_packet TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	started_at TEXT NOT NULL DEFAULT '',
	completed_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(project_id, id)
)`, s.assignmentsTbl)); err != nil {
		return fmt.Errorf("create project work assignments table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	work_item_id TEXT NOT NULL,
	assignment_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL,
	author_role_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(project_id, id)
)`, s.artifactsTbl)); err != nil {
		return fmt.Errorf("create project work artifacts table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	work_item_id TEXT NOT NULL,
	source_assignment_id TEXT NOT NULL DEFAULT '',
	source_run_id TEXT NOT NULL DEFAULT '',
	source_chat_session_id TEXT NOT NULL DEFAULT '',
	source_message_id TEXT NOT NULL DEFAULT '',
	target_role_id TEXT NOT NULL DEFAULT '',
	target_assignment_id TEXT NOT NULL DEFAULT '',
	target_work_item_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	summary TEXT NOT NULL,
	recommended_next_action TEXT NOT NULL,
	linked_artifact_ids TEXT NOT NULL DEFAULT '[]',
	linked_memory_ids TEXT NOT NULL DEFAULT '[]',
	context_refs TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL,
	provenance_kind TEXT NOT NULL DEFAULT '',
	trust_label TEXT NOT NULL DEFAULT '',
	created_by_role_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	status_changed_at TEXT NOT NULL,
	PRIMARY KEY(project_id, id)
)`, s.handoffsTbl)); err != nil {
		return fmt.Errorf("create project handoffs table: %w", err)
	}
	if err := s.ensureColumn(ctx, s.assignmentsTbl, "driver_kind", `TEXT NOT NULL DEFAULT 'hecate_task'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, s.assignmentsTbl, "context_packet", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	for _, column := range []string{"default_driver_kind", "default_provider", "default_model", "default_agent_profile"} {
		if err := s.ensureColumn(ctx, s.rolesTbl, column, `TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, s.rolesTbl, "skill_ids", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	for _, stmt := range []struct {
		table string
		name  string
		cols  string
	}{
		{s.rolesTbl, "project_idx", "project_id"},
		{s.workItemsTbl, "project_idx", "project_id"},
		{s.reviewersTbl, "work_item_idx", "project_id, work_item_id"},
		{s.assignmentsTbl, "work_item_idx", "project_id, work_item_id"},
		{s.artifactsTbl, "work_item_idx", "project_id, work_item_id"},
		{s.handoffsTbl, "work_item_idx", "project_id, work_item_id"},
	} {
		name := strings.Trim(stmt.table, `"`) + "_" + stmt.name
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s" ON %s (%s)`, name, stmt.table, stmt.cols)); err != nil {
			return fmt.Errorf("create %s index: %w", stmt.name, err)
		}
	}
	return nil
}

func (s *SQLiteStore) ensureColumn(ctx context.Context, quotedTable, column, definition string) error {
	exists, err := s.columnExists(ctx, quotedTable, column)
	if err != nil {
		return fmt.Errorf("inspect sqlite project work columns: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, quotedTable, column, definition)); err != nil {
		return fmt.Errorf("migrate sqlite project work %s: %w", column, err)
	}
	return nil
}

func (s *SQLiteStore) columnExists(ctx context.Context, quotedTable, column string) (bool, error) {
	bare := strings.Trim(quotedTable, `"`)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, bare))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *SQLiteStore) ListRoles(ctx context.Context, projectID string) ([]AgentRoleProfile, error) {
	projectID = strings.TrimSpace(projectID)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT id, project_id, name, description, instructions, default_driver_kind, default_provider, default_model, default_agent_profile, skill_ids, created_at, updated_at
FROM %s
WHERE project_id = ?
ORDER BY name ASC, id ASC`, s.rolesTbl), projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := BuiltInRoleProfiles(projectID)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortRoles(items)
	return items, nil
}

func (s *SQLiteStore) CreateRole(ctx context.Context, role AgentRoleProfile) (AgentRoleProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	role = normalizeRole(role, time.Now().UTC())
	if IsBuiltInRoleID(role.ID) || role.BuiltIn {
		return AgentRoleProfile{}, ErrBuiltInRole
	}
	if err := validateRole(role); err != nil {
		return AgentRoleProfile{}, err
	}
	skillIDs, err := encodeStringList(role.SkillIDs)
	if err != nil {
		return AgentRoleProfile{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (id, project_id, name, description, instructions, default_driver_kind, default_provider, default_model, default_agent_profile, skill_ids, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.rolesTbl),
		role.ID, role.ProjectID, role.Name, role.Description, role.Instructions,
		role.DefaultDriverKind, role.DefaultProvider, role.DefaultModel, role.DefaultAgentProfile, skillIDs,
		formatTime(role.CreatedAt), formatTime(role.UpdatedAt),
	)
	if err != nil {
		if isSQLiteConstraint(err) {
			return AgentRoleProfile{}, ErrDuplicateRole
		}
		return AgentRoleProfile{}, err
	}
	return s.getCustomRole(ctx, role.ProjectID, role.ID)
}

func (s *SQLiteStore) UpdateRole(ctx context.Context, projectID, id string, update func(*AgentRoleProfile)) (AgentRoleProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	if IsBuiltInRoleID(id) {
		return AgentRoleProfile{}, ErrBuiltInRole
	}
	role, err := s.getCustomRole(ctx, projectID, id)
	if err != nil {
		return AgentRoleProfile{}, err
	}
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
	skillIDs, err := encodeStringList(role.SkillIDs)
	if err != nil {
		return AgentRoleProfile{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET name = ?, description = ?, instructions = ?, default_driver_kind = ?, default_provider = ?, default_model = ?, default_agent_profile = ?, skill_ids = ?, updated_at = ?
WHERE project_id = ? AND id = ?`, s.rolesTbl),
		role.Name, role.Description, role.Instructions,
		role.DefaultDriverKind, role.DefaultProvider, role.DefaultModel, role.DefaultAgentProfile, skillIDs,
		formatTime(role.UpdatedAt), projectID, id,
	)
	if err != nil {
		return AgentRoleProfile{}, err
	}
	return s.getCustomRole(ctx, projectID, id)
}

func (s *SQLiteStore) DeleteRole(ctx context.Context, projectID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	if IsBuiltInRoleID(id) {
		return ErrBuiltInRole
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND id = ?`, s.rolesTbl), projectID, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *SQLiteStore) ListWorkItems(ctx context.Context, projectID string) ([]WorkItem, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT id, project_id, title, brief, status, priority, owner_role_id, created_at, updated_at
FROM %s
WHERE project_id = ?
ORDER BY updated_at DESC, id ASC`, s.workItemsTbl), strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []WorkItem
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return nil, err
		}
		reviewers, err := s.loadReviewers(ctx, item.ProjectID, item.ID)
		if err != nil {
			return nil, err
		}
		item.ReviewerRoleIDs = reviewers
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CreateWorkItem(ctx context.Context, item WorkItem) (WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item = normalizeWorkItem(item, time.Now().UTC())
	if err := validateWorkItem(item); err != nil {
		return WorkItem{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkItem{}, err
	}
	if err := s.insertWorkItem(ctx, tx, item); err != nil {
		_ = tx.Rollback()
		return WorkItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return s.getRequiredWorkItem(ctx, item.ProjectID, item.ID)
}

func (s *SQLiteStore) GetWorkItem(ctx context.Context, projectID, id string) (WorkItem, bool, error) {
	item, err := s.getRequiredWorkItem(ctx, projectID, id)
	if errors.Is(err, ErrNotFound) {
		return WorkItem{}, false, nil
	}
	if err != nil {
		return WorkItem{}, false, err
	}
	return item, true, nil
}

func (s *SQLiteStore) UpdateWorkItem(ctx context.Context, projectID, id string, update func(*WorkItem)) (WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	item, err := s.getRequiredWorkItem(ctx, projectID, id)
	if err != nil {
		return WorkItem{}, err
	}
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkItem{}, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET title = ?, brief = ?, status = ?, priority = ?, owner_role_id = ?, updated_at = ?
WHERE project_id = ? AND id = ?`, s.workItemsTbl),
		item.Title, item.Brief, item.Status, item.Priority, item.OwnerRoleID,
		formatTime(item.UpdatedAt), projectID, id,
	); err != nil {
		_ = tx.Rollback()
		return WorkItem{}, err
	}
	if err := s.replaceReviewers(ctx, tx, item); err != nil {
		_ = tx.Rollback()
		return WorkItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return s.getRequiredWorkItem(ctx, projectID, id)
}

func (s *SQLiteStore) DeleteWorkItem(ctx context.Context, projectID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ?`, s.handoffsTbl),
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ?`, s.artifactsTbl),
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ?`, s.assignmentsTbl),
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ?`, s.reviewersTbl),
	} {
		if _, err := tx.ExecContext(ctx, stmt, projectID, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND id = ?`, s.workItemsTbl), projectID, id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := requireAffected(res); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListAssignments(ctx context.Context, filter AssignmentFilter) ([]Assignment, error) {
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.WorkItemID = strings.TrimSpace(filter.WorkItemID)
	query := fmt.Sprintf(`
SELECT id, project_id, work_item_id, role_id, driver_kind, status, task_id, run_id, chat_session_id, message_id, context_snapshot_id, context_packet, created_at, updated_at, started_at, completed_at
FROM %s
WHERE project_id = ?`, s.assignmentsTbl)
	args := []any{filter.ProjectID}
	if filter.WorkItemID != "" {
		query += ` AND work_item_id = ?`
		args = append(args, filter.WorkItemID)
	}
	query += ` ORDER BY created_at ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Assignment
	for rows.Next() {
		item, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CreateAssignment(ctx context.Context, assignment Assignment) (Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment = normalizeAssignment(assignment, time.Now().UTC())
	if err := validateAssignment(assignment); err != nil {
		return Assignment{}, err
	}
	if _, ok, err := s.GetWorkItem(ctx, assignment.ProjectID, assignment.WorkItemID); err != nil {
		return Assignment{}, err
	} else if !ok {
		return Assignment{}, fmt.Errorf("%w: work item not found", ErrNotFound)
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, work_item_id, role_id, driver_kind, status, task_id, run_id, chat_session_id,
	message_id, context_snapshot_id, context_packet, created_at, updated_at, started_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.assignmentsTbl),
		assignment.ID, assignment.ProjectID, assignment.WorkItemID, assignment.RoleID, assignment.DriverKind,
		assignment.Status, assignment.TaskID, assignment.RunID, assignment.ChatSessionID,
		assignment.MessageID, assignment.ContextSnapshotID, string(assignment.ContextPacket), formatTime(assignment.CreatedAt),
		formatTime(assignment.UpdatedAt), formatTime(assignment.StartedAt), formatTime(assignment.CompletedAt),
	)
	if err != nil {
		if isSQLiteConstraint(err) {
			return Assignment{}, ErrDuplicate
		}
		return Assignment{}, err
	}
	return s.getRequiredAssignment(ctx, assignment.ProjectID, assignment.ID)
}

func (s *SQLiteStore) UpdateAssignment(ctx context.Context, projectID, id string, update func(*Assignment)) (Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	id = strings.TrimSpace(id)
	item, err := s.getRequiredAssignment(ctx, projectID, id)
	if err != nil {
		return Assignment{}, err
	}
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
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET role_id = ?, driver_kind = ?, status = ?, task_id = ?, run_id = ?, chat_session_id = ?, message_id = ?,
	context_snapshot_id = ?, context_packet = ?, updated_at = ?, started_at = ?, completed_at = ?
WHERE project_id = ? AND id = ?`, s.assignmentsTbl),
		item.RoleID, item.DriverKind, item.Status, item.TaskID, item.RunID, item.ChatSessionID,
		item.MessageID, item.ContextSnapshotID, string(item.ContextPacket), formatTime(item.UpdatedAt),
		formatTime(item.StartedAt), formatTime(item.CompletedAt), projectID, id,
	)
	if err != nil {
		return Assignment{}, err
	}
	return s.getRequiredAssignment(ctx, projectID, id)
}

func (s *SQLiteStore) DeleteAssignment(ctx context.Context, projectID, workItemID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	workItemID = strings.TrimSpace(workItemID)
	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND assignment_id = ?`, s.artifactsTbl),
		projectID,
		id,
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND (source_assignment_id = ? OR target_assignment_id = ?)`, s.handoffsTbl),
		projectID,
		id,
		id,
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	res, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ? AND id = ?`, s.assignmentsTbl),
		projectID,
		workItemID,
		id,
	)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := requireAffected(res); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]CollaborationArtifact, error) {
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.WorkItemID = strings.TrimSpace(filter.WorkItemID)
	filter.AssignmentID = strings.TrimSpace(filter.AssignmentID)
	query := fmt.Sprintf(`
SELECT id, project_id, work_item_id, assignment_id, kind, title, body, author_role_id, created_at, updated_at
FROM %s
WHERE project_id = ?`, s.artifactsTbl)
	args := []any{filter.ProjectID}
	if filter.WorkItemID != "" {
		query += ` AND work_item_id = ?`
		args = append(args, filter.WorkItemID)
	}
	if filter.AssignmentID != "" {
		query += ` AND assignment_id = ?`
		args = append(args, filter.AssignmentID)
	}
	query += ` ORDER BY created_at ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CollaborationArtifact
	for rows.Next() {
		item, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CreateArtifact(ctx context.Context, artifact CollaborationArtifact) (CollaborationArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact = normalizeArtifact(artifact, time.Now().UTC())
	if err := validateArtifact(artifact); err != nil {
		return CollaborationArtifact{}, err
	}
	if _, ok, err := s.GetWorkItem(ctx, artifact.ProjectID, artifact.WorkItemID); err != nil {
		return CollaborationArtifact{}, err
	} else if !ok {
		return CollaborationArtifact{}, fmt.Errorf("%w: work item not found", ErrNotFound)
	}
	if artifact.AssignmentID != "" {
		assignment, err := s.getRequiredAssignment(ctx, artifact.ProjectID, artifact.AssignmentID)
		if errors.Is(err, ErrNotFound) {
			return CollaborationArtifact{}, fmt.Errorf("%w: assignment not found", err)
		}
		if err != nil {
			return CollaborationArtifact{}, err
		}
		if assignment.WorkItemID != artifact.WorkItemID {
			return CollaborationArtifact{}, fmt.Errorf("%w: assignment not found", ErrNotFound)
		}
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, work_item_id, assignment_id, kind, title, body, author_role_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.artifactsTbl),
		artifact.ID, artifact.ProjectID, artifact.WorkItemID, artifact.AssignmentID,
		artifact.Kind, artifact.Title, artifact.Body, artifact.AuthorRoleID,
		formatTime(artifact.CreatedAt), formatTime(artifact.UpdatedAt),
	)
	if err != nil {
		if isSQLiteConstraint(err) {
			return CollaborationArtifact{}, ErrDuplicate
		}
		return CollaborationArtifact{}, err
	}
	return s.getRequiredArtifact(ctx, artifact.ProjectID, artifact.ID)
}

func (s *SQLiteStore) ListHandoffs(ctx context.Context, filter HandoffFilter) ([]Handoff, error) {
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.WorkItemID = strings.TrimSpace(filter.WorkItemID)
	filter.Status = strings.TrimSpace(filter.Status)
	query := fmt.Sprintf(`
SELECT id, project_id, work_item_id, source_assignment_id, source_run_id, source_chat_session_id, source_message_id, target_role_id, target_assignment_id, target_work_item_id, title, summary, recommended_next_action, linked_artifact_ids, linked_memory_ids, context_refs, status, provenance_kind, trust_label, created_by_role_id, created_at, updated_at, status_changed_at
FROM %s
WHERE project_id = ?`, s.handoffsTbl)
	args := []any{filter.ProjectID}
	if filter.WorkItemID != "" {
		query += ` AND work_item_id = ?`
		args = append(args, filter.WorkItemID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC, id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Handoff
	for rows.Next() {
		item, err := scanHandoff(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CreateHandoff(ctx context.Context, handoff Handoff) (Handoff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	handoff = normalizeHandoff(handoff, time.Now().UTC(), false)
	if err := validateHandoff(handoff); err != nil {
		return Handoff{}, err
	}
	if _, ok, err := s.GetWorkItem(ctx, handoff.ProjectID, handoff.WorkItemID); err != nil {
		return Handoff{}, err
	} else if !ok {
		return Handoff{}, fmt.Errorf("%w: work item not found", ErrNotFound)
	}
	if err := s.validateHandoffAssignments(ctx, handoff); err != nil {
		return Handoff{}, err
	}
	linkedArtifacts, err := encodeStringList(handoff.LinkedArtifactIDs)
	if err != nil {
		return Handoff{}, err
	}
	linkedMemory, err := encodeStringList(handoff.LinkedMemoryIDs)
	if err != nil {
		return Handoff{}, err
	}
	contextRefs, err := encodeStringList(handoff.ContextRefs)
	if err != nil {
		return Handoff{}, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (
	id, project_id, work_item_id, source_assignment_id, source_run_id, source_chat_session_id, source_message_id,
	target_role_id, target_assignment_id, target_work_item_id, title, summary, recommended_next_action,
	linked_artifact_ids, linked_memory_ids, context_refs, status, provenance_kind, trust_label, created_by_role_id,
	created_at, updated_at, status_changed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.handoffsTbl),
		handoff.ID, handoff.ProjectID, handoff.WorkItemID, handoff.SourceAssignmentID,
		handoff.SourceRunID, handoff.SourceChatSessionID, handoff.SourceMessageID,
		handoff.TargetRoleID, handoff.TargetAssignmentID, handoff.TargetWorkItemID,
		handoff.Title, handoff.Summary, handoff.RecommendedNextAction, linkedArtifacts,
		linkedMemory, contextRefs, handoff.Status, handoff.ProvenanceKind,
		handoff.TrustLabel, handoff.CreatedByRoleID, formatTime(handoff.CreatedAt),
		formatTime(handoff.UpdatedAt), formatTime(handoff.StatusChangedAt),
	)
	if err != nil {
		if isSQLiteConstraint(err) {
			return Handoff{}, ErrDuplicate
		}
		return Handoff{}, err
	}
	return s.getRequiredHandoff(ctx, handoff.ProjectID, handoff.ID)
}

func (s *SQLiteStore) UpdateHandoff(ctx context.Context, projectID, workItemID, id string, update func(*Handoff)) (Handoff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.getRequiredHandoff(ctx, projectID, id)
	if err != nil {
		return Handoff{}, err
	}
	if item.WorkItemID != strings.TrimSpace(workItemID) {
		return Handoff{}, ErrNotFound
	}
	originalID := item.ID
	originalProjectID := item.ProjectID
	originalWorkItemID := item.WorkItemID
	originalCreatedAt := item.CreatedAt
	originalStatus := item.Status
	if update != nil {
		update(&item)
	}
	if strings.TrimSpace(item.ID) != originalID ||
		strings.TrimSpace(item.ProjectID) != originalProjectID ||
		strings.TrimSpace(item.WorkItemID) != originalWorkItemID {
		return Handoff{}, fmt.Errorf("%w: handoff id, project_id, and work_item_id cannot be changed", ErrInvalid)
	}
	item.ID = originalID
	item.ProjectID = originalProjectID
	item.WorkItemID = originalWorkItemID
	item.CreatedAt = originalCreatedAt
	item.UpdatedAt = time.Now().UTC()
	item = normalizeHandoff(item, item.UpdatedAt, strings.TrimSpace(item.Status) != originalStatus)
	if err := validateHandoff(item); err != nil {
		return Handoff{}, err
	}
	if err := s.validateHandoffAssignments(ctx, item); err != nil {
		return Handoff{}, err
	}
	linkedArtifacts, err := encodeStringList(item.LinkedArtifactIDs)
	if err != nil {
		return Handoff{}, err
	}
	linkedMemory, err := encodeStringList(item.LinkedMemoryIDs)
	if err != nil {
		return Handoff{}, err
	}
	contextRefs, err := encodeStringList(item.ContextRefs)
	if err != nil {
		return Handoff{}, err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s SET
	source_assignment_id = ?, source_run_id = ?, source_chat_session_id = ?, source_message_id = ?,
	target_role_id = ?, target_assignment_id = ?, target_work_item_id = ?, title = ?, summary = ?,
	recommended_next_action = ?, linked_artifact_ids = ?, linked_memory_ids = ?, context_refs = ?,
	status = ?, provenance_kind = ?, trust_label = ?, created_by_role_id = ?, updated_at = ?, status_changed_at = ?
WHERE project_id = ? AND id = ? AND work_item_id = ?`, s.handoffsTbl),
		item.SourceAssignmentID, item.SourceRunID, item.SourceChatSessionID, item.SourceMessageID,
		item.TargetRoleID, item.TargetAssignmentID, item.TargetWorkItemID, item.Title,
		item.Summary, item.RecommendedNextAction, linkedArtifacts, linkedMemory, contextRefs,
		item.Status, item.ProvenanceKind, item.TrustLabel, item.CreatedByRoleID,
		formatTime(item.UpdatedAt), formatTime(item.StatusChangedAt),
		item.ProjectID, item.ID, item.WorkItemID,
	)
	if err != nil {
		return Handoff{}, err
	}
	if err := requireAffected(res); err != nil {
		return Handoff{}, err
	}
	return s.getRequiredHandoff(ctx, item.ProjectID, item.ID)
}

func (s *SQLiteStore) DeleteHandoff(ctx context.Context, projectID, workItemID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ? AND id = ?`, s.handoffsTbl), strings.TrimSpace(projectID), strings.TrimSpace(workItemID), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *SQLiteStore) DeleteProject(ctx context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteWhereProject(ctx, strings.TrimSpace(projectID))
}

func (s *SQLiteStore) Clear(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	for _, table := range []string{s.handoffsTbl, s.artifactsTbl, s.assignmentsTbl, s.reviewersTbl, s.workItemsTbl, s.rolesTbl} {
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, table))
		if err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (s *SQLiteStore) deleteWhereProject(ctx context.Context, projectID string) (int, error) {
	deleted := 0
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	for _, table := range []string{s.handoffsTbl, s.artifactsTbl, s.assignmentsTbl, s.reviewersTbl, s.workItemsTbl, s.rolesTbl} {
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, table), projectID)
		if err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (s *SQLiteStore) insertWorkItem(ctx context.Context, tx *sql.Tx, item WorkItem) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (id, project_id, title, brief, status, priority, owner_role_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.workItemsTbl),
		item.ID, item.ProjectID, item.Title, item.Brief, item.Status, item.Priority,
		item.OwnerRoleID, formatTime(item.CreatedAt), formatTime(item.UpdatedAt),
	); err != nil {
		if isSQLiteConstraint(err) {
			return ErrDuplicate
		}
		return err
	}
	return s.replaceReviewers(ctx, tx, item)
}

func (s *SQLiteStore) replaceReviewers(ctx context.Context, tx *sql.Tx, item WorkItem) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = ? AND work_item_id = ?`, s.reviewersTbl), item.ProjectID, item.ID); err != nil {
		return err
	}
	for _, roleID := range item.ReviewerRoleIDs {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s (project_id, work_item_id, role_id)
VALUES (?, ?, ?)`, s.reviewersTbl), item.ProjectID, item.ID, roleID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) loadReviewers(ctx context.Context, projectID, workItemID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT role_id
FROM %s
WHERE project_id = ? AND work_item_id = ?
ORDER BY role_id ASC`, s.reviewersTbl), projectID, workItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reviewers []string
	for rows.Next() {
		var roleID string
		if err := rows.Scan(&roleID); err != nil {
			return nil, err
		}
		reviewers = append(reviewers, roleID)
	}
	return reviewers, rows.Err()
}

func (s *SQLiteStore) getCustomRole(ctx context.Context, projectID, id string) (AgentRoleProfile, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, name, description, instructions, default_driver_kind, default_provider, default_model, default_agent_profile, skill_ids, created_at, updated_at
FROM %s
WHERE project_id = ? AND id = ?`, s.rolesTbl), strings.TrimSpace(projectID), strings.TrimSpace(id))
	role, err := scanRole(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRoleProfile{}, ErrNotFound
	}
	return role, err
}

func (s *SQLiteStore) getRequiredWorkItem(ctx context.Context, projectID, id string) (WorkItem, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, title, brief, status, priority, owner_role_id, created_at, updated_at
FROM %s
WHERE project_id = ? AND id = ?`, s.workItemsTbl), strings.TrimSpace(projectID), strings.TrimSpace(id))
	item, err := scanWorkItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkItem{}, ErrNotFound
	}
	if err != nil {
		return WorkItem{}, err
	}
	reviewers, err := s.loadReviewers(ctx, item.ProjectID, item.ID)
	if err != nil {
		return WorkItem{}, err
	}
	item.ReviewerRoleIDs = reviewers
	return item, nil
}

func (s *SQLiteStore) getRequiredAssignment(ctx context.Context, projectID, id string) (Assignment, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, work_item_id, role_id, driver_kind, status, task_id, run_id, chat_session_id, message_id, context_snapshot_id, context_packet, created_at, updated_at, started_at, completed_at
FROM %s
WHERE project_id = ? AND id = ?`, s.assignmentsTbl), strings.TrimSpace(projectID), strings.TrimSpace(id))
	item, err := scanAssignment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, ErrNotFound
	}
	return item, err
}

func (s *SQLiteStore) getRequiredArtifact(ctx context.Context, projectID, id string) (CollaborationArtifact, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, work_item_id, assignment_id, kind, title, body, author_role_id, created_at, updated_at
FROM %s
WHERE project_id = ? AND id = ?`, s.artifactsTbl), strings.TrimSpace(projectID), strings.TrimSpace(id))
	item, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CollaborationArtifact{}, ErrNotFound
	}
	return item, err
}

func (s *SQLiteStore) getRequiredHandoff(ctx context.Context, projectID, id string) (Handoff, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT id, project_id, work_item_id, source_assignment_id, source_run_id, source_chat_session_id, source_message_id, target_role_id, target_assignment_id, target_work_item_id, title, summary, recommended_next_action, linked_artifact_ids, linked_memory_ids, context_refs, status, provenance_kind, trust_label, created_by_role_id, created_at, updated_at, status_changed_at
FROM %s
WHERE project_id = ? AND id = ?`, s.handoffsTbl), strings.TrimSpace(projectID), strings.TrimSpace(id))
	item, err := scanHandoff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	return item, err
}

func (s *SQLiteStore) validateHandoffAssignments(ctx context.Context, handoff Handoff) error {
	if handoff.SourceAssignmentID != "" {
		assignment, err := s.getRequiredAssignment(ctx, handoff.ProjectID, handoff.SourceAssignmentID)
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: source assignment not found", err)
		}
		if err != nil {
			return err
		}
		if assignment.WorkItemID != handoff.WorkItemID {
			return fmt.Errorf("%w: source assignment not found", ErrNotFound)
		}
	}
	if handoff.TargetAssignmentID != "" {
		assignment, err := s.getRequiredAssignment(ctx, handoff.ProjectID, handoff.TargetAssignmentID)
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: target assignment not found", err)
		}
		if err != nil {
			return err
		}
		if handoff.TargetWorkItemID != "" && assignment.WorkItemID != handoff.TargetWorkItemID {
			return fmt.Errorf("%w: target assignment not found", ErrNotFound)
		}
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanRole(row scanner) (AgentRoleProfile, error) {
	var item AgentRoleProfile
	var skillIDs string
	var createdAt, updatedAt string
	if err := row.Scan(
		&item.ID,
		&item.ProjectID,
		&item.Name,
		&item.Description,
		&item.Instructions,
		&item.DefaultDriverKind,
		&item.DefaultProvider,
		&item.DefaultModel,
		&item.DefaultAgentProfile,
		&skillIDs,
		&createdAt,
		&updatedAt,
	); err != nil {
		return AgentRoleProfile{}, err
	}
	var err error
	if item.SkillIDs, err = decodeStringList(skillIDs); err != nil {
		return AgentRoleProfile{}, err
	}
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return AgentRoleProfile{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return AgentRoleProfile{}, err
	}
	return item, nil
}

func scanWorkItem(row scanner) (WorkItem, error) {
	var item WorkItem
	var createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.ProjectID, &item.Title, &item.Brief, &item.Status, &item.Priority, &item.OwnerRoleID, &createdAt, &updatedAt); err != nil {
		return WorkItem{}, err
	}
	var err error
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return WorkItem{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return WorkItem{}, err
	}
	return item, nil
}

func scanAssignment(row scanner) (Assignment, error) {
	var item Assignment
	var createdAt, updatedAt, startedAt, completedAt string
	if err := row.Scan(
		&item.ID, &item.ProjectID, &item.WorkItemID, &item.RoleID, &item.DriverKind, &item.Status,
		&item.TaskID, &item.RunID, &item.ChatSessionID, &item.MessageID,
		&item.ContextSnapshotID, &item.ContextPacket, &createdAt, &updatedAt, &startedAt, &completedAt,
	); err != nil {
		return Assignment{}, err
	}
	var err error
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return Assignment{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Assignment{}, err
	}
	if item.StartedAt, err = parseTime(startedAt); err != nil {
		return Assignment{}, err
	}
	if item.CompletedAt, err = parseTime(completedAt); err != nil {
		return Assignment{}, err
	}
	return item, nil
}

func scanArtifact(row scanner) (CollaborationArtifact, error) {
	var item CollaborationArtifact
	var createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.ProjectID, &item.WorkItemID, &item.AssignmentID, &item.Kind, &item.Title, &item.Body, &item.AuthorRoleID, &createdAt, &updatedAt); err != nil {
		return CollaborationArtifact{}, err
	}
	var err error
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return CollaborationArtifact{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return CollaborationArtifact{}, err
	}
	return item, nil
}

func scanHandoff(row scanner) (Handoff, error) {
	var item Handoff
	var linkedArtifactIDs, linkedMemoryIDs, contextRefs string
	var createdAt, updatedAt, statusChangedAt string
	if err := row.Scan(
		&item.ID, &item.ProjectID, &item.WorkItemID, &item.SourceAssignmentID,
		&item.SourceRunID, &item.SourceChatSessionID, &item.SourceMessageID,
		&item.TargetRoleID, &item.TargetAssignmentID, &item.TargetWorkItemID,
		&item.Title, &item.Summary, &item.RecommendedNextAction,
		&linkedArtifactIDs, &linkedMemoryIDs, &contextRefs, &item.Status,
		&item.ProvenanceKind, &item.TrustLabel, &item.CreatedByRoleID,
		&createdAt, &updatedAt, &statusChangedAt,
	); err != nil {
		return Handoff{}, err
	}
	var err error
	if item.LinkedArtifactIDs, err = decodeStringList(linkedArtifactIDs); err != nil {
		return Handoff{}, err
	}
	if item.LinkedMemoryIDs, err = decodeStringList(linkedMemoryIDs); err != nil {
		return Handoff{}, err
	}
	if item.ContextRefs, err = decodeStringList(contextRefs); err != nil {
		return Handoff{}, err
	}
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return Handoff{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Handoff{}, err
	}
	if item.StatusChangedAt, err = parseTime(statusChangedAt); err != nil {
		return Handoff{}, err
	}
	return item, nil
}

func requireAffected(res sql.Result) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func isSQLiteConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "constraint")
}

func encodeStringList(values []string) (string, error) {
	values = normalizeStringList(values)
	if values == nil {
		values = []string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeStringList(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil, err
	}
	return normalizeStringList(values), nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
