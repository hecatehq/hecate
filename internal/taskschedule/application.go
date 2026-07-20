package taskschedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/taskstate"
)

var (
	ErrStoreNotConfigured             = errors.New("task schedule store is not configured")
	ErrTaskStoreNotConfigured         = errors.New("task store is not configured")
	ErrTaskIDRequired                 = errors.New("task id is required")
	ErrTaskNotFound                   = errors.New("task not found")
	ErrChatOwnedTaskCannotBeScheduled = errors.New("chat-owned tasks cannot be scheduled")
	ErrScheduleNotFound               = errors.New("task schedule not found")
	ErrScheduleUpdateConflict         = errors.New("task schedule changed while updating; reload and try again")
	ErrOnceScheduleElapsed            = errors.New("once schedule fired or changed while updating; choose a future run_at")
)

const maxScheduleUpdateAttempts = 8

type Application struct {
	store taskstate.ScheduleStore
	tasks taskstate.Store
	idgen func(string) string
	now   func() time.Time
}

type Options struct {
	Store       taskstate.ScheduleStore
	Tasks       taskstate.Store
	IDGenerator func(string) string
	Now         func() time.Time
}

type UpsertCommand struct {
	TaskID         string
	Kind           string
	CronExpression string
	Timezone       string
	RunAt          time.Time
	Enabled        bool
}

func New(opts Options) *Application {
	app := &Application{store: opts.Store, tasks: opts.Tasks, idgen: opts.IDGenerator, now: opts.Now}
	if app.idgen == nil {
		app.idgen = newResourceID
	}
	if app.now == nil {
		app.now = func() time.Time { return time.Now().UTC() }
	}
	return app
}

func (app *Application) Upsert(ctx context.Context, cmd UpsertCommand) (taskstate.TaskSchedule, error) {
	if app == nil || app.store == nil {
		return taskstate.TaskSchedule{}, ErrStoreNotConfigured
	}
	if app.tasks == nil {
		return taskstate.TaskSchedule{}, ErrTaskStoreNotConfigured
	}
	taskID := strings.TrimSpace(cmd.TaskID)
	if taskID == "" {
		return taskstate.TaskSchedule{}, apperrors.Validation(ErrTaskIDRequired)
	}
	task, found, err := app.tasks.GetTask(ctx, taskID)
	if err != nil {
		return taskstate.TaskSchedule{}, err
	}
	if !found {
		return taskstate.TaskSchedule{}, ErrTaskNotFound
	}
	if strings.TrimSpace(task.OriginKind) == "chat" {
		return taskstate.TaskSchedule{}, apperrors.Validation(ErrChatOwnedTaskCannotBeScheduled)
	}

	contended := false
	for range maxScheduleUpdateAttempts {
		if err := ctx.Err(); err != nil {
			return taskstate.TaskSchedule{}, err
		}
		now := app.now().UTC()
		existing, found, err := app.store.GetTaskScheduleByTask(ctx, taskID)
		if err != nil {
			return taskstate.TaskSchedule{}, err
		}
		expectedRevision := int64(0)
		schedule := taskstate.TaskSchedule{TaskID: taskID, CreatedAt: now}
		if found {
			expectedRevision = existing.Revision
			schedule.ID = existing.ID
			schedule.CreatedAt = existing.CreatedAt
		} else {
			schedule.ID = app.idgen("schedule")
		}
		schedule.Kind = cmd.Kind
		schedule.CronExpression = cmd.CronExpression
		schedule.Timezone = cmd.Timezone
		schedule.RunAt = cmd.RunAt
		schedule.Enabled = cmd.Enabled
		schedule.UpdatedAt = now
		schedule, err = Normalize(schedule, now)
		if err != nil {
			if contended && errors.Is(err, ErrRunAtNotFuture) {
				return taskstate.TaskSchedule{}, apperrors.Conflict(ErrOnceScheduleElapsed)
			}
			return taskstate.TaskSchedule{}, apperrors.Validation(err)
		}
		stored, applied, err := app.store.CompareAndSwapTaskSchedule(ctx, taskstate.TaskScheduleCompareAndSwap{
			Schedule: schedule, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return taskstate.TaskSchedule{}, app.mapTaskDeletionRace(ctx, taskID, err)
		}
		if applied {
			return stored, nil
		}
		contended = true
	}
	return taskstate.TaskSchedule{}, app.mapTaskDeletionRace(
		ctx,
		taskID,
		apperrors.Conflict(ErrScheduleUpdateConflict),
	)
}

func (app *Application) mapTaskDeletionRace(ctx context.Context, taskID string, fallback error) error {
	_, found, err := app.tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !found {
		return ErrTaskNotFound
	}
	return fallback
}

func (app *Application) GetForTask(ctx context.Context, taskID string) (taskstate.TaskSchedule, error) {
	if app == nil || app.store == nil {
		return taskstate.TaskSchedule{}, ErrStoreNotConfigured
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return taskstate.TaskSchedule{}, apperrors.Validation(ErrTaskIDRequired)
	}
	schedule, found, err := app.store.GetTaskScheduleByTask(ctx, taskID)
	if err != nil {
		return taskstate.TaskSchedule{}, err
	}
	if !found {
		return taskstate.TaskSchedule{}, ErrScheduleNotFound
	}
	return schedule, nil
}

func (app *Application) List(ctx context.Context, filter taskstate.TaskScheduleFilter) ([]taskstate.TaskSchedule, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	return app.store.ListTaskSchedules(ctx, filter)
}

func (app *Application) DeleteForTask(ctx context.Context, taskID string) error {
	schedule, err := app.GetForTask(ctx, taskID)
	if err != nil {
		return err
	}
	return app.store.DeleteTaskSchedule(ctx, schedule.ID)
}

func (app *Application) ListOccurrencesForTask(ctx context.Context, taskID string, limit int) ([]taskstate.TaskScheduleOccurrence, error) {
	schedule, err := app.GetForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return app.store.ListTaskScheduleOccurrences(ctx, taskstate.TaskScheduleOccurrenceFilter{
		ScheduleID: schedule.ID,
		Limit:      limit,
	})
}

func newResourceID(prefix string) string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(entropy[:])
}
