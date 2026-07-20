package taskschedule

import (
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
)

func TestNormalizeOnceSchedule(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	runAt := now.Add(90 * time.Minute)
	got, err := Normalize(taskstate.TaskSchedule{
		Kind:     taskstate.TaskScheduleKindOnce,
		Timezone: "Europe/Madrid",
		RunAt:    runAt,
		Enabled:  true,
	}, now)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if !got.NextRunAt.Equal(runAt) || got.CronExpression != "" {
		t.Fatalf("normalized once schedule = %+v, want next run_at and no cron expression", got)
	}
}

func TestNormalizeCronScheduleUsesIANAZone(t *testing.T) {
	t.Parallel()

	// 08:30 UTC is 10:30 in Madrid in July, so the next 09:00 local fire
	// is the following day at 07:00 UTC.
	now := time.Date(2026, time.July, 20, 8, 30, 0, 0, time.UTC)
	got, err := Normalize(taskstate.TaskSchedule{
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * *",
		Timezone:       "Europe/Madrid",
		Enabled:        true,
	}, now)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	want := time.Date(2026, time.July, 21, 7, 0, 0, 0, time.UTC)
	if !got.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %v, want %v", got.NextRunAt, want)
	}
}

func TestNormalizeRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		schedule taskstate.TaskSchedule
		want     error
	}{
		{name: "missing kind", schedule: taskstate.TaskSchedule{Timezone: "UTC"}, want: ErrKindRequired},
		{name: "invented kind", schedule: taskstate.TaskSchedule{Kind: "interval", Timezone: "UTC"}, want: ErrKindInvalid},
		{name: "missing timezone", schedule: taskstate.TaskSchedule{Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 9 * * *"}, want: ErrTimezoneRequired},
		{name: "invalid timezone", schedule: taskstate.TaskSchedule{Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 9 * * *", Timezone: "Mars/Olympus"}, want: ErrTimezoneInvalid},
		{name: "process local timezone", schedule: taskstate.TaskSchedule{Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 9 * * *", Timezone: "Local"}, want: ErrTimezoneInvalid},
		{name: "past once", schedule: taskstate.TaskSchedule{Kind: taskstate.TaskScheduleKindOnce, Timezone: "UTC", RunAt: now}, want: ErrRunAtNotFuture},
		{name: "cron seconds", schedule: taskstate.TaskSchedule{Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 0 9 * * *", Timezone: "UTC"}, want: ErrCronExpressionInvalid},
		{name: "cron without future occurrence while disabled", schedule: taskstate.TaskSchedule{Kind: taskstate.TaskScheduleKindCron, CronExpression: "0 0 31 2 *", Timezone: "UTC", Enabled: false}, want: ErrCronExpressionInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Normalize(tc.schedule, now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Normalize() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeDisabledCronCanBeReenabled(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	disabled, err := Normalize(taskstate.TaskSchedule{
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * *",
		Timezone:       "UTC",
		Enabled:        false,
	}, now)
	if err != nil {
		t.Fatalf("Normalize(disabled) error = %v", err)
	}
	if !disabled.NextRunAt.IsZero() {
		t.Fatalf("disabled next_run_at = %v, want zero", disabled.NextRunAt)
	}

	disabled.Enabled = true
	enabled, err := Normalize(disabled, now)
	if err != nil {
		t.Fatalf("Normalize(reenabled) error = %v", err)
	}
	want := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	if !enabled.NextRunAt.Equal(want) {
		t.Fatalf("reenabled next_run_at = %v, want %v", enabled.NextRunAt, want)
	}
}

func TestNextAfterCoalescesMissedCronOccurrences(t *testing.T) {
	t.Parallel()

	next, err := NextAfter(taskstate.TaskSchedule{
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "*/5 * * * *",
		Timezone:       "UTC",
	}, time.Date(2026, time.July, 20, 8, 17, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NextAfter() error = %v", err)
	}
	want := time.Date(2026, time.July, 20, 8, 20, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestNextAfterRejectsCronWithoutFutureOccurrence(t *testing.T) {
	t.Parallel()

	_, err := NextAfter(taskstate.TaskSchedule{
		Kind:           taskstate.TaskScheduleKindCron,
		CronExpression: "0 0 31 2 *",
		Timezone:       "UTC",
	}, time.Date(2026, time.July, 20, 8, 17, 0, 0, time.UTC))
	if !errors.Is(err, ErrCronExpressionInvalid) {
		t.Fatalf("NextAfter() error = %v, want ErrCronExpressionInvalid", err)
	}
}
