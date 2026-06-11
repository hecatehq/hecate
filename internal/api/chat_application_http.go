package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/chatapp"
)

var chatAppErrorMappings = []appErrorMapping{
	{
		Match: func(err error) bool {
			return errors.Is(err, chatapp.ErrNoSettingsProvided) ||
				chatapp.IsValidationError(err)
		},
		Status: http.StatusBadRequest,
		Code:   errCodeInvalidRequest,
	},
	{
		Match: func(err error) bool {
			return errors.Is(err, chatapp.ErrExternalSessionOnly) ||
				errors.Is(err, chatapp.ErrHecateSessionOnly)
		},
		Status: http.StatusConflict,
		Code:   errCodeRuntimeMismatch,
	},
}

func writeChatAppError(w http.ResponseWriter, err error) bool {
	return writeAppError(w, err, chatAppErrorMappings)
}
