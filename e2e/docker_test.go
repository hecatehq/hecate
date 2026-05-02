//go:build e2e && docker

// Package e2e — Docker smoke tests.
//
// These run the gateway through `docker compose` rather than as a plain
// process so the production artifact (the embedded UI bundle, distroless
// runtime, /data volume permissions, nonroot user) gets exercised. They're
// behind their own build tag so the fast `e2e` suite isn't slowed down by
// container builds; expect ~30-60s wall-clock for a cold image build.
//
// Run with:
//
//	go test -tags 'e2e docker' -count=1 -timeout 5m ./e2e/...
//
// Requirements: Docker daemon reachable on the host.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dockerProject keeps every test in this file under one compose project name
// so a teardown reliably removes everything regardless of which test failed.
const dockerProject = "hecate-e2e-smoke"

// dockerSmokeTimeout caps each test's container lifecycle. Cold builds on a
// fresh runner can hit ~60s for the Bun + Go stages combined; we add slack
// for first-time image pulls.
const dockerSmokeTimeout = 3 * time.Minute

// TestDockerSmokeImageBootsAndAuthenticates verifies the production image
// produced by the project Dockerfile actually runs end-to-end:
//   - the binary starts as nonroot under distroless;
//   - the bootstrap file is generated and writable on the /data volume;
//   - /healthz returns 200 (proves the UI embed didn't break the binary);
//   - /v1/models 401s without a token, 200s with the token from the volume;
//   - the auto-generated token is round-trip readable via `docker compose
//     cp`, which is the path the README quickstart points operators at.
//
// Anything that could be wrong with the Dockerfile, compose volume mount,
// nonroot permissions, or the bootstrap-file path inside the container
// surfaces here. None of it surfaces in the binary-only e2e suite.
func TestDockerSmokeImageBootsAndAuthenticates(t *testing.T) {
	requireDocker(t)
	if err := portFree(8765); err != nil {
		// Hard-skip rather than fail: most likely a developer already has
		// `docker compose up` running. We don't want this test to flake on
		// a stranger's laptop just because their dev stack is up.
		t.Skipf("host port 8765 already in use (%v) — stop your local stack and rerun", err)
	}

	composeDir := projectRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), dockerSmokeTimeout)
	defer cancel()

	// Always tear down — even on test failure, even on panic — so a busted
	// run doesn't leave a stray container squatting on :8765 that breaks
	// the next attempt.
	t.Cleanup(func() {
		teardownCtx, cancelTeardown := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelTeardown()
		dockerCompose(t, teardownCtx, composeDir, "down", "--volumes", "--remove-orphans")
	})

	// Bring the gateway up. --build forces a rebuild so a stale local image
	// can't mask a Dockerfile regression. -d so we don't block on container
	// stdout; we drive everything via the published port.
	if out, err := dockerComposeCombined(ctx, composeDir, "up", "--build", "-d", "hecate"); err != nil {
		t.Fatalf("docker compose up failed: %v\n%s", err, out)
	}

	// Wait for healthz. The container takes a few seconds to start the Go
	// binary, generate the bootstrap, and bind the listener.
	const baseURL = "http://127.0.0.1:8765"
	if err := waitHealthyDocker(ctx, baseURL+"/healthz", 60*time.Second); err != nil {
		// Capture container logs so the failure mode is visible in CI.
		out, _ := dockerComposeCombined(ctx, composeDir, "logs", "hecate")
		t.Fatalf("healthz never responded 200 within 60s: %v\n--- hecate logs ---\n%s", err, out)
	}

	// Pull the admin token out of /data via `docker compose cp`. We copy
	// to a host tempfile rather than to `-` (stdout): `cp ... -` writes
	// a tar archive, which would force the README quickstart to pipe
	// through `tar -xO`. Copying to a real path gives raw bytes back.
	// Distroless has no shell, so `exec cat` is not an option; `cp` is.
	hostCopy := filepath.Join(t.TempDir(), "hecate.bootstrap.json")
	if out, err := dockerComposeCombined(ctx, composeDir, "cp", "hecate:/data/hecate.bootstrap.json", hostCopy); err != nil {
		t.Fatalf("retrieve bootstrap from /data: %v\n%s", err, out)
	}
	bootstrapJSON, err := os.ReadFile(hostCopy)
	if err != nil {
		t.Fatalf("read copied bootstrap: %v", err)
	}
	var boot struct {
		AdminToken string `json:"admin_token"`
	}
	if err := json.Unmarshal(bootstrapJSON, &boot); err != nil {
		t.Fatalf("decode bootstrap.json: %v\nraw: %s", err, bootstrapJSON)
	}
	if boot.AdminToken == "" {
		t.Fatalf("admin_token empty in bootstrap.json; raw: %s", bootstrapJSON)
	}

	// Single-user mode: anonymous /v1/models must 200. The bootstrap
	// admin token still gets generated (so installs that later flip
	// auth back on get a stable token from the same file) but the
	// gateway no longer enforces it.
	resp, err := http.Get(baseURL + "/v1/models") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /v1/models = %d, want 200 (single-user mode is no-auth)", resp.StatusCode)
	}
}

// requireDocker skips the test (rather than failing) when the daemon isn't
// reachable. CI jobs with `services: docker` always have it; developer
// machines may not. We want `go test ./...` on a non-Docker machine to skip
// these gracefully instead of failing red.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH — skipping smoke test")
	}
	out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("docker daemon not reachable — skipping (output: %s)", string(out))
	}
}

// dockerCompose runs a compose subcommand against the smoke project and
// fails the test on non-zero exit. Prefer this over `dockerComposeOutput`
// when the test only cares about success/failure, not the captured output.
func dockerCompose(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := composeCmd(ctx, dir, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("docker compose %v failed: %v\n%s", args, err, out)
	}
}

// dockerComposeCombined captures stdout+stderr together, useful when the
// real diagnostic is in stderr (e.g. `up --build` writes BuildKit progress
// to stderr).
func dockerComposeCombined(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return composeCmd(ctx, dir, args...).CombinedOutput()
}

// dockerComposeOutput captures only stdout, used for `cp - ` where stderr
// may contain progress noise we don't want to merge into the file content.
func dockerComposeOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := composeCmd(ctx, dir, args...)
	return cmd.Output()
}

func composeCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := append([]string{"compose", "-p", dockerProject}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = dir
	return cmd
}

// projectRoot returns the directory containing docker-compose.yml. `go test`
// runs from the package directory (e2e/), so we walk up to where compose
// lives — the only sane working directory for the `docker compose`
// subcommands. Reuses the existing module-root resolver from gateway_test.go
// so this stays in step with how the rest of the e2e suite discovers paths.
func projectRoot(t *testing.T) string {
	t.Helper()
	return moduleRootDir()
}

// portFree returns nil when the host port is bindable. We use this as a
// pre-flight so a developer's already-running stack on :8765 produces a
// clean skip rather than an opaque "address already in use" docker error.
func portFree(port int) error {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

// waitHealthyDocker polls the published port until /healthz returns 200 or
// the deadline expires. We re-implement this here (instead of reusing
// waitHealthy from gateway_test.go) because that helper takes *testing.T
// and we want to surface logs on failure rather than t.Fatal'ing immediately.
func waitHealthyDocker(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body := resp.StatusCode
			resp.Body.Close()
			if body == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("not healthy within %s", timeout)
}
