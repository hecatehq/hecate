package api

import (
	"context"
	"testing"

	"github.com/hecatehq/hecate/internal/modelcaps"
)

func TestHandlerApplicationHelpersAreNilSafe(t *testing.T) {
	t.Parallel()

	var handler *Handler
	if handler.taskApplication() == nil {
		t.Fatal("taskApplication() returned nil")
	}
	if handler.chatApplication() == nil {
		t.Fatal("chatApplication() returned nil")
	}
	if handler.providerApplication() == nil {
		t.Fatal("providerApplication() returned nil")
	}
	if handler.projectApplication() == nil {
		t.Fatal("projectApplication() returned nil")
	}
	if handler.projectWorkApplication() == nil {
		t.Fatal("projectWorkApplication() returned nil")
	}
	models := handler.modelApplication()
	if models == nil {
		t.Fatal("modelApplication() returned nil")
	}
	caps, err := models.ResolveCapabilities(context.Background(), "openai", "gpt-4o")
	if err != nil {
		t.Fatalf("ResolveCapabilities(no handler service) returned error: %v", err)
	}
	if caps.ToolCalling != modelcaps.ToolCallingParallel {
		t.Fatalf("tool_calling = %q, want parallel", caps.ToolCalling)
	}
}
