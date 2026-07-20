package sandbox

import (
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"sync"
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
	if got := WrapperPath(); got != bwrapPath {
		t.Errorf("WrapperPath after override = %q, want %q", got, bwrapPath)
	}
	reset()

	if got := DetectWrapper(t.Context()); got != prev {
		t.Errorf("DetectWrapper after reset = %q, want %q", got, prev)
	}
}

func TestSetWrapperForTesting_UpdatesKindAndPathTogether(t *testing.T) {
	for _, tc := range []struct {
		kind WrapperKind
		path string
	}{
		{kind: WrapperNone, path: ""},
		{kind: WrapperBwrap, path: bwrapPath},
		{kind: WrapperSandboxExec, path: sandboxExecBinary},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			reset := SetWrapperForTesting(tc.kind)
			defer reset()
			if got := DetectWrapper(t.Context()); got != tc.kind {
				t.Fatalf("DetectWrapper() = %q, want %q", got, tc.kind)
			}
			if got := WrapperPath(); got != tc.path {
				t.Fatalf("WrapperPath() = %q, want %q", got, tc.path)
			}
		})
	}
}

func TestWrapperSelectionSnapshotStaysCoherentDuringOverrideChanges(t *testing.T) {
	selections := []wrapperSelection{
		{kind: WrapperBwrap, path: "/test/wrappers/bwrap"},
		{kind: WrapperSandboxExec, path: "/test/wrappers/sandbox-exec"},
	}
	wrapperOverrideMu.Lock()
	previous := wrapperOverride
	initial := selections[0]
	wrapperOverride = &initial
	wrapperOverrideMu.Unlock()
	t.Cleanup(func() {
		wrapperOverrideMu.Lock()
		wrapperOverride = previous
		wrapperOverrideMu.Unlock()
	})

	const iterations = 2000
	failures := make(chan string, 1)
	report := func(message string) {
		select {
		case failures <- message:
		default:
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for index := 0; index < iterations; index++ {
			wrapperOverrideMu.Lock()
			wrapperOverride = &selections[index%len(selections)]
			wrapperOverrideMu.Unlock()
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				argv := wrappedArgv([]string{"tool", "arg"}, "/workspace", false)
				switch argv[0] {
				case selections[0].path:
					if len(argv) < 2 || argv[1] != "--ro-bind" {
						report(fmt.Sprintf("bwrap path paired with non-bwrap argv: %#v", argv))
						return
					}
				case selections[1].path:
					if len(argv) < 2 || argv[1] != "-p" {
						report(fmt.Sprintf("sandbox-exec path paired with non-sandbox argv: %#v", argv))
						return
					}
				default:
					report(fmt.Sprintf("wrapped argv used a path outside its snapshot: %#v", argv))
					return
				}

				info := HealthInfo()
				if !((info.Kind == selections[0].kind && info.Path == selections[0].path) ||
					(info.Kind == selections[1].kind && info.Path == selections[1].path)) {
					report(fmt.Sprintf("HealthInfo mixed wrapper selection: %+v", info))
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
}

func TestSandboxExecProbeCommand_UsesDarwinExecutable(t *testing.T) {
	cmd := sandboxExecProbeCommand(t.Context())
	wantPath := "/usr/bin/sandbox-exec"
	wantArgs := []string{
		"/usr/bin/sandbox-exec",
		"-p",
		"(version 1)(allow default)",
		"/usr/bin/true",
	}
	if cmd.Path != wantPath {
		t.Errorf("cmd.Path = %q, want %q", cmd.Path, wantPath)
	}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Errorf("cmd.Args = %q, want %q", cmd.Args, wantArgs)
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
