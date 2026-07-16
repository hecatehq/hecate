// Package prompttokens provides the shared, conservative prompt estimate used
// by policy, budget preflight, and provider usage fallbacks.
package prompttokens

import (
	"math"
	"strings"

	"github.com/hecatehq/hecate/pkg/types"
)

const (
	textBytesPerToken        = 4
	unknownImageTokenFloor   = 1024
	inlineImageBytesPerToken = 750
	visualPatchEdgePixels    = 28
)

// EstimateMessages returns a best-effort input-token estimate. Text preserves
// Hecate's existing four-bytes-per-token approximation. Images use validated
// dimensions when available and otherwise receive a non-zero conservative
// floor, raised for large inline payloads. This keeps image-only requests from
// bypassing prompt policy while avoiding base64 wire expansion as a token count.
func EstimateMessages(messages []types.Message) int {
	var textBytes int64
	var imageTokens int64
	for _, message := range messages {
		blockTextParts := make([]string, 0, len(message.ContentBlocks))
		for _, block := range message.ContentBlocks {
			if block.Type == "" || block.Type == "text" {
				blockTextParts = append(blockTextParts, block.Text)
			}
			if block.Type == "image" || block.Type == "image_url" {
				imageTokens = saturatingAdd(imageTokens, estimateImage(block.Image))
			}
		}
		textBytes = saturatingAdd(textBytes, int64(len(message.Content)))
		if blockTextBytes := contentBlockTextBytes(message.Content, blockTextParts); blockTextBytes > 0 {
			textBytes = saturatingAdd(textBytes, blockTextBytes)
		}
	}
	estimate := saturatingAdd(textBytes/textBytesPerToken, imageTokens)
	if estimate > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(estimate)
}

// contentBlockTextBytes returns only block text that is not already represented
// by Message.Content. Compatibility normalizers preserve the same text twice:
// once as structured blocks and once as a flattened legacy view. OpenAI joins
// block boundaries with blank lines, while Anthropic message/system paths use
// one or two newlines. Treat those canonical flattened forms as mirrors; only
// genuinely distinct dual representations remain additive.
func contentBlockTextBytes(content string, parts []string) int64 {
	if len(parts) == 0 {
		return 0
	}
	if content != "" {
		for _, separator := range []string{"", "\n", "\n\n"} {
			if content == strings.Join(parts, separator) {
				return 0
			}
		}
	}
	var bytes int64
	for _, part := range parts {
		bytes = saturatingAdd(bytes, int64(len(part)))
	}
	return bytes
}

func estimateImage(image *types.ContentImage) int64 {
	if image != nil && image.Width > 0 && image.Height > 0 {
		widthPatches := (int64(image.Width) + visualPatchEdgePixels - 1) / visualPatchEdgePixels
		heightPatches := (int64(image.Height) + visualPatchEdgePixels - 1) / visualPatchEdgePixels
		if widthPatches > math.MaxInt64/heightPatches {
			return math.MaxInt64
		}
		return widthPatches * heightPatches
	}

	inlineBytes := int64(0)
	if image != nil {
		encoded := strings.TrimSpace(image.Data)
		if encoded == "" {
			encoded = dataURIBase64Payload(image.URL)
		}
		inlineBytes = approximateDecodedBase64Bytes(encoded)
	}
	estimate := inlineBytes / inlineImageBytesPerToken
	if estimate < unknownImageTokenFloor {
		return unknownImageTokenFloor
	}
	return estimate
}

func dataURIBase64Payload(value string) string {
	value = strings.TrimSpace(value)
	comma := strings.IndexByte(value, ',')
	if comma <= 0 || !strings.HasPrefix(strings.ToLower(value[:comma]), "data:image/") ||
		!strings.Contains(strings.ToLower(value[:comma]), ";base64") {
		return ""
	}
	return value[comma+1:]
}

func approximateDecodedBase64Bytes(encoded string) int64 {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return 0
	}
	length := int64(len(encoded))
	decoded := length / 4 * 3
	if remainder := length % 4; remainder > 1 {
		decoded += remainder - 1
	}
	if strings.HasSuffix(encoded, "==") {
		decoded -= 2
	} else if strings.HasSuffix(encoded, "=") {
		decoded--
	}
	if decoded < 0 {
		return 0
	}
	return decoded
}

func saturatingAdd(left, right int64) int64 {
	if right > 0 && left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
