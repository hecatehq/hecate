package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/providerapp"
)

var providerAppErrorMappings = []appErrorMapping{
	{
		Match: func(err error) bool {
			return errors.Is(err, providerapp.ErrRuntimeNotConfigured) ||
				providerapp.IsValidationError(err)
		},
		Status: http.StatusBadRequest,
		Code:   errCodeInvalidRequest,
	},
	{
		Match: func(err error) bool {
			return providerapp.IsConflictError(err)
		},
		Status: http.StatusConflict,
		Code:   errCodeInvalidRequest,
	},
}

func writeProviderAppError(w http.ResponseWriter, err error) {
	if errors.Is(err, providerapp.ErrControlPlaneNotConfigured) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if writeAppError(w, err, providerAppErrorMappings) {
		return
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
}
