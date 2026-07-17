package taskstate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/pkg/types"
)

// RecordRichInputProviderAttempt is the durable boundary immediately before a
// provider can receive a rich input. It deliberately mutates only the stored
// route fields: a worker must never replace an authoritative run with its
// stale copy merely to record a provider attempt.
func (s *MemoryStore) RecordRichInputProviderAttempt(_ context.Context, attempt RichInputProviderAttempt) (RichInputProviderAttemptResult, error) {
	if err := validateRichInputProviderAttempt(attempt); err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	run, found := s.runs[attempt.RunID]
	if !found || run.TaskID != attempt.TaskID {
		return RichInputProviderAttemptResult{}, fmt.Errorf("run %q not found", attempt.RunID)
	}
	if run.Status != "running" {
		return RichInputProviderAttemptResult{Run: run}, nil
	}
	if strings.TrimSpace(run.InputRef) == "" {
		return RichInputProviderAttemptResult{Run: run}, fmt.Errorf("%w: rich input reference is missing", ErrRichInputProviderRouteConflict)
	}
	updated, err := mergeRichInputProviderAttempt(run, attempt)
	if err != nil {
		return RichInputProviderAttemptResult{Run: run}, err
	}
	s.runs[updated.ID] = updated
	s.signalRun(updated.ID)
	return RichInputProviderAttemptResult{Run: updated, Applied: true}, nil
}

// RecordRichInputProviderAttempt serializes route admission with every other
// durable run mutation. SQLite's BEGIN IMMEDIATE setting acquires the writer
// lock before the read; Postgres adds FOR UPDATE in sqliteGetRunTx.
func (s *SQLiteStore) RecordRichInputProviderAttempt(ctx context.Context, attempt RichInputProviderAttempt) (RichInputProviderAttemptResult, error) {
	if err := validateRichInputProviderAttempt(attempt); err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	defer tx.Rollback()

	run, err := s.sqliteGetRunTx(ctx, tx, attempt.TaskID, attempt.RunID)
	if err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	if run.Status != "running" {
		return RichInputProviderAttemptResult{Run: run}, nil
	}
	if strings.TrimSpace(run.InputRef) == "" {
		return RichInputProviderAttemptResult{Run: run}, fmt.Errorf("%w: rich input reference is missing", ErrRichInputProviderRouteConflict)
	}
	updated, err := mergeRichInputProviderAttempt(run, attempt)
	if err != nil {
		return RichInputProviderAttemptResult{Run: run}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET payload = ?
		WHERE id = ? AND task_id = ? AND status = 'running'
	`, s.runsTable), string(payload), updated.ID, updated.TaskID)
	if err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	if affected == 0 {
		current, err := s.sqliteGetRunTx(ctx, tx, attempt.TaskID, attempt.RunID)
		if err != nil {
			return RichInputProviderAttemptResult{}, err
		}
		return RichInputProviderAttemptResult{Run: current}, nil
	}
	if err := tx.Commit(); err != nil {
		return RichInputProviderAttemptResult{}, err
	}
	s.signalRun(updated.ID)
	return RichInputProviderAttemptResult{Run: updated, Applied: true}, nil
}

func validateRichInputProviderAttempt(attempt RichInputProviderAttempt) error {
	if strings.TrimSpace(attempt.TaskID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(attempt.RunID) == "" {
		return fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(attempt.Provider) == "" {
		return fmt.Errorf("provider is required")
	}
	if !attempt.ProviderInstance.Valid() {
		return fmt.Errorf("provider instance is required")
	}
	return nil
}

func mergeRichInputProviderAttempt(run types.TaskRun, attempt RichInputProviderAttempt) (types.TaskRun, error) {
	// InputProviderInstance may be set at admission for an explicit provider,
	// before the governor rewrites a model and the router resolves the final
	// route. It still pins the provider generation from that point onward.
	if run.InputProviderInstance.Valid() && run.InputProviderInstance != attempt.ProviderInstance {
		return types.TaskRun{}, fmt.Errorf("%w: provider instance changed", ErrRichInputProviderRouteConflict)
	}
	if run.InputProviderDisclosedInstance.Valid() && run.InputProviderDisclosedInstance != attempt.ProviderInstance {
		return types.TaskRun{}, fmt.Errorf("%w: provider disclosure instance changed", ErrRichInputProviderRouteConflict)
	}
	provider := strings.TrimSpace(attempt.Provider)
	// TaskRun's initial provider, kind, and model are request hints, not the
	// final rich-input route. The separate dispatch marker makes the first
	// policy-rewritten route admissible while keeping all later attempts exact.
	hasPriorDispatch := run.InputProviderDispatchRecorded
	if existing := normalizedRichInputProvider(run.Provider); hasPriorDispatch && existing != "" && existing != provider {
		return types.TaskRun{}, fmt.Errorf("%w: provider changed from %q to %q", ErrRichInputProviderRouteConflict, existing, provider)
	}
	kind := strings.TrimSpace(attempt.ProviderKind)
	if existing := strings.TrimSpace(run.ProviderKind); hasPriorDispatch && existing != "" && kind != "" && existing != kind {
		return types.TaskRun{}, fmt.Errorf("%w: provider kind changed from %q to %q", ErrRichInputProviderRouteConflict, existing, kind)
	}
	model := strings.TrimSpace(attempt.Model)
	if existing := strings.TrimSpace(run.Model); hasPriorDispatch && existing != "" && model != "" && existing != model {
		return types.TaskRun{}, fmt.Errorf("%w: model changed from %q to %q", ErrRichInputProviderRouteConflict, existing, model)
	}

	updated := run
	updated.Provider = provider
	if kind != "" {
		updated.ProviderKind = kind
	}
	if model != "" {
		updated.Model = model
	}
	updated.InputProviderInstance = attempt.ProviderInstance
	updated.InputProviderDispatchRecorded = true
	return updated, nil
}

func normalizedRichInputProvider(provider string) string {
	provider = strings.TrimSpace(provider)
	if strings.EqualFold(provider, "auto") {
		return ""
	}
	return provider
}
