package cloudruntime

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const (
	HeaderActorID       = "X-Hecate-Cloud-Actor-ID"
	HeaderOrgID         = "X-Hecate-Cloud-Org-ID"
	HeaderProjectID     = "X-Hecate-Cloud-Project-ID"
	HeaderRuntimeID     = "X-Hecate-Cloud-Runtime-ID"
	HeaderRuntimeSecret = "X-Hecate-Cloud-Runtime-Secret"
)

var ErrMissingIdentity = errors.New("missing cloud runtime identity")

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
	if identity.ActorID == "" || identity.OrgID == "" || identity.ProjectID == "" || identity.RuntimeID == "" {
		return Identity{}, ErrMissingIdentity
	}
	return identity, nil
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
