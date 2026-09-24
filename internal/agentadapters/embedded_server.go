package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/acp-adapter-kit/commandbridge"
	adapterprocess "github.com/hecatehq/acp-adapter-kit/process"
	claudecodeadapter "github.com/hecatehq/claude-code-acp-adapter/claudecodeadapter"
	codexadapter "github.com/hecatehq/codex-acp-adapter/codexadapter"
)

// This test-only escape hatch keeps the existing strict ACP peer fixtures
// useful while production always uses the embedded servers for owned adapters.
const adapterTestProcessOverridesEnv = "HECATE_AGENT_ADAPTER_TEST_PROCESS_OVERRIDES"

type embeddedACPServer interface {
	Serve(io.Reader, io.Writer) error
}

type providerProcessRunner struct {
	command   string
	path      string
	baseEnv   []string
	runner    commandbridge.ProcessRunner
	adapterID string
	trust     *ExecutableTrustManager
}

const providerProcessWaitDelay = 2 * time.Second

func newProviderProcessRunner(command, path string, baseEnv []string) providerProcessRunner {
	return newProviderProcessRunnerWithTrust("", command, path, baseEnv, nil)
}

func newProviderProcessRunnerWithTrust(adapterID, command, path string, baseEnv []string, trust *ExecutableTrustManager) providerProcessRunner {
	if baseEnv == nil {
		baseEnv = []string{}
	}
	return providerProcessRunner{
		command:   strings.TrimSpace(command),
		path:      strings.TrimSpace(path),
		baseEnv:   append([]string(nil), baseEnv...),
		runner:    commandbridge.NewProcessRunner(baseEnv),
		adapterID: strings.TrimSpace(adapterID),
		trust:     trust,
	}
}

func (r providerProcessRunner) Run(ctx context.Context, spec adapterprocess.Spec) (adapterprocess.Result, error) {
	return r.run(ctx, r.bindCommand(spec), nil)
}

func (r providerProcessRunner) RunStream(ctx context.Context, spec adapterprocess.Spec, onStdout func([]byte) error) (adapterprocess.Result, error) {
	return r.run(ctx, r.bindCommand(spec), onStdout)
}

// run starts the provider directly so the executable permit can be released
// immediately after the actual child-start boundary. Holding it until a long
// prompt process exits would make an operator revoke wait indefinitely;
// releasing it before Start would leave a same-process approval race.
//
// Stdout is attached as a writer rather than consumed through StdoutPipe. That
// lets exec.Cmd.WaitDelay bound an escaped descendant that inherits a provider
// pipe without calling Wait before the provider's output has been drained.
func (r providerProcessRunner) run(ctx context.Context, spec adapterprocess.Spec, onStdout func([]byte) error) (adapterprocess.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	permit, err := r.authorize(ctx, spec.Command)
	if err != nil {
		return adapterprocess.Result{}, err
	}
	if permit != nil {
		defer permit.Close()
	}
	command, args, dir, env, err := r.prepareRun(spec)
	if err != nil {
		return adapterprocess.Result{}, err
	}
	stdoutLimit := spec.StdoutLimit
	if stdoutLimit <= 0 {
		stdoutLimit = adapterprocess.DefaultOutputLimit
	}
	stderrLimit := spec.StderrLimit
	if stderrLimit <= 0 {
		stderrLimit = adapterprocess.DefaultOutputLimit
	}
	stdout := &providerProcessOutput{
		buffer:   &limitedBuffer{limit: stdoutLimit},
		onStdout: onStdout,
	}
	stderr := &limitedBuffer{limit: stderrLimit}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.WaitDelay = providerProcessWaitDelay
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	attachProcessTree, releaseProcessTree, err := prepareAgentProcessTree(cmd)
	if err != nil {
		return adapterprocess.Result{}, fmt.Errorf("prepare provider process tree: %w", err)
	}
	defer releaseProcessTree()
	stdout.cancel = func() {
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		}
	}
	if err := cmd.Start(); err != nil {
		if os.IsNotExist(err) || errors.Is(err, exec.ErrNotFound) {
			return adapterprocess.Result{}, &adapterprocess.CommandNotFoundError{Command: command, Err: err}
		}
		return adapterprocess.Result{}, fmt.Errorf("start process %q: %w", command, err)
	}
	if permit != nil {
		permit.Close()
	}
	if err := attachProcessTree(); err != nil {
		terminateProcess(cmd)
		return adapterprocess.Result{}, fmt.Errorf("supervise provider process tree: %w", err)
	}

	observeErr := waitAgentProcessExitWithoutReaping(ctx, cmd)
	if cmd.Cancel != nil {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	stdoutBytes, stdoutTruncated, streamErr := stdout.snapshot()
	stderrBytes, stderrTruncated := snapshotProviderProcessBuffer(stderr)
	result := adapterprocess.Result{
		Command:         command,
		Args:            append([]string(nil), args...),
		Dir:             dir,
		Stdout:          stdoutBytes,
		Stderr:          stderrBytes,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, fmt.Errorf("process cancelled: %w", ctxErr)
	}
	if streamErr != nil {
		return result, fmt.Errorf("stream process stdout: %w", streamErr)
	}
	if observeErr != nil {
		return result, fmt.Errorf("observe provider process exit: %w", observeErr)
	}
	if waitErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return result, &adapterprocess.ExitError{Command: command, Code: exitErr.ExitCode(), Stderr: stderrBytes}
	}
	return result, fmt.Errorf("run process %q: %w", command, waitErr)
}

func (r providerProcessRunner) prepareRun(spec adapterprocess.Spec) (string, []string, string, []string, error) {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return "", nil, "", nil, errors.New("process command is required")
	}
	if strings.ContainsRune(command, '\x00') {
		return "", nil, "", nil, errors.New("process command contains NUL byte")
	}
	if !filepath.IsAbs(command) {
		return "", nil, "", nil, fmt.Errorf("process command must be absolute with a host-owned base environment: %s", command)
	}
	if isProviderShellCommand(command) {
		return "", nil, "", nil, fmt.Errorf("process command %q is a shell; use fixed argv without a shell", command)
	}
	args := append([]string(nil), spec.Args...)
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return "", nil, "", nil, errors.New("process argument contains NUL byte")
		}
	}
	dir, err := adapterprocess.CleanWorkingDir(spec.Dir)
	if err != nil {
		return "", nil, "", nil, err
	}
	env, err := adapterprocess.BuildEnv(r.baseEnv, spec.Env)
	if err != nil {
		return "", nil, "", nil, err
	}
	return filepath.Clean(command), args, dir, env, nil
}

func isProviderShellCommand(command string) bool {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(command)), ".exe")
	switch base {
	case "sh", "bash", "zsh", "dash", "ksh", "fish", "cmd", "powershell", "pwsh":
		return true
	default:
		return false
	}
}

type providerProcessOutput struct {
	buffer   *limitedBuffer
	onStdout func([]byte) error
	cancel   func()

	mu        sync.Mutex
	streamErr error
}

func (w *providerProcessOutput) Write(p []byte) (int, error) {
	if w == nil || w.buffer == nil {
		return len(p), nil
	}
	_, _ = w.buffer.Write(p)
	if len(p) == 0 || w.onStdout == nil {
		return len(p), nil
	}
	if err := w.onStdout(append([]byte(nil), p...)); err != nil {
		w.mu.Lock()
		if w.streamErr == nil {
			w.streamErr = err
		}
		w.mu.Unlock()
		if w.cancel != nil {
			w.cancel()
		}
		return 0, err
	}
	return len(p), nil
}

func (w *providerProcessOutput) snapshot() ([]byte, bool, error) {
	if w == nil {
		return nil, false, nil
	}
	data, truncated := snapshotProviderProcessBuffer(w.buffer)
	w.mu.Lock()
	err := w.streamErr
	w.mu.Unlock()
	return data, truncated, err
}

func snapshotProviderProcessBuffer(buffer *limitedBuffer) ([]byte, bool) {
	if buffer == nil {
		return nil, false
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.Buffer.Bytes()...), buffer.truncated
}

// Start lets embedded adapters run short-lived discovery exchanges through the
// same resolved provider binary and constrained base environment as prompts.
// It is intentionally separate from Run because discovery needs stdin/stdout
// pipes while retaining the host's executable binding.
func (r providerProcessRunner) Start(ctx context.Context, spec adapterprocess.StartSpec) (*adapterprocess.Child, error) {
	bound := r.bindStartCommand(spec)
	permit, err := r.authorize(ctx, bound.Command)
	if err != nil {
		return nil, err
	}
	if permit != nil {
		defer permit.Close()
	}
	return r.runner.Start(ctx, bound)
}

func (r providerProcessRunner) authorize(ctx context.Context, command string) (*ExecutablePermit, error) {
	if r.trust == nil {
		return nil, nil
	}
	if strings.TrimSpace(command) == "" || strings.TrimSpace(command) != r.path {
		return nil, ErrExecutableIdentityUnavailable
	}
	permit, err := r.trust.AuthorizePath(ctx, r.adapterID, command)
	return permit, executableTrustBoundaryError(err)
}

func (r providerProcessRunner) bindCommand(spec adapterprocess.Spec) adapterprocess.Spec {
	if strings.TrimSpace(spec.Command) == r.command && r.path != "" {
		spec.Command = r.path
	}
	return spec
}

func (r providerProcessRunner) bindStartCommand(spec adapterprocess.StartSpec) adapterprocess.StartSpec {
	if strings.TrimSpace(spec.Command) == r.command && r.path != "" {
		spec.Command = r.path
	}
	return spec
}

func newEmbeddedACPServer(adapter Adapter, providerPath string, baseEnv []string) (embeddedACPServer, error) {
	return newEmbeddedACPServerWithTrust(adapter, providerPath, baseEnv, nil)
}

func newEmbeddedACPServerWithTrust(adapter Adapter, providerPath string, baseEnv []string, trust *ExecutableTrustManager) (embeddedACPServer, error) {
	runner := newProviderProcessRunnerWithTrust(adapter.ID, adapter.Command, providerPath, baseEnv, trust)
	version := embeddedAdapterVersion(adapter.ID)
	switch adapter.ID {
	case "codex":
		return codexadapter.NewServerWithRunner(version, runner), nil
	case "claude_code":
		return claudecodeadapter.NewServerWithRunner(version, runner), nil
	default:
		return nil, fmt.Errorf("adapter %q has no embedded ACP server", adapter.ID)
	}
}

func embeddedAdapterVersion(adapterID string) string {
	module := ""
	switch adapterID {
	case "codex":
		module = "github.com/hecatehq/codex-acp-adapter"
	case "claude_code":
		module = "github.com/hecatehq/claude-code-acp-adapter"
	default:
		return "embedded"
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "embedded"
	}
	for _, dep := range info.Deps {
		if dep.Path != module {
			continue
		}
		version := strings.TrimSpace(strings.TrimPrefix(dep.Version, "v"))
		if version == "" || version == "(devel)" {
			return "embedded"
		}
		return version
	}
	return "embedded"
}

func adapterUsesEmbeddedServer(adapter Adapter) bool {
	return adapter.Embedded && !adapterTestProcessOverride(adapter.ID)
}

func adapterTestProcessOverride(adapterID string) bool {
	adapterID = strings.TrimSpace(adapterID)
	for _, value := range strings.Split(os.Getenv(adapterTestProcessOverridesEnv), ",") {
		value = strings.TrimSpace(value)
		if value == "all" || value == adapterID {
			return true
		}
	}
	return false
}

func runtimeAdapter(adapter Adapter) Adapter {
	if !adapter.Embedded || !adapterTestProcessOverride(adapter.ID) {
		return adapter
	}
	adapter.Embedded = false
	adapter.Command = adapter.TestProcessCommand
	adapter.Args = append([]string(nil), adapter.TestProcessArgs...)
	adapter.CandidatePaths = append([]string(nil), adapter.TestProcessCandidatePaths...)
	return adapter
}
