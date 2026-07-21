package agentadapters

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const agentDiagnosticOutputLimit = int64(256 * 1024)

type agentDiagnosticOutput struct {
	stdout          string
	stderr          string
	stdoutTruncated bool
	stderrTruncated bool
}

func (o agentDiagnosticOutput) combined() string {
	if o.stdout == "" {
		return o.stderr
	}
	if o.stderr == "" {
		return o.stdout
	}
	return o.stdout + "\n" + o.stderr
}

// runAgentDiagnostic owns version, auth, help, and model-list subprocesses as
// the same process unit used for a direct ACP peer. Output is bounded per
// stream, and closing the process unit kills provider descendants as well as
// the leader. Callers must still place an appropriate deadline on ctx.
func runAgentDiagnostic(ctx context.Context, command string, args, env []string) (agentDiagnosticOutput, error) {
	if strings.TrimSpace(command) == "" {
		return agentDiagnosticOutput{}, fmt.Errorf("diagnostic command is required")
	}
	cmd := exec.CommandContext(ctx, command, append([]string(nil), args...)...)
	cmd.Env = append([]string(nil), env...)
	attachProcessTree, releaseProcessTree, err := prepareAgentProcessTree(cmd)
	if err != nil {
		return agentDiagnosticOutput{}, fmt.Errorf("prepare diagnostic process tree: %w", err)
	}
	defer releaseProcessTree()

	stdout := &limitedBuffer{limit: agentDiagnosticOutputLimit}
	stderr := &limitedBuffer{limit: agentDiagnosticOutputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return snapshotAgentDiagnosticOutput(stdout, stderr), err
	}
	if err := attachProcessTree(); err != nil {
		terminateProcess(cmd)
		return snapshotAgentDiagnosticOutput(stdout, stderr), fmt.Errorf("supervise diagnostic process tree: %w", err)
	}
	err = cmd.Wait()
	// The leader can exit successfully after spawning a detached-from-stdio
	// descendant. Wait has already reaped the leader, so cancel only the owned
	// process unit here; on Unix this signals the process group, and on Windows
	// it terminates the Job Object. Do not call terminateProcess, which would
	// invoke Wait a second time.
	if cmd.Cancel != nil {
		_ = cmd.Cancel()
	}
	return snapshotAgentDiagnosticOutput(stdout, stderr), err
}

func snapshotAgentDiagnosticOutput(stdout, stderr *limitedBuffer) agentDiagnosticOutput {
	stdout.mu.Lock()
	stdoutText := stdout.Buffer.String()
	stdoutTruncated := stdout.truncated
	stdout.mu.Unlock()
	stderr.mu.Lock()
	stderrText := stderr.Buffer.String()
	stderrTruncated := stderr.truncated
	stderr.mu.Unlock()
	return agentDiagnosticOutput{
		stdout:          stdoutText,
		stderr:          stderrText,
		stdoutTruncated: stdoutTruncated,
		stderrTruncated: stderrTruncated,
	}
}
