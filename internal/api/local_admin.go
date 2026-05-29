package api

import "net/http"

func requireLoopbackClient(w http.ResponseWriter, r *http.Request, action string) bool {
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		WriteError(w, http.StatusForbidden, errCodeForbidden, action+" is only available to local loopback clients")
		return false
	}
	if hasForwardedClientHeaders(r) {
		WriteError(w, http.StatusForbidden, errCodeForbidden, action+" rejects forwarded client headers")
		return false
	}
	return true
}
