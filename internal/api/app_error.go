package api

import "net/http"

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
