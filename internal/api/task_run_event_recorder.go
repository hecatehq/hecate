package api

import (
	"context"

	"github.com/hecatehq/hecate/internal/runtimeevents"
)

func (h *Handler) taskRunEventRecorder() runtimeevents.Recorder {
	return runtimeevents.NewRecorder(h.taskStore, runtimeevents.WithSnapshot(h.taskRunStreamSnapshotData))
}

func (h *Handler) taskRunStreamSnapshotData(ctx context.Context, taskID, runID string) (map[string]any, error) {
	projector := newTaskRunStreamProjector(h.taskStore)
	state, err := projector.liveState(ctx, taskID, runID)
	if err != nil {
		return nil, err
	}
	snapshot, err := projector.snapshotEventData(state)
	if err != nil {
		return nil, err
	}
	return map[string]any{"snapshot": snapshot}, nil
}
