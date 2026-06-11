package apperrors

import (
	"errors"
	"testing"
)

func TestValidationErrorWrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("bad request")
	err := Validation(cause)
	if !IsValidationError(err) || !errors.Is(err, cause) || err.Error() != "bad request" {
		t.Fatalf("Validation() = %v, IsValidation=%v, want wrapped validation cause", err, IsValidationError(err))
	}
	if Validation(nil) != nil {
		t.Fatal("Validation(nil) != nil")
	}
}

func TestConflictErrorWrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("duplicate")
	err := Conflict(cause)
	if !IsConflictError(err) || !errors.Is(err, cause) || err.Error() != "duplicate" {
		t.Fatalf("Conflict() = %v, IsConflict=%v, want wrapped conflict cause", err, IsConflictError(err))
	}
	if Conflict(nil) != nil {
		t.Fatal("Conflict(nil) != nil")
	}
}
