package taskruncoord

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestOriginGate_CloseWaitsForAdmittedMutationAndRejectsLateAdmission(t *testing.T) {
	t.Parallel()

	gate := NewOriginGate()
	origin := Origin{Kind: " chat ", ID: " session_1 "}
	lease, err := gate.Begin(t.Context(), origin)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	closed := make(chan struct {
		closure *Closure
		err     error
	}, 1)
	go func() {
		closure, closeErr := gate.Close(context.Background(), origin)
		closed <- struct {
			closure *Closure
			err     error
		}{closure: closure, err: closeErr}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		lateLease, beginErr := gate.Begin(t.Context(), origin)
		if errors.Is(beginErr, ErrOriginRunAdmissionClosed) {
			break
		}
		if beginErr != nil {
			t.Fatalf("late Begin() error = %v, want ErrOriginRunAdmissionClosed", beginErr)
		}
		lateLease.Release()
		if time.Now().After(deadline) {
			t.Fatal("Close() did not close origin admission")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case result := <-closed:
		t.Fatalf("Close() returned before admitted mutation released: err=%v closure=%v", result.err, result.closure)
	case <-time.After(50 * time.Millisecond):
	}

	lease.Release()
	var closure *Closure
	select {
	case result := <-closed:
		if result.err != nil {
			t.Fatalf("Close() error = %v", result.err)
		}
		closure = result.closure
	case <-time.After(3 * time.Second):
		t.Fatal("Close() did not return after admitted mutation released")
	}
	if _, err := gate.Begin(t.Context(), origin); !errors.Is(err, ErrOriginRunAdmissionClosed) {
		t.Fatalf("Begin() while closure held error = %v, want ErrOriginRunAdmissionClosed", err)
	}
	closure.Release()
	newLease, err := gate.Begin(t.Context(), origin)
	if err != nil {
		t.Fatalf("Begin() after Release error = %v", err)
	}
	newLease.Release()
}

func TestOriginGate_CommitTombstonesOrigin(t *testing.T) {
	t.Parallel()

	gate := NewOriginGate()
	origin := Origin{Kind: "chat", ID: "session_deleted"}
	closure, err := gate.Close(t.Context(), origin)
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closure.Commit()
	closure.Release()

	if _, err := gate.Begin(t.Context(), origin); !errors.Is(err, ErrOriginRunAdmissionClosed) {
		t.Fatalf("Begin() after Commit error = %v, want ErrOriginRunAdmissionClosed", err)
	}
	lease, err := gate.Begin(t.Context(), Origin{Kind: "chat", ID: "session_other"})
	if err != nil {
		t.Fatalf("Begin(other origin) error = %v", err)
	}
	lease.Release()
}

func TestOriginGate_ValidatorAllowsCommittedStateReclamation(t *testing.T) {
	t.Parallel()

	gate := NewOriginGate()
	validationErr := fmt.Errorf("%w: owner missing", ErrOriginNotFound)
	gate.SetValidator(" chat ", func(context.Context, Origin) error { return validationErr })
	origin := Origin{Kind: "chat", ID: "session_deleted"}
	closure, err := gate.Close(t.Context(), origin)
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closure.Commit()

	gate.mu.Lock()
	retained := len(gate.states)
	gate.mu.Unlock()
	if retained != 0 {
		t.Fatalf("origin states after committed validated deletion = %d, want 0", retained)
	}
	if _, err := gate.Begin(t.Context(), origin); !errors.Is(err, ErrOriginUnavailable) || !errors.Is(err, validationErr) {
		t.Fatalf("Begin() after reclaimed commit error = %v, want validator-backed origin unavailable", err)
	}

	projectOrigin := Origin{Kind: "project_work_item", ID: "work_deleted"}
	projectClosure, err := gate.Close(t.Context(), projectOrigin)
	if err != nil {
		t.Fatalf("Close(project origin) error = %v", err)
	}
	projectClosure.Commit()
	if _, err := gate.Begin(t.Context(), projectOrigin); !errors.Is(err, ErrOriginRunAdmissionClosed) {
		t.Fatalf("Begin(project origin) after chat validator commit error = %v, want retained tombstone", err)
	}
}

func TestOriginGate_ValidatorFailureReleasesAdmission(t *testing.T) {
	t.Parallel()

	gate := NewOriginGate()
	validationErr := errors.New("owner store unavailable")
	gate.SetValidator("chat", func(context.Context, Origin) error { return validationErr })
	origin := Origin{Kind: "chat", ID: "session_missing"}
	if _, err := gate.Begin(t.Context(), origin); !errors.Is(err, ErrOriginValidationFailed) || errors.Is(err, ErrOriginUnavailable) || !errors.Is(err, validationErr) || err.Error() != ErrOriginValidationFailed.Error() {
		t.Fatalf("Begin() error = %v, want sanitized ErrOriginValidationFailed with validation cause", err)
	}

	closure, err := gate.Close(t.Context(), origin)
	if err != nil {
		t.Fatalf("Close() after failed validation error = %v", err)
	}
	closure.Release()
}

func TestOriginGate_PartialOriginFailsClosed(t *testing.T) {
	t.Parallel()

	gate := NewOriginGate()
	for _, origin := range []Origin{{Kind: "chat"}, {ID: "session_without_kind"}} {
		if _, err := gate.Begin(t.Context(), origin); !errors.Is(err, ErrOriginUnavailable) {
			t.Fatalf("Begin(%+v) error = %v, want ErrOriginUnavailable", origin, err)
		}
	}
	lease, err := gate.Begin(t.Context(), Origin{})
	if err != nil {
		t.Fatalf("Begin(unowned task) error = %v", err)
	}
	lease.Release()
}

func TestOriginGate_CloseCancellationReopensOrigin(t *testing.T) {
	t.Parallel()

	gate := NewOriginGate()
	origin := Origin{Kind: "chat", ID: "session_cancelled_close"}
	lease, err := gate.Begin(t.Context(), origin)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if closure, err := gate.Close(ctx, origin); !errors.Is(err, context.Canceled) || closure != nil {
		t.Fatalf("Close(cancelled) = closure %v, err %v; want nil/context.Canceled", closure, err)
	}
	lease.Release()
	newLease, err := gate.Begin(t.Context(), origin)
	if err != nil {
		t.Fatalf("Begin() after cancelled Close error = %v", err)
	}
	newLease.Release()
}
