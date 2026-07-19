package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/taskapp"
)

var taskAppErrorMappings = []appErrorMapping{
	validationAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest),
	sentinelAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest,
		taskapp.ErrStoreNotConfigured,
		taskapp.ErrRunnerNotConfigured,
		taskapp.ErrProjectStoreNotConfigured,
		taskapp.ErrProjectNotFound,
		taskapp.ErrPromptRequired,
		taskapp.ErrRunNotModelCallRetryable,
		taskapp.ErrBudgetLower,
	),
	sentinelAppErrorMapping(http.StatusNotFound, errCodeNotFound,
		taskapp.ErrTaskNotFound,
		taskapp.ErrRunNotFound,
		taskapp.ErrApprovalNotFound,
	),
	sentinelAppErrorMapping(http.StatusConflict, errCodeInvalidRequest,
		taskapp.ErrActiveRun,
		taskapp.ErrOtherActiveRun,
		taskapp.ErrDeleteActiveRun,
		taskapp.ErrRunNotRetryable,
		taskapp.ErrRunNotResumable,
		taskapp.ErrOriginRunAdmissionClosed,
		taskapp.ErrOriginUnavailable,
	),
}

func writeTaskAppError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, taskapp.ErrOriginValidationFailed) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, taskapp.ErrOriginValidationFailed.Error())
		return true
	}
	return writeAppError(w, err, taskAppErrorMappings)
}
