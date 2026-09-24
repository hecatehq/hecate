package agentadapters

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

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
	runner    commandbridge.ProcessRunner
	adapterID string
	trust     *ExecutableTrustManager
}

func newProviderProcessRunner(command, path string, baseEnv []string) providerProcessRunner {
	return newProviderProcessRunnerWithTrust("", command, path, baseEnv, nil)
}

func newProviderProcessRunnerWithTrust(adapterID, command, path string, baseEnv []string, trust *ExecutableTrustManager) providerProcessRunner {
	return providerProcessRunner{
		command:   strings.TrimSpace(command),
		path:      strings.TrimSpace(path),
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

// run deliberately enters the kit through Start instead of Run so the
// executable permit can be released immediately after the actual child-start
// boundary. Holding it until a long prompt process exits would make an
// operator revoke wait indefinitely; releasing it before Run would leave a
// same-process approval race. Output remains bounded to the kit's public
// default and Child preserves the kit's process-unit cancellation semantics.
func (r providerProcessRunner) run(ctx context.Context, spec adapterprocess.Spec, onStdout func([]byte) error) (adapterprocess.Result, error) {
	permit, err := r.authorize(ctx, spec.Command)
	if err != nil {
		return adapterprocess.Result{}, err
	}
	if permit != nil {
		defer permit.Close()
	}
	child, err := r.runner.Start(ctx, adapterprocess.StartSpec{
		Command:     spec.Command,
		Args:        append([]string(nil), spec.Args...),
		Dir:         spec.Dir,
		Env:         spec.Env,
		StderrLimit: spec.StderrLimit,
	})
	if err != nil {
		return adapterprocess.Result{}, err
	}
	if permit != nil {
		permit.Close()
	}
	_ = child.Stdin.Close()

	limit := spec.StdoutLimit
	if limit <= 0 {
		limit = adapterprocess.DefaultOutputLimit
	}
	stdout := make([]byte, 0, minInt64(limit, 32*1024))
	buffer := make([]byte, 32*1024)
	var readErr error
	var streamErr error
	truncated := false
	for {
		n, err := child.Stdout.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			remaining := limit - int64(len(stdout))
			if remaining > 0 {
				keep := int64(n)
				if keep > remaining {
					keep = remaining
					truncated = true
				}
				stdout = append(stdout, chunk[:int(keep)]...)
			} else {
				truncated = true
			}
			if onStdout != nil && streamErr == nil {
				if callbackErr := onStdout(append([]byte(nil), chunk...)); callbackErr != nil {
					streamErr = callbackErr
					_ = child.Kill()
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	waitErr := child.Wait()
	result := adapterprocess.Result{
		Command:         child.Command,
		Args:            append([]string(nil), child.Args...),
		Dir:             child.Dir,
		Stdout:          stdout,
		Stderr:          child.Stderr(),
		StdoutTruncated: truncated,
		StderrTruncated: child.StderrTruncated(),
	}
	if streamErr != nil {
		return result, fmt.Errorf("stream process stdout: %w", streamErr)
	}
	if readErr != nil {
		return result, fmt.Errorf("read process stdout: %w", readErr)
	}
	return result, waitErr
}

func minInt64(a, b int64) int {
	if a < b {
		return int(a)
	}
	return int(b)
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
		return nil, fmt.Errorf("%w: embedded adapter requested an unapproved executable", ErrExecutableIdentityUnavailable)
	}
	return r.trust.AuthorizePath(ctx, r.adapterID, command)
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
