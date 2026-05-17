package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Gated-repo tests: HuggingFace authorization wiring on the
// download request. The installer's existing 401/403 → ErrorKindGated
// path covers gated-without-token; this set covers the
// gated-with-token success path.

// authRecorder records the Authorization header seen by the upstream
// across calls. Used to assert the installer forwarded the operator's
// HF token correctly.
type authRecorder struct {
	mu      sync.Mutex
	headers []string
}

func (r *authRecorder) record(h string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = append(r.headers, h)
}

func (r *authRecorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.headers))
	copy(out, r.headers)
	return out
}

func startAuthGatedGGUFSource(t *testing.T, body []byte, requireToken string) (*httptest.Server, *authRecorder) {
	t.Helper()
	rec := &authRecorder{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		rec.record(auth)
		if auth != "Bearer "+requireToken {
			http.Error(w, "gated", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	return srv, rec
}

func TestInstaller_GatedRepoSucceedsWithToken(t *testing.T) {
	t.Parallel()

	body := []byte("gated-bytes")
	digest := sha256.Sum256(body)
	expectedSHA := hex.EncodeToString(digest[:])

	srv, rec := startAuthGatedGGUFSource(t, body, "hf_test_token_123")
	defer srv.Close()

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:                  srv.Client(),
		Clock:                 fixedClock(time.Unix(1700000000, 0).UTC()),
		ProgressIntervalBytes: 1,
	})
	handle, err := inst.Install(context.Background(), InstallSpec{
		URL:     srv.URL + "/test/resolve/main/gated.gguf",
		SHA256:  expectedSHA,
		HFToken: "hf_test_token_123",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	events := drainEvents(t, handle)
	final := lastEvent(events)
	if final.Kind != ProgressCompleted {
		t.Fatalf("final = %+v; want completed", final)
	}
	// Authorization header must have been sent on the GET. With
	// only one upstream call expected; assert exactly one + the
	// header value.
	got := rec.seen()
	if len(got) != 1 {
		t.Fatalf("upstream calls = %d; want 1", len(got))
	}
	if got[0] != "Bearer hf_test_token_123" {
		t.Fatalf("Authorization header = %q; want Bearer hf_test_token_123", got[0])
	}
}

func TestInstaller_GatedRepoFailsWhenTokenAbsent(t *testing.T) {
	t.Parallel()

	srv, _ := startAuthGatedGGUFSource(t, []byte("never reached"), "secret-token")
	defer srv.Close()

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})
	handle, err := inst.Install(context.Background(), InstallSpec{
		URL: srv.URL + "/test/resolve/main/gated.gguf",
		// HFToken intentionally empty.
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	events := drainEvents(t, handle)
	final := lastEvent(events)
	if final.Kind != ProgressFailed {
		t.Fatalf("final = %+v; want failed", final)
	}
	if final.ErrorKind != ErrorKindGated {
		t.Fatalf("error kind = %q; want %q", final.ErrorKind, ErrorKindGated)
	}
}

func TestInstaller_TokenFromEnvFallback(t *testing.T) {
	// t.Setenv pins the env var for the test only; t.Parallel
	// would race against sibling tests reading the same var, so
	// these env-mutating tests run serially.

	body := []byte("env-token-body")
	digest := sha256.Sum256(body)
	hex256 := hex.EncodeToString(digest[:])

	srv, rec := startAuthGatedGGUFSource(t, body, "env-supplied-token")
	defer srv.Close()

	// HUGGINGFACE_TOKEN env-var fallback fires when InstallSpec
	// doesn't carry a token.
	t.Setenv("HUGGINGFACE_TOKEN", "env-supplied-token")

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})
	handle, err := inst.Install(context.Background(), InstallSpec{
		URL:    srv.URL + "/test/resolve/main/env-token.gguf",
		SHA256: hex256,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	events := drainEvents(t, handle)
	final := lastEvent(events)
	if final.Kind != ProgressCompleted {
		t.Fatalf("final = %+v; want completed (env token should have authed)", final)
	}
	got := rec.seen()
	if len(got) != 1 || got[0] != "Bearer env-supplied-token" {
		t.Fatalf("Authorization = %v; want one Bearer env-supplied-token", got)
	}
}

func TestInstaller_SpecTokenOverridesEnv(t *testing.T) {
	// Serial — see TestInstaller_TokenFromEnvFallback.

	body := []byte("override-body")
	digest := sha256.Sum256(body)
	hex256 := hex.EncodeToString(digest[:])

	srv, rec := startAuthGatedGGUFSource(t, body, "spec-token-wins")
	defer srv.Close()

	t.Setenv("HUGGINGFACE_TOKEN", "env-token-loses")

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})
	handle, _ := inst.Install(context.Background(), InstallSpec{
		URL:     srv.URL + "/test/resolve/main/override.gguf",
		SHA256:  hex256,
		HFToken: "spec-token-wins",
	})
	events := drainEvents(t, handle)
	if lastEvent(events).Kind != ProgressCompleted {
		t.Fatalf("expected completed, got %+v", lastEvent(events))
	}
	got := rec.seen()
	if got[0] != "Bearer spec-token-wins" {
		t.Fatalf("Authorization = %q; spec-token should have overridden env", got[0])
	}
}

func TestInstaller_NoTokenSentForPublicURL(t *testing.T) {
	// Serial — uses t.Setenv to clear the env var.
	// Public (non-gated) URL: when no token is set the request
	// should NOT carry an Authorization header at all. Some HF
	// CDNs treat the presence of an Authorization header as a
	// signal to enforce auth; an empty/garbage header would
	// regress public installs.

	body := []byte("public")
	digest := sha256.Sum256(body)
	hex256 := hex.EncodeToString(digest[:])

	var seenAuth []string
	var mu sync.Mutex
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	// Make sure the env doesn't bleed in.
	t.Setenv("HUGGINGFACE_TOKEN", "")

	inst, _, _ := makeInstallerWithCatalog(t, NewCatalog(), InstallerOptions{
		HTTP:  srv.Client(),
		Clock: fixedClock(time.Unix(1700000000, 0).UTC()),
	})
	handle, _ := inst.Install(context.Background(), InstallSpec{
		URL:    srv.URL + "/foo/resolve/main/public.gguf",
		SHA256: hex256,
	})
	events := drainEvents(t, handle)
	if lastEvent(events).Kind != ProgressCompleted {
		t.Fatalf("expected completed, got %+v", lastEvent(events))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seenAuth) != 1 || seenAuth[0] != "" {
		t.Fatalf("Authorization = %v; want empty (no header)", seenAuth)
	}
}
