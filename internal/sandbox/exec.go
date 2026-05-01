package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ResourceLimits caps resources consumed by the shell subprocess and its
// descendants. Zero values leave the current kernel limit in place.
// The gateway populates this from environment variables and embeds it in
// the JSON request sent to the sandboxd worker; the worker applies the
// limits before spawning the shell.
type ResourceLimits struct {
	// MaxOutputBytes caps the combined stdout+stderr that drainProcessOutput
	// will buffer. When the cap is reached the command is cancelled and
	// an OutputLimitExceededError is returned. 0 means no cap.
	MaxOutputBytes int64
	// MaxCPUSeconds caps CPU time via RLIMIT_CPU. 0 means no cap.
	MaxCPUSeconds uint64
	// MaxOpenFiles caps open file descriptors via RLIMIT_NOFILE. 0 means no cap.
	MaxOpenFiles uint64
	// MaxAddressSpaceBytes caps the virtual address space via RLIMIT_AS. 0
	// means no cap. Behaviour on macOS matches RLIMIT_AS semantics (virtual
	// memory); on Linux it is strictly the process address space.
	MaxAddressSpaceBytes uint64
}

// OutputLimitExceededError is returned when a command's combined stdout and
// stderr output exceeds ResourceLimits.MaxOutputBytes.
type OutputLimitExceededError struct {
	Limit int64
}

func (e *OutputLimitExceededError) Error() string {
	return fmt.Sprintf("command output exceeded limit of %d bytes; configure GATEWAY_TASK_MAX_OUTPUT_BYTES to widen", e.Limit)
}

// IsOutputLimitExceeded reports whether err is or wraps an
// OutputLimitExceededError.
func IsOutputLimitExceeded(err error) bool {
	var target *OutputLimitExceededError
	return errors.As(err, &target)
}

type Policy struct {
	AllowedRoot string
	ReadOnly    bool
	// Network is the master gate. When false, any command that
	// looks like it would touch the network (curl, wget, git fetch,
	// http(s) URLs, ...) is rejected before launch. When true, the
	// per-URL constraints below further bound what's allowed.
	Network bool
	// AllowedHosts, when non-empty AND Network is true, restricts
	// HTTP-style URLs in the command to exactly these hostnames
	// (no subdomain wildcards). Empty means "all public hosts
	// allowed". Mirrors the agent_loop http_request tool's
	// allowlist semantics so a single config knob — e.g.
	// "github.com,registry.npmjs.org" — applies to both.
	AllowedHosts []string
	// AllowPrivateIPs, when false AND Network is true, blocks URLs
	// whose host parses as a loopback / RFC1918 / link-local IP
	// literal (10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, ::1,
	// fc00::/7, fe80::/10). Doesn't resolve DNS — that would slow
	// every shell invocation and TOCTOU-race anyway. Operators who
	// need internal addresses (sidecars, the gateway's own admin
	// API) flip this to true; the threat model should be documented
	// before doing so.
	AllowPrivateIPs bool
}

type Command struct {
	Command          string
	WorkingDirectory string
	Timeout          time.Duration
	Policy           Policy
	Limits           ResourceLimits
}

type FileRequest struct {
	Path             string
	Content          string
	WorkingDirectory string
	Policy           Policy
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type FileResult struct {
	Path         string
	BytesWritten int
}

type OutputChunk struct {
	Stream string
	Text   string
}

type Executor interface {
	Run(ctx context.Context, command Command) (Result, error)
	RunStreaming(ctx context.Context, command Command, onChunk func(OutputChunk)) (Result, error)
	WriteFile(ctx context.Context, request FileRequest) (FileResult, error)
	AppendFile(ctx context.Context, request FileRequest) (FileResult, error)
}

type PolicyError struct {
	Reason string
}

func (e *PolicyError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "sandbox policy denied"
	}
	return "sandbox policy denied: " + e.Reason
}

func IsPolicyDenied(err error) bool {
	var policyErr *PolicyError
	return errors.As(err, &policyErr)
}

type LocalExecutor struct{}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

func (e *LocalExecutor) Run(ctx context.Context, command Command) (Result, error) {
	return e.RunStreaming(ctx, command, nil)
}

func (e *LocalExecutor) RunStreaming(ctx context.Context, command Command, onChunk func(OutputChunk)) (Result, error) {
	workingDirectory, err := resolveWorkingDirectory(command.WorkingDirectory, command.Policy)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	if err := validateCommand(command.Command, command.Policy); err != nil {
		return Result{ExitCode: -1}, err
	}

	// Always derive a cancel-able context so drainProcessOutput can kill
	// the child process when the output-size cap is hit.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if command.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		runCtx, timeoutCancel = context.WithTimeout(runCtx, command.Timeout)
		defer timeoutCancel()
	}

	cmd := exec.CommandContext(runCtx, "sh", "-lc", command.Command)
	if workingDirectory != "" {
		cmd.Dir = workingDirectory
	}

	// Apply process resource limits (CPU, file descriptors, address space)
	// before the child process starts so it inherits them. Each sandboxd
	// worker handles exactly one command, so limiting the worker process is
	// equivalent to limiting the command it spawns.
	applyProcessResourceLimits(command.Limits)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, err
	}

	streamEvents := make(chan outputEvent, 8)
	go streamPipe(stdoutPipe, "stdout", streamEvents)
	go streamPipe(stderrPipe, "stderr", streamEvents)

	// Watchdog: close the pipe read-ends whenever runCtx ends so that
	// streamPipe goroutines unblock even when orphan grandchildren of sh
	// keep the write-end open. This covers the context-timeout and
	// external-cancel paths. cancelWithPipes (below) handles the same
	// problem for the synchronous output-cap path — both may fire; the
	// second Close() on each pipe is a harmless os.ErrClosed.
	go func() {
		<-runCtx.Done()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}()

	// cancelWithPipes kills the child process AND closes the pipe read-ends
	// synchronously so drainProcessOutput can return immediately when the
	// output-size cap is hit (without waiting for the watchdog to be
	// scheduled). The watchdog above provides the same guarantee for the
	// timeout / external-cancel paths.
	cancelWithPipes := func() {
		cancel()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}

	// drainProcessOutput calls cancelWithPipes() if the output cap is
	// exceeded so the drain loop always terminates cleanly.
	readErr := drainProcessOutput(streamEvents, &stdout, &stderr, onChunk, command.Limits.MaxOutputBytes, cancelWithPipes)
	if readErr != nil {
		_ = cmd.Wait()
		return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1}, readErr
	}

	err = cmd.Wait()
	result := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}

	if errors.Is(runCtx.Err(), context.Canceled) {
		result.ExitCode = -1
		return result, context.Canceled
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		return result, runCtx.Err()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, err
	}
	result.ExitCode = -1
	return result, err
}

func (e *LocalExecutor) WriteFile(_ context.Context, request FileRequest) (FileResult, error) {
	return writeFile(request, false)
}

func (e *LocalExecutor) AppendFile(_ context.Context, request FileRequest) (FileResult, error) {
	return writeFile(request, true)
}

func writeFile(request FileRequest, appendMode bool) (FileResult, error) {
	if request.Policy.ReadOnly {
		return FileResult{}, &PolicyError{Reason: "write access is disabled"}
	}
	targetPath, err := ResolvePath(request.WorkingDirectory, request.Path, request.Policy)
	if err != nil {
		return FileResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return FileResult{}, err
	}
	if !appendMode {
		if err := os.WriteFile(targetPath, []byte(request.Content), 0o644); err != nil {
			return FileResult{}, err
		}
		return FileResult{Path: targetPath, BytesWritten: len(request.Content)}, nil
	}
	handle, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return FileResult{}, err
	}
	defer handle.Close()
	if _, err := io.WriteString(handle, request.Content); err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: targetPath, BytesWritten: len(request.Content)}, nil
}

type outputEvent struct {
	chunk OutputChunk
	err   error
	done  bool
}

func streamPipe(pipe io.ReadCloser, streamName string, events chan<- outputEvent) {
	defer pipe.Close()

	reader := bufio.NewReader(pipe)
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			events <- outputEvent{
				chunk: OutputChunk{Stream: streamName, Text: string(buffer[:n])},
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			events <- outputEvent{done: true}
			return
		}
		if errors.Is(err, os.ErrClosed) {
			events <- outputEvent{done: true}
			return
		}
		events <- outputEvent{done: true, err: err}
		return
	}
}

// drainProcessOutput reads output events from the streaming goroutines until
// both stdout and stderr report done. cancelCmd is called when the output cap
// is exceeded — it cancels the exec.CommandContext that owns the child process,
// causing the pipes to close and the drain loop to terminate without leaving
// goroutines blocked.
func drainProcessOutput(events <-chan outputEvent, stdout, stderr *bytes.Buffer, onChunk func(OutputChunk), maxBytes int64, cancelCmd context.CancelFunc) error {
	var (
		readErr          error
		totalBytes       int64
		completedStreams int
	)
	for completedStreams < 2 {
		event := <-events
		if event.done {
			completedStreams++
			if event.err != nil && readErr == nil {
				readErr = event.err
			}
			continue
		}
		// If we already have an error (e.g. output cap hit), keep draining
		// events without accumulating them so the pipe goroutines can finish.
		if readErr != nil {
			continue
		}

		n := int64(len(event.chunk.Text))
		if maxBytes > 0 {
			totalBytes += n
			if totalBytes > maxBytes {
				readErr = &OutputLimitExceededError{Limit: maxBytes}
				cancelCmd() // kill the child; pipes close → goroutines send done events
				continue
			}
		}

		switch event.chunk.Stream {
		case "stdout":
			stdout.WriteString(event.chunk.Text)
		case "stderr":
			stderr.WriteString(event.chunk.Text)
		}
		if onChunk != nil {
			onChunk(event.chunk)
		}
	}
	return readErr
}

func ResolvePath(workingDirectory, targetPath string, policy Policy) (string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return "", fmt.Errorf("target path is required")
	}

	baseDirectory := strings.TrimSpace(workingDirectory)
	if baseDirectory == "" {
		baseDirectory = "."
	}
	var err error
	baseDirectory, err = filepath.Abs(baseDirectory)
	if err != nil {
		return "", err
	}

	resolvedPath := targetPath
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(baseDirectory, resolvedPath)
	}
	resolvedPath, err = filepath.Abs(resolvedPath)
	if err != nil {
		return "", err
	}

	if err := ensureWithinAllowedRoot(resolvedPath, policy); err != nil {
		return "", err
	}
	return resolvedPath, nil
}

func resolveWorkingDirectory(workingDirectory string, policy Policy) (string, error) {
	if strings.TrimSpace(workingDirectory) == "" {
		if strings.TrimSpace(policy.AllowedRoot) == "" {
			return "", nil
		}
		workingDirectory = policy.AllowedRoot
	}
	resolvedDirectory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", err
	}
	if err := ensureWithinAllowedRoot(resolvedDirectory, policy); err != nil {
		return "", err
	}
	return resolvedDirectory, nil
}

func ensureWithinAllowedRoot(path string, policy Policy) error {
	allowedRoot := strings.TrimSpace(policy.AllowedRoot)
	if allowedRoot == "" {
		return nil
	}
	resolvedRoot, err := filepath.Abs(allowedRoot)
	if err != nil {
		return err
	}
	relativePath, err := filepath.Rel(resolvedRoot, path)
	if err != nil {
		return err
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return &PolicyError{Reason: fmt.Sprintf("path %q escapes allowed root %q", path, resolvedRoot)}
	}
	return nil
}

func validateCommand(command string, policy Policy) error {
	if policy.ReadOnly && commandMutatesState(command) {
		return &PolicyError{Reason: "write access is disabled"}
	}
	if !policy.Network {
		if commandRequestsNetwork(command) {
			return &PolicyError{Reason: "network access is disabled"}
		}
		return nil
	}
	// Network is allowed; enforce per-URL constraints (scheme
	// allowlist, optional host allowlist, private-IP block) on
	// any HTTP/HTTPS URL the command spells out. This is best-
	// effort static parsing — clever obfuscation (base64, env
	// var indirection, raw sockets via `nc`) bypasses it. For
	// hard isolation, run the gateway in a network namespace or
	// behind a filtering egress proxy.
	for _, raw := range extractCommandURLs(command) {
		if err := validateURLAgainstPolicy(raw, policy); err != nil {
			return err
		}
	}
	return nil
}

// extractCommandURLs pulls out http(s) URLs that appear as
// whitespace-separated tokens in the command string. Designed
// for the common cases — `curl https://x`, `wget http://y`,
// `git clone https://github.com/foo/bar` — without trying to
// parse the shell language. Strips trailing shell punctuation
// (`;`, `&`, `|`, `)`, `"`, `'`) so a quoted URL doesn't end up
// with `;` in its host.
func extractCommandURLs(command string) []string {
	var out []string
	for _, token := range strings.Fields(command) {
		// A token can have a leading quote / paren we want to
		// drop before checking the prefix.
		token = strings.TrimLeft(token, "'\"`(<")
		if !(strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://")) {
			continue
		}
		token = strings.TrimRight(token, ";&|)>'\"`,")
		if token != "" {
			out = append(out, token)
		}
	}
	return out
}

// validateURLAgainstPolicy applies scheme + host + private-IP
// rules to a single URL. Returns a PolicyError naming the
// specific reason the URL was rejected so the operator (and the
// LLM, when this surfaces as a tool error) can fix it.
func validateURLAgainstPolicy(raw string, policy Policy) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		// Couldn't parse — be safe and reject when we can't
		// determine the host. Real curl invocations parse
		// cleanly; a malformed URL is suspicious here.
		return &PolicyError{Reason: fmt.Sprintf("URL %q is not parseable", raw)}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &PolicyError{Reason: fmt.Sprintf("scheme %q not allowed (http or https only)", u.Scheme)}
	}
	host := u.Hostname()
	if host == "" {
		return &PolicyError{Reason: fmt.Sprintf("URL %q has no host", raw)}
	}
	if !policy.AllowPrivateIPs {
		if reason := checkLiteralPrivateIP(host); reason != "" {
			return &PolicyError{Reason: fmt.Sprintf("host %q is %s", host, reason)}
		}
	}
	if len(policy.AllowedHosts) > 0 && !hostInAllowlist(host, policy.AllowedHosts) {
		return &PolicyError{Reason: fmt.Sprintf("host %q is not in the allowlist", host)}
	}
	return nil
}

// checkLiteralPrivateIP returns a non-empty reason when the host
// parses as an IP literal in a blocked range. Hostnames (which
// would require DNS resolution to classify) return "" — we
// deliberately don't resolve DNS here; see the Policy comment.
func checkLiteralPrivateIP(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsPrivate():
		return "a private network address"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return "link-local"
	case ip.IsUnspecified():
		return "the unspecified address"
	case ip.IsMulticast():
		return "multicast"
	}
	return ""
}

func hostInAllowlist(host string, allowed []string) bool {
	host = strings.ToLower(host)
	for _, h := range allowed {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

func commandRequestsNetwork(command string) bool {
	lowerCommand := strings.ToLower(command)
	patterns := []string{
		"curl ",
		"wget ",
		"ping ",
		"ssh ",
		"scp ",
		"nc ",
		"netcat ",
		"telnet ",
		"ftp ",
		"http://",
		"https://",
		"git clone ",
		"git fetch ",
		"git pull ",
		"git push ",
		"git ls-remote ",
	}
	return containsAnyPattern(lowerCommand, patterns)
}

func commandMutatesState(command string) bool {
	lowerCommand := strings.ToLower(command)
	patterns := []string{
		"rm ",
		"mv ",
		"cp ",
		"mkdir ",
		"touch ",
		"tee ",
		"sed -i",
		"git add ",
		"git apply ",
		"git cherry-pick ",
		"git clean ",
		"git commit ",
		"git merge ",
		"git push ",
		"git rebase ",
		"git restore ",
		"git revert ",
		"git switch ",
		"git checkout ",
	}
	if containsAnyPattern(lowerCommand, patterns) {
		return true
	}
	return strings.Contains(lowerCommand, " >") || strings.Contains(lowerCommand, ">>")
}

func containsAnyPattern(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(value, pattern) {
			return true
		}
	}
	return false
}
