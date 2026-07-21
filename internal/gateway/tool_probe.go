package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	toolProbeName    = "hecate_capability_probe"
	toolProbeTimeout = 20 * time.Second

	ToolProbeSupported    = "supported"
	ToolProbeUnsupported  = "unsupported"
	ToolProbeInconclusive = "inconclusive"

	ToolProbeReasonNone           = ""
	ToolProbeReasonNoToolCall     = "no_tool_call"
	ToolProbeReasonToolRejected   = "tool_rejected"
	ToolProbeReasonAuthentication = "authentication"
	ToolProbeReasonRateLimited    = "rate_limited"
	ToolProbeReasonTimeout        = "timeout"
	ToolProbeReasonNetwork        = "network"
	ToolProbeReasonProviderFailed = "provider_failure"
	ToolProbeReasonProviderChange = "provider_changed"
	ToolProbeReasonPolicyDenied   = "policy_denied"
	ToolProbeReasonConfiguration  = "configuration"
	ToolProbeReasonUnexpected     = "unexpected_result"
)

var (
	ErrToolProbeUnavailable    = errors.New("model tool capability probing is unavailable")
	ErrToolProbeInvalid        = errors.New("invalid model tool capability probe")
	ErrToolProbeModelRewritten = errors.New("model tool capability probe cannot follow a model rewrite")
)

// ToolCallingProbeRequest deliberately carries an opaque provider generation
// fence. Its inputs are all operator-selected metadata; Hecate supplies the
// static prompt, schema, and forced tool choice internally.
type ToolCallingProbeRequest struct {
	Provider         string
	Model            string
	ProviderInstance types.ProviderInstanceIdentity
}

// ToolCallingProbeResult contains only a safe outcome summary. It deliberately
// omits the probe prompt, model output, tool arguments, provider endpoint, and
// provider-generation identity.
type ToolCallingProbeResult struct {
	Provider string
	Model    string
	Status   string
	Reason   string
	TraceID  string
}

// ProbeToolCalling makes one bounded, exact-provider diagnostic request. It
// never calls HandleChat, the normal executor, provider retry/failover, usage
// finalization, health accounting, or an Hecate tool loop. A tool call is only
// observed in the response; it is never parsed or executed.
func (s *Service) ProbeToolCalling(ctx context.Context, input ToolCallingProbeRequest) (ToolCallingProbeResult, error) {
	if s == nil || s.router == nil || s.preflight == nil || s.providers == nil {
		return ToolCallingProbeResult{}, ErrToolProbeUnavailable
	}
	input.Provider = strings.TrimSpace(input.Provider)
	input.Model = strings.TrimSpace(input.Model)
	if input.Provider == "" || input.Model == "" || !input.ProviderInstance.Valid() {
		return ToolCallingProbeResult{}, ErrToolProbeInvalid
	}

	requestID, err := newToolProbeRequestID()
	if err != nil {
		return ToolCallingProbeResult{}, err
	}
	trace := s.startToolProbeTrace(requestID)
	defer trace.Finalize()
	ctx, cancel := context.WithTimeout(ctx, toolProbeTimeout)
	defer cancel()
	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())

	result := ToolCallingProbeResult{
		Provider: input.Provider,
		Model:    input.Model,
		Status:   ToolProbeInconclusive,
		Reason:   ToolProbeReasonUnexpected,
		TraceID:  trace.TraceID,
	}
	trace.Record("model_capability.probe_started", map[string]any{
		telemetry.AttrGenAIProviderName:         input.Provider,
		telemetry.AttrGenAIRequestModel:         input.Model,
		telemetry.AttrHecateRouteReason:         "operator_tool_capability_probe",
		telemetry.AttrHecateRequestMessageCount: 1,
	})
	defer func() {
		trace.Record("model_capability.probe_finished", map[string]any{
			telemetry.AttrGenAIProviderName:        input.Provider,
			telemetry.AttrGenAIRequestModel:        input.Model,
			"hecate.model_capability.probe.status": result.Status,
			"hecate.model_capability.probe.reason": result.Reason,
		})
	}()

	req := toolProbeChatRequest(input, requestID)
	if s.governor != nil {
		if err := s.governor.Check(ctx, req); err != nil {
			result.Reason = ToolProbeReasonPolicyDenied
			return result, fmt.Errorf("%w: %v", ErrToolProbeUnavailable, err)
		}
		if rewrite := s.governor.RewriteResult(req); rewrite.Applied {
			result.Reason = ToolProbeReasonPolicyDenied
			return result, ErrToolProbeModelRewritten
		}
	}

	decision, err := s.router.Route(ctx, req)
	if err != nil {
		result.Reason = ToolProbeReasonConfiguration
		return result, fmt.Errorf("route model tool capability probe: %w", err)
	}
	if decision.Provider != input.Provider || decision.Model != input.Model || decision.ProviderInstance != input.ProviderInstance {
		result.Reason = ToolProbeReasonProviderChange
		return result, fmt.Errorf("%w: probe route changed before dispatch", ErrToolProbeUnavailable)
	}
	if _, err := s.preflight.Evaluate(ctx, req, decision); err != nil {
		result.Reason = toolProbePreflightReason(err)
		return result, fmt.Errorf("preflight model tool capability probe: %w", err)
	}
	instance, err := providerInstanceForDispatch(s.providers, req, decision)
	if err != nil {
		result.Reason = ToolProbeReasonProviderChange
		return result, fmt.Errorf("dispatch model tool capability probe: %w", err)
	}
	trace.Record("provider.call.started", map[string]any{
		telemetry.AttrGenAIProviderName: input.Provider,
		telemetry.AttrGenAIRequestModel: input.Model,
		telemetry.AttrHecateRouteReason: "operator_tool_capability_probe",
	})
	response, err := instance.Provider.Chat(ctx, req)
	if err != nil {
		result.Status, result.Reason = classifyToolProbeError(err)
		trace.Record("provider.call.failed", map[string]any{
			telemetry.AttrGenAIProviderName: input.Provider,
			telemetry.AttrGenAIRequestModel: input.Model,
			telemetry.AttrHecateErrorKind:   result.Reason,
		})
		return result, nil
	}
	if toolProbeResponseCallsExpectedTool(response) {
		result.Status = ToolProbeSupported
		result.Reason = ToolProbeReasonNone
	} else {
		result.Status = ToolProbeInconclusive
		result.Reason = ToolProbeReasonNoToolCall
	}
	trace.Record("provider.call.succeeded", map[string]any{
		telemetry.AttrGenAIProviderName: input.Provider,
		telemetry.AttrGenAIRequestModel: input.Model,
	})
	return result, nil
}

func toolProbeChatRequest(input ToolCallingProbeRequest, requestID string) types.ChatRequest {
	return types.ChatRequest{
		RequestID: requestID,
		Model:     input.Model,
		Scope:     types.RequestScope{ProviderHint: input.Provider},
		Requirements: types.ChatRequestRequirements{
			NoProviderFailover: true,
			ExactProvider:      true,
			ProviderInstance:   input.ProviderInstance,
		},
		MaxTokens:   16,
		Temperature: 0,
		Messages: []types.Message{{
			Role:    "user",
			Content: "Call the hecate_capability_probe tool now. Do not answer with text.",
		}},
		Tools: []types.Tool{{
			Type: "function",
			Function: types.ToolFunction{
				Name:        toolProbeName,
				Description: "A harmless Hecate capability check. It has no side effects.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
		}},
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"hecate_capability_probe"}}`),
	}
}

func toolProbeResponseCallsExpectedTool(response *types.ChatResponse) bool {
	if response == nil {
		return false
	}
	for _, choice := range response.Choices {
		for _, call := range choice.Message.ToolCalls {
			if call.Function.Name == toolProbeName {
				return true
			}
		}
	}
	return false
}

func classifyToolProbeError(err error) (status string, reason string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return ToolProbeInconclusive, ToolProbeReasonTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ToolProbeInconclusive, ToolProbeReasonUnexpected
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ToolProbeInconclusive, ToolProbeReasonTimeout
		}
		return ToolProbeInconclusive, ToolProbeReasonNetwork
	}
	var upstream *providers.UpstreamError
	if errors.As(err, &upstream) {
		if explicitlyRejectsToolSchema(upstream) {
			return ToolProbeUnsupported, ToolProbeReasonToolRejected
		}
		switch upstream.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return ToolProbeInconclusive, ToolProbeReasonAuthentication
		case http.StatusTooManyRequests:
			return ToolProbeInconclusive, ToolProbeReasonRateLimited
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return ToolProbeInconclusive, ToolProbeReasonTimeout
		default:
			return ToolProbeInconclusive, ToolProbeReasonProviderFailed
		}
	}
	return ToolProbeInconclusive, ToolProbeReasonProviderFailed
}

func explicitlyRejectsToolSchema(upstream *providers.UpstreamError) bool {
	if upstream == nil || (upstream.StatusCode != http.StatusBadRequest && upstream.StatusCode != http.StatusUnprocessableEntity) {
		return false
	}
	text := strings.ToLower(upstream.Type + " " + upstream.Message)
	if !strings.Contains(text, "tool") && !strings.Contains(text, "function") {
		return false
	}
	// A provider can reject this diagnostic's forced selection syntax while
	// still accepting ordinary tools. Only an explicit support denial is proof
	// that the model cannot back Hecate tools; malformed tool_choice, unknown
	// tool names, and generic 400/422 responses stay inconclusive.
	if strings.Contains(text, "tool_choice") || strings.Contains(text, "tool choice") || strings.Contains(text, "forced tool") {
		return false
	}
	return strings.Contains(text, "tools are not supported") ||
		strings.Contains(text, "tool use is not supported") ||
		strings.Contains(text, "tool calling is not supported") ||
		strings.Contains(text, "function calling is not supported") ||
		strings.Contains(text, "does not support function calling") ||
		strings.Contains(text, "doesn't support function calling") ||
		strings.Contains(text, "functions are not supported") ||
		strings.Contains(text, "does not support tools") ||
		strings.Contains(text, "doesn't support tools") ||
		strings.Contains(text, "does not support tool use") ||
		strings.Contains(text, "doesn't support tool use") ||
		strings.Contains(text, "does not support tool calling") ||
		strings.Contains(text, "doesn't support tool calling") ||
		strings.Contains(text, "tools unsupported") ||
		strings.Contains(text, "tool calling unsupported") ||
		strings.Contains(text, "function calling unsupported")
}

func toolProbePreflightReason(err error) string {
	if preflight, ok := AsRoutePreflightError(err); ok {
		switch preflight.Kind {
		case RoutePreflightProviderChanged, RoutePreflightProviderNotFound:
			return ToolProbeReasonProviderChange
		case RoutePreflightRouteDenied:
			return ToolProbeReasonPolicyDenied
		}
	}
	return ToolProbeReasonConfiguration
}

func (s *Service) startToolProbeTrace(requestID string) *profiler.Trace {
	if s != nil && s.tracer != nil {
		return s.tracer.Start(requestID)
	}
	return profiler.NewTrace(requestID, nil)
}

func newToolProbeRequestID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "model_tool_probe_" + hex.EncodeToString(raw[:]), nil
}
