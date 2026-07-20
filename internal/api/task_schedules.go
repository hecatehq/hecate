package api

// PutTaskScheduleRequest is the complete replacement body for a Task's one
// schedule. Enabled is a pointer so the API can distinguish an explicit false
// value from an omitted field.
type PutTaskScheduleRequest struct {
	Kind           string `json:"kind"`
	CronExpression string `json:"cron_expression,omitempty"`
	Timezone       string `json:"timezone"`
	RunAt          string `json:"run_at,omitempty"`
	Enabled        *bool  `json:"enabled"`
}

type TaskScheduleResponse struct {
	Object string           `json:"object"`
	Data   TaskScheduleItem `json:"data"`
}

type TaskSchedulesResponse struct {
	Object string             `json:"object"`
	Data   []TaskScheduleItem `json:"data"`
}

type TaskScheduleOccurrencesResponse struct {
	Object string                       `json:"object"`
	Data   []TaskScheduleOccurrenceItem `json:"data"`
}

type TaskScheduleItem struct {
	ID             string `json:"id"`
	TaskID         string `json:"task_id"`
	Kind           string `json:"kind"`
	CronExpression string `json:"cron_expression,omitempty"`
	Timezone       string `json:"timezone"`
	RunAt          string `json:"run_at,omitempty"`
	Enabled        bool   `json:"enabled"`
	NextRunAt      string `json:"next_run_at,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type TaskScheduleOccurrenceItem struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	ScheduleID   string `json:"schedule_id"`
	ScheduledFor string `json:"scheduled_for"`
	Status       string `json:"status"`
	ClaimedAt    string `json:"claimed_at"`
	RunID        string `json:"run_id,omitempty"`
	Error        string `json:"error,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}
