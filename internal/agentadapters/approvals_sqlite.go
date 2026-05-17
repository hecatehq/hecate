package agentadapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
)

// SQLiteApprovalStore is the durable backend for agent-chat approvals
// and operator-authored grants. It satisfies ApprovalStore plus the
// grant-management surface the coordinator's HTTP layer uses.
//
// Schema is created by migrate() on construction; the store is safe
// to instantiate multiple times against the same client. Two tables:
//
//   - chat_approvals        — one row per RequestPermission. The full
//     ACP payload + options are stored as JSON blobs so the wire shape
//     can evolve without schema churn.
//   - chat_approval_grants — operator-authored "always allow /
//     always deny" decisions broader than `once` scope.
//
// Process-local waiters are NOT persisted: a Hecate restart cannot
// resurrect an in-flight ACP RequestPermission. ReconcilePending()
// (called from cmd/hecate at startup, before the gateway accepts
// requests) sweeps any pending rows from a prior process and marks
// them status=timed_out, path=startup_reconcile.
type SQLiteApprovalStore struct {
	client        *storage.SQLiteClient
	approvals     string // qualified (quoted) for SQL refs
	grants        string
	approvalsBase string // unquoted, for index names
	grantsBase    string
}

// NewSQLiteApprovalStore opens the store and runs migrations. Returns
// the store ready for use.
func NewSQLiteApprovalStore(ctx context.Context, client *storage.SQLiteClient) (*SQLiteApprovalStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	store := &SQLiteApprovalStore{
		client:        client,
		approvals:     client.QualifiedTable("chat_approvals"),
		grants:        client.QualifiedTable("chat_approval_grants"),
		approvalsBase: client.TableName("chat_approvals"),
		grantsBase:    client.TableName("chat_approval_grants"),
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// Backend identifies the backend choice for diagnostics + tests.
func (s *SQLiteApprovalStore) Backend() string { return "sqlite" }

// ─── ApprovalStore ───────────────────────────────────────────────────────────

func (s *SQLiteApprovalStore) CreateApproval(ctx context.Context, a Approval) (Approval, error) {
	if a.ID == "" {
		a.ID = newApprovalID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Status == "" {
		a.Status = ApprovalStatusPending
	}
	optionsJSON, err := json.Marshal(a.ACPOptions)
	if err != nil {
		return Approval{}, fmt.Errorf("encode acp_options: %w", err)
	}
	scopeChoicesJSON, err := json.Marshal(a.ScopeChoices)
	if err != nil {
		return Approval{}, fmt.Errorf("encode scope_choices: %w", err)
	}
	if a.ACPPayload == nil {
		a.ACPPayload = json.RawMessage("null")
	}

	_, err = s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (
				id, session_id, adapter_id, workspace, tool_kind, tool_name, status,
				acp_payload, acp_options, scope_choices,
				selected_option, scope, decision, path, decision_note,
				created_at, resolved_at, expires_at
			) VALUES (?,?,?,?,?,?,?, ?,?,?, ?,?,?,?,?, ?,?,?)`,
			s.approvals,
		),
		a.ID, a.SessionID, a.AdapterID, a.Workspace, a.ToolKind, a.ToolName, string(a.Status),
		[]byte(a.ACPPayload), optionsJSON, scopeChoicesJSON,
		a.SelectedOption, string(a.Scope), string(a.Decision), string(a.Path), a.DecisionNote,
		a.CreatedAt.UTC(), nullableTime(a.ResolvedAt), a.ExpiresAt.UTC(),
	)
	if err != nil {
		return Approval{}, fmt.Errorf("insert approval: %w", err)
	}
	return s.loadApproval(ctx, a.ID)
}

func (s *SQLiteApprovalStore) ResolveApproval(ctx context.Context, id string, status ApprovalStatus, decision ApprovalDecision, selectedOption string, scope ApprovalScope, path ApprovalResolutionPath, note string, resolvedAt time.Time) (Approval, error) {
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	// Atomic transition: only flips when the row is currently pending.
	// The RowsAffected() result distinguishes ErrApprovalNotFound from
	// ErrApprovalAlreadyResolved without a separate read+update race.
	res, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET
				status = ?,
				decision = ?,
				selected_option = ?,
				scope = ?,
				path = ?,
				decision_note = ?,
				resolved_at = ?
			 WHERE id = ? AND status = 'pending'`,
			s.approvals,
		),
		string(status), string(decision), selectedOption,
		string(scope), string(path), note,
		resolvedAt.UTC(),
		id,
	)
	if err != nil {
		return Approval{}, fmt.Errorf("update approval: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Row either doesn't exist or is already terminal. Read once
		// to disambiguate; this is a cheap one-row lookup.
		row, lerr := s.loadApproval(ctx, id)
		if lerr != nil {
			if errors.Is(lerr, sql.ErrNoRows) {
				return Approval{}, ErrApprovalNotFound
			}
			return Approval{}, lerr
		}
		return row, ErrApprovalAlreadyResolved
	}
	return s.loadApproval(ctx, id)
}

func (s *SQLiteApprovalStore) GetApproval(ctx context.Context, id string) (Approval, error) {
	row, err := s.loadApproval(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Approval{}, ErrApprovalNotFound
		}
		return Approval{}, err
	}
	return row, nil
}

func (s *SQLiteApprovalStore) ListApprovals(ctx context.Context, sessionID string, status ApprovalStatus) ([]Approval, error) {
	args := []any{sessionID}
	where := "session_id = ?"
	if status != "" {
		where += " AND status = ?"
		args = append(args, string(status))
	}
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, session_id, adapter_id, workspace, tool_kind, tool_name, status,
				acp_payload, acp_options, scope_choices,
				selected_option, scope, decision, path, decision_note,
				created_at, resolved_at, expires_at
			 FROM %s WHERE %s ORDER BY created_at ASC, id ASC`,
			s.approvals, where,
		),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query approvals: %w", err)
	}
	defer rows.Close()

	out := make([]Approval, 0)
	for rows.Next() {
		row, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteApprovalStore) FindMatchingGrant(ctx context.Context, sessionID, workspace, adapterID, toolKind string, now time.Time) (Grant, bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// Specificity ranking: session(0) → workspace_tool(1) → adapter_tool(2).
	// Within a rank, the most-recently-granted entry wins (matches the
	// memory store's "later overrides earlier" tiebreak so operator
	// re-grants take effect immediately).
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, scope, adapter_id, tool_kind, workspace, session_id, decision, granted_by, granted_at, expires_at
			 FROM %s
			 WHERE adapter_id = ? AND tool_kind = ?
			   AND (
				 (scope = 'session'        AND session_id = ?) OR
				 (scope = 'workspace_tool' AND workspace = ?)  OR
				 (scope = 'adapter_tool')
			   )
			   AND (expires_at IS NULL OR expires_at > ?)`,
			s.grants,
		),
		adapterID, toolKind, sessionID, workspace, now.UTC(),
	)
	if err != nil {
		return Grant{}, false, fmt.Errorf("query grants: %w", err)
	}
	defer rows.Close()

	type scored struct {
		grant Grant
		rank  int
	}
	rankFor := map[ApprovalScope]int{
		ApprovalScopeSession:       0,
		ApprovalScopeWorkspaceTool: 1,
		ApprovalScopeAdapterTool:   2,
	}
	var best scored
	bestSet := false
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return Grant{}, false, err
		}
		r, ok := rankFor[g.Scope]
		if !ok {
			continue
		}
		if !bestSet || r < best.rank || (r == best.rank && g.GrantedAt.After(best.grant.GrantedAt)) {
			best = scored{grant: g, rank: r}
			bestSet = true
		}
	}
	if err := rows.Err(); err != nil {
		return Grant{}, false, err
	}
	if !bestSet {
		return Grant{}, false, nil
	}
	return best.grant, true, nil
}

// ─── Grants ──────────────────────────────────────────────────────────────────

// CreateGrant persists an operator-authored grant. Used by the
// coordinator's Resolve method when scope > once.
func (s *SQLiteApprovalStore) CreateGrant(ctx context.Context, g Grant) (Grant, error) {
	if g.ID == "" {
		g.ID = newGrantID()
	}
	if g.GrantedAt.IsZero() {
		g.GrantedAt = time.Now().UTC()
	}
	_, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (id, scope, adapter_id, tool_kind, workspace, session_id, decision, granted_by, granted_at, expires_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			s.grants,
		),
		g.ID, string(g.Scope), g.AdapterID, g.ToolKind, g.Workspace, g.SessionID, string(g.Decision),
		g.GrantedBy, g.GrantedAt.UTC(), nullableTime(g.ExpiresAt),
	)
	if err != nil {
		return Grant{}, fmt.Errorf("insert grant: %w", err)
	}
	return g, nil
}

// ListGrants returns grants matching the filter, newest-first.
// Expired grants are excluded by the SQL predicate.
func (s *SQLiteApprovalStore) ListGrants(ctx context.Context, filter GrantFilter, now time.Time) ([]Grant, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	clauses := []string{"(expires_at IS NULL OR expires_at > ?)"}
	args := []any{now.UTC()}
	if filter.AdapterID != "" {
		clauses = append(clauses, "adapter_id = ?")
		args = append(args, filter.AdapterID)
	}
	if filter.Scope != "" {
		clauses = append(clauses, "scope = ?")
		args = append(args, string(filter.Scope))
	}
	if filter.ToolKind != "" {
		clauses = append(clauses, "tool_kind = ?")
		args = append(args, filter.ToolKind)
	}
	rows, err := s.client.DB().QueryContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, scope, adapter_id, tool_kind, workspace, session_id, decision, granted_by, granted_at, expires_at
			 FROM %s WHERE %s ORDER BY granted_at DESC, id ASC`,
			s.grants, strings.Join(clauses, " AND "),
		),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query grants: %w", err)
	}
	defer rows.Close()

	out := make([]Grant, 0)
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGrant removes a grant by id. Returns ErrApprovalNotFound when
// the id is unknown so the HTTP layer can surface 404 uniformly.
func (s *SQLiteApprovalStore) DeleteGrant(ctx context.Context, id string) error {
	res, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.grants),
		id,
	)
	if err != nil {
		return fmt.Errorf("delete grant: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrApprovalNotFound
	}
	return nil
}

// PruneExpiredGrants removes grants whose ExpiresAt is in the past.
// Returns the number deleted. Called by the retention worker. Live
// grants (no expiry, or expiry in the future) are never touched.
func (s *SQLiteApprovalStore) PruneExpiredGrants(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	res, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at <= ?`,
			s.grants,
		),
		now.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("prune expired grants: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}

// ─── Reconcile + retention ───────────────────────────────────────────────────

// ReconcilePending sweeps any approval rows that survived a Hecate
// restart in the pending state and marks them status=timed_out,
// path=startup_reconcile. Process-local waiters can't be
// resurrected, so the operator UI must not surface these as
// actionable.
//
// Called from cmd/hecate at startup, after migrate() and before the
// gateway accepts requests. Returns the number of rows reconciled.
func (s *SQLiteApprovalStore) ReconcilePending(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	const note = "process-local waiter lost on restart; reconciled at startup"
	res, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s SET
				status = 'timed_out',
				path = 'startup_reconcile',
				resolved_at = ?,
				decision_note = ?
			 WHERE status = 'pending'`,
			s.approvals,
		),
		now.UTC(), note,
	)
	if err != nil {
		return 0, fmt.Errorf("reconcile pending approvals: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}

// PruneApprovals deletes resolved (non-pending) approval rows older
// than maxAge or beyond maxCount, whichever fires first. Pending
// rows are never auto-pruned — they're caller state, not history.
// Returns total rows deleted.
func (s *SQLiteApprovalStore) PruneApprovals(ctx context.Context, now time.Time, maxAge time.Duration, maxCount int) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var deleted int64
	if maxAge > 0 {
		cutoff := now.Add(-maxAge).UTC()
		res, err := s.client.DB().ExecContext(
			ctx,
			fmt.Sprintf(
				`DELETE FROM %s WHERE status != 'pending' AND created_at < ?`,
				s.approvals,
			),
			cutoff,
		)
		if err != nil {
			return deleted, fmt.Errorf("prune approvals by age: %w", err)
		}
		n, _ := res.RowsAffected()
		deleted += n
	}
	if maxCount > 0 {
		// Keep newest maxCount resolved rows; drop the rest.
		res, err := s.client.DB().ExecContext(
			ctx,
			fmt.Sprintf(
				`DELETE FROM %s WHERE status != 'pending' AND id IN (
					SELECT id FROM %s WHERE status != 'pending'
					ORDER BY created_at DESC, id DESC
					LIMIT -1 OFFSET ?
				)`,
				s.approvals, s.approvals,
			),
			maxCount,
		)
		if err != nil {
			return deleted, fmt.Errorf("prune approvals by count: %w", err)
		}
		n, _ := res.RowsAffected()
		deleted += n
	}
	return deleted, nil
}

// ─── Internals ───────────────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteApprovalStore) loadApproval(ctx context.Context, id string) (Approval, error) {
	row := s.client.DB().QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT id, session_id, adapter_id, workspace, tool_kind, tool_name, status,
				acp_payload, acp_options, scope_choices,
				selected_option, scope, decision, path, decision_note,
				created_at, resolved_at, expires_at
			 FROM %s WHERE id = ?`,
			s.approvals,
		),
		id,
	)
	return scanApproval(row)
}

func scanApproval(scanner rowScanner) (Approval, error) {
	var (
		a               Approval
		statusStr       string
		scopeStr        string
		decisionStr     string
		pathStr         string
		acpPayload      []byte
		acpOptionsRaw   []byte
		scopeChoicesRaw []byte
		resolvedAt      sql.NullTime
	)
	err := scanner.Scan(
		&a.ID, &a.SessionID, &a.AdapterID, &a.Workspace, &a.ToolKind, &a.ToolName, &statusStr,
		&acpPayload, &acpOptionsRaw, &scopeChoicesRaw,
		&a.SelectedOption, &scopeStr, &decisionStr, &pathStr, &a.DecisionNote,
		&a.CreatedAt, &resolvedAt, &a.ExpiresAt,
	)
	if err != nil {
		return Approval{}, err
	}
	a.Status = ApprovalStatus(statusStr)
	a.Scope = ApprovalScope(scopeStr)
	a.Decision = ApprovalDecision(decisionStr)
	a.Path = ApprovalResolutionPath(pathStr)
	if len(acpPayload) > 0 {
		a.ACPPayload = json.RawMessage(acpPayload)
	}
	if len(acpOptionsRaw) > 0 {
		if err := json.Unmarshal(acpOptionsRaw, &a.ACPOptions); err != nil {
			return Approval{}, fmt.Errorf("decode acp_options: %w", err)
		}
	}
	if len(scopeChoicesRaw) > 0 {
		if err := json.Unmarshal(scopeChoicesRaw, &a.ScopeChoices); err != nil {
			return Approval{}, fmt.Errorf("decode scope_choices: %w", err)
		}
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time.UTC()
		a.ResolvedAt = &t
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ExpiresAt = a.ExpiresAt.UTC()
	return a, nil
}

func scanGrant(scanner rowScanner) (Grant, error) {
	var (
		g           Grant
		scopeStr    string
		decisionStr string
		expiresAt   sql.NullTime
	)
	err := scanner.Scan(
		&g.ID, &scopeStr, &g.AdapterID, &g.ToolKind, &g.Workspace, &g.SessionID, &decisionStr,
		&g.GrantedBy, &g.GrantedAt, &expiresAt,
	)
	if err != nil {
		return Grant{}, err
	}
	g.Scope = ApprovalScope(scopeStr)
	g.Decision = ApprovalDecision(decisionStr)
	g.GrantedAt = g.GrantedAt.UTC()
	if expiresAt.Valid {
		t := expiresAt.Time.UTC()
		g.ExpiresAt = &t
	}
	return g, nil
}

func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}

func (s *SQLiteApprovalStore) migrate(ctx context.Context) error {
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL,
				adapter_id TEXT NOT NULL,
				workspace TEXT NOT NULL DEFAULT '',
				tool_kind TEXT NOT NULL DEFAULT '',
				tool_name TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				acp_payload BLOB,
				acp_options BLOB NOT NULL,
				scope_choices BLOB NOT NULL,
				selected_option TEXT NOT NULL DEFAULT '',
				scope TEXT NOT NULL DEFAULT '',
				decision TEXT NOT NULL DEFAULT '',
				path TEXT NOT NULL DEFAULT '',
				decision_note TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMP NOT NULL,
				resolved_at TIMESTAMP,
				expires_at TIMESTAMP NOT NULL
			)`,
			s.approvals,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat_approvals: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s_session_idx ON %s (session_id, status, created_at)`,
			s.approvalsBase, s.approvals,
		),
	); err != nil {
		return fmt.Errorf("create approvals session index: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s_status_idx ON %s (status, created_at)`,
			s.approvalsBase, s.approvals,
		),
	); err != nil {
		return fmt.Errorf("create approvals status index: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				id TEXT PRIMARY KEY,
				scope TEXT NOT NULL,
				adapter_id TEXT NOT NULL,
				tool_kind TEXT NOT NULL,
				workspace TEXT NOT NULL DEFAULT '',
				session_id TEXT NOT NULL DEFAULT '',
				decision TEXT NOT NULL,
				granted_by TEXT NOT NULL DEFAULT '',
				granted_at TIMESTAMP NOT NULL,
				expires_at TIMESTAMP
			)`,
			s.grants,
		),
	); err != nil {
		return fmt.Errorf("migrate sqlite chat_approval_grants: %w", err)
	}
	if _, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s_lookup_idx ON %s (adapter_id, tool_kind, scope)`,
			s.grantsBase, s.grants,
		),
	); err != nil {
		return fmt.Errorf("create grants lookup index: %w", err)
	}
	return nil
}
