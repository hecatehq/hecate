package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/version"
)

func TestHandleHealthIncludesVersion(t *testing.T) {
	// Override the build-time variable for the duration of the test so we
	// don't depend on whatever main happens to embed (or "dev" if no
	// ldflag was passed). Restore on exit so this can't bleed into other
	// tests in the same binary run.
	prev := version.Version
	version.Version = "v0.42.0-test"
	t.Cleanup(func() { version.Version = prev })

	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	h.HandleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body["status"]; got != "ok" {
		t.Fatalf(`status field = %v, want "ok"`, got)
	}
	if got := body["version"]; got != "v0.42.0-test" {
		t.Fatalf(`version field = %v, want "v0.42.0-test" (ldflag-injected variable not surfaced)`, got)
	}
	if _, ok := body["time"]; !ok {
		t.Fatal("time field missing")
	}
}

func TestHandleHealthIncludesSandboxInfo(t *testing.T) {
	// /healthz must surface the active OS-isolation wrapper so operators
	// can confirm whether bwrap / sandbox-exec / none was detected at
	// startup without parsing logs. Schema is the same shape as
	// sandbox.WrapperHealthInfo.
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.HandleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	sandboxField, ok := body["sandbox"].(map[string]any)
	if !ok {
		t.Fatalf("sandbox field missing or wrong shape: %#v", body["sandbox"])
	}
	osIso, ok := sandboxField["os_isolation"].(map[string]any)
	if !ok {
		t.Fatalf("sandbox.os_isolation field missing or wrong shape: %#v", sandboxField["os_isolation"])
	}
	if _, ok := osIso["kind"]; !ok {
		t.Errorf("sandbox.os_isolation.kind missing: %#v", osIso)
	}
}

func TestHandleHealthDefaultVersionIsDev(t *testing.T) {
	// "dev" is what local source builds report; release builds override
	// it via -ldflags. If this assertion regresses, something replaced
	// the package-level default and downstream tooling that filters on
	// "dev" will break.
	if version.Version != "dev" {
		t.Skipf("version.Version = %q (ldflag injected); skipping default check", version.Version)
	}
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.HandleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got := body["version"]; got != "dev" {
		t.Fatalf(`/healthz version = %v, want "dev"`, got)
	}
}
