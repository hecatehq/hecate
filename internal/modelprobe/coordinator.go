package modelprobe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	defaultLeaseDuration        = 45 * time.Second
	defaultVerifiedTTL          = 30 * 24 * time.Hour
	defaultInconclusiveCooldown = 90 * time.Second
)

// Coordinator coalesces same-process requests and uses Store leases to avoid
// duplicate paid provider calls across Hecate processes. It intentionally
// releases its mutex before invoking the caller's probe function.
type Coordinator struct {
	store                Store
	now                  func() time.Time
	leaseDuration        time.Duration
	verifiedTTL          time.Duration
	inconclusiveCooldown time.Duration

	mu       sync.Mutex
	inFlight map[string]*call
}

type call struct {
	done   chan struct{}
	record Record
	err    error
}

type Outcome struct {
	Status string
	Reason string
}

func NewCoordinator(store Store) *Coordinator {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Coordinator{
		store:                store,
		now:                  func() time.Time { return time.Now().UTC() },
		leaseDuration:        defaultLeaseDuration,
		verifiedTTL:          defaultVerifiedTTL,
		inconclusiveCooldown: defaultInconclusiveCooldown,
		inFlight:             make(map[string]*call),
	}
}

func (c *Coordinator) Store() Store {
	if c == nil {
		return nil
	}
	return c.store
}

// Verify returns an active cached result, joins an in-process probe, reports a
// lease held by another process as testing, or runs exactly one caller-owned
// probe. The caller must not execute tools: it is responsible only for a
// single harmless capability request.
func (c *Coordinator) Verify(ctx context.Context, key Key, run func(context.Context) Outcome) (Record, bool, error) {
	if c == nil || c.store == nil {
		return Record{}, false, ErrInvalid
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return Record{}, false, err
	}
	flightKey := memoryKey(key)
	c.mu.Lock()
	if existing := c.inFlight[flightKey]; existing != nil {
		c.mu.Unlock()
		select {
		case <-existing.done:
			return existing.record, false, existing.err
		case <-ctx.Done():
			return Record{}, false, ctx.Err()
		}
	}
	active := &call{done: make(chan struct{})}
	c.inFlight[flightKey] = active
	c.mu.Unlock()

	record, performed, err := c.verify(ctx, key, run)

	c.mu.Lock()
	active.record = record
	active.err = err
	delete(c.inFlight, flightKey)
	close(active.done)
	c.mu.Unlock()
	return record, performed, err
}

func (c *Coordinator) verify(ctx context.Context, key Key, run func(context.Context) Outcome) (Record, bool, error) {
	now := c.nowUTC()
	leaseID, err := randomLeaseID()
	if err != nil {
		return Record{}, false, err
	}
	record, acquired, err := c.store.Acquire(ctx, key, now, now.Add(c.leaseDuration), leaseID)
	if err != nil || !acquired {
		return record, false, err
	}

	outcome := Outcome{Status: StatusInconclusive, Reason: ReasonUnexpectedResult}
	// The lease may have been acquired immediately before the operator's
	// request disconnected. Never start a billable diagnostic once its caller
	// has already been canceled; complete the lease with a short cooldown
	// instead so a replacement request can decide whether to retry.
	if run != nil && ctx.Err() == nil {
		outcome = run(ctx)
	}
	completedAt := c.nowUTC()
	completed := record
	completed.Status = normalizeStatus(outcome.Status)
	if completed.Status == "" || completed.Status == StatusTesting {
		completed.Status = StatusInconclusive
	}
	completed.Reason = normalizeReason(outcome.Reason)
	completed.CheckedAt = completedAt
	if completed.Status == StatusSupported || completed.Status == StatusUnsupported {
		completed.ExpiresAt = completedAt.Add(c.verifiedTTL)
	} else {
		completed.ExpiresAt = completedAt.Add(c.inconclusiveCooldown)
	}
	// A browser navigation or client disconnect must not leave a durable
	// `testing` lease behind after the upstream call has already returned.
	completeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completed, err = c.store.Complete(completeCtx, completed)
	return completed, true, err
}

func (c *Coordinator) nowUTC() time.Time {
	if c == nil || c.now == nil {
		return time.Now().UTC()
	}
	return c.now().UTC()
}

func randomLeaseID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
