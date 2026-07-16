package taskruncoord

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type originUnavailableError struct {
	cause error
}

func (err *originUnavailableError) Error() string {
	return ErrOriginUnavailable.Error()
}

func (err *originUnavailableError) Unwrap() []error {
	return []error{ErrOriginUnavailable, err.cause}
}

type originValidationError struct {
	cause error
}

func (err *originValidationError) Error() string {
	return ErrOriginValidationFailed.Error()
}

func (err *originValidationError) Unwrap() []error {
	return []error{ErrOriginValidationFailed, err.cause}
}

var (
	ErrOriginRunAdmissionClosed = errors.New("task origin is closed to new runs")
	ErrOriginUnavailable        = errors.New("task origin is unavailable")
	// ErrOriginNotFound is returned by a validator only after its durable
	// owner store has confirmed absence. Gate callers may then settle stale
	// work instead of retrying it indefinitely.
	ErrOriginNotFound = errors.New("task origin was not found")
	// ErrOriginValidationFailed distinguishes a transient validator/store
	// failure from confirmed owner absence. Its public string is deliberately
	// generic so HTTP callers never receive storage details.
	ErrOriginValidationFailed = errors.New("task origin validation failed")
)

// Origin identifies the durable owner whose deletion must settle every run
// before that owner disappears.
type Origin struct {
	Kind string
	ID   string
}

func (origin Origin) normalize() Origin {
	return Origin{
		Kind: strings.TrimSpace(origin.Kind),
		ID:   strings.TrimSpace(origin.ID),
	}
}

func (origin Origin) unowned() bool {
	return origin.Kind == "" && origin.ID == ""
}

func (origin Origin) complete() bool {
	return origin.Kind != "" && origin.ID != ""
}

// Validator lets the API composition layer reject a run whose durable owner
// was already deleted, including after a process restart lost in-memory
// tombstones. It runs after admission is counted, so a concurrent Close waits
// until validation and the guarded mutation finish.
type Validator func(context.Context, Origin) error

type originState struct {
	active     int
	closures   int
	tombstoned bool
	changed    chan struct{}
}

// Gate serializes origin-owned run creation against destructive cleanup. It is
// process-scoped and safe for concurrent use.
type Gate struct {
	mu         sync.Mutex
	states     map[Origin]*originState
	validators map[string]Validator
}

func NewOriginGate() *Gate {
	return &Gate{
		states:     make(map[Origin]*originState),
		validators: make(map[string]Validator),
	}
}

// SetValidator installs durable owner validation for one origin kind.
// Committed tombstones are only reclaimable when their exact origin kind has a
// validator.
func (gate *Gate) SetValidator(kind string, validator Validator) {
	if gate == nil || validator == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return
	}
	gate.mu.Lock()
	gate.validators[kind] = validator
	for origin, state := range gate.states {
		if origin.Kind == kind {
			gate.pruneLocked(origin, state)
		}
	}
	gate.mu.Unlock()
}

// Lease holds one admitted origin mutation. Call Release after the runner has
// either persisted the run or failed without creating it.
type Lease struct {
	gate   *Gate
	origin Origin
	once   sync.Once
}

func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		if lease.gate == nil || !lease.origin.complete() {
			return
		}
		lease.gate.releaseAdmission(lease.origin)
	})
}

// Begin admits one run mutation unless cleanup already owns the origin. A task
// with no origin remains independent; a partial origin fails closed.
func (gate *Gate) Begin(ctx context.Context, origin Origin) (*Lease, error) {
	origin = origin.normalize()
	lease := &Lease{gate: gate, origin: origin}
	if gate == nil || origin.unowned() {
		return lease, nil
	}
	if !origin.complete() {
		return nil, ErrOriginUnavailable
	}

	gate.mu.Lock()
	state := gate.stateLocked(origin)
	if state.tombstoned || state.closures > 0 {
		gate.mu.Unlock()
		return nil, ErrOriginRunAdmissionClosed
	}
	state.active++
	validator := gate.validators[origin.Kind]
	gate.mu.Unlock()

	if validator != nil {
		if err := validator(ctx, origin); err != nil {
			lease.Release()
			if errors.Is(err, ErrOriginNotFound) {
				return nil, &originUnavailableError{cause: err}
			}
			return nil, &originValidationError{cause: err}
		}
	}
	return lease, nil
}

func (gate *Gate) releaseAdmission(origin Origin) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	state, ok := gate.states[origin]
	if !ok || state.active == 0 {
		return
	}
	state.active--
	gate.notifyLocked(state)
	gate.pruneLocked(origin, state)
}

// Closure keeps an origin closed while its owner is being deleted. Release
// reopens the origin after a failed deletion. Commit marks successful deletion;
// the gate retains that tombstone unless the exact origin kind has a durable
// validator that can safely preserve the decision after state reclamation.
type Closure struct {
	gate   *Gate
	origin Origin
	once   sync.Once
}

func (closure *Closure) Release() {
	closure.finish(false)
}

func (closure *Closure) Commit() {
	closure.finish(true)
}

func (closure *Closure) finish(commit bool) {
	if closure == nil {
		return
	}
	closure.once.Do(func() {
		if closure.gate == nil || !closure.origin.complete() {
			return
		}
		closure.gate.finishClosure(closure.origin, commit)
	})
}

// Close blocks new admissions and waits for every already-admitted mutation.
// The returned closure must be released or committed by the destructive owner.
func (gate *Gate) Close(ctx context.Context, origin Origin) (*Closure, error) {
	origin = origin.normalize()
	closure := &Closure{gate: gate, origin: origin}
	if gate == nil || origin.unowned() {
		return closure, nil
	}
	if !origin.complete() {
		return nil, ErrOriginUnavailable
	}

	gate.mu.Lock()
	state := gate.stateLocked(origin)
	state.closures++
	for state.active > 0 {
		changed := state.changed
		gate.mu.Unlock()
		select {
		case <-ctx.Done():
			gate.finishClosure(origin, false)
			return nil, ctx.Err()
		case <-changed:
		}
		gate.mu.Lock()
		state = gate.stateLocked(origin)
	}
	gate.mu.Unlock()
	return closure, nil
}

func (gate *Gate) finishClosure(origin Origin, commit bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	state, ok := gate.states[origin]
	if !ok {
		return
	}
	if commit {
		state.tombstoned = true
	}
	if state.closures > 0 {
		state.closures--
	}
	gate.notifyLocked(state)
	gate.pruneLocked(origin, state)
}

func (gate *Gate) stateLocked(origin Origin) *originState {
	state := gate.states[origin]
	if state == nil {
		state = &originState{changed: make(chan struct{})}
		gate.states[origin] = state
	}
	return state
}

func (gate *Gate) notifyLocked(state *originState) {
	close(state.changed)
	state.changed = make(chan struct{})
}

func (gate *Gate) pruneLocked(origin Origin, state *originState) {
	if state.active == 0 && state.closures == 0 && (!state.tombstoned || gate.validators[origin.Kind] != nil) {
		delete(gate.states, origin)
	}
}
