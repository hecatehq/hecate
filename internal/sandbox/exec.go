package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

// ResourceLimits caps resources consumed by the shell subprocess and the
// reader goroutines that drain its output. The only knob today is the
// combined-output byte cap: per-call CPU / FD / address-space caps via
// setrlimit would shrink the calling process (the long-running gateway),
// so they're intentionally left to deployment-level controls (systemd
// CPUQuota=, LimitNOFILE=, MemoryMax=, or container --cpus / --memory
// flags). See docs/runtime/sandbox.md for the layer model.
type ResourceLimits struct {
	// MaxOutputBytes caps the combined stdout+stderr that outputCollector
	// will buffer. When the cap is reached the command is cancelled and
	// an OutputLimitExceededError is returned. 0 means no cap.
	MaxOutputBytes int64
}

// DefaultResourceLimits returns the env-driven resource caps that a zero-value
// Command.Limits would receive at execution time. Callers may use this for
// telemetry previews or to pass the same default limits explicitly.
func DefaultResourceLimits() ResourceLimits {
	return defaultLimits()
}

// OutputLimitExceededError is returned when a command's combined stdout and
// stderr output exceeds ResourceLimits.MaxOutputBytes.
type OutputLimitExceededError struct {
	Limit int64
}

func (e *OutputLimitExceededError) Error() string {
	return fmt.Sprintf("command output exceeded limit of %d bytes; configure HECATE_TASK_MAX_OUTPUT_BYTES to widen", e.Limit)
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
	// RTKEnabled runs the command through RTK's shell wrapper after Hecate
	// policy validation and before sandbox wrapping. This is a per-call
	// choice so operators can enable compacted command output for one chat
	// without changing every task subprocess on the gateway.
	RTKEnabled bool
}

const rtkCommand = "rtk"

// RTKAvailable reports whether the optional RTK command-output wrapper is
// visible in the gateway process PATH.
func RTKAvailable() (string, bool) {
	path, err := exec.LookPath(rtkCommand)
	if err != nil {
		return "", false
	}
	return path, true
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

type LocalExecutor struct {
	processes processrunner.Runner
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{processes: processrunner.NewLocalRunner()}
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

	// Merge caller-supplied limits with the gateway defaults. A non-zero
	// caller value always wins; zero means "use the env-driven default".
	if command.Limits.MaxOutputBytes == 0 {
		command.Limits.MaxOutputBytes = defaultLimits().MaxOutputBytes
	}

	// Always derive a cancel-able context so the output collector can kill
	// the child process when the output-size cap is hit.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if command.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		runCtx, timeoutCancel = context.WithTimeout(runCtx, command.Timeout)
		defer timeoutCancel()
	}

	// Layer 2 — wrap the argv with bwrap (Linux) or sandbox-exec (macOS)
	// when one is available. No-op on Windows / Linux without bwrap.
	argv := ShellArgv(command)
	argv = wrappedArgv(argv, workingDirectory, command.Policy.Network)

	collector := newOutputCollector(command.Limits.MaxOutputBytes, cancel, onChunk)
	processResult, err := e.processRunner().RunStreaming(runCtx, processrunner.Request{
		Command:        argv[0],
		Args:           argv[1:],
		Dir:            workingDirectory,
		MaxStdoutBytes: command.Limits.MaxOutputBytes,
		MaxStderrBytes: command.Limits.MaxOutputBytes,
		// Sanitised environment — gateway secrets stay out of scope. See
		// sanitisedEnv() for the allowlist and the reasoning.
		Env: sanitisedEnv(),
	}, collector.handleProcessChunk)
	if limitErr := collector.err(); limitErr != nil {
		result := collector.result()
		result.ExitCode = -1
		return result, limitErr
	}

	result := collector.result()
	if result.Stdout == "" && result.Stderr == "" {
		result.Stdout = processResult.Stdout
		result.Stderr = processResult.Stderr
	}
	result.ExitCode = processResult.ExitCode
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

// ShellArgv returns the argv Hecate will launch for a sandboxed command.
// Policy checks still inspect Command.Command before this wrapper is chosen.
func ShellArgv(command Command) []string {
	base := []string{"sh", "-lc", command.Command}
	if !command.RTKEnabled {
		return base
	}
	argv := make([]string, 0, 1+len(base))
	argv = append(argv, rtkCommand)
	argv = append(argv, base...)
	return argv
}

func (e *LocalExecutor) processRunner() processrunner.Runner {
	if e != nil && e.processes != nil {
		return e.processes
	}
	return processrunner.NewLocalRunner()
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
	if allowedRoot := strings.TrimSpace(request.Policy.AllowedRoot); allowedRoot != "" {
		root, err := filepath.Abs(allowedRoot)
		if err != nil {
			return FileResult{}, err
		}
		rel, err := filepath.Rel(root, targetPath)
		if err != nil {
			return FileResult{}, err
		}
		fsys, err := workspacefs.New(root)
		if err != nil {
			return FileResult{}, err
		}
		if !appendMode {
			path, err := fsys.WriteFile(rel, []byte(request.Content), 0o644)
			if err != nil {
				return FileResult{}, err
			}
			return FileResult{Path: path, BytesWritten: len(request.Content)}, nil
		}
		path, err := fsys.AppendFile(rel, []byte(request.Content), 0o644)
		if err != nil {
			return FileResult{}, err
		}
		return FileResult{Path: path, BytesWritten: len(request.Content)}, nil
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

type outputCollector struct {
	mu       sync.Mutex
	stdout   strings.Builder
	stderr   strings.Builder
	maxBytes int64
	written  int64
	limitErr error
	cancel   context.CancelFunc
	onChunk  func(OutputChunk)
}

func newOutputCollector(maxBytes int64, cancel context.CancelFunc, onChunk func(OutputChunk)) *outputCollector {
	return &outputCollector{maxBytes: maxBytes, cancel: cancel, onChunk: onChunk}
}

func (c *outputCollector) handleProcessChunk(chunk processrunner.Chunk) {
	text := chunk.Text
	if text == "" {
		return
	}

	cancel := false
	c.mu.Lock()
	if c.limitErr != nil {
		c.mu.Unlock()
		return
	}
	if c.maxBytes > 0 {
		remaining := c.maxBytes - c.written
		if remaining <= 0 {
			c.limitErr = &OutputLimitExceededError{Limit: c.maxBytes}
			cancel = true
			c.mu.Unlock()
			if cancel && c.cancel != nil {
				c.cancel()
			}
			return
		}
		if int64(len(text)) > remaining {
			text = text[:remaining]
			c.limitErr = &OutputLimitExceededError{Limit: c.maxBytes}
			cancel = true
		}
	}
	c.written += int64(len(text))
	switch chunk.Stream {
	case "stderr":
		_, _ = c.stderr.WriteString(text)
	default:
		_, _ = c.stdout.WriteString(text)
	}
	c.mu.Unlock()

	if c.onChunk != nil && text != "" {
		c.onChunk(OutputChunk{Stream: chunk.Stream, Text: text})
	}
	if cancel && c.cancel != nil {
		c.cancel()
	}
}

func (c *outputCollector) result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Result{Stdout: c.stdout.String(), Stderr: c.stderr.String()}
}

func (c *outputCollector) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitErr
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

	allowedRoot := strings.TrimSpace(policy.AllowedRoot)
	if allowedRoot == "" {
		return resolvedPath, nil
	}
	root, err := filepath.Abs(allowedRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolvedPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &PolicyError{Reason: fmt.Sprintf("path %q escapes allowed root %q", resolvedPath, root)}
	}
	resolvedPath, err = workspacefs.SafeJoin(root, rel)
	if err != nil {
		return "", &PolicyError{Reason: err.Error()}
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
	allowedRoot := strings.TrimSpace(policy.AllowedRoot)
	if allowedRoot != "" {
		root, err := filepath.Abs(allowedRoot)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, resolvedDirectory)
		if err != nil {
			return "", err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", &PolicyError{Reason: fmt.Sprintf("path %q escapes allowed root %q", resolvedDirectory, root)}
		}
		resolvedDirectory, err = workspacefs.SafeJoin(root, rel)
		if err != nil {
			return "", &PolicyError{Reason: err.Error()}
		}
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

// ValidateCommand applies the same best-effort static command policy checks
// used by LocalExecutor before a process is spawned. Workspace-level process
// surfaces such as terminals call this before constructing their own exec.Cmd
// so shell tools and long-lived terminals reject the same obvious read-only and
// network-policy violations.
func ValidateCommand(command string, policy Policy) error {
	return validateCommand(command, policy)
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

// sanitisedEnv returns the allowlisted subset of the gateway's environment
// that the shell subprocess is allowed to see. Passing an explicit list
// instead of inheriting the full gateway env keeps secrets like
// OPENAI_API_KEY and DATABASE_URL out of scope for tool-spawned commands.
//
// The allowlist is intentionally conservative: only variables required for
// normal program execution are forwarded. Variables needed by git (author
// identity) are also included because agent tasks commonly run git commands.
func sanitisedEnv() []string {
	allowed := []string{
		// Shell / process execution
		"PATH", "SHELL",
		// User identity
		"HOME", "USER", "LOGNAME",
		// Temporary file locations
		"TMPDIR", "TEMP", "TMP",
		// Locale
		"LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_TIME",
		// Timezone
		"TZ",
		// Terminal (some tools branch on its presence)
		"TERM",
		// Git author identity — agent tasks commonly run git commit.
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
	}
	env := make([]string, 0, len(allowed))
	for _, key := range allowed {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// defaultLimits reads sandbox limit configuration from environment
// variables. Set HECATE_TASK_MAX_OUTPUT_BYTES=0 to disable the cap
// entirely. The default of 4 MiB bounds memory growth in the gateway
// reader goroutines for runaway commands.
func defaultLimits() ResourceLimits {
	return ResourceLimits{
		MaxOutputBytes: envInt64("HECATE_TASK_MAX_OUTPUT_BYTES", 4*1024*1024),
	}
}

func envInt64(key string, fallback int64) int64 {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

// ResolveWorkingDirectory is the public wrapper around the existing
// resolveWorkingDirectory helper. It exists so other packages
// (specifically internal/workspace's LocalWorkspace.OpenTerminal) can
// reuse the working-directory escape check without re-implementing
// it. The implementation hasn't changed; only its visibility has.
func ResolveWorkingDirectory(workingDirectory string, policy Policy) (string, error) {
	return resolveWorkingDirectory(workingDirectory, policy)
}

// SanitizedEnv is the public wrapper around sanitisedEnv. Same shape
// as the rationale for ResolveWorkingDirectory above: workspace-level
// callers spawn processes too and need the same allowlisted env to
// avoid leaking gateway secrets into child processes.
func SanitizedEnv() []string {
	return sanitisedEnv()
}
