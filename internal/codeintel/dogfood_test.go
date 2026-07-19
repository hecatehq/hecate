package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestService_RealProviderDogfood(t *testing.T) {
	if os.Getenv("HECATE_CODEINTEL_DOGFOOD") != "1" {
		t.Skip("set HECATE_CODEINTEL_DOGFOOD=1 to exercise an installed provider")
	}
	workspace := os.Getenv("HECATE_CODEINTEL_WORKSPACE")
	if workspace == "" {
		var err error
		workspace, err = filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("resolve repository root: %v", err)
		}
	}
	operation := Operation(os.Getenv("HECATE_CODEINTEL_OPERATION"))
	if operation == "" {
		operation = OpDocumentSymbols
	}
	path := os.Getenv("HECATE_CODEINTEL_FILE")
	if path == "" && operation != OpCapabilities && operation != OpWorkspaceSymbols {
		path = "internal/codeintel/service.go"
	}
	timeout := 30 * time.Second
	if raw := os.Getenv("HECATE_CODEINTEL_TIMEOUT_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			t.Fatalf("HECATE_CODEINTEL_TIMEOUT_SECONDS must be a positive integer")
		}
		timeout = time.Duration(seconds) * time.Second
	}
	service := NewService()
	service.timeout = timeout
	request := Request{
		Operation:  operation,
		Path:       path,
		Language:   os.Getenv("HECATE_CODEINTEL_LANGUAGE"),
		Query:      os.Getenv("HECATE_CODEINTEL_QUERY"),
		Line:       dogfoodEnvInt(t, "HECATE_CODEINTEL_LINE"),
		Column:     dogfoodEnvInt(t, "HECATE_CODEINTEL_COLUMN"),
		MaxResults: absoluteMaxResults,
	}
	started := time.Now()
	result, err := service.Query(context.Background(), workspace, request)
	if err != nil {
		t.Fatalf("real-provider query: %v", err)
	}
	t.Logf("provider=%s operation=%s results=%d elapsed=%s", result.Provider, result.Operation, len(result.Items), time.Since(started))
}

func dogfoodEnvInt(t *testing.T, name string) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		t.Fatalf("%s must be a positive integer", name)
	}
	return value
}
