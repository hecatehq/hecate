package browserrunner

import (
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestBrowserProtocolEventsScopeAndDecode(t *testing.T) {
	t.Parallel()
	var events browserProtocolEvents
	var browserCalls, pageCalls int
	events.listen("", func(event any) {
		if _, ok := event.(*target.EventAttachedToTarget); !ok {
			t.Errorf("browser event = %T", event)
		}
		browserCalls++
	})
	events.listen("page-session", func(event any) {
		paused, ok := event.(*fetch.EventRequestPaused)
		if !ok || paused.RequestID != "request-1" || paused.Request == nil || paused.Request.Method != "POST" {
			t.Errorf("page event = %#v", event)
		}
		pageCalls++
	})
	for _, payload := range []string{
		`{"method":"Target.attachedToTarget","params":{"sessionId":"child","targetInfo":{"targetId":"popup"}}}`,
		`{"method":"Fetch.requestPaused","sessionId":"page-session","params":{"requestId":"request-1","request":{"url":"https://example.test/","method":"POST"}}}`,
		`{"method":"Fetch.requestPaused","sessionId":"other-session","params":null}`,
		`{"id":1,"result":{}}`,
		`{"method":"Some.newEvent","sessionId":"page-session","params":{}}`,
	} {
		if err := events.observe([]byte(payload)); err != nil {
			t.Fatalf("observe() error = %v", err)
		}
	}
	if browserCalls != 1 || pageCalls != 1 {
		t.Fatalf("calls browser=%d page=%d", browserCalls, pageCalls)
	}
	for _, payload := range []string{
		`{`,
		`{"method":"Fetch.requestPaused","sessionId":"page-session","params":null}`,
		`{"method":"Fetch.requestPaused","sessionId":"page-session","params":{"request":false}}`,
	} {
		if !errors.Is(events.observe([]byte(payload)), errBrowserProtocolEventMalformed) {
			t.Fatal("malformed observed event did not fail closed")
		}
	}
}

func TestBrowserProtocolRelayObservesBeforeForwardingAndDrains(t *testing.T) {
	t.Parallel()
	upstream, browser := net.Pipe()
	downstream, client := net.Pipe()
	defer upstream.Close()
	defer browser.Close()
	defer downstream.Close()
	defer client.Close()
	proxy := &boundedCDPProxy{limit: 1024}
	drain := newListenerDrain()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseObserver := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseObserver()
	var observed atomic.Int32
	proxy.events.listen("session", drain.wrap(func(any) {
		close(entered)
		<-release
		observed.Add(1)
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.forward("browser", &boundedWebSocketConn{Conn: upstream}, &boundedWebSocketConn{Conn: downstream}, ws.StateClientSide, ws.StateServerSide, &browserProtocolBudget{remaining: 4096})
	}()
	payload := []byte(`{"method":"Fetch.requestPaused","sessionId":"session","params":{"requestId":"blocked","request":{"method":"POST"}}}`)
	write := make(chan error, 1)
	go func() { write <- wsutil.WriteServerMessage(browser, ws.OpText, payload) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("observer not entered")
	}
	awaitTestWrite(t, write)
	drain.stop()
	drained := make(chan struct{})
	go func() { drain.wait(); close(drained) }()
	select {
	case <-drained:
		t.Fatal("drain completed during admitted observation")
	default:
	}
	_ = client.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
	var one [1]byte
	if _, err := client.Read(one[:]); err == nil {
		t.Fatal("event was forwarded before observation completed")
	}
	_ = client.SetReadDeadline(time.Time{})
	releaseObserver()
	messages, err := wsutil.ReadServerMessage(client, nil)
	if err != nil || len(messages) != 1 || string(messages[0].Payload) != string(payload) || observed.Load() != 1 {
		t.Fatalf("forwarded before completed observation: %v", err)
	}
	<-drained
	// No observer work can enter after the drain fence, even while transport
	// shutdown is still finishing.
	if err := proxy.events.observe(payload); err != nil || observed.Load() != 1 {
		t.Fatal("late event bypassed drain")
	}
	_ = browser.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not exit")
	}
}

func TestBrowserProtocolRelayRejectsBeforeObservingOversizedEvents(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		limit, budget int64
	}{
		{"message", 16, 1024}, {"session", 1024, 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream, browser := net.Pipe()
			downstream, client := net.Pipe()
			defer upstream.Close()
			defer browser.Close()
			defer downstream.Close()
			defer client.Close()
			var calls atomic.Int32
			proxy := &boundedCDPProxy{limit: test.limit}
			proxy.events.listen("", func(any) { calls.Add(1) })
			done := make(chan struct{})
			go func() {
				defer close(done)
				proxy.forward("browser", &boundedWebSocketConn{Conn: upstream}, &boundedWebSocketConn{Conn: downstream}, ws.StateClientSide, ws.StateServerSide, &browserProtocolBudget{remaining: test.budget})
			}()
			go func() {
				_ = wsutil.WriteServerMessage(browser, ws.OpText, []byte(`{"method":"Browser.downloadWillBegin","params":{"url":"`+strings.Repeat("x", 32)+`"}}`))
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("oversized event was not rejected")
			}
			if calls.Load() != 0 || !proxy.terminalFailureWasObserved() {
				t.Fatal("protocol budget did not reject before observation")
			}
		})
	}
}

func TestBrowserProtocolRelayMalformedObservedEventAbortsWithoutForwarding(t *testing.T) {
	t.Parallel()
	upstream, browser := net.Pipe()
	downstream, client := net.Pipe()
	defer upstream.Close()
	defer browser.Close()
	defer downstream.Close()
	defer client.Close()
	var aborted atomic.Int32
	proxy := &boundedCDPProxy{limit: 1024, onFatal: func() { aborted.Add(1) }}
	proxy.events.listen("session", func(any) { t.Error("malformed event reached observer") })
	// Expected socket errors are suppressed after intentional shutdown starts,
	// but a real protocol violation racing teardown must remain terminal.
	proxy.beginStop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.forward("browser", &boundedWebSocketConn{Conn: upstream}, &boundedWebSocketConn{Conn: downstream}, ws.StateClientSide, ws.StateServerSide, &browserProtocolBudget{remaining: 4096})
	}()
	if err := wsutil.WriteServerMessage(browser, ws.OpText, []byte(`{"method":"Fetch.requestPaused","sessionId":"session","params":null}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("malformed event blocked relay termination")
	}
	if aborted.Load() != 1 || !proxy.terminalFailureWasObserved() {
		t.Fatal("malformed event did not abort the browser")
	}
}

func TestBrowserProtocolEventsPreserveTerminalPolicyObservation(t *testing.T) {
	t.Parallel()
	var events browserProtocolEvents
	monitor := newFlowEventMonitor(requestPolicy{})
	drain := newListenerDrain()
	var aborted atomic.Int32
	events.listen("session", drain.wrap(func(event any) {
		if _, ok := event.(*network.EventWebSocketCreated); ok {
			monitor.markPolicyViolation()
			aborted.Add(1)
		}
	}))
	if err := events.observe([]byte(`{"method":"Network.webSocketCreated","sessionId":"session","params":{"requestId":"socket","url":"wss://example.test/"}}`)); err != nil {
		t.Fatal(err)
	}
	drain.stop()
	drain.wait()
	if !errors.Is(flowTerminalError(monitor, false, nil), ErrFlowPolicyViolation) || aborted.Load() != 1 {
		t.Fatal("admitted final policy observation was lost at terminal evaluation")
	}
}
