package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/providerapp"
)

var providerAppErrorMappings = []appErrorMapping{
	validationAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest),
	conflictAppErrorMapping(http.StatusConflict, errCodeInvalidRequest),
	sentinelAppErrorMapping(http.StatusBadRequest, errCodeInvalidRequest, providerapp.ErrRuntimeNotConfigured),
}

func writeProviderAppError(w http.ResponseWriter, err error) {
	if errors.Is(err, providerapp.ErrControlPlaneNotConfigured) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	writeAppErrorWithFallback(w, err, providerAppErrorMappings, http.StatusInternalServerError, errCodeGatewayError)
}
