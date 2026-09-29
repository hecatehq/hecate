package api

import (
	"context"
	"errors"

	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/providerdispatch"
	"github.com/hecatehq/hecate/pkg/types"
)

// Snapshot costs, not file bodies, in the final-dispatch hook. The governor may
// rewrite the model after initial admission; a smaller final window must fail
// before any file-bearing provider I/O. At this late boundary the transcript is
// already committed, so failure settles that turn rather than releasing drafts.
func (h *Handler) nativeTextDispatchBudgetCheck(ctx context.Context, messages []types.Message) providerdispatch.AttemptRecorder {
	ordinaryBytes, textBytes := nativeInlineAttachmentCosts(messages)
	if textBytes == 0 {
		return nil
	}
	return func(route types.RouteDecision) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		caps, err := h.resolveModelCapabilities(ctx, route.Provider, route.Model)
		if err != nil {
			return errors.New("attachment context budget is unavailable for the final model route")
		}
		current, err := h.modelApplication().ResolveProviderRoute(ctx, route.Provider, route.Model)
		if err != nil || current.Name != route.Provider || !route.ProviderInstance.Valid() || current.Instance != route.ProviderInstance {
			return errors.New("attachment provider changed before context admission")
		}
		if textBytes > chatapp.NativeTextContextBudget(caps.MaxContextTokens, ordinaryBytes) {
			return chatapp.ErrNativeTextContextTooLarge
		}
		return ctx.Err()
	}
}

func nativeInlineAttachmentCosts(messages []types.Message) (int, int) {
	ordinaryBytes, textBytes := 1024, 0
	for _, message := range messages {
		ordinaryBytes += len(message.Content) + 256
		for _, block := range message.ContentBlocks {
			if block.AttachmentInput {
				textBytes += len(block.Text)
			} else if block.Image != nil {
				ordinaryBytes += nativeChatImageContextBytes
			}
		}
	}
	return ordinaryBytes, textBytes
}
