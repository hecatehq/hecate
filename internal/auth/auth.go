// Package auth is a single-user-mode shim. It exposes the same surface
// the handlers used to call against (Principal, Authenticator, etc.)
// but every method returns a fully-permitted anonymous principal. The
// gateway binds to 127.0.0.1 by default and a same-origin guard
// rejects cross-origin browser calls — that's the whole threat model.
//
// Kept as a thin shim instead of deleted because the API handlers
// thread `auth.Principal` through dozens of call sites; rewriting all
// of them at once would be a much larger blast radius. This file lets
// every existing import keep compiling while behaving as no-op.
package auth

import (
	"net/http"
)

// Principal represents the (anonymous) caller. Single-user mode treats
// every request as the operator; the fields are kept for compatibility
// with code that reads them but they're always empty.
type Principal struct {
	Role             string
	Name             string
	Tenant           string
	Source           string
	KeyID            string
	AllowedProviders []string
	AllowedModels    []string
}

// IsAdmin always returns true: there's only one user and they own
// everything.
func (p Principal) IsAdmin() bool { return true }

// IsAnonymous reports whether the principal carries no identity
// information. Always true in single-user mode.
func (p Principal) IsAnonymous() bool { return true }

// Anonymous is the canonical anonymous principal. Returned by every
// authenticator call.
var Anonymous = Principal{Role: "admin", Source: "anonymous"}

// Introspection mirrors the historical shape callers expected. Single-
// user mode always reports authenticated=true with the anonymous
// principal.
type Introspection struct {
	Authenticated bool
	InvalidToken  bool
	Principal     Principal
}

// Authenticator is a no-op in single-user mode.
type Authenticator struct{}

// NewAuthenticator returns an authenticator. Arguments are ignored —
// single-user mode has no token to validate. Variadic so existing
// callers that pass server-config + control-plane-store don't need
// to be updated.
func NewAuthenticator(_ ...any) *Authenticator {
	return &Authenticator{}
}

// Enabled always returns false in single-user mode — there's no auth
// to enable. UI surfaces use this to decide whether to show login
// flows.
func (a *Authenticator) Enabled() bool { return false }

// Introspect returns the anonymous principal regardless of the
// request's headers.
func (a *Authenticator) Introspect(_ *http.Request) Introspection {
	return Introspection{Authenticated: true, Principal: Anonymous}
}

// Authenticate matches the historical signature for callers that
// expect a (Principal, ok) tuple. Always succeeds.
func (a *Authenticator) Authenticate(_ *http.Request) (Principal, bool) {
	return Anonymous, true
}

// PrincipalFromRequest returns the anonymous principal regardless of
// any header state. Wired to the handlers that previously extracted a
// tenant key from the Authorization header.
func PrincipalFromRequest(_ *http.Request) Principal { return Anonymous }
