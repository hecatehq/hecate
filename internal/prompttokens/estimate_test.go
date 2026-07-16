package prompttokens

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestEstimateMessagesAccountsForVisualTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image *types.ContentImage
		want  int
	}{
		{name: "known dimensions", image: &types.ContentImage{Width: 1000, Height: 1000}, want: 1296},
		{name: "remote URL floor", image: &types.ContentImage{URL: "https://example.test/image.png"}, want: unknownImageTokenFloor},
		{
			name:  "large inline payload",
			image: &types.ContentImage{Data: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 1_500_000)))},
			want:  2000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := EstimateMessages([]types.Message{{ContentBlocks: []types.ContentBlock{{Type: "image_url", Image: test.image}}}})
			if got != test.want {
				t.Fatalf("EstimateMessages() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestEstimateMessagesDoesNotDoubleCountMirroredBlockText(t *testing.T) {
	t.Parallel()

	got := EstimateMessages([]types.Message{{
		Content: "12345678",
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "12345678"},
			{Type: "image", Image: &types.ContentImage{Width: 28, Height: 28}},
		},
	}})
	if got != 3 {
		t.Fatalf("EstimateMessages() = %d, want 3", got)
	}
}

func TestEstimateMessagesDoesNotDoubleCountCanonicalMultiBlockText(t *testing.T) {
	t.Parallel()

	for _, content := range []string{
		"12345678abcdefgh",
		"12345678\nabcdefgh",
		"12345678\n\nabcdefgh",
	} {
		got := EstimateMessages([]types.Message{{
			Content: content,
			ContentBlocks: []types.ContentBlock{
				{Type: "text", Text: "12345678"},
				{Type: "text", Text: "abcdefgh"},
			},
		}})
		if want := len(content) / textBytesPerToken; got != want {
			t.Fatalf("EstimateMessages(%q) = %d, want %d", content, got, want)
		}
	}

	content := "12345678\n"
	got := EstimateMessages([]types.Message{{
		Content: content,
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "12345678"},
			{Type: "text", Text: ""},
		},
	}})
	if want := len(content) / textBytesPerToken; got != want {
		t.Fatalf("EstimateMessages(trailing empty block) = %d, want %d", got, want)
	}
}

func TestEstimateMessagesIncludesDistinctRichBlockText(t *testing.T) {
	t.Parallel()

	got := EstimateMessages([]types.Message{{
		Content: "12345678",
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "abcdefgh"},
			{Type: "image", Image: &types.ContentImage{Width: 28, Height: 28}},
		},
	}})
	if got != 5 {
		t.Fatalf("EstimateMessages() = %d, want 5", got)
	}
}
