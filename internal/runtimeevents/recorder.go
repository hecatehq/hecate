package runtimeevents

import (
	"context"
	"fmt"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type SnapshotFunc func(ctx context.Context, taskID, runID string) (map[string]any, error)

type Option func(*Recorder)

type SnapshotMode int

const (
	SnapshotNone SnapshotMode = iota
	SnapshotRequired
	SnapshotBestEffort
)

type SnapshotPlacement int

const (
	// SnapshotOverridesData preserves the historical API append behavior:
	// caller data is written first, then recorder-owned snapshot fields win.
	SnapshotOverridesData SnapshotPlacement = iota
	// SnapshotProvidesBase preserves the historical orchestrator behavior:
	// snapshot fields are the base runtime state, then event-specific fields win.
	SnapshotProvidesBase
)

type Recorder struct {
	store    taskstate.Store
	snapshot SnapshotFunc
	now      func() time.Time
}

type Event struct {
	TaskID            string
	RunID             string
	EventType         string
	Data              map[string]any
	RequestID         string
	TraceID           string
	CreatedAt         time.Time
	SnapshotMode      SnapshotMode
	SnapshotPlacement SnapshotPlacement
}

func NewRecorder(store taskstate.Store, opts ...Option) Recorder {
	recorder := Recorder{
		store: store,
		now:   time.Now,
	}
	for _, opt := range opts {
		opt(&recorder)
	}
	return recorder
}

func WithSnapshot(snapshot SnapshotFunc) Option {
	return func(r *Recorder) {
		r.snapshot = snapshot
	}
}

func WithClock(now func() time.Time) Option {
	return func(r *Recorder) {
		if now != nil {
			r.now = now
		}
	}
}

func (r Recorder) Append(ctx context.Context, event Event) (types.TaskRunEvent, error) {
	if r.store == nil {
		return types.TaskRunEvent{}, fmt.Errorf("run event recorder store is not configured")
	}

	data := copyEventData(event.Data)
	if event.SnapshotMode != SnapshotNone {
		if r.snapshot == nil {
			if event.SnapshotMode != SnapshotBestEffort {
				return types.TaskRunEvent{}, fmt.Errorf("run event snapshot function is not configured")
			}
		} else {
			snapshot, err := r.snapshot(ctx, event.TaskID, event.RunID)
			if err != nil {
				if event.SnapshotMode != SnapshotBestEffort {
					return types.TaskRunEvent{}, err
				}
			} else if event.SnapshotPlacement == SnapshotProvidesBase {
				data = mergeEventData(snapshot, data)
			} else {
				data = mergeEventData(data, snapshot)
			}
		}
	}

	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = r.now()
	}
	return r.store.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    event.TaskID,
		RunID:     event.RunID,
		EventType: event.EventType,
		Data:      data,
		RequestID: event.RequestID,
		TraceID:   event.TraceID,
		CreatedAt: createdAt.UTC(),
	})
}

func mergeEventData(base, overlay map[string]any) map[string]any {
	out := copyEventData(base)
	if out == nil && len(overlay) > 0 {
		out = make(map[string]any, len(overlay))
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func copyEventData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	copied := make(map[string]any, len(data))
	for key, value := range data {
		copied[key] = value
	}
	return copied
}
