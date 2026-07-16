package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	maxConcurrentChatImageTurns    = 2
	maxConcurrentExternalFileTurns = 2
	chatImageTurnRetryAfter        = 1
	chatExternalFileTurnRetryAfter = 1
)

type chatAttachmentTurnAdmission interface {
	TryAcquire() bool
	Release()
}

type chatImageTurnAdmission = chatAttachmentTurnAdmission

// Attachment-turn permits are separate from upload permits: uploads bound
// untrusted body reads and decodes, while turns bound retained bodies,
// serialization/staging, and the selected runtime's synchronous call lifetime.
type fixedChatAttachmentTurnAdmission struct {
	permits chan struct{}
}

func newChatImageTurnAdmission(capacity int) *fixedChatAttachmentTurnAdmission {
	if capacity <= 0 {
		panic("chat image turn admission capacity must be positive")
	}
	return newFixedChatAttachmentTurnAdmission(capacity)
}

func newChatExternalFileTurnAdmission(capacity int) *fixedChatAttachmentTurnAdmission {
	if capacity <= 0 {
		panic("external file turn admission capacity must be positive")
	}
	return newFixedChatAttachmentTurnAdmission(capacity)
}

func newFixedChatAttachmentTurnAdmission(capacity int) *fixedChatAttachmentTurnAdmission {
	return &fixedChatAttachmentTurnAdmission{permits: make(chan struct{}, capacity)}
}

func (g *fixedChatAttachmentTurnAdmission) TryAcquire() bool {
	select {
	case g.permits <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *fixedChatAttachmentTurnAdmission) Release() {
	<-g.permits
}

// directModelTurnMayUseImageBodies decides admission without loading a body.
// Current ids on image-capable routes require a permit because validating them
// requires a claim.
// Historical metadata mirrors the hydration policy closely enough to leave
// routes that will certainly omit every image outside the image-turn gate.
func directModelTurnMayUseImageBodies(
	session chat.Session,
	currentAttachmentIDs []string,
	includeHistoricalImages bool,
	historicalProvider string,
	historicalProviderInstance types.ProviderInstanceIdentity,
) bool {
	if !includeHistoricalImages {
		return false
	}
	if len(currentAttachmentIDs) > 0 {
		return true
	}
	historicalProvider = strings.TrimSpace(historicalProvider)
	if historicalProvider == "" || !historicalProviderInstance.Valid() {
		return false
	}

	skipThroughIndex := compactedTranscriptMessageIndex(session.Messages, session.ContextSummary.ThroughMessageID)
	for i := len(session.Messages) - 1; i > skipThroughIndex; i-- {
		message := session.Messages[i]
		if message.Role != "user" || strings.TrimSpace(message.Provider) != historicalProvider || message.ProviderInstance != historicalProviderInstance {
			continue
		}
		for j := len(message.Attachments) - 1; j >= 0; j-- {
			attachment := message.Attachments[j]
			if attachment.SizeBytes > 0 && attachment.SizeBytes <= agentChatMaxImageHistoryBytes {
				return true
			}
		}
	}
	return false
}

func writeChatImageTurnBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(chatImageTurnRetryAfter))
	WriteErrorDetails(w, http.StatusTooManyRequests, errCodeImageTurnBusy, "image turn capacity is busy", ErrorDetails{
		Fields: map[string]any{
			"max_concurrent_image_turns": maxConcurrentChatImageTurns,
			"retry_after_seconds":        chatImageTurnRetryAfter,
		},
	})
}

func writeChatExternalFileTurnBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(chatExternalFileTurnRetryAfter))
	WriteErrorDetails(w, http.StatusTooManyRequests, errCodeExternalFileTurnBusy, "external file turn capacity is busy", ErrorDetails{
		Fields: map[string]any{
			"max_concurrent_external_file_turns": maxConcurrentExternalFileTurns,
			"retry_after_seconds":                chatExternalFileTurnRetryAfter,
		},
	})
}
