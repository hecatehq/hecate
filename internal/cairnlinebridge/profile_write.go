package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
)

// UpsertAgentProfile mirrors a Hecate Agent Preset plus its runtime posture
// into Cairnline legacy compatibility records. It is metadata/config only; it
// does not grant tools or launch any runtime.
func UpsertAgentProfile(ctx context.Context, service *cairnline.Service, profile agentprofiles.Profile) (cairnline.AgentProfile, error) {
	if service == nil {
		return cairnline.AgentProfile{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := AgentProfile(profile)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.AgentProfile{}, errors.Join(cairnline.ErrInvalid, errors.New("agent preset id is required"))
	}
	if err := upsertExecutionProfile(ctx, service, ExecutionProfile(profile)); err != nil {
		return cairnline.AgentProfile{}, err
	}
	written, err := service.UpdateAgentProfile(ctx, item)
	if err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.AgentProfile{}, err
		}
		return service.CreateAgentProfile(ctx, item)
	}
	return written, nil
}

// DeleteAgentProfile removes only the legacy profile compatibility metadata.
// Execution profiles are intentionally left in place because Hecate presets may
// share the same execution_profile hint.
func DeleteAgentProfile(ctx context.Context, service *cairnline.Service, id string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("agent preset id is required"))
	}
	return service.DeleteAgentProfile(ctx, id)
}
