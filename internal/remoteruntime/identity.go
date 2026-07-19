package remoteruntime

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const (
	HeaderActorID       = "X-Hecate-Remote-Actor-ID"
	HeaderOrgID         = "X-Hecate-Remote-Org-ID"
	HeaderProjectID     = "X-Hecate-Remote-Project-ID"
	HeaderRuntimeID     = "X-Hecate-Remote-Runtime-ID"
	HeaderRuntimeSecret = "X-Hecate-Remote-Runtime-Secret"
)

var ErrMissingIdentity = errors.New("missing remote runtime identity")

type Identity struct {
	ActorID   string
	OrgID     string
	ProjectID string
	RuntimeID string
}

func FromHeaders(header http.Header) (Identity, error) {
	identity := Identity{
		ActorID:   strings.TrimSpace(header.Get(HeaderActorID)),
		OrgID:     strings.TrimSpace(header.Get(HeaderOrgID)),
		ProjectID: strings.TrimSpace(header.Get(HeaderProjectID)),
		RuntimeID: strings.TrimSpace(header.Get(HeaderRuntimeID)),
	}
	// ProjectID is optional because "No project" is a valid operator scope.
	// Actor, organization, and runtime identity still form the trusted remote
	// boundary for every request.
	if identity.ActorID == "" || identity.OrgID == "" || identity.RuntimeID == "" {
		return Identity{}, ErrMissingIdentity
	}
	return identity, nil
}

// HeadersPresent reports whether a request is attempting to enter through the
// trusted remote-runtime boundary. Local requests carry none of these headers.
func HeadersPresent(header http.Header) bool {
	for _, name := range []string{
		HeaderActorID,
		HeaderOrgID,
		HeaderProjectID,
		HeaderRuntimeID,
		HeaderRuntimeSecret,
	} {
		if strings.TrimSpace(header.Get(name)) != "" {
			return true
		}
	}
	return false
}

type contextKey struct{}

func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, identity)
}

func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	if !ok {
		return Identity{}, false
	}
	return identity, true
}

func ActorForAudit(ctx context.Context, fallback string) string {
	if identity, ok := FromContext(ctx); ok && strings.TrimSpace(identity.ActorID) != "" {
		return "cloud:" + strings.TrimSpace(identity.ActorID)
	}
	return fallback
}
