package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/taskschedule"
)

func TestTaskScheduleUpdateConflictMapsToHTTP409(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  error
	}{
		{name: "exhausted CAS", err: taskschedule.ErrScheduleUpdateConflict},
		{name: "elapsed once after lost CAS", err: taskschedule.ErrOnceScheduleElapsed},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			if !writeTaskScheduleAppError(recorder, apperrors.Conflict(testCase.err)) {
				t.Fatal("writeTaskScheduleAppError() = false, want mapped conflict")
			}
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", recorder.Code)
			}
			if got := decodeAppErrorType(t, recorder.Body.Bytes()); got != errCodeConflict {
				t.Fatalf("error type = %q, want %q", got, errCodeConflict)
			}
		})
	}
}
