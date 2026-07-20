package api

import (
	"net/http"

	"github.com/hecatehq/hecate/internal/taskschedule"
)

var taskScheduleAppErrorMappings = []appErrorMapping{
	validationAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest),
	conflictAppErrorMapping(http.StatusConflict, errCodeConflict),
	sentinelAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest,
		taskschedule.ErrStoreNotConfigured,
		taskschedule.ErrTaskStoreNotConfigured,
	),
	sentinelAppErrorMapping(http.StatusNotFound, errCodeNotFound,
		taskschedule.ErrTaskNotFound,
		taskschedule.ErrScheduleNotFound,
	),
}

func writeTaskScheduleAppError(w http.ResponseWriter, err error) bool {
	return writeAppError(w, err, taskScheduleAppErrorMappings)
}
