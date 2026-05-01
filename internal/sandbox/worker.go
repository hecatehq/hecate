package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	workerOperationRun        = "run"
	workerOperationWriteFile  = "write_file"
	workerOperationAppendFile = "append_file"
)

type WorkerExecutor struct{}

type workerRequest struct {
	Operation string       `json:"operation"`
	Command   *Command     `json:"command,omitempty"`
	File      *FileRequest `json:"file,omitempty"`
}

type workerResponse struct {
	Result     Result     `json:"result"`
	FileResult FileResult `json:"file_result"`
	Error      string     `json:"error,omitempty"`
	ErrorKind  string     `json:"error_kind,omitempty"`
}

type workerEvent struct {
	Type      string          `json:"type"`
	Stream    string          `json:"stream,omitempty"`
	Text      string          `json:"text,omitempty"`
	Result    Result          `json:"result"`
	Error     string          `json:"error,omitempty"`
	ErrorKind string          `json:"error_kind,omitempty"`
	Response  *workerResponse `json:"response,omitempty"`
}

var sandboxdBinary struct {
	once sync.Once
	path string
	err  error
}

func NewWorkerExecutor() *WorkerExecutor {
	return &WorkerExecutor{}
}

func (e *WorkerExecutor) Run(ctx context.Context, command Command) (Result, error) {
	return e.RunStreaming(ctx, command, nil)
}

func (e *WorkerExecutor) RunStreaming(ctx context.Context, command Command, onChunk func(OutputChunk)) (Result, error) {
	// Merge caller-supplied limits with the gateway defaults. A non-zero
	// caller value always wins; zero means "use the default".
	defaults := defaultWorkerLimits()
	if command.Limits.MaxOutputBytes == 0 {
		command.Limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if command.Limits.MaxCPUSeconds == 0 {
		command.Limits.MaxCPUSeconds = defaults.MaxCPUSeconds
	}
	if command.Limits.MaxOpenFiles == 0 {
		command.Limits.MaxOpenFiles = defaults.MaxOpenFiles
	}
	if command.Limits.MaxAddressSpaceBytes == 0 {
		command.Limits.MaxAddressSpaceBytes = defaults.MaxAddressSpaceBytes
	}

	result, err := invokeStreamingWorker(ctx, workerRequest{
		Operation: workerOperationRun,
		Command:   &command,
	}, command.Timeout+5*time.Second, onChunk)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	return result, nil
}

func (e *WorkerExecutor) WriteFile(ctx context.Context, request FileRequest) (FileResult, error) {
	return invokeFileWorker(ctx, workerOperationWriteFile, request)
}

func (e *WorkerExecutor) AppendFile(ctx context.Context, request FileRequest) (FileResult, error) {
	return invokeFileWorker(ctx, workerOperationAppendFile, request)
}

func ServeWorker(ctx context.Context, input io.Reader, output io.Writer) error {
	var request workerRequest
	if err := json.NewDecoder(input).Decode(&request); err != nil {
		return err
	}

	executor := NewLocalExecutor()
	response := workerResponse{}

	switch request.Operation {
	case workerOperationRun:
		if request.Command == nil {
			return fmt.Errorf("command request is required")
		}
		encoder := json.NewEncoder(output)
		result, err := executor.RunStreaming(ctx, *request.Command, func(chunk OutputChunk) {
			_ = encoder.Encode(workerEvent{
				Type:   "chunk",
				Stream: chunk.Stream,
				Text:   chunk.Text,
			})
		})
		finalEvent := workerEvent{
			Type:   "result",
			Result: result,
		}
		if err != nil {
			finalEvent.Error = err.Error()
			finalEvent.ErrorKind = classifyWorkerError(err)
		}
		return encoder.Encode(finalEvent)
	case workerOperationWriteFile:
		if request.File == nil {
			return fmt.Errorf("file request is required")
		}
		result, err := executor.WriteFile(ctx, *request.File)
		response.FileResult = result
		if err != nil {
			response.Error = err.Error()
			response.ErrorKind = classifyWorkerError(err)
		}
	case workerOperationAppendFile:
		if request.File == nil {
			return fmt.Errorf("file request is required")
		}
		result, err := executor.AppendFile(ctx, *request.File)
		response.FileResult = result
		if err != nil {
			response.Error = err.Error()
			response.ErrorKind = classifyWorkerError(err)
		}
	default:
		return fmt.Errorf("unsupported worker operation %q", request.Operation)
	}

	return json.NewEncoder(output).Encode(workerEvent{
		Type:     "response",
		Response: &response,
	})
}

func invokeWorker(ctx context.Context, request workerRequest, defaultTimeout time.Duration) (workerResponse, error) {
	cmd, stderr, execCtx, cancel, err := prepareWorkerCommand(ctx, request, defaultTimeout)
	if err != nil {
		return workerResponse{}, err
	}
	defer cancel()
	stdout := &bytes.Buffer{}
	cmd.Stdout = stdout

	if err := cmd.Run(); err != nil {
		return workerResponse{}, workerCommandError(execCtx, stderr, err)
	}

	return decodeWorkerResponse(stdout)
}

func invokeStreamingWorker(ctx context.Context, request workerRequest, defaultTimeout time.Duration, onChunk func(OutputChunk)) (Result, error) {
	cmd, stderr, execCtx, cancel, err := prepareWorkerCommand(ctx, request, defaultTimeout)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	defer cancel()
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, err
	}

	decoder := json.NewDecoder(stdoutPipe)
	var finalResult Result
	var finalErr error
	for {
		var event workerEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			finalErr = err
			break
		}
		switch event.Type {
		case "chunk":
			if onChunk != nil {
				onChunk(OutputChunk{Stream: event.Stream, Text: event.Text})
			}
		case "result":
			finalResult = event.Result
			if event.Error != "" {
				finalErr = decodeWorkerError(event.Error, event.ErrorKind)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return finalResult, workerCommandError(execCtx, stderr, err)
	}
	return finalResult, finalErr
}

func invokeFileWorker(ctx context.Context, operation string, request FileRequest) (FileResult, error) {
	response, err := invokeWorker(ctx, workerRequest{
		Operation: operation,
		File:      &request,
	}, 5*time.Second)
	if err != nil {
		return FileResult{}, err
	}
	if response.Error != "" {
		return response.FileResult, decodeWorkerError(response.Error, response.ErrorKind)
	}
	return response.FileResult, nil
}

func prepareWorkerCommand(ctx context.Context, request workerRequest, defaultTimeout time.Duration) (*exec.Cmd, *bytes.Buffer, context.Context, context.CancelFunc, error) {
	execCtx, cancel := workerExecContext(ctx, defaultTimeout)
	payload, err := json.Marshal(request)
	if err != nil {
		cancel()
		return nil, nil, nil, nil, err
	}
	cmd, stderr, err := newWorkerCommand(execCtx, payload)
	if err != nil {
		cancel()
		return nil, nil, nil, nil, err
	}
	return cmd, stderr, execCtx, cancel, nil
}

func workerExecContext(ctx context.Context, defaultTimeout time.Duration) (context.Context, context.CancelFunc) {
	if defaultTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

func newWorkerCommand(ctx context.Context, payload []byte) (*exec.Cmd, *bytes.Buffer, error) {
	binaryPath, err := sandboxdBinaryPath()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, binaryPath, "worker")
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = workerEnv() // sanitised environment — no gateway secrets
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	return cmd, stderr, nil
}

// workerEnv returns a minimal, curated environment for the sandboxd worker
// subprocess. Passing an explicit list instead of inheriting the full gateway
// environment prevents the worker — and any shell command it spawns — from
// reading gateway secrets such as OPENAI_API_KEY or POSTGRES_DSN.
//
// The allowlist is intentionally conservative: only variables required for
// normal program execution are forwarded. Variables needed by git (author
// identity) are also included because agent tasks commonly run git commands.
func workerEnv() []string {
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
		// Terminal (needed by some interactive tools)
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

// defaultWorkerLimits reads sandbox resource-limit configuration from
// environment variables and returns a ResourceLimits with sensible defaults.
// Set a variable to 0 to disable the corresponding cap.
//
// Environment variables:
//
//	GATEWAY_TASK_MAX_OUTPUT_BYTES     — stdout+stderr cap (default: 4 MiB)
//	GATEWAY_SANDBOX_RLIMIT_CPU        — CPU time in seconds (default: 300)
//	GATEWAY_SANDBOX_RLIMIT_NOFILE     — open file descriptors (default: 1024)
//	GATEWAY_SANDBOX_RLIMIT_AS         — virtual address space in bytes (default: 0 = off)
func defaultWorkerLimits() ResourceLimits {
	return ResourceLimits{
		MaxOutputBytes:       workerEnvInt64("GATEWAY_TASK_MAX_OUTPUT_BYTES", 4*1024*1024),
		MaxCPUSeconds:        workerEnvUint64("GATEWAY_SANDBOX_RLIMIT_CPU", 300),
		MaxOpenFiles:         workerEnvUint64("GATEWAY_SANDBOX_RLIMIT_NOFILE", 1024),
		MaxAddressSpaceBytes: workerEnvUint64("GATEWAY_SANDBOX_RLIMIT_AS", 0),
	}
}

func workerEnvInt64(key string, fallback int64) int64 {
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

func workerEnvUint64(key string, fallback uint64) uint64 {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func workerCommandError(execCtx context.Context, stderr *bytes.Buffer, err error) error {
	if errors.Is(execCtx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("sandbox worker failed: %s", message)
}

func decodeWorkerResponse(stdout *bytes.Buffer) (workerResponse, error) {
	var event workerEvent
	if err := json.NewDecoder(stdout).Decode(&event); err != nil {
		return workerResponse{}, err
	}
	if event.Response == nil {
		return workerResponse{}, fmt.Errorf("sandbox worker response missing")
	}
	return *event.Response, nil
}

func classifyWorkerError(err error) string {
	switch {
	case IsPolicyDenied(err):
		return "policy"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "generic"
	}
}

func decodeWorkerError(message, kind string) error {
	switch kind {
	case "policy":
		reason := strings.TrimSpace(message)
		reason = strings.TrimPrefix(reason, "sandbox policy denied:")
		reason = strings.TrimSpace(reason)
		return &PolicyError{Reason: reason}
	case "timeout":
		return context.DeadlineExceeded
	default:
		return fmt.Errorf("%s", message)
	}
}

// sandboxdBinaryPath resolves the sandboxd executable using the following
// order, stopping at the first hit:
//
//  1. SANDBOXD_BIN env var — explicit operator override.
//  2. Next to os.Executable() — bundled app (Tauri release build). Tauri's
//     externalBin copies the binary as sandboxd-{triple}; a plain "sandboxd"
//     is also accepted for hand-built layouts and non-Tauri installs.
//  3. exec.LookPath("sandboxd") — developer PATH install (make install).
//  4. go build from source — dev / CI machines where the repo source and the
//     Go toolchain are both present. Fails fast with a clear error if go is
//     not on PATH instead of the generic "executable file not found" that
//     callers used to see.
func sandboxdBinaryPath() (string, error) {
	sandboxdBinary.once.Do(func() {
		sandboxdBinary.path, sandboxdBinary.err = resolveSandboxdPath()
	})
	return sandboxdBinary.path, sandboxdBinary.err
}

func resolveSandboxdPath() (string, error) {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		exeDir = filepath.Dir(exe)
	}
	return resolveSandboxdPathFrom(exeDir)
}

// resolveSandboxdPathFrom contains the actual resolution logic with the
// executable directory supplied externally so it can be exercised in tests
// without relying on os.Executable() returning a controllable path.
func resolveSandboxdPathFrom(exeDir string) (string, error) {
	// 1. Explicit override.
	if v := strings.TrimSpace(os.Getenv("SANDBOXD_BIN")); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil
		}
		return "", fmt.Errorf("SANDBOXD_BIN=%q does not point to an existing file", v)
	}

	// 2. Next to the running executable (bundled Tauri app or make install).
	if exeDir != "" {
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		// Tauri's externalBin copies binaries as name-{triple}; also try
		// plain name for hand-built and non-Tauri layouts.
		candidates := []string{
			filepath.Join(exeDir, "sandboxd-"+rustTriple()+ext),
			filepath.Join(exeDir, "sandboxd"+ext),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}

	// 3. PATH lookup — developer machine with make install.
	if p, err := exec.LookPath("sandboxd"); err == nil {
		return p, nil
	}

	// 4. Build from source — dev / CI only. Requires go on PATH and the
	// repo source to be present (runtime.Caller bakes the source path at
	// compile time, so this only works on the build machine or identical
	// file layouts).
	if _, err := exec.LookPath("go"); err != nil {
		return "", fmt.Errorf(
			"sandboxd binary not found: set SANDBOXD_BIN, add sandboxd to PATH, " +
				"or install the Go toolchain so it can be built from source",
		)
	}
	src := repoRoot()
	if _, err := os.Stat(filepath.Join(src, "cmd", "sandboxd")); err != nil {
		return "", fmt.Errorf(
			"sandboxd binary not found and source not available at %q: "+
				"set SANDBOXD_BIN or add sandboxd to PATH",
			src,
		)
	}
	cacheDir := filepath.Join(os.TempDir(), "hecate-sandboxd-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(cacheDir, "sandboxd")
	cmd := exec.Command("go", "build", "-o", out, filepath.Join(src, "cmd", "sandboxd"))
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build sandboxd: %w: %s", err, string(output))
	}
	return out, nil
}

// rustTriple returns an approximate Rust target triple for the current
// platform, matching the suffix Tauri's externalBin bundler applies.
// This does not need to be perfect — it is only used to probe for the
// triple-suffixed binary before falling back to the plain name.
func rustTriple() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "i686"
	}
	switch runtime.GOOS {
	case "darwin":
		return arch + "-apple-darwin"
	case "linux":
		return arch + "-unknown-linux-gnu"
	case "windows":
		return arch + "-pc-windows-msvc"
	default:
		return arch + "-unknown-unknown"
	}
}

func repoRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
