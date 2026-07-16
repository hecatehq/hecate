package dictationapp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrNotConfigured       = errors.New("dictation is not configured")
	ErrProviderRequired    = errors.New("dictation provider is required")
	ErrProviderNotFound    = errors.New("dictation provider was not found")
	ErrProviderUnsupported = errors.New("provider does not support dictation")
	ErrProviderUnavailable = errors.New("dictation provider is unavailable")
	ErrProviderChanged     = errors.New("dictation provider changed before transcription")
	ErrModelInvalid        = errors.New("dictation model is invalid")
	ErrAudioRequired       = errors.New("dictation audio is required")
)

type Application struct {
	registry providers.Registry
}

type Options struct {
	Registry providers.Registry
}

type ProviderOption struct {
	Provider          string
	ProviderKind      string
	DefaultModel      string
	Available         bool
	UnavailableReason string
}

// Route is an internal execution fence. ProviderInstance must never be
// rendered through the HTTP API or telemetry.
type Route struct {
	Provider         string
	ProviderKind     string
	DefaultModel     string
	ProviderInstance types.ProviderInstanceIdentity
}

type TranscribeCommand struct {
	Route     Route
	Audio     []byte
	Filename  string
	MediaType string
	Model     string
}

type Transcription struct {
	Provider     string
	ProviderKind string
	Model        string
	Text         string
}

func New(opts Options) *Application {
	return &Application{registry: opts.Registry}
}

func (app *Application) ProviderOptions() ([]ProviderOption, error) {
	if app == nil || app.registry == nil {
		return nil, ErrNotConfigured
	}
	instances := app.registry.AllInstances()
	options := make([]ProviderOption, 0, len(instances))
	for _, instance := range instances {
		transcriber, ok := instance.Provider.(providers.Transcriber)
		if !ok {
			continue
		}
		capability := transcriber.TranscriptionCapability()
		if strings.TrimSpace(capability.DefaultModel) == "" {
			continue
		}
		available, reason := providerAvailable(instance.Provider)
		options = append(options, ProviderOption{
			Provider:          instance.Provider.Name(),
			ProviderKind:      string(instance.Provider.Kind()),
			DefaultModel:      strings.TrimSpace(capability.DefaultModel),
			Available:         available,
			UnavailableReason: reason,
		})
	}
	// Local-first is a product boundary, not just presentation: the default UI
	// selection should keep audio on the operator's machine when a local route
	// is configured and available.
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].Available != options[j].Available {
			return options[i].Available
		}
		if options[i].ProviderKind != options[j].ProviderKind {
			return options[i].ProviderKind == string(providers.KindLocal)
		}
		return strings.ToLower(options[i].Provider) < strings.ToLower(options[j].Provider)
	})
	return options, nil
}

func (app *Application) ResolveRoute(providerName string) (Route, error) {
	if app == nil || app.registry == nil {
		return Route{}, ErrNotConfigured
	}
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return Route{}, ErrProviderRequired
	}
	instance, ok := app.registry.GetInstance(providerName)
	if !ok {
		return Route{}, fmt.Errorf("%w: %s", ErrProviderNotFound, providerName)
	}
	transcriber, ok := instance.Provider.(providers.Transcriber)
	if !ok {
		return Route{}, fmt.Errorf("%w: %s", ErrProviderUnsupported, providerName)
	}
	capability := transcriber.TranscriptionCapability()
	if strings.TrimSpace(capability.DefaultModel) == "" {
		return Route{}, fmt.Errorf("%w: %s", ErrProviderUnsupported, providerName)
	}
	if available, reason := providerAvailable(instance.Provider); !available {
		return Route{}, fmt.Errorf("%w: %s", ErrProviderUnavailable, reason)
	}
	return Route{
		Provider:         instance.Provider.Name(),
		ProviderKind:     string(instance.Provider.Kind()),
		DefaultModel:     strings.TrimSpace(capability.DefaultModel),
		ProviderInstance: instance.Identity,
	}, nil
}

func (app *Application) Transcribe(ctx context.Context, cmd TranscribeCommand) (Transcription, error) {
	if app == nil || app.registry == nil {
		return Transcription{}, ErrNotConfigured
	}
	if len(cmd.Audio) == 0 {
		return Transcription{}, ErrAudioRequired
	}
	model := strings.TrimSpace(cmd.Model)
	if model == "" {
		model = strings.TrimSpace(cmd.Route.DefaultModel)
	}
	if !validModel(model) {
		return Transcription{}, ErrModelInvalid
	}

	current, ok := app.registry.GetInstance(cmd.Route.Provider)
	if !ok || !cmd.Route.ProviderInstance.Valid() || current.Identity != cmd.Route.ProviderInstance {
		return Transcription{}, ErrProviderChanged
	}
	transcriber, ok := current.Provider.(providers.Transcriber)
	if !ok || strings.TrimSpace(transcriber.TranscriptionCapability().DefaultModel) == "" {
		return Transcription{}, ErrProviderChanged
	}
	if available, _ := providerAvailable(current.Provider); !available {
		return Transcription{}, ErrProviderChanged
	}

	response, err := transcriber.Transcribe(ctx, providers.TranscriptionRequest{
		Audio:     cmd.Audio,
		Filename:  cmd.Filename,
		MediaType: cmd.MediaType,
		Model:     model,
	})
	if err != nil {
		return Transcription{}, err
	}
	if response == nil || strings.TrimSpace(response.Text) == "" {
		return Transcription{}, fmt.Errorf("provider returned an empty transcript")
	}
	resolvedModel := strings.TrimSpace(response.Model)
	if resolvedModel == "" {
		resolvedModel = model
	}
	return Transcription{
		Provider:     current.Provider.Name(),
		ProviderKind: string(current.Provider.Kind()),
		Model:        resolvedModel,
		Text:         strings.TrimSpace(response.Text),
	}, nil
}

func providerAvailable(provider providers.Provider) (bool, string) {
	if enabled, ok := provider.(providers.Enabler); ok && !enabled.Enabled() {
		return false, "provider is disabled"
	}
	if credential, ok := provider.(providers.CredentialReporter); ok && credential.CredentialState() == providers.CredentialStateMissing {
		return false, "provider credentials are missing"
	}
	if validator, ok := provider.(providers.Validator); ok {
		if err := validator.Validate(); err != nil {
			return false, "provider configuration is incomplete"
		}
	}
	return true, ""
}

func validModel(model string) bool {
	if model == "" || len(model) > 256 {
		return false
	}
	return strings.IndexFunc(model, unicode.IsControl) == -1
}
