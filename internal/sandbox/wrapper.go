package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// WrapperKind identifies which OS-level isolation tool the gateway
// detected at startup. Stable string values; safe to surface on
// /healthz and in logs.
type WrapperKind string

const (
	// WrapperNone — no OS-level wrap. Layer 0 + Layer 1 still apply.
	// This is the result on Windows, on Linux without bwrap, and on
	// any platform where the wrapper probe fails.
	WrapperNone WrapperKind = "none"
	// WrapperBwrap — bubblewrap. Linux. Provides filesystem and
	// network confinement.
	WrapperBwrap WrapperKind = "bwrap"
	// WrapperSandboxExec — Apple Seatbelt via /usr/bin/sandbox-exec.
	// macOS. Provides network confinement; filesystem confinement is
	// roadmap work and not enabled in v1.
	WrapperSandboxExec WrapperKind = "sandbox-exec"
)

// bwrapPath is the standard install path for bubblewrap on every
// distro that ships it (Debian/Ubuntu via package "bubblewrap",
// Fedora via "bubblewrap", Arch via "bubblewrap", Alpine via
// "bubblewrap"). We probe this exact path first; PATH lookup is
// attempted only as a fallback for non-standard installs.
const bwrapPath = "/usr/bin/bwrap"

// sandboxExecBinary is the standard macOS path for Apple's Seatbelt
// wrapper. Present on every supported macOS version.
const sandboxExecBinary = "/usr/bin/sandbox-exec"

// sandboxExecProbeExecutable is a stable no-op executable on macOS. Keep this
// absolute because the probe intentionally does not depend on the gateway PATH.
const sandboxExecProbeExecutable = "/usr/bin/true"

// detectedWrapper caches the result of probing for an available
// wrapper at startup. Computed once via sync.Once. Tests can override
// via SetWrapperForTesting.
var (
	detectedWrapperOnce sync.Once
	detectedWrapper     WrapperKind
	detectedWrapperPath string
	wrapperOverride     *wrapperSelection // test-only override
	wrapperOverrideMu   sync.Mutex
)

type wrapperSelection struct {
	kind WrapperKind
	path string
}

// DetectWrapper returns the OS-level isolation wrapper the gateway
// will use for shell tool calls. The result is cached after the first
// call; subsequent invocations return the same value without re-probing.
//
// Detection logic:
//   - macOS: WrapperSandboxExec (binary ships with the OS).
//   - Linux: WrapperBwrap if /usr/bin/bwrap exists AND a probe call
//     succeeds. Probe runs `bwrap --ro-bind / / --proc /proc --dev /dev
//     --unshare-pid /bin/true` to catch the unprivileged-userns-disabled
//     case (some hardened kernels and unprivileged Docker containers
//     return EPERM).
//   - Linux without bwrap, Windows, anything else: WrapperNone.
//
// Test environments can short-circuit detection via SetWrapperForTesting.
func DetectWrapper(ctx context.Context) WrapperKind {
	return detectWrapperSelection(ctx).kind
}

func detectWrapperSelection(ctx context.Context) wrapperSelection {
	wrapperOverrideMu.Lock()
	if wrapperOverride != nil {
		selection := *wrapperOverride
		wrapperOverrideMu.Unlock()
		return selection
	}
	wrapperOverrideMu.Unlock()
	detectedWrapperOnce.Do(func() {
		detectedWrapper, detectedWrapperPath = probeWrapper(ctx)
	})
	return wrapperSelection{kind: detectedWrapper, path: detectedWrapperPath}
}

// WrapperPath returns the absolute path to the detected wrapper
// binary, or "" if no wrapper is active.
func WrapperPath() string {
	return detectWrapperSelection(context.Background()).path
}

// SetWrapperForTesting forces DetectWrapper to return the given kind
// (and resolves the path to "" or the standard binary path). Callers
// must restore the prior state via the returned reset function. Test
// helper only — do not call from production code.
func SetWrapperForTesting(kind WrapperKind) (reset func()) {
	path := ""
	switch kind {
	case WrapperBwrap:
		path = bwrapPath
	case WrapperSandboxExec:
		path = sandboxExecBinary
	}
	wrapperOverrideMu.Lock()
	prev := wrapperOverride
	wrapperOverride = &wrapperSelection{kind: kind, path: path}
	wrapperOverrideMu.Unlock()
	return func() {
		wrapperOverrideMu.Lock()
		wrapperOverride = prev
		wrapperOverrideMu.Unlock()
	}
}

func probeWrapper(ctx context.Context) (WrapperKind, string) {
	switch runtime.GOOS {
	case "darwin":
		// sandbox-exec usually ships with macOS, but newer or hardened
		// environments can leave the binary present while refusing to run
		// profiles. Probe before enabling it so shell tools do not fail at
		// execution time with sandbox-exec's generic service errors.
		if _, err := os.Stat(sandboxExecBinary); err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			probe := sandboxExecProbeCommand(probeCtx)
			if err := probe.Run(); err == nil {
				return WrapperSandboxExec, sandboxExecBinary
			}
		}
		return WrapperNone, ""
	case "linux":
		path := bwrapPath
		if _, err := os.Stat(path); err != nil {
			// Try PATH lookup as a fallback for non-standard installs.
			if found, lookupErr := exec.LookPath("bwrap"); lookupErr == nil {
				path = found
			} else {
				return WrapperNone, ""
			}
		}
		// Probe — does bwrap actually work in this environment? Some
		// hardened kernels (unprivileged_userns_clone=0) and some
		// container runtimes refuse the namespace creation, in which
		// case bwrap is installed but unusable. We surface this as
		// WrapperNone rather than a half-broken bwrap.
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		probe := exec.CommandContext(probeCtx, path,
			"--ro-bind", "/", "/",
			"--proc", "/proc",
			"--dev", "/dev",
			"--unshare-pid",
			"/bin/true",
		)
		if err := probe.Run(); err != nil {
			return WrapperNone, ""
		}
		return WrapperBwrap, path
	default:
		return WrapperNone, ""
	}
}

func sandboxExecProbeCommand(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, sandboxExecBinary, "-p", `(version 1)(allow default)`, sandboxExecProbeExecutable)
}

// applyWrapper rewrites cmd in place to wrap the original argv with
// bwrap (Linux) or sandbox-exec (macOS), as detected by DetectWrapper.
// On WrapperNone (Windows, Linux without bwrap, etc.) the cmd is
// returned unchanged and the call runs with Layer 0 + Layer 1 only.
//
// workspace is the read-write bind target on Linux. If empty, only the
// read-only host root is bound — useful for commands that don't write
// (e.g. `git status` on a checkout that doesn't need writes), but
// most callers should pass the resolved workspace path.
//
// network=false drops the network namespace on Linux and applies a
// (deny network*) Seatbelt rule on macOS. network=true leaves the
// network alone and lets the policy validation upstream decide which
// hosts the command can reach (best-effort, string-match).
func applyWrapper(cmd *exec.Cmd, workspace string, network bool) {
	argv := wrappedArgv(cmd.Args, workspace, network)
	if len(argv) == 0 {
		return
	}
	if equalStringSlices(argv, cmd.Args) {
		return
	}
	cmd.Path = argv[0]
	cmd.Args = argv
}

// ApplyWrapper rewrites cmd in place with the same OS-level sandbox wrapper
// LocalExecutor uses for one-shot shell execution. Long-lived workspace
// processes use this to keep terminal and shell execution on the same
// isolation path when bwrap or sandbox-exec is available.
func ApplyWrapper(cmd *exec.Cmd, workspace string, network bool) {
	applyWrapper(cmd, workspace, network)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wrappedArgv(argv []string, workspace string, network bool) []string {
	selection := detectWrapperSelection(context.Background())
	switch selection.kind {
	case WrapperBwrap:
		return bwrapArgvWithWrapperPath(argv, workspace, network, false, selection.path)
	case WrapperSandboxExec:
		return sandboxExecArgvWithWrapperPath(argv, network, selection.path)
	default:
		return argv
	}
}

// WrapReadOnlyArgv applies the detected OS-level sandbox wrapper to an argv
// without routing it through a shell. Under bwrap the workspace is rebound
// read-only so paths below /tmp remain visible after the wrapper mounts its
// scratch tmpfs; network=false also removes network access where the platform
// wrapper supports it. Fixed-command process surfaces use this to preserve
// argument boundaries while sharing the shell sandbox's isolation layer.
func WrapReadOnlyArgv(argv []string, workspace string, network bool, extraReadOnlyPaths ...string) []string {
	selection := detectWrapperSelection(context.Background())
	switch selection.kind {
	case WrapperBwrap:
		return bwrapArgvWithWrapperPath(argv, workspace, network, true, selection.path, extraReadOnlyPaths...)
	case WrapperSandboxExec:
		return sandboxExecArgvWithWrapperPath(argv, network, selection.path)
	default:
		return argv
	}
}

func applyBwrap(cmd *exec.Cmd, workspace string, network bool) {
	argv := bwrapArgv(cmd.Args, workspace, network)
	if len(argv) == 0 {
		return
	}
	cmd.Path = argv[0]
	cmd.Args = argv
}

func bwrapArgv(argv []string, workspace string, network bool) []string {
	return bwrapArgvWithWorkspaceMode(argv, workspace, network, false)
}

func bwrapArgvWithWorkspaceMode(argv []string, workspace string, network, readOnly bool, extraReadOnlyPaths ...string) []string {
	return bwrapArgvWithWrapperPath(argv, workspace, network, readOnly, detectWrapperSelection(context.Background()).path, extraReadOnlyPaths...)
}

func bwrapArgvWithWrapperPath(argv []string, workspace string, network, readOnly bool, wrapperPath string, extraReadOnlyPaths ...string) []string {
	args := []string{
		"--ro-bind", "/", "/",
		// Procfs and devfs need explicit mounts; bwrap does NOT bind
		// them when the host root is bound read-only because /proc is
		// per-pidns and /dev is a synthetic filesystem.
		"--proc", "/proc",
		"--dev", "/dev",
		// /tmp is read-write so commands that need scratch space work.
		// Without this, anything that touches /tmp (most build tools)
		// fails on the read-only host bind.
		"--tmpfs", "/tmp",
		"--unshare-pid",
	}
	if workspace != "" {
		bindMode := "--bind"
		if readOnly {
			bindMode = "--ro-bind"
		}
		// The order matters: --ro-bind above makes / read-only and the
		// /tmp tmpfs provides scratch space, then this bind restores the
		// workspace path with the requested access mode.
		args = append(args, bindMode, workspace, workspace)
	}
	for _, path := range extraReadOnlyPaths {
		if strings.TrimSpace(path) == "" || path == workspace {
			continue
		}
		args = append(args, "--ro-bind", path, path)
	}
	if !network {
		args = append(args, "--unshare-net")
	}
	// Append the original argv (sh -lc <command>).
	args = append(args, argv...)

	if wrapperPath == "" {
		wrapperPath = bwrapPath
	}
	return append([]string{wrapperPath}, args...)
}

func applySandboxExec(cmd *exec.Cmd, network bool) {
	argv := sandboxExecArgv(cmd.Args, network)
	if len(argv) == 0 {
		return
	}
	cmd.Path = argv[0]
	cmd.Args = argv
}

func sandboxExecArgv(argv []string, network bool) []string {
	return sandboxExecArgvWithWrapperPath(argv, network, sandboxExecBinary)
}

func sandboxExecArgvWithWrapperPath(argv []string, network bool, wrapperPath string) []string {
	// v1: network-only profile. File-write confinement to the workspace
	// is roadmap work — Seatbelt SBPL needs careful tuning to allow
	// macOS frameworks (Mach IPC, sysctl reads, /private/var/folders
	// scratch dirs) without degrading the file-write deny.
	if network {
		// Network allowed → no Seatbelt needed; let the call run unwrapped
		// so the LLM-supplied policy + host allowlist stay the only check.
		// (We don't emit a no-op profile because that adds a fork+exec
		// cost for nothing.)
		return argv
	}
	const profile = `(version 1)(deny network*)(allow default)`
	if wrapperPath == "" {
		wrapperPath = sandboxExecBinary
	}
	return append([]string{wrapperPath, "-p", profile}, argv...)
}

// WrapperHealthInfo is the shape served on /healthz under
// "sandbox.os_isolation". Operators can read this to know which
// wrapper (if any) is active without parsing logs.
type WrapperHealthInfo struct {
	Kind   WrapperKind `json:"kind"`
	Path   string      `json:"path,omitempty"`
	Reason string      `json:"reason,omitempty"`
}

// HealthInfo returns a snapshot of the active wrapper for /healthz.
// The reason field explains WrapperNone outcomes (probe failed, not
// installed, unsupported platform).
func HealthInfo() WrapperHealthInfo {
	selection := detectWrapperSelection(context.Background())
	kind := selection.kind
	info := WrapperHealthInfo{Kind: kind, Path: selection.path}
	if kind == WrapperNone {
		switch runtime.GOOS {
		case "windows":
			info.Reason = "windows: no kernel-level wrapper available without elevated privileges"
		case "linux":
			info.Reason = fmt.Sprintf("linux: %s not found, or probe failed (unprivileged user namespaces disabled?)", bwrapPath)
		case "darwin":
			info.Reason = fmt.Sprintf("darwin: %s missing — was the binary stripped from a hardened image?", sandboxExecBinary)
		default:
			info.Reason = fmt.Sprintf("unsupported platform: %s", runtime.GOOS)
		}
	}
	return info
}
