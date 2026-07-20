package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
)

func TestLocalExecutorSeparatesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `printf 'hello stdout'; printf 'hello stderr' >&2`,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "hello stdout" {
		t.Fatalf("stdout = %q, want hello stdout", result.Stdout)
	}
	if result.Stderr != "hello stderr" {
		t.Fatalf("stderr = %q, want hello stderr", result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", result.ExitCode)
	}
}

func TestLocalExecutorUsesRTKWhenEnabled(t *testing.T) {
	reset := SetWrapperForTesting(WrapperNone)
	defer reset()

	process := &recordingProcessRunner{
		result: processrunner.Result{Stdout: "compacted", ExitCode: 0},
	}
	exec := &LocalExecutor{processes: process}
	result, err := exec.Run(context.Background(), Command{
		Command:    `printf 'compacted'`,
		Timeout:    5 * time.Second,
		RTKEnabled: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "compacted" {
		t.Fatalf("stdout = %q, want compacted", result.Stdout)
	}
	if process.req.Command != "rtk" {
		t.Fatalf("process command = %q, want rtk", process.req.Command)
	}
	if got := strings.Join(process.req.Args, "\x00"); got != "sh\x00-lc\x00printf 'compacted'" {
		t.Fatalf("process args = %q, want RTK shell argv", got)
	}
}

func TestLocalExecutorStreamsThroughProcessRunner(t *testing.T) {
	reset := SetWrapperForTesting(WrapperNone)
	defer reset()

	workspace := t.TempDir()
	process := &recordingProcessRunner{
		result: processrunner.Result{Stdout: "streamed", ExitCode: 0},
		chunks: []processrunner.Chunk{{Stream: "stdout", Text: "streamed"}},
	}
	exec := &LocalExecutor{processes: process}

	var chunks []OutputChunk
	result, err := exec.RunStreaming(context.Background(), Command{
		Command:          `printf streamed`,
		WorkingDirectory: workspace,
		Timeout:          time.Second,
		Limits:           ResourceLimits{MaxOutputBytes: 10},
	}, func(chunk OutputChunk) {
		chunks = append(chunks, chunk)
	})

	if err != nil {
		t.Fatalf("RunStreaming() error = %v", err)
	}
	if result.Stdout != "streamed" {
		t.Fatalf("stdout = %q, want streamed", result.Stdout)
	}
	if process.req.Command != "sh" {
		t.Fatalf("process command = %q, want sh", process.req.Command)
	}
	if got := strings.Join(process.req.Args, "\x00"); got != "-lc\x00printf streamed" {
		t.Fatalf("process args = %q, want shell argv", got)
	}
	if process.req.Dir != workspace {
		t.Fatalf("process dir = %q, want %q", process.req.Dir, workspace)
	}
	if process.req.MaxStdoutBytes != 10 || process.req.MaxStderrBytes != 10 {
		t.Fatalf("process caps = stdout %d stderr %d, want 10/10", process.req.MaxStdoutBytes, process.req.MaxStderrBytes)
	}
	if len(chunks) != 1 || chunks[0].Text != "streamed" {
		t.Fatalf("chunks = %+v, want streamed chunk", chunks)
	}
}

func TestLocalExecutorOutputLimitCancelsThroughProcessRunner(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `printf abcdef`,
		Timeout: time.Second,
		Limits:  ResourceLimits{MaxOutputBytes: 3},
	})

	if !IsOutputLimitExceeded(err) {
		t.Fatalf("Run() error = %v, want output limit exceeded", err)
	}
	if result.Stdout != "abc" {
		t.Fatalf("stdout = %q, want capped abc", result.Stdout)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit_code = %d, want -1", result.ExitCode)
	}
}

func TestShellArgvUsesRTKOnlyWhenEnabled(t *testing.T) {
	got := strings.Join(ShellArgv(Command{Command: "go test ./...", RTKEnabled: true}), "\x00")
	want := strings.Join([]string{"rtk", "sh", "-lc", "go test ./..."}, "\x00")
	if got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	got = strings.Join(ShellArgv(Command{Command: "go test ./..."}), "\x00")
	want = strings.Join([]string{"sh", "-lc", "go test ./..."}, "\x00")
	if got != want {
		t.Fatalf("plain argv = %q, want %q", got, want)
	}
}

type recordingProcessRunner struct {
	req    processrunner.Request
	result processrunner.Result
	chunks []processrunner.Chunk
}

func (r *recordingProcessRunner) Run(ctx context.Context, req processrunner.Request) (processrunner.Result, error) {
	return r.RunStreaming(ctx, req, nil)
}

func (r *recordingProcessRunner) RunStreaming(_ context.Context, req processrunner.Request, onChunk func(processrunner.Chunk)) (processrunner.Result, error) {
	r.req = req
	for _, chunk := range r.chunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	return r.result, nil
}

func TestRTKAvailableReportsPathPresence(t *testing.T) {
	dir := t.TempDir()
	rtkPath := filepath.Join(dir, "rtk")
	if err := os.WriteFile(rtkPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(rtk) error = %v", err)
	}
	t.Setenv("PATH", dir)

	gotPath, ok := RTKAvailable()
	if !ok {
		t.Fatal("RTKAvailable() ok = false, want true")
	}
	if gotPath != rtkPath {
		t.Fatalf("RTKAvailable() path = %q, want %q", gotPath, rtkPath)
	}
}

func TestRTKAvailableReportsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	gotPath, ok := RTKAvailable()
	if ok {
		t.Fatal("RTKAvailable() ok = true, want false")
	}
	if gotPath != "" {
		t.Fatalf("RTKAvailable() path = %q, want empty", gotPath)
	}
}

func TestLocalExecutorTimeout(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `sleep 1`,
		Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit_code = %d, want -1", result.ExitCode)
	}
}

func TestLocalExecutorDeniedByNetworkPolicy(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `curl https://example.com`,
		Policy:  Policy{Network: false},
		Timeout: time.Second,
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("Run() error = %v, want policy denial", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit_code = %d, want -1", result.ExitCode)
	}
}

func TestLocalExecutorDeniedByReadOnlyPolicy(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `touch denied.txt`,
		Policy:  Policy{ReadOnly: true},
		Timeout: time.Second,
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("Run() error = %v, want policy denial", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit_code = %d, want -1", result.ExitCode)
	}
}

func TestResolvePathRejectsEscapeFromAllowedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workingDirectory := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	_, err := ResolvePath(workingDirectory, "../outside.txt", Policy{AllowedRoot: workingDirectory})
	if !IsPolicyDenied(err) {
		t.Fatalf("ResolvePath() error = %v, want policy denial", err)
	}
}

func TestLocalExecutor_NetworkPolicy_BlocksPrivateIPLiteral(t *testing.T) {
	// When Network=true (operator allowed network) the sandbox still
	// rejects URLs whose host parses as a private/loopback IP literal
	// unless AllowPrivateIPs is also true. Mirrors the http_request
	// tool's SSRF guard so a single config knob can apply to both
	// surfaces.
	t.Parallel()
	exec := NewLocalExecutor()
	result, err := exec.Run(context.Background(), Command{
		Command: `curl http://127.0.0.1:8080/secrets`,
		Policy:  Policy{Network: true},
		Timeout: time.Second,
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("Run() error = %v, want policy denial for loopback URL", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("error = %q, want mention of loopback", err.Error())
	}
	if result.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1", result.ExitCode)
	}
}

func TestLocalExecutor_NetworkPolicy_AllowsPrivateIPWhenFlagSet(t *testing.T) {
	// AllowPrivateIPs=true skips the private-IP block; useful for
	// agents that legitimately need to call internal sidecars or the
	// gateway's own admin API. Operators should document the threat
	// model before flipping this on.
	t.Parallel()
	exec := NewLocalExecutor()
	// Use `echo` with a 127.0.0.1 URL — we just need the validator
	// to PASS (no actual HTTP); the echo command runs locally.
	_, err := exec.Run(context.Background(), Command{
		Command: `echo http://127.0.0.1/probe`,
		Policy:  Policy{Network: true, AllowPrivateIPs: true},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() with AllowPrivateIPs=true should pass validation; got %v", err)
	}
}

func TestLocalExecutor_NetworkPolicy_HostAllowlistEnforced(t *testing.T) {
	// Non-empty AllowedHosts restricts URLs to exactly those hostnames
	// (no subdomain wildcarding). A request to a host outside the
	// allowlist is rejected before the command runs.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `curl https://evil.example.com/data`,
		Policy: Policy{
			Network:      true,
			AllowedHosts: []string{"github.com", "registry.npmjs.org"},
		},
		Timeout: time.Second,
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("Run() error = %v, want policy denial for off-allowlist host", err)
	}
	if !strings.Contains(err.Error(), "evil.example.com") || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("error = %q, want mention of host + allowlist", err.Error())
	}
}

func TestLocalExecutor_NetworkPolicy_HostAllowlistAllowsListed(t *testing.T) {
	// A URL whose host IS on the allowlist passes validation. We use
	// an obviously-fake-but-allowed host and `echo` rather than
	// `curl` so we don't actually hit the network in unit tests.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `echo https://github.com/foo/bar.git`,
		Policy: Policy{
			Network:      true,
			AllowedHosts: []string{"github.com"},
		},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() with allowed host should pass; got %v", err)
	}
}

func TestLocalExecutor_NetworkPolicy_HostAllowlistCaseInsensitive(t *testing.T) {
	// Hostnames are case-insensitive per RFC 1035; the allowlist
	// check shouldn't reject a valid URL just because of casing.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `echo https://GitHub.com/foo`,
		Policy: Policy{
			Network:      true,
			AllowedHosts: []string{"github.com"},
		},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("case-insensitive host match should pass; got %v", err)
	}
}

func TestLocalExecutor_NetworkPolicy_PublicHTTPSPasses(t *testing.T) {
	// Baseline: with Network=true and no allowlist, a public
	// http(s) URL passes validation. Pinned to make sure the new
	// validator doesn't accidentally over-block when the operator
	// hasn't set any constraints beyond the master Network=true.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `echo https://example.com/ok`,
		Policy:  Policy{Network: true},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("public https URL with no constraints should pass; got %v", err)
	}
}

func TestLocalExecutor_NetworkPolicy_MultipleURLsAllChecked(t *testing.T) {
	// Important non-bypass guarantee: when a single command spells
	// out several URLs (a `curl` chained with a `git clone`, or a
	// shell one-liner that hits two endpoints), the validator must
	// reject if ANY URL fails — not just the first one. Otherwise
	// an operator could sneak a denied host past the check by
	// putting an allowed URL first.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `curl https://github.com/ok && curl https://evil.example.com/data`,
		Policy: Policy{
			Network:      true,
			AllowedHosts: []string{"github.com"},
		},
		Timeout: time.Second,
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("Run() error = %v, want policy denial for second-position off-allowlist URL", err)
	}
	if !strings.Contains(err.Error(), "evil.example.com") {
		t.Errorf("error = %q, want it to name the bad host (proves we didn't stop after the first allowed URL)", err.Error())
	}
}

func TestLocalExecutor_NetworkPolicy_URLWithPortAndUserinfo(t *testing.T) {
	// `url.Parse` extracts the bare hostname via Hostname(),
	// stripping the port and userinfo. The allowlist check should
	// match on the bare host, so https://user:pass@github.com:8443/x
	// passes when github.com is allowed. Without this test, a
	// regression that compared u.Host (which includes port) against
	// the allowlist would silently break common URLs.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `echo https://user:pass@github.com:8443/foo`,
		Policy: Policy{
			Network:      true,
			AllowedHosts: []string{"github.com"},
		},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("URL with port + userinfo should pass when bare host is allowed; got %v", err)
	}
}

func TestLocalExecutor_NetworkPolicy_PrivateIPBlockedRegardlessOfAllowlist(t *testing.T) {
	// The private-IP block is independent of AllowedHosts: even
	// when AllowedHosts is empty (no host restriction) and the
	// operator hasn't flipped AllowPrivateIPs, a 10.x address must
	// still be rejected. Covers the common misconfiguration of
	// "I just want network on" missing the private-IP exposure.
	t.Parallel()
	exec := NewLocalExecutor()
	_, err := exec.Run(context.Background(), Command{
		Command: `curl http://10.0.0.1/internal`,
		Policy:  Policy{Network: true},
		Timeout: time.Second,
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("Run() error = %v, want policy denial for RFC1918 host", err)
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error = %q, want mention of 'private'", err.Error())
	}
}

func TestLocalExecutorWritesFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	exec := NewLocalExecutor()
	result, err := exec.WriteFile(context.Background(), FileRequest{
		WorkingDirectory: root,
		Path:             "note.txt",
		Content:          "hello sandbox",
		Policy:           Policy{AllowedRoot: root},
	})
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "hello sandbox" {
		t.Fatalf("file contents = %q, want hello sandbox", string(content))
	}
	if result.Path == "" {
		t.Fatal("result path is empty")
	}
}

func TestLocalExecutorWriteFileRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	exec := NewLocalExecutor()
	_, err := exec.WriteFile(context.Background(), FileRequest{
		WorkingDirectory: root,
		Path:             "linked/escape.txt",
		Content:          "nope",
		Policy:           Policy{AllowedRoot: root},
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("WriteFile() error = %v, want policy denial", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file stat error = %v, want not exist", statErr)
	}
}

func TestLocalExecutorAppendFileRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	exec := NewLocalExecutor()
	_, err := exec.AppendFile(context.Background(), FileRequest{
		WorkingDirectory: root,
		Path:             "linked/escape.txt",
		Content:          "nope",
		Policy:           Policy{AllowedRoot: root},
	})
	if !IsPolicyDenied(err) {
		t.Fatalf("AppendFile() error = %v, want policy denial", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file stat error = %v, want not exist", statErr)
	}
}
