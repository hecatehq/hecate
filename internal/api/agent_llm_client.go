package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/pkg/types"
)

type gatewayAgentLLMClient struct {
	service *gateway.Service
}

func (c gatewayAgentLLMClient) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("chat service is not configured")
	}
	result, err := c.service.HandleChat(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Response, nil
}

func (c gatewayAgentLLMClient) ChatStream(ctx context.Context, req types.ChatRequest, onContentDelta func(string)) (*types.ChatResponse, error) {
	if c.service == nil {
		return nil, fmt.Errorf("chat service is not configured")
	}
	resp, err := c.service.HandleChatStreamCapture(ctx, req, onContentDelta)
	if err == nil || !isStreamingUnsupported(err) {
		return resp, err
	}
	return c.Chat(ctx, req)
}

func isStreamingUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "does not support streaming")
}
