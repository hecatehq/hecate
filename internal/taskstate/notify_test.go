package taskstate

import (
	"testing"
	"time"
)

func expectWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected a wake signal, got none")
	}
}

func expectNoWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("expected no wake signal, but received one")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRunEventBusSignalsSubscriber(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	defer unsubscribe()

	bus.signalRun("run-1")
	expectWake(t, ch)
}

func TestRunEventBusCoalescesAndRecovers(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	defer unsubscribe()

	// Two signals before a read collapse to one buffered wake — the
	// reader re-reads the store either way, so the duplicate is noise.
	bus.signalRun("run-1")
	bus.signalRun("run-1")
	expectWake(t, ch)
	expectNoWake(t, ch)

	// The channel is reusable after draining.
	bus.signalRun("run-1")
	expectWake(t, ch)
}

func TestRunEventBusScopesByRun(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	defer unsubscribe()

	bus.signalRun("run-2")
	expectNoWake(t, ch)

	bus.signalRun("") // empty run id is a no-op, must not panic
	expectNoWake(t, ch)
}

func TestRunEventBusUnsubscribeStopsDelivery(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	unsubscribe()

	// Signalling a removed subscriber must neither deliver nor panic.
	bus.signalRun("run-1")
	expectNoWake(t, ch)
}

func TestRunEventBusFansOutToMultipleSubscribers(t *testing.T) {
	var bus runEventBus
	a, unsubA := bus.SubscribeRun("run-1")
	defer unsubA()
	b, unsubB := bus.SubscribeRun("run-1")
	defer unsubB()

	bus.signalRun("run-1")
	expectWake(t, a)
	expectWake(t, b)
}
