package api

import (
	"errors"
	"net/http"
)

func writeTaskAppError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errTaskStoreNotConfigured),
		errors.Is(err, errTaskRunnerNotConfigured),
		errors.Is(err, errTaskProjectStoreNotConfigured),
		errors.Is(err, errTaskProjectNotFound),
		errors.Is(err, errTaskPromptRequired),
		errors.Is(err, errTaskRunNotTurnRetryable),
		errors.Is(err, errTaskBudgetLower),
		isTaskValidationError(err):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, errTaskNotFound),
		errors.Is(err, errTaskRunNotFound),
		errors.Is(err, errTaskApprovalNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, err.Error())
	case errors.Is(err, errTaskHasActiveRun),
		errors.Is(err, errTaskHasOtherActiveRun),
		errors.Is(err, errTaskDeleteActiveRun),
		errors.Is(err, errTaskRunNotRetryable),
		errors.Is(err, errTaskRunNotResumable):
		WriteError(w, http.StatusConflict, errCodeInvalidRequest, err.Error())
	default:
		return false
	}
	return true
}
