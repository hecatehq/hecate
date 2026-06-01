package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type taskRunStreamWriter struct {
	w       io.Writer
	flusher http.Flusher
}

func newTaskRunStreamWriter(w io.Writer, flusher http.Flusher) taskRunStreamWriter {
	return taskRunStreamWriter{w: w, flusher: flusher}
}

func taskRunStreamSnapshotPayload(state TaskRunStreamEventData) ([]byte, error) {
	return json.Marshal(TaskRunStreamEventResponse{
		Object: "task_run_stream_event",
		Data:   state,
	})
}

func taskRunStreamStateJSON(state TaskRunStreamEventData) ([]byte, error) {
	return json.Marshal(state)
}

func (s taskRunStreamWriter) writeSnapshotPayload(sequence int64, payload []byte) {
	fmt.Fprintf(s.w, "id: %d\nevent: snapshot\ndata: %s\n\n", sequence, payload)
}

func (s taskRunStreamWriter) writeDonePayload(sequence int64, payload []byte) {
	fmt.Fprintf(s.w, "id: %d\nevent: done\ndata: %s\n\n", sequence, payload)
}

func (s taskRunStreamWriter) writeError(err error) {
	fmt.Fprintf(s.w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
	s.flush()
}

func (s taskRunStreamWriter) writeKeepAlive() {
	fmt.Fprint(s.w, ": keep-alive\n\n")
	s.flush()
}

func (s taskRunStreamWriter) flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
