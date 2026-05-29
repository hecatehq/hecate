package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestTaskRunStreamWriter_WritesSnapshotAndDoneFrames(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := newTaskRunStreamWriter(&buf, nil)
	payload, err := taskRunStreamSnapshotPayload(TaskRunStreamEventData{
		Sequence:  42,
		Terminal:  true,
		EventType: "run.completed",
		Run: renderTaskRun(types.TaskRun{
			ID:     "run-writer",
			TaskID: "task-writer",
			Status: "completed",
		}),
	})
	if err != nil {
		t.Fatalf("taskRunStreamSnapshotPayload() error = %v", err)
	}

	writer.writeSnapshotPayload(42, payload)
	writer.writeDonePayload(42, payload)

	got := buf.String()
	if strings.Count(got, "id: 42\n") != 2 {
		t.Fatalf("stream ids = %q, want two id: 42 frames", got)
	}
	if !strings.Contains(got, "event: snapshot\n") {
		t.Fatalf("stream = %q, want snapshot frame", got)
	}
	if !strings.Contains(got, "event: done\n") {
		t.Fatalf("stream = %q, want done frame", got)
	}
	if !strings.Contains(got, `"object":"task_run_stream_event"`) {
		t.Fatalf("stream = %q, want task_run_stream_event payload", got)
	}
}

func TestTaskRunStreamWriter_WritesErrorAndKeepAlive(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := newTaskRunStreamWriter(&buf, nil)

	writer.writeError(assertErr("projection failed"))
	writer.writeKeepAlive()

	got := buf.String()
	if !strings.Contains(got, "event: error\n") {
		t.Fatalf("stream = %q, want error frame", got)
	}
	if !strings.Contains(got, `"projection failed"`) {
		t.Fatalf("stream = %q, want error message", got)
	}
	if !strings.Contains(got, ": keep-alive\n\n") {
		t.Fatalf("stream = %q, want keep-alive comment", got)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}
