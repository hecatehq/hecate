package agentadapters

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/hecatehq/hecate/internal/remoteruntime"
)

const (
	acpPeerKindEmbedded = "embedded"
	acpPeerKindProcess  = "process"
)

// acpPeer owns one ACP transport independently of whether the adapter server
// runs inside Hecate or as a compatibility/direct-ACP child process.
type acpPeer struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	path   string
	kind   string
	cmd    *exec.Cmd
	stderr *limitedBuffer

	closeOnce   sync.Once
	cleanupOnce sync.Once
	done        chan struct{}
	cleanup     func()
	close       func()
}

func resolveAdapterPeerExecutable(ctx context.Context, adapter Adapter, lookup LookupFunc) (string, error) {
	runtime := runtimeAdapter(adapter)
	if _, remote := remoteruntime.FromContext(ctx); remote {
		// Remote execution may use a different filesystem. Never fall back to a
		// host-personal absolute path when the request is scoped to that runtime.
		runtime.CandidatePaths = nil
	}
	return resolveExecutable(runtime, lookup)
}

func launchACPAdapterPeer(ctx context.Context, adapter Adapter, workspace, resolvedPath string) (*acpPeer, error) {
	processEnv, err := prepareAdapterProcessEnv(ctx, adapter, os.Environ())
	if err != nil {
		return nil, err
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError && processEnv.cleanup != nil {
			processEnv.cleanup()
		}
	}()

	var peer *acpPeer
	if adapterUsesEmbeddedServer(adapter) {
		peer, err = launchEmbeddedACPAdapterPeer(adapter, resolvedPath, processEnv.values)
	} else {
		peer, err = launchProcessACPAdapterPeer(runtimeAdapter(adapter), workspace, resolvedPath, processEnv.values)
	}
	if err != nil {
		return nil, err
	}
	peer.cleanup = processEnv.cleanup
	go func() {
		<-peer.done
		peer.cleanupOnce.Do(func() {
			if peer.cleanup != nil {
				peer.cleanup()
			}
		})
	}()
	cleanupOnError = false
	return peer, nil
}

func launchEmbeddedACPAdapterPeer(adapter Adapter, resolvedPath string, baseEnv []string) (*acpPeer, error) {
	server, err := newEmbeddedACPServer(adapter, resolvedPath, baseEnv)
	if err != nil {
		return nil, err
	}
	serverInput, clientInput := io.Pipe()
	clientOutput, serverOutput := io.Pipe()
	done := make(chan struct{})
	peer := &acpPeer{
		stdin:  clientInput,
		stdout: clientOutput,
		path:   resolvedPath,
		kind:   acpPeerKindEmbedded,
		done:   done,
	}
	peer.close = func() {
		_ = clientInput.Close()
		_ = clientOutput.Close()
	}
	go func() {
		defer close(done)
		serveErr := server.Serve(serverInput, serverOutput)
		_ = serverInput.Close()
		if serveErr != nil {
			_ = serverOutput.CloseWithError(serveErr)
			return
		}
		_ = serverOutput.Close()
	}()
	return peer, nil
}

func launchProcessACPAdapterPeer(adapter Adapter, workspace, resolvedPath string, env []string) (*acpPeer, error) {
	cmd := exec.CommandContext(context.Background(), resolvedPath, append([]string(nil), adapter.Args...)...)
	configureCommandProcessGroup(cmd)
	cmd.Dir = workspace
	cmd.Env = append([]string(nil), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create ACP stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create ACP stdout pipe: %w", err)
	}
	stderr := &limitedBuffer{limit: 256 * 1024}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start ACP adapter %q: %w", adapter.ID, err)
	}
	done := make(chan struct{})
	peer := &acpPeer{
		stdin:  stdin,
		stdout: stdout,
		path:   resolvedPath,
		kind:   acpPeerKindProcess,
		cmd:    cmd,
		stderr: stderr,
		done:   done,
	}
	peer.close = func() {
		terminateProcess(cmd)
		close(done)
	}
	return peer, nil
}

func (p *acpPeer) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		go p.close()
	})
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *acpPeer) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *acpPeer) Kind() string {
	if p == nil {
		return ""
	}
	return p.kind
}

func (p *acpPeer) Stderr() string {
	if p == nil || p.stderr == nil {
		return ""
	}
	return p.stderr.String()
}

func (p *acpPeer) DisableAndClearStderr() {
	if p != nil && p.stderr != nil {
		p.stderr.disableAndClear()
	}
}
