package remoteruntime

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestFromHeadersRequiresCompleteIdentity(t *testing.T) {
	header := http.Header{}
	header.Set(HeaderActorID, "actor_1")
	header.Set(HeaderOrgID, "org_1")
	header.Set(HeaderProjectID, "proj_1")
	header.Set(HeaderRuntimeID, "rt_1")

	identity, err := FromHeaders(header)
	if err != nil {
		t.Fatalf("FromHeaders() error = %v, want nil", err)
	}
	if identity.ActorID != "actor_1" || identity.OrgID != "org_1" || identity.ProjectID != "proj_1" || identity.RuntimeID != "rt_1" {
		t.Fatalf("identity = %+v", identity)
	}

	header.Del(HeaderProjectID)
	if _, err := FromHeaders(header); !errors.Is(err, ErrMissingIdentity) {
		t.Fatalf("FromHeaders() error = %v, want ErrMissingIdentity", err)
	}
}

func TestActorForAuditPrefersCloudActor(t *testing.T) {
	ctx := WithIdentity(context.Background(), Identity{ActorID: "actor_1"})

	if got := ActorForAudit(ctx, "operator:req_1"); got != "cloud:actor_1" {
		t.Fatalf("ActorForAudit() = %q, want cloud actor", got)
	}
	if got := ActorForAudit(context.Background(), "operator:req_1"); got != "operator:req_1" {
		t.Fatalf("ActorForAudit() fallback = %q", got)
	}
}
