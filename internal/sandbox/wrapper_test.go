package sandbox

import (
	"os/exec"
	"runtime"
	"testing"
)

// TestSetWrapperForTesting_OverrideAndReset verifies the test helper round-trip:
// the override is observed by DetectWrapper and the reset function restores
// the prior state.
func TestSetWrapperForTesting_OverrideAndReset(t *testing.T) {
	prev := DetectWrapper(t.Context())

	reset := SetWrapperForTesting(WrapperBwrap)
	if got := DetectWrapper(t.Context()); got != WrapperBwrap {
		t.Errorf("DetectWrapper after override = %q, want %q", got, WrapperBwrap)
	}
	reset()

	if got := DetectWrapper(t.Context()); got != prev {
		t.Errorf("DetectWrapper after reset = %q, want %q", got, prev)
	}
}

// TestApplyWrapper_None_LeavesCmdUnchanged: when no wrapper is active the
// applyWrapper call must not rewrite cmd.Path or cmd.Args.
func TestApplyWrapper_None_LeavesCmdUnchanged(t *testing.T) {
	reset := SetWrapperForTesting(WrapperNone)
	defer reset()

	cmd := exec.Command("sh", "-lc", "echo hi")
	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)

	applyWrapper(cmd, "/tmp/work", false)

	if cmd.Path != originalPath {
		t.Errorf("cmd.Path = %q, want unchanged %q", cmd.Path, originalPath)
	}
	if len(cmd.Args) != len(originalArgs) {
		t.Errorf("cmd.Args = %v, want unchanged %v", cmd.Args, originalArgs)
	}
}

// TestApplyWrapper_Bwrap_RewritesArgv: with the bwrap wrapper active,
// applyWrapper must prepend the bwrap argv prefix and the original sh argv
// must appear after.
func TestApplyWrapper_Bwrap_RewritesArgv(t *testing.T) {
	reset := SetWrapperForTesting(WrapperBwrap)
	defer reset()

	cmd := exec.Command("sh", "-lc", "echo hi")
	applyWrapper(cmd, "/tmp/work", false)

	// First arg is the wrapper binary path. The rest must contain the
	// original argv at the tail and the --unshare-net flag because
	// network=false.
	if len(cmd.Args) < 4 {
		t.Fatalf("cmd.Args too short: %v", cmd.Args)
	}
	tail := cmd.Args[len(cmd.Args)-3:]
	if tail[0] != "sh" || tail[1] != "-lc" || tail[2] != "echo hi" {
		t.Errorf("argv tail = %v, want [sh -lc echo hi]", tail)
	}
	hasUnshareNet := false
	hasBindWorkspace := false
	for i, a := range cmd.Args {
		if a == "--unshare-net" {
			hasUnshareNet = true
		}
		if a == "--bind" && i+2 < len(cmd.Args) && cmd.Args[i+1] == "/tmp/work" && cmd.Args[i+2] == "/tmp/work" {
			hasBindWorkspace = true
		}
	}
	if !hasUnshareNet {
		t.Error("expected --unshare-net when network=false")
	}
	if !hasBindWorkspace {
		t.Error("expected --bind /tmp/work /tmp/work for the workspace")
	}
}

// TestApplyWrapper_Bwrap_NetworkAllowedSkipsUnshareNet: when network is
// allowed the unshare-net flag must be absent.
func TestApplyWrapper_Bwrap_NetworkAllowedSkipsUnshareNet(t *testing.T) {
	reset := SetWrapperForTesting(WrapperBwrap)
	defer reset()

	cmd := exec.Command("sh", "-lc", "curl https://example.com")
	applyWrapper(cmd, "/tmp/work", true)

	for _, a := range cmd.Args {
		if a == "--unshare-net" {
			t.Errorf("--unshare-net should not appear when network=true; argv = %v", cmd.Args)
		}
	}
}

// TestApplyWrapper_SandboxExec_NetworkDeniedAttachesProfile: with network=false
// the sandbox-exec wrapper must wrap the argv with -p <profile>.
func TestApplyWrapper_SandboxExec_NetworkDeniedAttachesProfile(t *testing.T) {
	reset := SetWrapperForTesting(WrapperSandboxExec)
	defer reset()

	cmd := exec.Command("sh", "-lc", "echo hi")
	applyWrapper(cmd, "/tmp/work", false)

	if len(cmd.Args) < 5 {
		t.Fatalf("cmd.Args too short: %v", cmd.Args)
	}
	if cmd.Args[0] != sandboxExecBinary {
		t.Errorf("argv[0] = %q, want %q", cmd.Args[0], sandboxExecBinary)
	}
	if cmd.Args[1] != "-p" {
		t.Errorf("argv[1] = %q, want -p", cmd.Args[1])
	}
	// Profile string must contain the deny-network rule.
	if !contains(cmd.Args[2], "deny network") {
		t.Errorf("profile %q does not contain a deny-network rule", cmd.Args[2])
	}
}

// TestApplyWrapper_SandboxExec_NetworkAllowedSkipsWrapping: when network is
// allowed sandbox-exec is a no-op (the upstream allowlist does the work).
func TestApplyWrapper_SandboxExec_NetworkAllowedSkipsWrapping(t *testing.T) {
	reset := SetWrapperForTesting(WrapperSandboxExec)
	defer reset()

	cmd := exec.Command("sh", "-lc", "curl https://example.com")
	originalPath := cmd.Path
	applyWrapper(cmd, "/tmp/work", true)

	if cmd.Path != originalPath {
		t.Errorf("cmd.Path = %q, want unchanged %q (no-op when network allowed)", cmd.Path, originalPath)
	}
}

// TestHealthInfo_ReportsKindAndReason: HealthInfo must report the active
// kind, and on WrapperNone the reason must match the platform.
func TestHealthInfo_ReportsKindAndReason(t *testing.T) {
	reset := SetWrapperForTesting(WrapperNone)
	defer reset()

	info := HealthInfo()
	if info.Kind != WrapperNone {
		t.Errorf("Kind = %q, want %q", info.Kind, WrapperNone)
	}
	switch runtime.GOOS {
	case "linux":
		if !contains(info.Reason, "/usr/bin/bwrap") {
			t.Errorf("linux WrapperNone Reason = %q, want mention of /usr/bin/bwrap", info.Reason)
		}
	case "darwin":
		if !contains(info.Reason, "/usr/bin/sandbox-exec") {
			t.Errorf("darwin WrapperNone Reason = %q, want mention of /usr/bin/sandbox-exec", info.Reason)
		}
	case "windows":
		if !contains(info.Reason, "windows") {
			t.Errorf("windows WrapperNone Reason = %q, want mention of windows", info.Reason)
		}
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
