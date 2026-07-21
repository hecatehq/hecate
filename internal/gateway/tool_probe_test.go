package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProbeToolCallingMakesOneExactHarmlessDispatch(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{response: toolProbeResponse()}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider:         provider.Name(),
		Model:            "custom-model",
		ProviderInstance: instance.Identity,
	})
	if err != nil {
		t.Fatalf("ProbeToolCalling() error = %v", err)
	}
	if result.Status != ToolProbeSupported || result.Reason != ToolProbeReasonNone || result.TraceID == "" {
		t.Fatalf("ProbeToolCalling() = %+v", result)
	}
	request, calls := provider.lastRequest()
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	if request.Requirements.ToolCalling || !request.Requirements.NoProviderFailover || !request.Requirements.ExactProvider || request.Requirements.ProviderInstance != instance.Identity {
		t.Fatalf("probe requirements = %+v", request.Requirements)
	}
	if request.Scope.ProviderHint != provider.Name() || request.Model != "custom-model" || len(request.Tools) != 1 || request.Tools[0].Function.Name != toolProbeName {
		t.Fatalf("probe request = %+v", request)
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(request.ToolChoice, &choice); err != nil || choice.Type != "function" || choice.Function.Name != toolProbeName {
		t.Fatalf("tool_choice = %s, err = %v", request.ToolChoice, err)
	}
	if len(request.Messages) != 1 || request.Messages[0].Content == "" || request.MaxTokens > 16 {
		t.Fatalf("probe payload = %+v", request)
	}
}

func TestProbeToolCallingDoesNotRetryOrFailOver(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{err: &providers.UpstreamError{StatusCode: 503, Message: "temporary upstream failure"}}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err != nil {
		t.Fatalf("ProbeToolCalling() error = %v", err)
	}
	if result.Status != ToolProbeInconclusive || result.Reason != ToolProbeReasonProviderFailed {
		t.Fatalf("ProbeToolCalling() = %+v", result)
	}
	if _, calls := provider.lastRequest(); calls != 1 {
		t.Fatalf("provider calls = %d, want one exact attempt", calls)
	}
}

func TestProbeToolCallingClassifiesExplicitToolRejection(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{err: &providers.UpstreamError{StatusCode: 400, Type: "invalid_request", Message: "tools are not supported by this model"}}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err != nil || result.Status != ToolProbeUnsupported || result.Reason != ToolProbeReasonToolRejected {
		t.Fatalf("ProbeToolCalling() = %+v, %v", result, err)
	}
}

func TestProbeToolCallingClassifiesExplicitFunctionCallingRejection(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{err: &providers.UpstreamError{StatusCode: 400, Type: "invalid_request", Message: "this model does not support function calling"}}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err != nil || result.Status != ToolProbeUnsupported || result.Reason != ToolProbeReasonToolRejected {
		t.Fatalf("ProbeToolCalling() = %+v, %v", result, err)
	}
}

func TestProbeToolCallingTreatsToolChoiceSyntaxRejectionAsInconclusive(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{err: &providers.UpstreamError{
		StatusCode: 400,
		Type:       "invalid_request",
		Message:    "invalid tool_choice: named function selection is unsupported",
	}}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err != nil || result.Status != ToolProbeInconclusive || result.Reason != ToolProbeReasonProviderFailed {
		t.Fatalf("ProbeToolCalling() = %+v, %v, want inconclusive tool-choice syntax failure", result, err)
	}
}

func TestProbeToolCallingTreatsGenericFunctionSchemaRejectionAsInconclusive(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{err: &providers.UpstreamError{
		StatusCode: 422,
		Type:       "invalid_request",
		Message:    "function schema is invalid for this endpoint",
	}}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err != nil || result.Status != ToolProbeInconclusive || result.Reason != ToolProbeReasonProviderFailed {
		t.Fatalf("ProbeToolCalling() = %+v, %v, want inconclusive function-schema failure", result, err)
	}
}

func TestProbeToolCallingRequiresExactReturnedToolName(t *testing.T) {
	t.Parallel()
	response := toolProbeResponse()
	response.Choices[0].Message.ToolCalls[0].Function.Name = " " + toolProbeName
	provider := &toolProbeProvider{response: response}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	service := newToolProbeService(registry, instance.Identity, nil)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err != nil || result.Status != ToolProbeInconclusive || result.Reason != ToolProbeReasonNoToolCall {
		t.Fatalf("ProbeToolCalling() = %+v, %v, want inconclusive exact-name mismatch", result, err)
	}
}

func TestProbeToolCallingFencesProviderReplacementBeforeDispatch(t *testing.T) {
	t.Parallel()
	first := &toolProbeProvider{name: "custom", response: toolProbeResponse()}
	replacement := &toolProbeProvider{name: "custom", response: toolProbeResponse()}
	registry := providers.NewMutableRegistry(first)
	instance, _ := registry.GetInstance(first.Name())
	service := newToolProbeService(registry, instance.Identity, nil)
	registry.Replace(replacement)

	_, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: first.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if err == nil {
		t.Fatal("ProbeToolCalling() error = nil, want generation fence failure")
	}
	if _, calls := first.lastRequest(); calls != 0 {
		t.Fatalf("first provider calls = %d, want 0", calls)
	}
	if _, calls := replacement.lastRequest(); calls != 0 {
		t.Fatalf("replacement provider calls = %d, want 0", calls)
	}
}

func TestProbeToolCallingRejectsPolicyRewrite(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{response: toolProbeResponse()}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	base := governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, governor.NewMemoryUsageStore(), nil)
	service := newToolProbeService(registry, instance.Identity, toolProbeRewriteGovernor{Governor: base})

	_, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if !errors.Is(err, ErrToolProbeModelRewritten) {
		t.Fatalf("ProbeToolCalling() error = %v, want rewrite rejection", err)
	}
	if _, calls := provider.lastRequest(); calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

func TestServiceRejectsVerifiedToolSupportAfterGovernorModelRewrite(t *testing.T) {
	t.Parallel()

	provider := &toolProbeProvider{capabilities: providers.Capabilities{
		Name:         "custom",
		Kind:         providers.KindLocal,
		DefaultModel: "custom-model",
		Models:       []string{"custom-model", "another-model"},
		ModelCapabilities: map[string]types.ModelCapabilities{
			"custom-model": {
				ImageInput:  modelcaps.ImageInputSupported,
				ToolCalling: modelcaps.ToolCallingUnknown,
				Source:      modelcaps.SourceProvider,
			},
			"another-model": {
				ImageInput:  modelcaps.ImageInputSupported,
				ToolCalling: modelcaps.ToolCallingUnknown,
				Source:      modelcaps.SourceProvider,
			},
		},
	}}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	base := governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, governor.NewMemoryUsageStore(), nil)
	service := NewService(Dependencies{
		Router:    router.NewRuleRouter("", catalog.NewRegistryCatalog(registry, nil)),
		Governor:  toolProbeRewriteGovernor{Governor: base},
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Resilience: ResilienceOptions{
			MaxAttempts:     1,
			FailoverEnabled: false,
		},
	})

	_, err := service.HandleChat(t.Context(), types.ChatRequest{
		RequestID: "verified-rich-rewrite",
		Model:     "custom-model",
		Scope:     types.RequestScope{ProviderHint: provider.Name()},
		Messages:  []types.Message{{Role: "user", Content: "continue the task"}},
		Requirements: types.ChatRequestRequirements{
			ToolCalling:              true,
			ToolCallingVerified:      true,
			ToolCallingVerifiedModel: "custom-model",
			ToolCallingVerifiedUntil: time.Now().UTC().Add(time.Hour),
			NoProviderFailover:       true,
			ExactProvider:            true,
			ProviderInstance:         instance.Identity,
		},
	})
	if err == nil {
		t.Fatal("HandleChat() error = nil, want rewritten model rejected before dispatch")
	}
	if _, calls := provider.lastRequest(); calls != 0 {
		t.Fatalf("provider calls = %d, want rewritten tool request blocked before dispatch", calls)
	}
}

func TestProbeToolCallingRespectsPolicyBeforeDispatch(t *testing.T) {
	t.Parallel()
	provider := &toolProbeProvider{response: toolProbeResponse()}
	registry := providers.NewRegistry(provider)
	instance, _ := registry.GetInstance(provider.Name())
	denyingGovernor := governor.NewStaticGovernor(config.GovernorConfig{
		DenyAll:         true,
		MaxPromptTokens: 64_000,
	}, governor.NewMemoryUsageStore(), nil)
	service := newToolProbeService(registry, instance.Identity, denyingGovernor)

	result, err := service.ProbeToolCalling(t.Context(), ToolCallingProbeRequest{
		Provider: provider.Name(), Model: "custom-model", ProviderInstance: instance.Identity,
	})
	if !errors.Is(err, ErrToolProbeUnavailable) || result.Status != ToolProbeInconclusive || result.Reason != ToolProbeReasonPolicyDenied {
		t.Fatalf("ProbeToolCalling() = %+v, %v, want policy denial before dispatch", result, err)
	}
	if _, calls := provider.lastRequest(); calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

func newToolProbeService(registry providers.Registry, identity types.ProviderInstanceIdentity, override governor.Governor) *Service {
	base := governor.NewStaticGovernor(config.GovernorConfig{MaxPromptTokens: 64_000}, governor.NewMemoryUsageStore(), nil)
	if override != nil {
		base = nil
	}
	var active governor.Governor = base
	if override != nil {
		active = override
	}
	return NewService(Dependencies{
		Router: staticFallbackRouter{route: types.RouteDecision{
			Provider: "custom", ProviderKind: string(providers.KindLocal), ProviderInstance: identity, Model: "custom-model", Reason: "pinned_provider_model",
		}},
		Governor:  active,
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Resilience: ResilienceOptions{
			MaxAttempts:     3,
			FailoverEnabled: true,
		},
	})
}

type toolProbeProvider struct {
	name         string
	err          error
	response     *types.ChatResponse
	capabilities providers.Capabilities

	mu      sync.Mutex
	request types.ChatRequest
	calls   int
}

func (p *toolProbeProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "custom"
}
func (p *toolProbeProvider) Kind() providers.Kind { return providers.KindLocal }
func (p *toolProbeProvider) DefaultModel() string {
	if p.capabilities.DefaultModel != "" {
		return p.capabilities.DefaultModel
	}
	return "custom-model"
}
func (p *toolProbeProvider) Supports(model string) bool {
	if len(p.capabilities.Models) == 0 {
		return true
	}
	for _, candidate := range p.capabilities.Models {
		if candidate == model {
			return true
		}
	}
	return p.DefaultModel() == model
}
func (p *toolProbeProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	if p.capabilities.Name != "" || len(p.capabilities.Models) > 0 || p.capabilities.DefaultModel != "" {
		return p.capabilities, nil
	}
	return providers.Capabilities{Name: p.Name(), Kind: p.Kind(), Models: []string{"custom-model"}, DefaultModel: "custom-model"}, nil
}
func (p *toolProbeProvider) Chat(_ context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	p.mu.Lock()
	p.request = req
	p.calls++
	p.mu.Unlock()
	return p.response, p.err
}
func (p *toolProbeProvider) lastRequest() (types.ChatRequest, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.request, p.calls
}

type toolProbeRewriteGovernor struct{ governor.Governor }

func (g toolProbeRewriteGovernor) RewriteResult(req types.ChatRequest) governor.RewriteResult {
	rewritten := req
	rewritten.Model = "another-model"
	return governor.RewriteResult{Request: rewritten, Applied: true, OriginalModel: req.Model, RewrittenModel: "another-model"}
}

func toolProbeResponse() *types.ChatResponse {
	return &types.ChatResponse{Choices: []types.ChatChoice{{
		Message: types.Message{Role: "assistant", ToolCalls: []types.ToolCall{{
			ID: "call_probe", Type: "function", Function: types.ToolCallFunction{Name: toolProbeName, Arguments: "{}"},
		}}},
	}}}
}
