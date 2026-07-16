package agentadapters

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxRetainedACPPromptStages        = 4
	maxRetainedACPPromptStagesProcess = 16
	acpPromptStageJanitorMin          = 100 * time.Millisecond
	acpPromptStageJanitorMax          = 30 * time.Second
)

var (
	errACPPromptStageCleanupPending   = errors.New("private staged prompt input cleanup is still pending")
	nextACPPromptStageSessionKey      atomic.Uint64
	processACPPromptStageCleanupOwner = newACPPromptStageCleanupOwner(maxRetainedACPPromptStagesProcess)
)

type acpPromptStageCleanupEntry struct {
	sessionKey uint64
	logger     *slog.Logger
}

// acpPromptStageCleanupOwner is the process-wide terminal owner for protected
// staging identities whose synchronous cleanup failed. One worker and one hard
// capacity bound cover retired as well as live sessions, so repeatedly closing
// sessions cannot accumulate an unbounded goroutine or handle set.
type acpPromptStageCleanupOwner struct {
	mu           sync.Mutex
	limit        int
	reservations int
	stages       map[*acpPromptStage]acpPromptStageCleanupEntry
	counts       map[uint64]int
	wake         chan struct{}
	changed      chan struct{}
	worker       bool
}

type acpPromptStageAdmission struct {
	mu         sync.Mutex
	owner      *acpPromptStageCleanupOwner
	sessionKey uint64
	active     bool
}

func newACPPromptStageCleanupOwner(limit int) *acpPromptStageCleanupOwner {
	if limit <= 0 {
		limit = 1
	}
	return &acpPromptStageCleanupOwner{
		limit:   limit,
		stages:  make(map[*acpPromptStage]acpPromptStageCleanupEntry),
		counts:  make(map[uint64]int),
		wake:    make(chan struct{}, 1),
		changed: make(chan struct{}),
	}
}

func (s *acpSession) promptStageCleanupKeyValue() uint64 {
	if s == nil {
		return 0
	}
	s.promptStageKeyOnce.Do(func() {
		key := nextACPPromptStageSessionKey.Add(1)
		if key == 0 {
			key = nextACPPromptStageSessionKey.Add(1)
		}
		s.promptStageKey = key
	})
	return s.promptStageKey
}

func (s *acpSession) promptStageCleanupOwnerValue() *acpPromptStageCleanupOwner {
	if s != nil && s.promptStageOwner != nil {
		return s.promptStageOwner
	}
	return processACPPromptStageCleanupOwner
}

func (s *acpSession) admitPromptFiles(fileCount int) (*acpPromptStageAdmission, error) {
	if s == nil || fileCount == 0 {
		return nil, nil
	}
	return s.promptStageCleanupOwnerValue().acquire(s.promptStageCleanupKeyValue())
}

func (o *acpPromptStageCleanupOwner) acquire(sessionKey uint64) (*acpPromptStageAdmission, error) {
	if o == nil || sessionKey == 0 {
		return nil, errACPPromptStageCleanupPending
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.counts[sessionKey] >= maxRetainedACPPromptStages || o.reservations+len(o.stages) >= o.limit {
		return nil, errACPPromptStageCleanupPending
	}
	o.reservations++
	o.counts[sessionKey]++
	return &acpPromptStageAdmission{owner: o, sessionKey: sessionKey, active: true}, nil
}

func (a *acpPromptStageAdmission) release() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.active {
		a.mu.Unlock()
		return
	}
	a.active = false
	owner := a.owner
	sessionKey := a.sessionKey
	a.mu.Unlock()
	owner.releaseReservation(sessionKey)
}

func (a *acpPromptStageAdmission) retain(stage *acpPromptStage, logger *slog.Logger) bool {
	if a == nil || stage == nil || stage.dir == "" {
		return false
	}
	a.mu.Lock()
	if !a.active {
		a.mu.Unlock()
		return false
	}
	a.active = false
	owner := a.owner
	sessionKey := a.sessionKey
	a.mu.Unlock()
	owner.retainReservation(sessionKey, stage, logger)
	return true
}

func (o *acpPromptStageCleanupOwner) releaseReservation(sessionKey uint64) {
	o.mu.Lock()
	if o.reservations > 0 {
		o.reservations--
	}
	o.decrementSessionLocked(sessionKey)
	o.notifyChangedLocked()
	o.mu.Unlock()
}

func (o *acpPromptStageCleanupOwner) retainReservation(sessionKey uint64, stage *acpPromptStage, logger *slog.Logger) {
	o.mu.Lock()
	if o.reservations > 0 {
		o.reservations--
	}
	o.stages[stage] = acpPromptStageCleanupEntry{sessionKey: sessionKey, logger: logger}
	if !o.worker {
		o.worker = true
		go o.run()
	}
	pendingSession := o.retainedCountLocked(sessionKey)
	pendingProcess := len(o.stages)
	o.notifyChangedLocked()
	wake := o.wake
	o.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
	if logger != nil {
		logger.Warn("private staged prompt input cleanup deferred",
			slog.Int("pending_session_stages", pendingSession),
			slog.Int("pending_process_stages", pendingProcess),
		)
	}
}

func (s *acpSession) cleanupPromptStage(stage *acpPromptStage, admission *acpPromptStageAdmission) error {
	if stage == nil {
		return nil
	}
	err := stage.cleanup()
	if err != nil && stage.dir != "" && admission != nil {
		admission.retain(stage, s.logger)
	}
	return err
}

func (s *acpSession) retainPromptStage(stage *acpPromptStage, admission *acpPromptStageAdmission) {
	if stage == nil || admission == nil {
		return
	}
	admission.retain(stage, s.logger)
}

func (s *acpSession) wakePromptStageCleanup() {
	if s == nil {
		return
	}
	s.promptStageCleanupOwnerValue().wakeCleanup()
}

func (o *acpPromptStageCleanupOwner) wakeCleanup() {
	if o == nil {
		return
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *acpPromptStageCleanupOwner) run() {
	delay := acpPromptStageJanitorMin
	for {
		o.mu.Lock()
		if len(o.stages) == 0 {
			o.worker = false
			o.mu.Unlock()
			return
		}
		o.mu.Unlock()

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-o.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		o.mu.Lock()
		stages := make([]*acpPromptStage, 0, len(o.stages))
		for stage := range o.stages {
			stages = append(stages, stage)
		}
		o.mu.Unlock()

		cleaned := 0
		for _, stage := range stages {
			if err := stage.cleanup(); err != nil && stage.dir != "" {
				continue
			}
			o.mu.Lock()
			entry, ok := o.stages[stage]
			if ok {
				delete(o.stages, stage)
				o.decrementSessionLocked(entry.sessionKey)
				o.notifyChangedLocked()
				cleaned++
			}
			pendingSession := o.retainedCountLocked(entry.sessionKey)
			pendingProcess := len(o.stages)
			o.mu.Unlock()
			if ok && entry.logger != nil {
				entry.logger.Info("private staged prompt input cleanup completed",
					slog.Int("pending_session_stages", pendingSession),
					slog.Int("pending_process_stages", pendingProcess),
				)
			}
		}

		if cleaned > 0 {
			delay = acpPromptStageJanitorMin
		} else if delay < acpPromptStageJanitorMax {
			delay *= 2
			if delay > acpPromptStageJanitorMax {
				delay = acpPromptStageJanitorMax
			}
		}
	}
}

func (s *acpSession) waitForPromptStageCleanup(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.promptStageCleanupOwnerValue().waitForSession(ctx, s.promptStageCleanupKeyValue())
}

func (o *acpPromptStageCleanupOwner) waitForSession(ctx context.Context, sessionKey uint64) error {
	if o == nil || sessionKey == 0 {
		return nil
	}
	for {
		o.mu.Lock()
		if o.counts[sessionKey] == 0 {
			o.mu.Unlock()
			return nil
		}
		changed := o.changed
		o.mu.Unlock()
		o.wakeCleanup()
		select {
		case <-changed:
			continue
		case <-ctx.Done():
			return errACPPromptStageCleanupPending
		}
	}
}

func (s *acpSession) pendingPromptStageCount() int {
	if s == nil {
		return 0
	}
	return s.promptStageCleanupOwnerValue().retainedCount(s.promptStageCleanupKeyValue())
}

func (o *acpPromptStageCleanupOwner) retainedCount(sessionKey uint64) int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.retainedCountLocked(sessionKey)
}

func (o *acpPromptStageCleanupOwner) retainedCountLocked(sessionKey uint64) int {
	count := 0
	for _, entry := range o.stages {
		if entry.sessionKey == sessionKey {
			count++
		}
	}
	return count
}

func (o *acpPromptStageCleanupOwner) decrementSessionLocked(sessionKey uint64) {
	if count := o.counts[sessionKey]; count > 1 {
		o.counts[sessionKey] = count - 1
	} else {
		delete(o.counts, sessionKey)
	}
}

func (o *acpPromptStageCleanupOwner) notifyChangedLocked() {
	close(o.changed)
	o.changed = make(chan struct{})
}
