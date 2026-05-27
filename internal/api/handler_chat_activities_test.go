package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/agentadapters"
)

func TestAgentChatActivityFromAdapterPreservesArtifactPreview(t *testing.T) {
	activity := agentChatActivityFromAdapter(agentadapters.Activity{
		ID:              "tool:call_1",
		Type:            "tool_call",
		Status:          "completed",
		Kind:            "execute",
		Title:           "call_1",
		Detail:          "execute · output: summarized",
		ArtifactPreview: "  full output\nline two\n",
	})

	if activity.ArtifactPreview != "  full output\nline two" {
		t.Fatalf("artifact preview = %q", activity.ArtifactPreview)
	}
}
