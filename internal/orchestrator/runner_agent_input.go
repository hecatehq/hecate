package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

const agentInputRoutePersistenceTimeout = 5 * time.Second

// recordAgentInputProviderAttempt closes the recovery window immediately
// before gateway dispatch. A worker can disappear after the provider starts
// receiving a rich input but before it writes a step or artifact, so this
// narrow state transition must survive independently of finalization.
func (r *Runner) recordAgentInputProviderAttempt(ctx context.Context, task types.Task, run types.TaskRun, route types.RouteDecision) error {
	if r == nil || r.store == nil || strings.TrimSpace(run.InputRef) == "" || !route.ProviderInstance.Valid() {
		return nil
	}
	provider := strings.TrimSpace(route.Provider)
	if provider == "" {
		return fmt.Errorf("provider attempt route is missing a provider")
	}

	persistenceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentInputRoutePersistenceTimeout)
	defer cancel()
	result, err := r.store.RecordRichInputProviderAttempt(persistenceCtx, taskstate.RichInputProviderAttempt{
		TaskID:           task.ID,
		RunID:            run.ID,
		Provider:         provider,
		ProviderKind:     strings.TrimSpace(route.ProviderKind),
		Model:            strings.TrimSpace(route.Model),
		ProviderInstance: route.ProviderInstance,
	})
	if err != nil {
		return fmt.Errorf("persist provider-attempt route: %w", err)
	}
	if result.Applied {
		return nil
	}
	return fmt.Errorf("run status changed to %q before provider dispatch", result.Run.Status)
}
