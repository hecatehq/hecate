package taskschedule

import (
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/robfig/cron/v3"
)

var (
	ErrKindRequired           = errors.New("schedule kind is required")
	ErrKindInvalid            = errors.New("schedule kind must be once or cron")
	ErrTimezoneRequired       = errors.New("schedule timezone is required")
	ErrTimezoneInvalid        = errors.New("schedule timezone is invalid")
	ErrRunAtRequired          = errors.New("run_at is required for a once schedule")
	ErrRunAtNotFuture         = errors.New("run_at must be in the future")
	ErrCronExpressionRequired = errors.New("cron_expression is required for a cron schedule")
	ErrCronExpressionInvalid  = errors.New("cron_expression must be a valid five-field cron expression")
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Normalize validates a schedule's operator-authored fields and derives its
// next occurrence. Returned timestamps are always UTC. A disabled schedule is
// valid but has no NextRunAt until it is enabled again.
func Normalize(schedule taskstate.TaskSchedule, now time.Time) (taskstate.TaskSchedule, error) {
	schedule.Kind = strings.TrimSpace(schedule.Kind)
	schedule.CronExpression = strings.TrimSpace(schedule.CronExpression)
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	schedule.RunAt = schedule.RunAt.UTC()
	schedule.NextRunAt = time.Time{}

	if schedule.Kind == "" {
		return taskstate.TaskSchedule{}, ErrKindRequired
	}
	if schedule.Kind != taskstate.TaskScheduleKindOnce && schedule.Kind != taskstate.TaskScheduleKindCron {
		return taskstate.TaskSchedule{}, ErrKindInvalid
	}
	if schedule.Timezone == "" {
		return taskstate.TaskSchedule{}, ErrTimezoneRequired
	}
	if schedule.Timezone == "Local" {
		return taskstate.TaskSchedule{}, fmt.Errorf("%w: %q", ErrTimezoneInvalid, schedule.Timezone)
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return taskstate.TaskSchedule{}, fmt.Errorf("%w: %q", ErrTimezoneInvalid, schedule.Timezone)
	}

	switch schedule.Kind {
	case taskstate.TaskScheduleKindOnce:
		if schedule.RunAt.IsZero() {
			return taskstate.TaskSchedule{}, ErrRunAtRequired
		}
		if !schedule.RunAt.After(now.UTC()) {
			return taskstate.TaskSchedule{}, ErrRunAtNotFuture
		}
		schedule.CronExpression = ""
		if schedule.Enabled {
			schedule.NextRunAt = schedule.RunAt
		}
	case taskstate.TaskScheduleKindCron:
		if schedule.CronExpression == "" {
			return taskstate.TaskSchedule{}, ErrCronExpressionRequired
		}
		parsed, err := cronParser.Parse(schedule.CronExpression)
		if err != nil {
			return taskstate.TaskSchedule{}, fmt.Errorf("%w: %v", ErrCronExpressionInvalid, err)
		}
		next, err := nextCronOccurrence(parsed, now, location)
		if err != nil {
			return taskstate.TaskSchedule{}, err
		}
		schedule.RunAt = time.Time{}
		if schedule.Enabled {
			schedule.NextRunAt = next
		}
	}

	return schedule, nil
}

// NextAfter returns the first occurrence strictly after after. The scheduler
// calls this with its current wall clock, rather than the just-fired time, so
// missed cron occurrences coalesce into one run after downtime.
func NextAfter(schedule taskstate.TaskSchedule, after time.Time) (time.Time, error) {
	if schedule.Kind == taskstate.TaskScheduleKindOnce {
		return time.Time{}, nil
	}
	if schedule.Kind != taskstate.TaskScheduleKindCron {
		return time.Time{}, ErrKindInvalid
	}
	timezone := strings.TrimSpace(schedule.Timezone)
	if timezone == "Local" {
		return time.Time{}, fmt.Errorf("%w: %q", ErrTimezoneInvalid, schedule.Timezone)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", ErrTimezoneInvalid, schedule.Timezone)
	}
	parsed, err := cronParser.Parse(strings.TrimSpace(schedule.CronExpression))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrCronExpressionInvalid, err)
	}
	return nextCronOccurrence(parsed, after, location)
}

func nextCronOccurrence(schedule cron.Schedule, after time.Time, location *time.Location) (time.Time, error) {
	next := schedule.Next(after.In(location))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: expression has no future occurrence", ErrCronExpressionInvalid)
	}
	return next.UTC(), nil
}
