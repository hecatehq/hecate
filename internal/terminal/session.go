package terminal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
)

type StartRequest struct {
	Workspace string
	Cols      int
	Rows      int
}

type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
}

type Launcher interface {
	Start(ctx context.Context, req StartRequest) (Session, error)
}

type PTYLauncher struct {
	logger *slog.Logger
}

func NewPTYLauncher(logger *slog.Logger) *PTYLauncher {
	return &PTYLauncher{logger: logger}
}

func (l *PTYLauncher) Start(ctx context.Context, req StartRequest) (Session, error) {
	const attempts = 3
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		session, err := l.startOnce(ctx, req)
		if err == nil {
			return session, nil
		}
		lastErr = err
		if !DeviceNotConfigured(err) || attempt == attempts {
			return nil, err
		}
		if l.logger != nil {
			l.logger.Debug("terminal pty allocation failed; retrying", "attempt", attempt, "error", err)
		}
		timer := time.NewTimer(time.Duration(attempt*25) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (l *PTYLauncher) startOnce(ctx context.Context, req StartRequest) (Session, error) {
	cols, rows := normalizedSize(req.Cols, req.Rows)
	pt, err := gopty.New()
	if err != nil {
		return nil, err
	}
	if err := pt.Resize(cols, rows); err != nil && l.logger != nil {
		l.logger.Debug("terminal initial resize failed", "error", err)
	}
	shell, args, err := defaultShell()
	if err != nil {
		_ = pt.Close()
		return nil, err
	}
	cmd := pt.CommandContext(ctx, shell, args...)
	cmd.Dir = req.Workspace
	cmd.Env = terminalEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		_ = pt.Close()
		return nil, err
	}
	return &ptySession{pty: pt, cmd: cmd}, nil
}

func DeviceNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "device not configured")
}

func normalizedSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return cols, rows
}

func terminalEnv(env []string) []string {
	filtered := make([]string, 0, len(env)+2)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && terminalEnvSecretKey(key) {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = append(filtered, "TERM=xterm-256color", "COLORTERM=truecolor")
	return filtered
}

func terminalEnvSecretKey(key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "HECATE_") {
		return strings.Contains(normalized, "TOKEN") ||
			strings.Contains(normalized, "SECRET") ||
			strings.Contains(normalized, "KEY")
	}
	for _, marker := range []string{
		"API_KEY",
		"AUTH_TOKEN",
		"OAUTH_TOKEN",
		"ACCESS_TOKEN",
		"SECRET",
		"PASSWORD",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

type ptySession struct {
	pty gopty.Pty
	cmd *gopty.Cmd
}

func (s *ptySession) Read(p []byte) (int, error) {
	return s.pty.Read(p)
}

func (s *ptySession) Write(p []byte) (int, error) {
	return s.pty.Write(p)
}

func (s *ptySession) Close() error {
	return s.pty.Close()
}

func (s *ptySession) Resize(cols, rows int) error {
	cols, rows = normalizedSize(cols, rows)
	return s.pty.Resize(cols, rows)
}

func (s *ptySession) Wait() error {
	return s.cmd.Wait()
}
