package api

import (
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
		taskapp.ErrRunNotTurnRetryable,
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
	),
}

func writeTaskAppError(w http.ResponseWriter, err error) bool {
	return writeAppError(w, err, taskAppErrorMappings)
}
