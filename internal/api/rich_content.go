package api

import "github.com/hecatehq/hecate/pkg/types"

// chatMessagesContainImages reports whether a compatibility request carries
// image content that could be disclosed upstream. Compatibility routes do not
// opt into Hecate's native capability admission, but they still use this bit to
// keep a retryable failure from sending the same image to another provider.
func chatMessagesContainImages(messages []types.Message) bool {
	for _, message := range messages {
		for _, block := range message.ContentBlocks {
			if block.Type == "image" || block.Type == "image_url" {
				return true
			}
		}
	}
	return false
}
