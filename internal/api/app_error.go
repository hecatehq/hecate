package api

import (
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/apperrors"
)

type appErrorMapping struct {
	Match  func(error) bool
	Status int
	Code   string
}

func writeAppError(w http.ResponseWriter, err error, mappings []appErrorMapping) bool {
	for _, mapping := range mappings {
		if mapping.Match == nil || !mapping.Match(err) {
			continue
		}
		WriteError(w, mapping.Status, mapping.Code, err.Error())
		return true
	}
	return false
}

func writeAppErrorWithFallback(w http.ResponseWriter, err error, mappings []appErrorMapping, status int, code string) {
	if writeAppError(w, err, mappings) {
		return
	}
	WriteError(w, status, code, err.Error())
}

func validationAppErrorMapping(status int, code string) appErrorMapping {
	return appErrorMapping{
		Match:  apperrors.IsValidationError,
		Status: status,
		Code:   code,
	}
}

func conflictAppErrorMapping(status int, code string) appErrorMapping {
	return appErrorMapping{
		Match:  apperrors.IsConflictError,
		Status: status,
		Code:   code,
	}
}

func sentinelAppErrorMapping(status int, code string, targets ...error) appErrorMapping {
	return appErrorMapping{
		Match: func(err error) bool {
			for _, target := range targets {
				if errors.Is(err, target) {
					return true
				}
			}
			return false
		},
		Status: status,
		Code:   code,
	}
}
