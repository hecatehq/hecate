package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const executableTrustApprovedByOperator = "operator"

type executableTrustContextKey struct{}

type executableTrustContext struct {
	manager   *ExecutableTrustManager
	adapterID string
}

// WithExecutableTrust binds one adapter's execution policy to a call tree.
// Process-launch seams consult this context immediately before starting a
// child. The embedded provider runner retains the same manager for deferred
// provider launches after the initial ACP session has been created.
func WithExecutableTrust(ctx context.Context, manager *ExecutableTrustManager, adapterID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if manager == nil {
		return ctx
	}
	return context.WithValue(ctx, executableTrustContextKey{}, executableTrustContext{
		manager:   manager,
		adapterID: strings.TrimSpace(adapterID),
	})
}

func executableTrustFromContext(ctx context.Context) (*ExecutableTrustManager, string) {
	if ctx == nil {
		return nil, ""
	}
	value, _ := ctx.Value(executableTrustContextKey{}).(executableTrustContext)
	return value.manager, strings.TrimSpace(value.adapterID)
}

// ExecutableTrustManager owns executable measurement, operator approval, and
// final-spawn serialization for one Hecate runtime host. It is intentionally
// separate from ACP tool approval grants: this decision answers whether Hecate
// may start an app at all, not which actions that app may take after launch.
type ExecutableTrustManager struct {
	runtimeHostID string
	store         ExecutableTrustStore
	now           func() time.Time

	locksMu sync.Mutex
	locks   map[string]*sync.RWMutex
}

func NewExecutableTrustManager(runtimeHostID string, store ExecutableTrustStore) *ExecutableTrustManager {
	runtimeHostID = strings.TrimSpace(runtimeHostID)
	if runtimeHostID == "" {
		runtimeHostID = "local"
	}
	return &ExecutableTrustManager{
		runtimeHostID: runtimeHostID,
		store:         store,
		now:           time.Now,
		locks:         make(map[string]*sync.RWMutex),
	}
}

func (m *ExecutableTrustManager) RuntimeHostID() string {
	if m == nil {
		return ""
	}
	return m.runtimeHostID
}

func (m *ExecutableTrustManager) lockFor(adapterID string) *sync.RWMutex {
	adapterID = strings.TrimSpace(adapterID)
	m.locksMu.Lock()
	defer m.locksMu.Unlock()
	lock := m.locks[adapterID]
	if lock == nil {
		lock = &sync.RWMutex{}
		m.locks[adapterID] = lock
	}
	return lock
}

// InspectAdapter passively resolves and measures the executable currently
// selected for adapterID. It never runs the discovered app.
func (m *ExecutableTrustManager) InspectAdapter(ctx context.Context, adapterID string) ExecutableTrustStatus {
	adapter, ok := BuiltInByID(strings.TrimSpace(adapterID))
	if !ok {
		return unavailableExecutableTrustStatus("adapter_not_found")
	}
	path, err := resolveAdapterPeerExecutable(ctx, adapter, nil)
	if err != nil {
		return m.unavailableStatus(ctx, adapter.ID, "executable_not_found")
	}
	return m.InspectPath(ctx, adapter.ID, path)
}

// InspectPath compares a passively measured path with the approved identity.
// The supplied path must come from Hecate's own resolver, never from a client.
func (m *ExecutableTrustManager) InspectPath(ctx context.Context, adapterID, path string) ExecutableTrustStatus {
	if m == nil || m.store == nil {
		return unavailableExecutableTrustStatus("trust_store_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return unavailableExecutableTrustStatus("request_cancelled")
	}
	if strings.TrimSpace(path) == "" {
		return m.unavailableStatus(ctx, adapterID, "executable_not_found")
	}
	if strings.HasPrefix(path, "dev-override://") {
		return ExecutableTrustStatus{
			SchemaVersion: ExecutableIdentitySchemaVersion,
			State:         ExecutableTrustStateApproved,
			Reason:        "development_override",
		}
	}
	identity, err := measureExecutableIdentityContext(ctx, path)
	if err != nil {
		return m.unavailableStatus(ctx, adapterID, "measurement_failed")
	}
	record, err := m.store.Get(ctx, m.runtimeHostID, strings.TrimSpace(adapterID))
	if errors.Is(err, ErrExecutableTrustNotFound) {
		current := cloneExecutableIdentity(identity)
		return ExecutableTrustStatus{
			SchemaVersion: ExecutableIdentitySchemaVersion,
			State:         ExecutableTrustStateUnapproved,
			Reason:        "approval_required",
			Current:       &current,
		}
	}
	if err != nil {
		return unavailableExecutableTrustStatus("trust_store_unavailable")
	}
	current := cloneExecutableIdentity(identity)
	approved := cloneExecutableIdentity(record.Identity)
	approvedAt := record.ApprovedAt
	state := ExecutableTrustStateApproved
	reason := "identity_approved"
	if !sameExecutableIdentity(identity, record.Identity) {
		state = ExecutableTrustStateChanged
		reason = "identity_changed"
	}
	return ExecutableTrustStatus{
		SchemaVersion: ExecutableIdentitySchemaVersion,
		State:         state,
		Reason:        reason,
		Current:       &current,
		Approved:      &approved,
		ApprovedBy:    record.ApprovedBy,
		ApprovedAt:    &approvedAt,
	}
}

func (m *ExecutableTrustManager) unavailableStatus(ctx context.Context, adapterID, reason string) ExecutableTrustStatus {
	status := unavailableExecutableTrustStatus(reason)
	if m == nil || m.store == nil {
		return status
	}
	record, err := m.store.Get(ctx, m.runtimeHostID, strings.TrimSpace(adapterID))
	if err != nil {
		return status
	}
	approved := cloneExecutableIdentity(record.Identity)
	approvedAt := record.ApprovedAt
	status.Approved = &approved
	status.ApprovedBy = record.ApprovedBy
	status.ApprovedAt = &approvedAt
	return status
}

func unavailableExecutableTrustStatus(reason string) ExecutableTrustStatus {
	return ExecutableTrustStatus{
		SchemaVersion: ExecutableIdentitySchemaVersion,
		State:         ExecutableTrustStateUnavailable,
		Reason:        strings.TrimSpace(reason),
	}
}

func sameExecutableIdentity(a, b ExecutableIdentity) bool {
	return strings.TrimSpace(a.SchemaVersion) == strings.TrimSpace(b.SchemaVersion) &&
		strings.TrimSpace(a.IdentityToken) != "" &&
		strings.TrimSpace(a.IdentityToken) == strings.TrimSpace(b.IdentityToken)
}

// Approve re-resolves and re-measures server-side. expectedIdentity is only a
// compare-and-swap token for the identity the operator reviewed; no client path
// or digest is ever persisted as an observed fact.
func (m *ExecutableTrustManager) Approve(ctx context.Context, adapterID, expectedIdentity, approvedBy string) (ExecutableTrustRecord, error) {
	if m == nil || m.store == nil {
		return ExecutableTrustRecord{}, fmt.Errorf("%w: trust store is unavailable", ErrExecutableIdentityUnavailable)
	}
	adapterID = strings.TrimSpace(adapterID)
	adapter, ok := BuiltInByID(adapterID)
	if !ok {
		return ExecutableTrustRecord{}, fmt.Errorf("agent adapter %q not found", adapterID)
	}
	lock := m.lockFor(adapterID)
	lock.Lock()
	defer lock.Unlock()

	path, err := resolveAdapterPeerExecutable(ctx, adapter, nil)
	if err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("%w: %v", ErrExecutableIdentityUnavailable, err)
	}
	identity, err := measureExecutableIdentityContext(ctx, path)
	if err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("measure executable identity: %w", err)
	}
	if strings.TrimSpace(expectedIdentity) == "" || expectedIdentity != identity.IdentityToken {
		return ExecutableTrustRecord{}, ErrExecutableTrustConflict
	}
	approvedBy = strings.TrimSpace(approvedBy)
	if approvedBy == "" {
		approvedBy = executableTrustApprovedByOperator
	}
	record := ExecutableTrustRecord{
		RuntimeHostID: m.runtimeHostID,
		AdapterID:     adapter.ID,
		Identity:      cloneExecutableIdentity(identity),
		ApprovedBy:    approvedBy,
		ApprovedAt:    m.now().UTC(),
	}
	record, err = m.store.Approve(ctx, record)
	if err != nil {
		return ExecutableTrustRecord{}, fmt.Errorf("persist executable approval: %w", err)
	}
	return cloneExecutableTrustRecord(record), nil
}

func (m *ExecutableTrustManager) Revoke(ctx context.Context, adapterID string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("%w: trust store is unavailable", ErrExecutableIdentityUnavailable)
	}
	adapterID = strings.TrimSpace(adapterID)
	if _, ok := BuiltInByID(adapterID); !ok {
		return fmt.Errorf("agent adapter %q not found", adapterID)
	}
	lock := m.lockFor(adapterID)
	lock.Lock()
	defer lock.Unlock()
	if err := m.store.Revoke(ctx, m.runtimeHostID, adapterID); err != nil {
		return fmt.Errorf("revoke executable approval: %w", err)
	}
	return nil
}

// ExecutablePermit holds the in-process approval/read lease until the caller
// has started the child process. Close is idempotent.
type ExecutablePermit struct {
	Identity ExecutableIdentity
	once     sync.Once
	release  func()
}

func (p *ExecutablePermit) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.release != nil {
			p.release()
		}
	})
}

// AuthorizePath performs the final measurement and approval comparison for an
// already-resolved Hecate-owned path. Callers must hold the returned permit
// until cmd.Start (or the equivalent provider-runner start) has completed.
func (m *ExecutableTrustManager) AuthorizePath(ctx context.Context, adapterID, path string) (*ExecutablePermit, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("%w: trust store is unavailable", ErrExecutableIdentityUnavailable)
	}
	adapterID = strings.TrimSpace(adapterID)
	lock := m.lockFor(adapterID)
	lock.RLock()
	release := func() { lock.RUnlock() }

	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	identity, err := measureExecutableIdentityContext(ctx, path)
	if err != nil {
		release()
		return nil, fmt.Errorf("measure executable identity: %w", err)
	}
	record, err := m.store.Get(ctx, m.runtimeHostID, adapterID)
	if errors.Is(err, ErrExecutableTrustNotFound) {
		release()
		return nil, ErrExecutableTrustRequired
	}
	if err != nil {
		release()
		return nil, fmt.Errorf("%w: %v", ErrExecutableIdentityUnavailable, err)
	}
	if !sameExecutableIdentity(identity, record.Identity) {
		release()
		return nil, ErrExecutableIdentityChanged
	}
	return &ExecutablePermit{Identity: cloneExecutableIdentity(identity), release: release}, nil
}

// executableTrustBoundaryError collapses internal measurement detail before a
// trust failure crosses the embedded ACP JSON-RPC boundary. The peer can then
// return one exact, non-sensitive sentinel for Hecate to classify locally.
func executableTrustBoundaryError(err error) error {
	switch {
	case errors.Is(err, ErrExecutableTrustRequired):
		return ErrExecutableTrustRequired
	case errors.Is(err, ErrExecutableIdentityChanged), errors.Is(err, ErrExecutableTrustConflict):
		return ErrExecutableIdentityChanged
	case errors.Is(err, ErrExecutableIdentityRaced):
		return ErrExecutableIdentityRaced
	case errors.Is(err, ErrExecutableIdentityUnavailable):
		return ErrExecutableIdentityUnavailable
	default:
		return err
	}
}

// embeddedACPExecutableTrustError restores only exact Hecate-owned trust
// sentinels returned by an in-process adapter. Direct ACP peers remain
// untrusted and cannot manufacture local policy errors through error data.
func embeddedACPExecutableTrustError(adapter Adapter, err error) error {
	if err == nil || !adapterUsesEmbeddedServer(adapter) {
		return err
	}
	var requestErr *acp.RequestError
	if !errors.As(err, &requestErr) {
		return err
	}
	data, ok := requestErr.Data.(map[string]any)
	if !ok {
		return err
	}
	message, ok := data["error"].(string)
	if !ok {
		return err
	}
	switch message {
	case ErrExecutableTrustRequired.Error():
		return ErrExecutableTrustRequired
	case ErrExecutableIdentityChanged.Error():
		return ErrExecutableIdentityChanged
	case ErrExecutableIdentityRaced.Error():
		return ErrExecutableIdentityRaced
	case ErrExecutableIdentityUnavailable.Error():
		return ErrExecutableIdentityUnavailable
	default:
		return err
	}
}
