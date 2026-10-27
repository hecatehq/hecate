package taskstate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func (s *SQLiteStore) SubmitRunSteering(ctx context.Context, in RunSteeringSubmission) (RunSteeringResult, error) {
	if err := ValidateRunSteeringSubmission(in); err != nil {
		return RunSteeringResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunSteeringResult{}, err
	}
	defer tx.Rollback()
	task, err := s.sqliteGetTaskTx(ctx, tx, in.TaskID)
	if err != nil {
		return RunSteeringResult{}, err
	}
	run, err := s.sqliteGetRunTx(ctx, tx, in.TaskID, in.RunID)
	if err != nil {
		return RunSteeringResult{}, err
	}
	artifacts, err := s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: in.TaskID, RunID: in.RunID, Kind: RunSteeringArtifactKind}, "")
	if err != nil {
		return RunSteeringResult{}, err
	}
	artifact, replay, err := prepareRunSteering(task, run, artifacts, in)
	if err != nil {
		return RunSteeringResult{}, err
	}
	item, err := RunSteeringFromArtifact(artifact)
	if err != nil {
		return RunSteeringResult{}, err
	}
	if !replay {
		if err := s.sqliteUpsertSteeringArtifactTx(ctx, tx, artifact); err != nil {
			return RunSteeringResult{}, err
		}
		if _, err := s.sqliteInsertRunEventTx(ctx, tx, steeringEvent(artifact, runtimeevents.EventRunSteeringSubmitted, artifact.CreatedAt)); err != nil {
			return RunSteeringResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RunSteeringResult{}, err
	}
	s.signalRun(run.ID)
	return RunSteeringResult{RunSteering: item, Replay: replay}, nil
}

func (s *SQLiteStore) ApplyRunSteering(ctx context.Context, in RunSteeringApplication) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := s.sqliteGetTaskTx(ctx, tx, in.TaskID); err != nil {
		return false, err
	}
	run, err := s.sqliteGetRunTx(ctx, tx, in.TaskID, in.RunID)
	if err != nil {
		return false, err
	}
	artifacts, err := s.sqliteListArtifactsTx(ctx, tx, ArtifactFilter{TaskID: in.TaskID, RunID: in.RunID}, "")
	if err != nil {
		return false, err
	}
	var current *types.TaskArtifact
	for _, artifact := range artifacts {
		if artifact.ID == in.Conversation.ID {
			copy := artifact
			current = &copy
			break
		}
	}
	updates, err := prepareAppliedRunSteering(run, artifacts, current, in)
	if err != nil {
		return false, err
	}
	if err := s.sqliteUpsertSteeringArtifactTx(ctx, tx, in.Conversation); err != nil {
		return false, err
	}
	for _, artifact := range updates {
		if err := s.sqliteUpdateArtifactTx(ctx, tx, artifact); err != nil {
			return false, err
		}
		if _, err := s.sqliteInsertRunEventTx(ctx, tx, steeringEvent(artifact, runtimeevents.EventRunSteeringApplied, in.AppliedAt)); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.signalRun(run.ID)
	return true, nil
}

func (s *SQLiteStore) sqliteUpsertSteeringArtifactTx(ctx context.Context, tx storage.Tx, artifact types.TaskArtifact) error {
	payload, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, task_id, run_id, step_id, kind, status, created_at, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET status = excluded.status, payload = excluded.payload`, s.artifactsTable),
		artifact.ID, artifact.TaskID, artifact.RunID, artifact.StepID, artifact.Kind, artifact.Status, artifact.CreatedAt, string(payload))
	return err
}
