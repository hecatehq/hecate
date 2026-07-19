package codeintel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/processrunner"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	defaultQueryTimeout          = 15 * time.Second
	defaultDiagnosticInitialWait = 5 * time.Second
	defaultDiagnosticSettle      = 250 * time.Millisecond
	defaultMaxResults            = 50
	absoluteMaxResults           = 200
	maxOperationBytes            = 64
	maxRequestPathBytes          = 4 * 1024
	maxLanguageBytes             = 64
	maxQueryBytes                = 16 * 1024
)

var providerExecutableEnv = map[string]string{
	"gopls":    "HECATE_CODEINTEL_GOPLS_PATH",
	"tsc":      "HECATE_CODEINTEL_TSC_PATH",
	"ast-grep": "HECATE_CODEINTEL_AST_GREP_PATH",
}

type lookPathFunc func(string) (string, error)

// Service discovers and invokes only Hecate's fixed local code-intelligence
// providers. It does not accept executable names or arguments from a model.
type Service struct {
	lookPath              lookPathFunc
	providerPaths         map[string]string
	runner                processrunner.Runner
	servers               map[string][]serverSpec
	timeout               time.Duration
	diagnosticInitialWait time.Duration
	diagnosticSettle      time.Duration
	maxMessageBytes       int64
	maxTotalBytes         int64
}

func NewService() *Service {
	return &Service{
		lookPath:              exec.LookPath,
		providerPaths:         configuredProviderPaths(),
		runner:                newCodeIntelProcessRunner(),
		servers:               defaultServers(),
		timeout:               defaultQueryTimeout,
		diagnosticInitialWait: defaultDiagnosticInitialWait,
		diagnosticSettle:      defaultDiagnosticSettle,
		maxMessageBytes:       1024 * 1024,
		maxTotalBytes:         4 * 1024 * 1024,
	}
}

func configuredProviderPaths() map[string]string {
	paths := make(map[string]string, len(providerExecutableEnv))
	for provider, envName := range providerExecutableEnv {
		if path := strings.TrimSpace(os.Getenv(envName)); path != "" {
			paths[provider] = path
		}
	}
	return paths
}

func (s *Service) Query(ctx context.Context, workspaceRoot string, request Request) (Result, error) {
	if s == nil {
		return Result{}, fmt.Errorf("code intelligence service is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	fsys, err := openWorkspace(workspaceRoot)
	if err != nil {
		return Result{}, err
	}
	if len(request.Operation) > maxOperationBytes {
		return Result{}, fmt.Errorf("code intelligence operation exceeds the %d-byte limit", maxOperationBytes)
	}
	if len(request.Path) > maxRequestPathBytes {
		return Result{}, fmt.Errorf("code intelligence path exceeds the %d-byte limit", maxRequestPathBytes)
	}
	if len(request.Language) > maxLanguageBytes {
		return Result{}, fmt.Errorf("code intelligence language exceeds the %d-byte limit", maxLanguageBytes)
	}
	if len(request.Query) > maxQueryBytes {
		return Result{}, fmt.Errorf("code intelligence query exceeds the %d-byte limit", maxQueryBytes)
	}
	request.Operation = Operation(strings.TrimSpace(string(request.Operation)))
	rawPath := strings.TrimSpace(request.Path)
	if rawPath == "" {
		request.Path = ""
	} else {
		request.Path = filepath.Clean(rawPath)
	}
	if request.Operation == OpStructuralSearch {
		request.Language = normalizeStructuralLanguage(request.Language)
	} else {
		request.Language = normalizeLanguage(request.Language)
	}
	request.Query = strings.TrimSpace(request.Query)
	request.MaxResults = normalizeMaxResults(request.MaxResults)
	if !knownOperation(request.Operation) {
		return Result{}, fmt.Errorf("unsupported code intelligence operation %q", request.Operation)
	}

	providerCtx, cleanupProviderRuntime, err := prepareProviderRuntime(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("prepare code intelligence provider runtime")
	}
	defer cleanupProviderRuntime()

	queryCtx := providerCtx
	var cancel context.CancelFunc
	if s.timeout > 0 {
		queryCtx, cancel = context.WithTimeout(providerCtx, s.timeout)
		defer cancel()
	}

	var result Result
	switch request.Operation {
	case OpCapabilities:
		result, err = s.capabilities(queryCtx, fsys, request)
	case OpDefinition, OpReferences, OpHover, OpDocumentSymbols, OpWorkspaceSymbols, OpDiagnostics:
		result, err = s.queryLSP(queryCtx, fsys, request)
	case OpStructuralSearch:
		result, err = s.queryStructural(queryCtx, fsys, request)
	}
	if err == nil && queryCtx.Err() != nil {
		err = queryCtx.Err()
	}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("code intelligence %s timed out after %s: %w", request.Operation, s.timeout, context.DeadlineExceeded)
		}
		return Result{}, err
	}
	result.Operation = request.Operation
	result.Text = formatResult(result)
	return result, nil
}

func knownOperation(operation Operation) bool {
	switch operation {
	case OpCapabilities,
		OpDefinition,
		OpReferences,
		OpHover,
		OpDocumentSymbols,
		OpWorkspaceSymbols,
		OpDiagnostics,
		OpStructuralSearch:
		return true
	default:
		return false
	}
}

func openWorkspace(root string) (*workspacefs.FS, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory")
	}
	fsys, err := workspacefs.New(canonical)
	if err != nil {
		return nil, err
	}
	return fsys, nil
}

func normalizeMaxResults(value int) int {
	if value <= 0 {
		return defaultMaxResults
	}
	if value > absoluteMaxResults {
		return absoluteMaxResults
	}
	return value
}
