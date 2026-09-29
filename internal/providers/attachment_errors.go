package providers

import (
	"context"
	"errors"
	"net"

	"github.com/hecatehq/hecate/pkg/types"
)

const attachmentProviderError = "provider request with file attachments failed; upstream details withheld to protect file contents"

// A provider can echo arbitrary file text in an error. Pattern-based secret
// redaction cannot recognize it, so keep only retry/cancellation classification
// and never retain the original error as an unwrap-able cause.
func attachmentSafeError(req types.ChatRequest, err error) error {
	if err == nil {
		return nil
	}
	privateInput := false
	for _, message := range req.Messages {
		for _, block := range message.ContentBlocks {
			privateInput = privateInput || block.AttachmentInput
		}
	}
	if !privateInput {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return &UpstreamError{StatusCode: upstream.StatusCode, Type: "attachment_request_failed", Message: attachmentProviderError}
	}
	var network net.Error
	if errors.As(err, &network) {
		return attachmentNetworkError{timeout: network.Timeout(), temporary: network.Temporary()}
	}
	return errors.New(attachmentProviderError)
}

type attachmentNetworkError struct {
	timeout, temporary bool
}

func (attachmentNetworkError) Error() string     { return attachmentProviderError }
func (e attachmentNetworkError) Timeout() bool   { return e.timeout }
func (e attachmentNetworkError) Temporary() bool { return e.temporary }
