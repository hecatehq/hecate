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
	command string
	path    string
	runner  commandbridge.ProcessRunner
}

func newProviderProcessRunner(command, path string, baseEnv []string) providerProcessRunner {
	return providerProcessRunner{
		command: strings.TrimSpace(command),
		path:    strings.TrimSpace(path),
		runner:  commandbridge.NewProcessRunner(baseEnv),
	}
}

func (r providerProcessRunner) Run(ctx context.Context, spec adapterprocess.Spec) (adapterprocess.Result, error) {
	return r.runner.Run(ctx, r.bindCommand(spec))
}

func (r providerProcessRunner) RunStream(ctx context.Context, spec adapterprocess.Spec, onStdout func([]byte) error) (adapterprocess.Result, error) {
	return r.runner.RunStream(ctx, r.bindCommand(spec), onStdout)
}

func (r providerProcessRunner) bindCommand(spec adapterprocess.Spec) adapterprocess.Spec {
	if strings.TrimSpace(spec.Command) == r.command && r.path != "" {
		spec.Command = r.path
	}
	return spec
}

func newEmbeddedACPServer(adapter Adapter, providerPath string, baseEnv []string) (embeddedACPServer, error) {
	runner := newProviderProcessRunner(adapter.Command, providerPath, baseEnv)
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
