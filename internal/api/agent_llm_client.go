package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/pkg/types"
)

type gatewayAgentLLMClient struct {
	service *gateway.Service
}

func (c gatewayAgentLLMClient) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("chat service is not configured")
	}
	result, err := c.service.HandleChat(ctx, req)
	return agentLLMResponse(result), err
}

func (c gatewayAgentLLMClient) ChatStream(ctx context.Context, req types.ChatRequest, onContentDelta func(string)) (*types.ChatResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("chat service is not configured")
	}
	resp, err := c.service.HandleChatStreamCapture(ctx, req, onContentDelta)
	// A route-only response means the stream entered a provider call. Retrying
	// through Chat could re-disclose a provider-bound image to a different route.
	if err == nil || !isStreamingUnsupported(err) || resp != nil {
		return resp, err
	}
	return c.Chat(ctx, req)
}

func agentLLMResponse(result *gateway.ChatResult) *types.ChatResponse {
	if result == nil {
		return nil
	}
	if result.Response != nil {
		return result.Response
	}
	return &types.ChatResponse{
		Model: result.Metadata.Model,
		Route: types.RouteDecision{
			Provider:         result.Metadata.Provider,
			ProviderKind:     result.Metadata.ProviderKind,
			ProviderInstance: result.Metadata.ProviderInstance,
			Model:            result.Metadata.Model,
			Reason:           result.Metadata.RouteReason,
		},
	}
}

func isStreamingUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "does not support streaming")
}
