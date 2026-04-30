package api

import (
	"net"
	"net/http"
	"net/url"
)

// HandleBootstrapToken hands the auto-generated admin bearer token to a
// loopback caller so the local-machine UI can skip the manual paste step
// on first boot. Refuses for any other source:
//
//   - non-loopback connection peer (X-Forwarded-For is ignored — the
//     check reads the raw RemoteAddr, so a reverse proxy can't trick us
//     into handing the token to a remote browser),
//   - cross-origin request (Origin header doesn't match the request's
//     Host),
//   - operator-supplied token (GATEWAY_AUTH_TOKEN was set at boot —
//     the gateway doesn't hand out tokens it doesn't own; that secret
//     is the operator's, and the bootstrap-token surface stays sealed).
//
// The response is JSON: {"object":"bootstrap_token","data":{"token":"…"}}.
// Refusals return 403 with the standard error envelope and a brief
// reason so the UI's TokenGate can fall back to its paste flow.
func (h *Handler) HandleBootstrapToken(w http.ResponseWriter, r *http.Request) {
	if !h.bootstrapTokenExposable {
		WriteError(w, http.StatusForbidden, errCodeUnauthorized, "bootstrap-token surface is disabled (operator-supplied admin token)")
		return
	}
	if !isLoopbackRequest(r) {
		WriteError(w, http.StatusForbidden, errCodeUnauthorized, "bootstrap-token is only available to loopback callers")
		return
	}
	if !sameOriginRequest(r) {
		WriteError(w, http.StatusForbidden, errCodeUnauthorized, "bootstrap-token rejects cross-origin requests")
		return
	}
	token := h.config.Server.AuthToken
	if token == "" {
		WriteError(w, http.StatusForbidden, errCodeUnauthorized, "bootstrap-token has nothing to hand out (no admin token configured)")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "bootstrap_token",
		"data":   map[string]string{"token": token},
	})
}

// isLoopbackRequest reads the raw connection peer (no X-Forwarded-For
// trust) and reports whether it sits on the loopback interface. Empty
// or unparseable RemoteAddr fails closed.
func isLoopbackRequest(r *http.Request) bool {
	if r == nil || r.RemoteAddr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// sameOriginRequest confirms that when the browser supplied an Origin
// header, it's safe to honor on a loopback request. The loopback peer
// check (isLoopbackRequest) is the primary boundary; this is the
// browser-side defense. Requests without an Origin header (curl,
// server-to-server) pass.
//
// We accept the Origin when either:
//   - Its host matches the request's Host exactly (production: the
//     embedded UI is served by the gateway, so same-origin trivially).
//   - The Origin's hostname resolves to a loopback address (dev: a
//     Vite dev server on http://localhost:5173 proxies to the gateway
//     at http://127.0.0.1:8765, so Host and Origin disagree but both
//     ends sit on the loopback interface — and the loopback peer
//     check above already gates entry).
//
// The widened second clause is dev-server friendly without weakening
// the guard: a remote attacker who could spoof an Origin header still
// has to come from a loopback peer to reach this handler at all.
func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	hostname := u.Hostname()
	if hostname == "" {
		return false
	}
	if hostname == "localhost" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
