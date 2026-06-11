package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/taskapp"
)

var taskAppErrorMappings = []appErrorMapping{
	{
		Match: func(err error) bool {
			return errors.Is(err, taskapp.ErrStoreNotConfigured) ||
				errors.Is(err, taskapp.ErrRunnerNotConfigured) ||
				errors.Is(err, taskapp.ErrProjectStoreNotConfigured) ||
				errors.Is(err, taskapp.ErrProjectNotFound) ||
				errors.Is(err, taskapp.ErrPromptRequired) ||
				errors.Is(err, taskapp.ErrRunNotTurnRetryable) ||
				errors.Is(err, taskapp.ErrBudgetLower) ||
				taskapp.IsValidationError(err)
		},
		Status: http.StatusBadRequest,
		Code:   errCodeInvalidRequest,
	},
	{
		Match: func(err error) bool {
			return errors.Is(err, taskapp.ErrTaskNotFound) ||
				errors.Is(err, taskapp.ErrRunNotFound) ||
				errors.Is(err, taskapp.ErrApprovalNotFound)
		},
		Status: http.StatusNotFound,
		Code:   errCodeNotFound,
	},
	{
		Match: func(err error) bool {
			return errors.Is(err, taskapp.ErrActiveRun) ||
				errors.Is(err, taskapp.ErrOtherActiveRun) ||
				errors.Is(err, taskapp.ErrDeleteActiveRun) ||
				errors.Is(err, taskapp.ErrRunNotRetryable) ||
				errors.Is(err, taskapp.ErrRunNotResumable)
		},
		Status: http.StatusConflict,
		Code:   errCodeInvalidRequest,
	},
}

func writeTaskAppError(w http.ResponseWriter, err error) bool {
	return writeAppError(w, err, taskAppErrorMappings)
}
