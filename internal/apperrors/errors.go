package apperrors

import "errors"

type ValidationError struct {
	err error
}

func (e ValidationError) Error() string {
	return e.err.Error()
}

func (e ValidationError) Unwrap() error {
	return e.err
}

func Validation(err error) error {
	if err == nil {
		return nil
	}
	return ValidationError{err: err}
}

func IsValidationError(err error) bool {
	var validation ValidationError
	return errors.As(err, &validation)
}

type ConflictError struct {
	err error
}

func (e ConflictError) Error() string {
	return e.err.Error()
}

func (e ConflictError) Unwrap() error {
	return e.err
}

func Conflict(err error) error {
	if err == nil {
		return nil
	}
	return ConflictError{err: err}
}

func IsConflictError(err error) bool {
	var conflict ConflictError
	return errors.As(err, &conflict)
}
