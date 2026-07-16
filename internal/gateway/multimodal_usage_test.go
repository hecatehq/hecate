package gateway

import (
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestEstimateUsageCountsImageOnlyPrompts(t *testing.T) {
	t.Parallel()

	usage := estimateUsage(types.ChatRequest{
		MaxTokens: 10,
		Messages: []types.Message{{ContentBlocks: []types.ContentBlock{{
			Type:  "image_url",
			Image: &types.ContentImage{Width: 1000, Height: 1000},
		}}}},
	})
	if usage.PromptTokens != 1296 || usage.TotalTokens != 1306 {
		t.Fatalf("estimateUsage() = %+v, want 1296 prompt and 1306 total", usage)
	}
}
