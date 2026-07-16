package providers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
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

// ProviderInstanceIdentityReporter is implemented by provider adapters that
// can derive a stable, non-secret identity for their effective configuration
// generation. Registries assign a process-scoped identity to providers that do
// not implement it, which still fences a current request but changes whenever
// that provider object is recreated.
type ProviderInstanceIdentityReporter interface {
	ProviderInstanceIdentity() types.ProviderInstanceIdentity
}

// ProviderInstance binds a provider object to the opaque identity assigned by
// its registry. Callers that admit sensitive request bodies must carry this
// identity through routing and compare it with the instance fetched for the
// actual call.
type ProviderInstance struct {
	Provider Provider
	Identity types.ProviderInstanceIdentity
}

// AliasReporter lets provider-management surfaces expose stable identifiers
// that differ from the runtime routing name, such as a control-plane provider
// ID for an operator-named custom provider.
type AliasReporter interface {
	Aliases() []string
}

// CapabilityFamilyReporter exposes the canonical provider family used for
// provider-specific discovery and static model capability inference. It is
// deliberately separate from Name, which remains the configured routing key.
type CapabilityFamilyReporter interface {
	CapabilityFamily() string
}

// CapabilityRefresher is implemented by providers that can bypass their
// discovery cache for explicit operator refreshes.
type CapabilityRefresher interface {
	RefreshCapabilities(ctx context.Context) (Capabilities, error)
}

// Streamer is an optional interface providers may implement to support SSE streaming.
// ChatStream writes an OpenAI-compatible SSE body (including the final "data: [DONE]\n\n")
// to w and returns when the stream is complete or the context is cancelled.
type Streamer interface {
	ChatStream(ctx context.Context, req types.ChatRequest, w io.Writer) error
}

// TranscriptionCapability describes an explicitly verified audio
// transcription contract. Transcription models are deliberately not folded
// into chat capabilities: many upstreams do not list them in chat catalogs,
// and accepting every OpenAI-shaped provider would disclose microphone audio
// to an endpoint Hecate has not verified.
type TranscriptionCapability struct {
	DefaultModel string
}

type TranscriptionRequest struct {
	Audio     []byte
	Filename  string
	MediaType string
	Model     string
}

type TranscriptionResponse struct {
	Text  string
	Model string
}

// Transcriber is optional. Only provider instances with an explicitly
// configured transcription path and default model advertise the capability.
type Transcriber interface {
	TranscriptionCapability() TranscriptionCapability
	Transcribe(ctx context.Context, req TranscriptionRequest) (*TranscriptionResponse, error)
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
	GetInstance(name string) (ProviderInstance, bool)
	AllInstances() []ProviderInstance
}

type InMemoryRegistry struct {
	instances []ProviderInstance
	byName    map[string]ProviderInstance
}

func NewRegistry(items ...Provider) *InMemoryRegistry {
	instances := make([]ProviderInstance, 0, len(items))
	byName := make(map[string]ProviderInstance, len(items))
	for _, provider := range items {
		instance := newProviderInstance(provider)
		instances = append(instances, instance)
		byName[provider.Name()] = instance
	}

	return &InMemoryRegistry{
		instances: instances,
		byName:    byName,
	}
}

func (r *InMemoryRegistry) Get(name string) (Provider, bool) {
	instance, ok := r.byName[name]
	return instance.Provider, ok
}

func (r *InMemoryRegistry) All() []Provider {
	out := make([]Provider, 0, len(r.instances))
	for _, instance := range r.instances {
		out = append(out, instance.Provider)
	}
	return out
}

func (r *InMemoryRegistry) GetInstance(name string) (ProviderInstance, bool) {
	instance, ok := r.byName[name]
	return instance, ok
}

func (r *InMemoryRegistry) AllInstances() []ProviderInstance {
	out := make([]ProviderInstance, len(r.instances))
	copy(out, r.instances)
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
