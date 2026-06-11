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

func writeChatAdmissionError(w http.ResponseWriter, err error) bool {
	var limitErr chatapp.MessageLimitError
	if errors.As(err, &limitErr) {
		switch limitErr.Code {
		case "turns":
			WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeSessionLimitExceeded, limitErr.Message, ErrorDetails{
				Fields: map[string]any{
					"limit":      limitErr.Limit,
					"turns_used": limitErr.TurnsUsed,
				},
			})
		case "duration":
			WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeSessionDurationLimit, limitErr.Message, ErrorDetails{
				Fields: map[string]any{
					"limit_ms":   limitErr.LimitMS,
					"started_at": formatOptionalTime(limitErr.StartedAt),
					"turns_used": limitErr.TurnsUsed,
				},
			})
		case "idle":
			WriteErrorDetails(w, http.StatusUnprocessableEntity, errCodeSessionIdleTimeout, limitErr.Message, ErrorDetails{
				Fields: map[string]any{
					"limit_ms":   limitErr.LimitMS,
					"updated_at": formatOptionalTime(limitErr.UpdatedAt),
					"turns_used": limitErr.TurnsUsed,
				},
			})
		default:
			WriteError(w, http.StatusUnprocessableEntity, errCodeInvalidRequest, limitErr.Message)
		}
		return true
	}
	switch {
	case errors.Is(err, chatapp.ErrContentRequired):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, chatapp.ErrExecutionModeInvalid):
		writeChatExecutionModeInvalid(w)
	case errors.Is(err, chatapp.ErrExternalCannotRunHecate), errors.Is(err, chatapp.ErrHecateCannotRunExternal):
		writeAgentChatRuntimeMismatch(w, err.Error())
	default:
		return false
	}
	return true
}
