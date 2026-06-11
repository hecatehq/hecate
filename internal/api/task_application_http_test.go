package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteTaskAppError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{
			name:    "store_not_configured",
			err:     errTaskStoreNotConfigured,
			status:  http.StatusBadRequest,
			code:    errCodeInvalidRequest,
			message: "task store is not configured",
		},
		{
			name:    "validation",
			err:     taskValidation(errTaskIDRequired),
			status:  http.StatusBadRequest,
			code:    errCodeInvalidRequest,
			message: "task id is required",
		},
		{
			name:    "not_found",
			err:     errTaskRunNotFound,
			status:  http.StatusNotFound,
			code:    errCodeNotFound,
			message: "task run not found",
		},
		{
			name:    "conflict",
			err:     errTaskHasOtherActiveRun,
			status:  http.StatusConflict,
			code:    errCodeInvalidRequest,
			message: "task already has another active run",
		},
		{
			name:    "turn_retry_nonterminal",
			err:     errTaskRunNotTurnRetryable,
			status:  http.StatusBadRequest,
			code:    errCodeInvalidRequest,
			message: "run is not retryable from a turn (must be terminal)",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			if !writeTaskAppError(rec, tc.err) {
				t.Fatalf("writeTaskAppError(%v) = false, want true", tc.err)
			}
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.status, rec.Body.String())
			}
			payload := decodeRecorder[struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}](t, rec)
			if payload.Error.Type != tc.code || payload.Error.Message != tc.message {
				t.Fatalf("error = %#v, want type=%q message=%q", payload.Error, tc.code, tc.message)
			}
		})
	}
}

func TestWriteTaskAppErrorUnknown(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	if writeTaskAppError(rec, errors.New("boom")) {
		t.Fatal("writeTaskAppError(unknown) = true, want false")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty for unmapped error", rec.Body.String())
	}
}
