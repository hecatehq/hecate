package gateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/telemetry"
)

func TestTraceErrorAttrsIncludesOTelShapedErrorFields(t *testing.T) {
	t.Parallel()

	attrs := traceErrorAttrs("provider", errorKindProviderCallFailed, errors.New("boom"), map[string]any{
		"gen_ai.provider.name": "openai",
	})

	if got := attrs[telemetry.AttrHecatePhase]; got != "provider" {
		t.Fatalf("hecate.phase = %v, want provider", got)
	}
	if got := attrs[telemetry.AttrHecateErrorKind]; got != errorKindProviderCallFailed {
		t.Fatalf("hecate.error.kind = %v, want %q", got, errorKindProviderCallFailed)
	}
	if got := attrs[telemetry.AttrErrorType]; got != errorKindProviderCallFailed {
		t.Fatalf("error.type = %v, want %q", got, errorKindProviderCallFailed)
	}
	if got := attrs[telemetry.AttrErrorMessage]; got != "boom" {
		t.Fatalf("error.message = %v, want boom", got)
	}
	if got := attrs[telemetry.AttrGenAIProviderName]; got != "openai" {
		t.Fatalf("gen_ai.provider.name = %v, want openai", got)
	}
}

func TestTraceErrorAttrsRedactsInlineImagePayload(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("A", 128)
	attrs := traceErrorAttrs("provider", errorKindProviderCallFailed, errors.New("bad data:image/png;base64,"+payload), nil)
	message, _ := attrs[telemetry.AttrErrorMessage].(string)
	if strings.Contains(message, payload) || !strings.Contains(message, "[redacted inline image]") {
		t.Fatalf("error.message = %q", message)
	}
}
