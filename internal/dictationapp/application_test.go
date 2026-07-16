package dictationapp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProviderOptionsAreAvailableAndLocalFirst(t *testing.T) {
	t.Parallel()

	app := New(Options{Registry: providers.NewRegistry(
		&testTranscriber{name: "cloud-ready", kind: providers.KindCloud, credential: providers.CredentialStateConfigured},
		&testTranscriber{name: "local-ready", kind: providers.KindLocal, credential: providers.CredentialStateNotRequired},
		&testTranscriber{name: "cloud-missing", kind: providers.KindCloud, credential: providers.CredentialStateMissing},
	)})
	options, err := app.ProviderOptions()
	if err != nil {
		t.Fatalf("ProviderOptions() error = %v", err)
	}
	if len(options) != 3 {
		t.Fatalf("ProviderOptions() count = %d, want 3", len(options))
	}
	if options[0].Provider != "local-ready" || !options[0].Available || options[1].Provider != "cloud-ready" || !options[1].Available {
		t.Fatalf("available option order = %+v", options)
	}
	if options[2].Provider != "cloud-missing" || options[2].Available || options[2].UnavailableReason == "" {
		t.Fatalf("unavailable option = %+v", options[2])
	}
}

func TestTranscribeFencesProviderReplacementBeforeDisclosure(t *testing.T) {
	t.Parallel()

	first := &testTranscriber{name: "speech", kind: providers.KindCloud, credential: providers.CredentialStateConfigured}
	replacement := &testTranscriber{name: "speech", kind: providers.KindCloud, credential: providers.CredentialStateConfigured}
	registry := providers.NewMutableRegistry(first)
	app := New(Options{Registry: registry})
	route, err := app.ResolveRoute("speech")
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	registry.Replace(replacement)

	_, err = app.Transcribe(context.Background(), TranscribeCommand{Route: route, Audio: []byte("private audio")})
	if err == nil || !errors.Is(err, ErrProviderChanged) {
		t.Fatalf("Transcribe() error = %v, want ErrProviderChanged", err)
	}
	if first.calls.Load() != 0 || replacement.calls.Load() != 0 {
		t.Fatalf("audio disclosed: first calls=%d replacement calls=%d", first.calls.Load(), replacement.calls.Load())
	}
}

func TestTranscribeUsesOnlyExplicitRoute(t *testing.T) {
	t.Parallel()

	selected := &testTranscriber{name: "selected", kind: providers.KindCloud, credential: providers.CredentialStateConfigured, text: "selected transcript"}
	other := &testTranscriber{name: "other", kind: providers.KindCloud, credential: providers.CredentialStateConfigured, text: "other transcript"}
	app := New(Options{Registry: providers.NewRegistry(selected, other)})
	route, err := app.ResolveRoute("selected")
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	result, err := app.Transcribe(context.Background(), TranscribeCommand{Route: route, Audio: []byte("audio")})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.Text != "selected transcript" || selected.calls.Load() != 1 || other.calls.Load() != 0 {
		t.Fatalf("result=%+v selected calls=%d other calls=%d", result, selected.calls.Load(), other.calls.Load())
	}
}

type testTranscriber struct {
	name       string
	kind       providers.Kind
	credential providers.CredentialState
	text       string
	calls      atomic.Int64
}

func (p *testTranscriber) Name() string                               { return p.name }
func (p *testTranscriber) Kind() providers.Kind                       { return p.kind }
func (p *testTranscriber) DefaultModel() string                       { return "chat-model" }
func (p *testTranscriber) Supports(string) bool                       { return true }
func (p *testTranscriber) CredentialState() providers.CredentialState { return p.credential }
func (p *testTranscriber) Capabilities(context.Context) (providers.Capabilities, error) {
	return providers.Capabilities{Name: p.name, Kind: p.kind, DefaultModel: "chat-model"}, nil
}
func (p *testTranscriber) Chat(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
	return &types.ChatResponse{}, nil
}
func (p *testTranscriber) TranscriptionCapability() providers.TranscriptionCapability {
	return providers.TranscriptionCapability{DefaultModel: "speech-model"}
}
func (p *testTranscriber) Transcribe(context.Context, providers.TranscriptionRequest) (*providers.TranscriptionResponse, error) {
	p.calls.Add(1)
	text := p.text
	if text == "" {
		text = "transcript"
	}
	return &providers.TranscriptionResponse{Text: text, Model: "speech-model"}, nil
}
