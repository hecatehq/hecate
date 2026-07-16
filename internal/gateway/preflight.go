package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type RoutePreflight interface {
	Evaluate(ctx context.Context, req types.ChatRequest, decision types.RouteDecision) (*RoutePreflightResult, error)
}

type RoutePreflightResult struct {
	ProviderKind   string
	EstimatedUsage types.Usage
	EstimatedCost  types.CostBreakdown
}

type RoutePreflightErrorKind string

const (
	RoutePreflightProviderNotFound RoutePreflightErrorKind = "provider_not_found"
	RoutePreflightProviderChanged  RoutePreflightErrorKind = "provider_instance_changed"
	RoutePreflightRouteDenied      RoutePreflightErrorKind = "route_denied"
)

type RoutePreflightError struct {
	Kind                RoutePreflightErrorKind
	Provider            string
	Model               string
	ProviderKind        string
	EstimatedCostMicros int64
	Err                 error
}

func (e *RoutePreflightError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *RoutePreflightError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func AsRoutePreflightError(err error) (*RoutePreflightError, bool) {
	var target *RoutePreflightError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

type DefaultRoutePreflight struct {
	governor  governor.Governor
	providers providers.Registry
}

func NewDefaultRoutePreflight(governor governor.Governor, providers providers.Registry) *DefaultRoutePreflight {
	return &DefaultRoutePreflight{
		governor:  governor,
		providers: providers,
	}
}

func (p *DefaultRoutePreflight) Evaluate(ctx context.Context, req types.ChatRequest, decision types.RouteDecision) (*RoutePreflightResult, error) {
	instance, ok := p.providers.GetInstance(decision.Provider)
	if !ok {
		return nil, &RoutePreflightError{
			Kind:     RoutePreflightProviderNotFound,
			Provider: decision.Provider,
			Model:    decision.Model,
			Err:      fmt.Errorf("provider %q not found", decision.Provider),
		}
	}
	provider := instance.Provider
	if err := validateProviderInstanceFence(req, decision, instance.Identity); err != nil {
		return nil, &RoutePreflightError{
			Kind:         RoutePreflightProviderChanged,
			Provider:     decision.Provider,
			Model:        decision.Model,
			ProviderKind: string(provider.Kind()),
			Err:          err,
		}
	}

	estimatedUsage := estimateUsage(withResolvedModel(req, decision.Model))
	if err := p.governor.CheckRoute(ctx, req, decision, string(provider.Kind()), 0); err != nil {
		return nil, &RoutePreflightError{
			Kind:         RoutePreflightRouteDenied,
			Provider:     decision.Provider,
			Model:        decision.Model,
			ProviderKind: string(provider.Kind()),
			Err:          err,
		}
	}

	return &RoutePreflightResult{
		ProviderKind:   string(provider.Kind()),
		EstimatedUsage: estimatedUsage,
		EstimatedCost:  types.CostBreakdown{Currency: "USD"},
	}, nil
}

func validateProviderInstanceFence(req types.ChatRequest, decision types.RouteDecision, actual types.ProviderInstanceIdentity) error {
	if !requiresProviderInstanceFence(req) {
		return nil
	}
	if !decision.ProviderInstance.Valid() {
		return fmt.Errorf("provider %q bound route is missing an execution identity", decision.Provider)
	}
	if req.Requirements.ProviderInstance.Valid() && decision.ProviderInstance != req.Requirements.ProviderInstance {
		return fmt.Errorf("provider %q changed during bound route admission", decision.Provider)
	}
	if !actual.Valid() || decision.ProviderInstance != actual {
		return fmt.Errorf("provider %q changed after bound route admission", decision.Provider)
	}
	return nil
}

func requiresProviderInstanceFence(req types.ChatRequest) bool {
	return req.Requirements.ImageInput || req.Requirements.NoProviderFailover || req.Requirements.ProviderInstance.Valid()
}

func providerInstanceForDispatch(registry providers.Registry, req types.ChatRequest, decision types.RouteDecision) (providers.ProviderInstance, error) {
	if registry == nil {
		return providers.ProviderInstance{}, &RoutePreflightError{
			Kind:     RoutePreflightProviderNotFound,
			Provider: decision.Provider,
			Model:    decision.Model,
			Err:      fmt.Errorf("provider %q not found", decision.Provider),
		}
	}

	instance, ok := registry.GetInstance(decision.Provider)
	if !ok || instance.Provider == nil {
		return providers.ProviderInstance{}, &RoutePreflightError{
			Kind:     RoutePreflightProviderNotFound,
			Provider: decision.Provider,
			Model:    decision.Model,
			Err:      fmt.Errorf("provider %q not found", decision.Provider),
		}
	}
	if err := validateProviderInstanceFence(req, decision, instance.Identity); err != nil {
		return providers.ProviderInstance{}, &RoutePreflightError{
			Kind:         RoutePreflightProviderChanged,
			Provider:     decision.Provider,
			Model:        decision.Model,
			ProviderKind: string(instance.Provider.Kind()),
			Err:          err,
		}
	}
	return instance, nil
}
