package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/providerapp"
)

var providerAppErrorMappings = []appErrorMapping{
	validationAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest),
	conflictAppErrorMapping(http.StatusConflict, errCodeInvalidRequest),
	{
		Match: func(err error) bool {
			return errors.Is(err, providerapp.ErrRuntimeNotConfigured)
		},
		Status: http.StatusBadRequest,
		Code:   errCodeInvalidRequest,
	},
}

func writeProviderAppError(w http.ResponseWriter, err error) {
	if errors.Is(err, providerapp.ErrControlPlaneNotConfigured) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	writeAppErrorWithFallback(w, err, providerAppErrorMappings, http.StatusInternalServerError, errCodeGatewayError)
}
