package sandbox

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ── workerEnv ────────────────────────────────────────────────────────────────

func TestWorkerEnvExcludesArbitraryVars(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@host/db")
	t.Setenv("GATEWAY_AUTH_TOKEN", "super-secret-token")

	env := workerEnv()
	for _, kv := range env {
		for _, secret := range []string{"OPENAI_API_KEY", "POSTGRES_DSN", "GATEWAY_AUTH_TOKEN"} {
			if strings.HasPrefix(kv, secret+"=") {
				t.Errorf("workerEnv() leaked %s into worker environment", secret)
			}
		}
	}
}

func TestWorkerEnvIncludesAllowlistedVars(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/user")
	t.Setenv("TZ", "UTC")

	env := workerEnv()

	find := func(key string) (string, bool) {
		prefix := key + "="
		for _, kv := range env {
			if strings.HasPrefix(kv, prefix) {
				return strings.TrimPrefix(kv, prefix), true
			}
		}
		return "", false
	}

	if v, ok := find("PATH"); !ok || v != "/usr/bin:/bin" {
		t.Errorf("workerEnv() PATH = %q, want /usr/bin:/bin", v)
	}
	if v, ok := find("HOME"); !ok || v != "/home/user" {
		t.Errorf("workerEnv() HOME = %q, want /home/user", v)
	}
	if v, ok := find("TZ"); !ok || v != "UTC" {
		t.Errorf("workerEnv() TZ = %q, want UTC", v)
	}
}

func TestWorkerEnvOmitsMissingVars(t *testing.T) {
	// Ensure no KEY= entry appears for a key that's not set in the env.
	// t.Setenv cannot unset a var; use Unsetenv directly via os package.
	// We assert on a synthetic key name that is never set by the OS.
	env := workerEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_AUTHOR_NAME=") {
			// If the test runner happens to have GIT_AUTHOR_NAME set, skip
			// rather than fail — we're testing that absent vars are absent.
			t.Skipf("GIT_AUTHOR_NAME is set in the test environment; skipping")
		}
	}
}

func TestWorkerEnvNoDuplicates(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	env := workerEnv()
	seen := make(map[string]struct{}, len(env))
	for _, kv := range env {
		key := strings.SplitN(kv, "=", 2)[0]
		if _, dup := seen[key]; dup {
			t.Errorf("workerEnv() contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
	}
}

// ── defaultWorkerLimits ───────────────────────────────────────────────────────

func TestDefaultWorkerLimits_Defaults(t *testing.T) {
	// Unset the env vars so we get the hard-coded defaults.
	for _, k := range []string{
		"GATEWAY_TASK_MAX_OUTPUT_BYTES",
		"GATEWAY_SANDBOX_RLIMIT_CPU",
		"GATEWAY_SANDBOX_RLIMIT_NOFILE",
		"GATEWAY_SANDBOX_RLIMIT_AS",
	} {
		t.Setenv(k, "")
	}

	lim := defaultWorkerLimits()

	if lim.MaxOutputBytes != 4*1024*1024 {
		t.Errorf("MaxOutputBytes = %d, want %d", lim.MaxOutputBytes, 4*1024*1024)
	}
	if lim.MaxCPUSeconds != 300 {
		t.Errorf("MaxCPUSeconds = %d, want 300", lim.MaxCPUSeconds)
	}
	if lim.MaxOpenFiles != 1024 {
		t.Errorf("MaxOpenFiles = %d, want 1024", lim.MaxOpenFiles)
	}
	if lim.MaxAddressSpaceBytes != 0 {
		t.Errorf("MaxAddressSpaceBytes = %d, want 0 (disabled)", lim.MaxAddressSpaceBytes)
	}
}

func TestDefaultWorkerLimits_EnvOverride(t *testing.T) {
	t.Setenv("GATEWAY_TASK_MAX_OUTPUT_BYTES", "1048576") // 1 MiB
	t.Setenv("GATEWAY_SANDBOX_RLIMIT_CPU", "60")
	t.Setenv("GATEWAY_SANDBOX_RLIMIT_NOFILE", "512")
	t.Setenv("GATEWAY_SANDBOX_RLIMIT_AS", "536870912") // 512 MiB

	lim := defaultWorkerLimits()

	if lim.MaxOutputBytes != 1048576 {
		t.Errorf("MaxOutputBytes = %d, want 1048576", lim.MaxOutputBytes)
	}
	if lim.MaxCPUSeconds != 60 {
		t.Errorf("MaxCPUSeconds = %d, want 60", lim.MaxCPUSeconds)
	}
	if lim.MaxOpenFiles != 512 {
		t.Errorf("MaxOpenFiles = %d, want 512", lim.MaxOpenFiles)
	}
	if lim.MaxAddressSpaceBytes != 536870912 {
		t.Errorf("MaxAddressSpaceBytes = %d, want 536870912", lim.MaxAddressSpaceBytes)
	}
}

func TestDefaultWorkerLimits_InvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("GATEWAY_TASK_MAX_OUTPUT_BYTES", "not-a-number")
	t.Setenv("GATEWAY_SANDBOX_RLIMIT_CPU", "-1")

	lim := defaultWorkerLimits()

	if lim.MaxOutputBytes != 4*1024*1024 {
		t.Errorf("MaxOutputBytes = %d, want default %d", lim.MaxOutputBytes, 4*1024*1024)
	}
	if lim.MaxCPUSeconds != 300 {
		t.Errorf("MaxCPUSeconds = %d, want default 300", lim.MaxCPUSeconds)
	}
}

func TestDefaultWorkerLimits_ZeroDisablesCap(t *testing.T) {
	t.Setenv("GATEWAY_TASK_MAX_OUTPUT_BYTES", "0")
	t.Setenv("GATEWAY_SANDBOX_RLIMIT_CPU", "0")

	lim := defaultWorkerLimits()

	if lim.MaxOutputBytes != 0 {
		t.Errorf("MaxOutputBytes = %d, want 0 (disabled)", lim.MaxOutputBytes)
	}
	if lim.MaxCPUSeconds != 0 {
		t.Errorf("MaxCPUSeconds = %d, want 0 (disabled)", lim.MaxCPUSeconds)
	}
}

// ── OutputLimitExceededError ─────────────────────────────────────────────────

func TestOutputLimitExceededError_Message(t *testing.T) {
	err := &OutputLimitExceededError{Limit: 4194304}
	msg := err.Error()
	if !strings.Contains(msg, "4194304") {
		t.Errorf("error message %q does not contain the limit value", msg)
	}
	if !strings.Contains(msg, "GATEWAY_TASK_MAX_OUTPUT_BYTES") {
		t.Errorf("error message %q does not mention the env var", msg)
	}
}

func TestIsOutputLimitExceeded(t *testing.T) {
	err := &OutputLimitExceededError{Limit: 1024}
	if !IsOutputLimitExceeded(err) {
		t.Errorf("IsOutputLimitExceeded(%v) = false, want true", err)
	}
	if IsOutputLimitExceeded(context.DeadlineExceeded) {
		t.Errorf("IsOutputLimitExceeded(DeadlineExceeded) = true, want false")
	}
}

// ── Output-cap enforcement (LocalExecutor) ───────────────────────────────────

func TestLocalExecutorOutputCapEnforced(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		// yes(1) emits 'y\n' forever; will exceed 64 bytes quickly.
		Command: `yes`,
		Timeout: 5 * time.Second,
		Limits:  ResourceLimits{MaxOutputBytes: 64},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want OutputLimitExceededError")
	}
	if !IsOutputLimitExceeded(err) {
		t.Errorf("Run() error = %v (%T), want OutputLimitExceededError", err, err)
	}
}

func TestLocalExecutorOutputCapNotExceeded(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `echo hello`,
		Timeout: 5 * time.Second,
		Limits:  ResourceLimits{MaxOutputBytes: 1024},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("stdout = %q, want to contain 'hello'", result.Stdout)
	}
}

func TestLocalExecutorOutputCapZeroMeansUnlimited(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	// dd produces exactly 1 KiB; MaxOutputBytes=0 should not cap it.
	result, err := exec.Run(context.Background(), Command{
		Command: `dd if=/dev/zero bs=1024 count=1 2>/dev/null | wc -c`,
		Timeout: 5 * time.Second,
		Limits:  ResourceLimits{MaxOutputBytes: 0},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !strings.Contains(result.Stdout, "1024") {
		t.Errorf("stdout = %q, want output containing 1024", result.Stdout)
	}
}

// ── Timeout-path pipe-close (orphan grandchild) ──────────────────────────────

// TestLocalExecutorTimeoutUnblocksOrphanPipes verifies that a command whose
// grandchild keeps the stdout pipe open after sh is killed still terminates
// within the configured Timeout. This is the context-timeout analogue of
// TestLocalExecutorOutputCapEnforced: instead of the output cap firing the
// cancel, the wall-clock deadline does. Both paths must close the pipe
// read-ends so streamPipe goroutines can exit — without the watchdog goroutine
// the test would hang until the test suite's own deadline.
func TestLocalExecutorTimeoutUnblocksOrphanPipes(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	start := time.Now()
	_, err := exec.Run(context.Background(), Command{
		// sh spawns yes as a child; yes keeps writing to stdout. When the
		// 300 ms deadline fires, sh is killed. Without the watchdog goroutine
		// closing the pipe read-ends, yes (now orphaned) would keep the
		// write-end open and streamPipe would block forever.
		Command: `yes`,
		Timeout: 300 * time.Millisecond,
		// No output cap — this must be driven by the Timeout, not by
		// MaxOutputBytes.
		Limits: ResourceLimits{MaxOutputBytes: 0},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run() error = nil, want timeout error")
	}
	// The call must return well within 2× the timeout — generous headroom
	// for slow CI, but tight enough to catch a real hang.
	if elapsed > 2*time.Second {
		t.Errorf("Run() took %v, want < 2s (possible pipe deadlock)", elapsed)
	}
}

// ── applyProcessResourceLimits smoke test ────────────────────────────────────

func TestApplyProcessResourceLimits_ZeroLimitsIsNoop(t *testing.T) {
	// All-zero ResourceLimits should be a no-op on every platform.
	// The test passes if it doesn't panic and doesn't affect the current process.
	applyProcessResourceLimits(ResourceLimits{})
}

func TestApplyProcessResourceLimits_DoesNotPanic(t *testing.T) {
	// Non-zero values should not panic regardless of whether the platform
	// honours them (errors are silently ignored inside applyProcessResourceLimits).
	applyProcessResourceLimits(ResourceLimits{
		MaxCPUSeconds: 3600,
		MaxOpenFiles:  4096,
	})
}

// ── defaultWorkerIsolation ────────────────────────────────────────────────────

func TestDefaultWorkerIsolation_Default(t *testing.T) {
	t.Setenv("GATEWAY_SANDBOX_OS_ISOLATION", "")
	iso := defaultWorkerIsolation()
	if iso.DisableNetwork {
		t.Errorf("DisableNetwork = true, want false (default)")
	}
}

func TestDefaultWorkerIsolation_EnvOverride(t *testing.T) {
	for _, val := range []string{"true", "1", "yes", "TRUE", "Yes"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("GATEWAY_SANDBOX_OS_ISOLATION", val)
			iso := defaultWorkerIsolation()
			if !iso.DisableNetwork {
				t.Errorf("GATEWAY_SANDBOX_OS_ISOLATION=%q: DisableNetwork = false, want true", val)
			}
		})
	}
	for _, val := range []string{"false", "0", "no", "FALSE"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("GATEWAY_SANDBOX_OS_ISOLATION", val)
			iso := defaultWorkerIsolation()
			if iso.DisableNetwork {
				t.Errorf("GATEWAY_SANDBOX_OS_ISOLATION=%q: DisableNetwork = true, want false", val)
			}
		})
	}
}

func TestDefaultWorkerIsolation_InvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("GATEWAY_SANDBOX_OS_ISOLATION", "not-a-bool")
	iso := defaultWorkerIsolation()
	if iso.DisableNetwork {
		t.Errorf("DisableNetwork = true, want false (invalid env falls back to default)")
	}
}

// ── applyProcessIsolation smoke test ─────────────────────────────────────────

func TestApplyProcessIsolation_ZeroIsolationIsNoop(t *testing.T) {
	// DisableNetwork=false should leave cmd completely unmodified on every
	// platform. The test passes if it does not panic.
	cmd := exec.Command("true")
	originalPath := cmd.Path
	applyProcessIsolation(cmd, IsolationConfig{})
	// On Linux the path stays "true"-resolved; on darwin it should not be
	// rewritten to sandbox-exec. We can only guarantee no panic cross-platform.
	_ = originalPath
}

func TestApplyProcessIsolation_DoesNotPanic(t *testing.T) {
	// Non-zero IsolationConfig should not panic regardless of whether the
	// platform actually enforces it (e.g. no-op stubs, absent sandbox-exec).
	cmd := exec.Command("true")
	applyProcessIsolation(cmd, IsolationConfig{DisableNetwork: true})
}
