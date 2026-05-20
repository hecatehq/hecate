package providers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

type Kind string

const (
	KindCloud Kind = "cloud"
	KindLocal Kind = "local"
)

type Provider interface {
	Name() string
	Kind() Kind
	DefaultModel() string
	Capabilities(ctx context.Context) (Capabilities, error)
	Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error)
	Supports(model string) bool
}

// Streamer is an optional interface providers may implement to support SSE streaming.
// ChatStream writes an OpenAI-compatible SSE body (including the final "data: [DONE]\n\n")
// to w and returns when the stream is complete or the context is cancelled.
type Streamer interface {
	ChatStream(ctx context.Context, req types.ChatRequest, w io.Writer) error
}

// Validator is an optional interface for providers to surface configuration problems
// (e.g. missing API key) before any response bytes are written.
type Validator interface {
	Validate() error
}

type CredentialState string

const (
	CredentialStateConfigured  CredentialState = "configured"
	CredentialStateMissing     CredentialState = "missing"
	CredentialStateNotRequired CredentialState = "not_required"
	CredentialStateUnknown     CredentialState = "unknown"
)

// CredentialReporter lets operator-facing surfaces explain whether a provider
// is blocked on credentials without exposing the secret material itself.
type CredentialReporter interface {
	CredentialState() CredentialState
}

type Capabilities struct {
	Name              string
	Kind              Kind
	DefaultModel      string
	Models            []string
	ModelCapabilities map[string]types.ModelCapabilities
	Discoverable      bool
	DiscoverySource   string
	RefreshedAt       time.Time
	LastError         string
}

// Enabler is an optional interface a Provider may implement to signal that it
// has been administratively disabled. The catalog short-circuits health checks
// for disabled providers and reports them as status "disabled".
type Enabler interface {
	Enabled() bool
}

type Registry interface {
	Get(name string) (Provider, bool)
	All() []Provider
}

type InMemoryRegistry struct {
	providers []Provider
	byName    map[string]Provider
}

func NewRegistry(items ...Provider) *InMemoryRegistry {
	byName := make(map[string]Provider, len(items))
	for _, provider := range items {
		byName[provider.Name()] = provider
	}

	return &InMemoryRegistry{
		providers: items,
		byName:    byName,
	}
}

func (r *InMemoryRegistry) Get(name string) (Provider, bool) {
	provider, ok := r.byName[name]
	return provider, ok
}

func (r *InMemoryRegistry) All() []Provider {
	out := make([]Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		switch upstreamErr.StatusCode {
		case http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		}
	}

	return false
}
