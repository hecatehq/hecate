package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/apperrors"
)

func TestValidationAppErrorMapping(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	ok := writeAppError(rec, apperrors.Validation(errors.New("bad input")), []appErrorMapping{
		validationAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest),
	})

	if !ok {
		t.Fatal("writeAppError(validation) = false, want true")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := decodeAppErrorType(t, rec.Body.Bytes()); got != errCodeInvalidRequest {
		t.Fatalf("error type = %q, want %q", got, errCodeInvalidRequest)
	}
}

func TestConflictAppErrorMapping(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	ok := writeAppError(rec, apperrors.Conflict(errors.New("already exists")), []appErrorMapping{
		conflictAppErrorMapping(http.StatusConflict, errCodeConflict),
	})

	if !ok {
		t.Fatal("writeAppError(conflict) = false, want true")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if got := decodeAppErrorType(t, rec.Body.Bytes()); got != errCodeConflict {
		t.Fatalf("error type = %q, want %q", got, errCodeConflict)
	}
}

func TestWriteAppErrorWithFallback(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeAppErrorWithFallback(rec, errors.New("boom"), nil, http.StatusInternalServerError, errCodeGatewayError)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeAppErrorType(t, rec.Body.Bytes()); got != errCodeGatewayError {
		t.Fatalf("error type = %q, want %q", got, errCodeGatewayError)
	}
}

func decodeAppErrorType(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return payload.Error.Type
}
