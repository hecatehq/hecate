package browserrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestBoundedWebSocketMaskedClientPayloads(t *testing.T) {
	t.Parallel()
	for _, size := range []int{1, 3, 4, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 125, 126, 127, 65535, 65536, 65537} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			if err := server.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 251)
			}
			done := make(chan error, 1)
			go func() {
				endpoint := &boundedWebSocketConn{Conn: client}
				done <- endpoint.writeMessage(ws.StateClientSide, ws.OpBinary, payload)
			}()
			got, op, err := readBoundedWebSocketMessage(server, ws.StateServerSide, 128<<10)
			if err != nil || op != ws.OpBinary || string(got) != string(payload) {
				t.Fatalf("masked payload size %d: bytes=%d opcode=%v err=%v", size, len(got), op, err)
			}
			awaitTestWrite(t, done)
		})
	}
}

func TestReadChromiumDevToolsURLRequiresBoundedLoopbackBrowserEndpoint(t *testing.T) {
	t.Parallel()
	valid := "noise\nDevTools listening on ws://127.0.0.1:9222/devtools/browser/test\ntrailing"
	got, remainder, err := readChromiumDevToolsURL(strings.NewReader(valid))
	if err != nil || got != "ws://127.0.0.1:9222/devtools/browser/test" {
		t.Fatalf("readChromiumDevToolsURL() = %q, %v", got, err)
	}
	left, err := io.ReadAll(remainder)
	if err != nil || string(left) != "trailing" {
		t.Fatalf("startup remainder = %q, %v", left, err)
	}
	for _, raw := range []string{
		"DevTools listening on ws://example.test:9222/devtools/browser/test\n",
		"DevTools listening on ws://127.0.0.1:9222/devtools/page/test\n",
		strings.Repeat("diagnostic\n", maxBrowserStartupOutputBytes),
	} {
		if _, _, err := readChromiumDevToolsURL(strings.NewReader(raw)); err == nil {
			t.Fatalf("readChromiumDevToolsURL(%q...) accepted unsafe/unbounded output", raw[:min(len(raw), 48)])
		}
	}
}

func TestReadChromiumDevToolsURLRejectsOversizedSingleLineWithoutUnboundedRead(t *testing.T) {
	t.Parallel()
	input := &endlessStartupLineReader{}
	if _, _, err := readChromiumDevToolsURL(input); !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("readChromiumDevToolsURL() error = %v, want ErrInspectionFailed", err)
	}
	if input.bytesRead > maxBrowserStartupOutputBytes+1 {
		t.Fatalf("readChromiumDevToolsURL() consumed %d bytes from an unterminated line, want at most %d", input.bytesRead, maxBrowserStartupOutputBytes+1)
	}
}

type endlessStartupLineReader struct {
	bytesRead int
}

func (r *endlessStartupLineReader) Read(dst []byte) (int, error) {
	for index := range dst {
		dst[index] = 'x'
	}
	r.bytesRead += len(dst)
	return len(dst), nil
}

func TestChromiumCommandArgsPreserveSandboxAndMuteAudio(t *testing.T) {
	t.Parallel()
	args := chromiumCommandArgs("/private/profile", "MAP example.test 1.1.1.1, MAP * ~NOTFOUND")
	joined := strings.Join(args, "\n")
	for _, want := range []string{"--mute-audio", "--disable-breakpad", "--safebrowsing-disable-auto-update", "--password-store=basic", "--remote-debugging-port=0", "--user-data-dir=/private/profile", "--host-resolver-rules=MAP example.test 1.1.1.1, MAP * ~NOTFOUND"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Chromium arguments omit %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "--no-sandbox") {
		t.Fatalf("Chromium arguments weakened the process sandbox: %v", args)
	}
}

func TestReadBoundedWebSocketMessageRejectsOversizedProtocolFrame(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- wsutil.WriteServerMessage(server, ws.OpText, []byte(strings.Repeat("x", 1025)))
	}()
	_, _, err := readBoundedWebSocketMessage(client, ws.StateClientSide, 1024)
	if !errors.Is(err, wsutil.ErrFrameTooLarge) && !errors.Is(err, errBrowserProtocolMessageTooLarge) {
		t.Fatalf("readBoundedWebSocketMessage() error = %v, want bounded rejection", err)
	}
	_ = client.Close()
	<-writeDone
}

func TestReadBoundedWebSocketMessageAcceptsNormalAndFragmentedMessages(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		frames []ws.Frame
		want   string
	}{
		{name: "normal", frames: []ws.Frame{ws.NewTextFrame([]byte("normal"))}, want: "normal"},
		{name: "fragmented", frames: []ws.Frame{
			ws.NewFrame(ws.OpText, false, []byte("frag")),
			ws.NewFrame(ws.OpContinuation, true, []byte("mented")),
		}, want: "fragmented"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			writeDone := writeServerFrames(server, test.frames...)
			payload, opcode, err := readBoundedWebSocketMessage(client, ws.StateClientSide, 64)
			if err != nil || opcode != ws.OpText || string(payload) != test.want {
				t.Fatalf("readBoundedWebSocketMessage() = %q, %v, %v; want %q, text, nil", payload, opcode, err, test.want)
			}
			awaitTestWrite(t, writeDone)
		})
	}
}

func TestReadBoundedWebSocketMessageRejectsOversizedFragmentedMessage(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer server.Close()
	writeDone := writeServerFrames(server,
		ws.NewFrame(ws.OpText, false, []byte("123456")),
		ws.NewFrame(ws.OpContinuation, true, []byte("789012")),
	)
	_, _, err := readBoundedWebSocketMessage(client, ws.StateClientSide, 10)
	if !errors.Is(err, errBrowserProtocolMessageTooLarge) {
		t.Fatalf("readBoundedWebSocketMessage() error = %v, want aggregate message rejection", err)
	}
	_ = client.Close()
	awaitTestWrite(t, writeDone)
}

func TestReadBoundedWebSocketMessageHandlesControlFrameBetweenFragments(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	peerDone := make(chan error, 1)
	go func() {
		if err := ws.WriteFrame(server, ws.NewFrame(ws.OpText, false, []byte("rea"))); err != nil {
			peerDone <- err
			return
		}
		if err := ws.WriteFrame(server, ws.NewPingFrame([]byte("alive"))); err != nil {
			peerDone <- err
			return
		}
		messages, err := wsutil.ReadClientMessage(server, nil)
		if err != nil {
			peerDone <- err
			return
		}
		if len(messages) != 1 || messages[0].OpCode != ws.OpPong || string(messages[0].Payload) != "alive" {
			peerDone <- errors.New("missing matching pong")
			return
		}
		peerDone <- ws.WriteFrame(server, ws.NewFrame(ws.OpContinuation, true, []byte("dy")))
	}()
	payload, opcode, err := readBoundedWebSocketMessage(client, ws.StateClientSide, 64)
	if err != nil || opcode != ws.OpText || string(payload) != "ready" {
		t.Fatalf("readBoundedWebSocketMessage() = %q, %v, %v; want ready text", payload, opcode, err)
	}
	awaitTestWrite(t, peerDone)
}

func TestReadBoundedWebSocketMessageAcknowledgesClose(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	peerDone := make(chan error, 1)
	go func() {
		if err := ws.WriteFrame(server, ws.NewCloseFrame(ws.NewCloseFrameBody(ws.StatusNormalClosure, "done"))); err != nil {
			peerDone <- err
			return
		}
		messages, err := wsutil.ReadClientMessage(server, nil)
		if err != nil {
			peerDone <- err
			return
		}
		if len(messages) != 1 || messages[0].OpCode != ws.OpClose {
			peerDone <- errors.New("missing close acknowledgement")
			return
		}
		peerDone <- nil
	}()
	_, _, err := readBoundedWebSocketMessage(client, ws.StateClientSide, 64)
	var closed wsutil.ClosedError
	if !errors.As(err, &closed) {
		t.Fatalf("readBoundedWebSocketMessage() error = %v, want websocket close", err)
	}
	awaitTestWrite(t, peerDone)
}

func TestBrowserProtocolBudgetRejectsAggregateSubLimitMessages(t *testing.T) {
	t.Parallel()
	budget := &browserProtocolBudget{remaining: 10}
	if err := budget.consume(6); err != nil {
		t.Fatalf("first budget consume failed: %v", err)
	}
	if err := budget.consume(4); err != nil {
		t.Fatalf("exact remaining budget consume failed: %v", err)
	}
	if err := budget.consume(1); !errors.Is(err, errBrowserProtocolBudgetExceeded) {
		t.Fatalf("aggregate budget error = %v, want errBrowserProtocolBudgetExceeded", err)
	}
}

func TestBoundedCDPProxyForwardAppliesAggregateInboundBudget(t *testing.T) {
	t.Parallel()
	source, sourcePeer := net.Pipe()
	destination, destinationPeer := net.Pipe()
	defer source.Close()
	defer sourcePeer.Close()
	defer destination.Close()
	defer destinationPeer.Close()

	reported := make(chan error, 1)
	fatal := make(chan struct{})
	proxy := &boundedCDPProxy{
		limit: 8,
		onError: func(err error) {
			select {
			case reported <- err:
			default:
			}
		},
		onFatal: func() { close(fatal) },
	}
	forwardDone := make(chan struct{})
	go func() {
		proxy.forward(
			"browser to Hecate",
			&boundedWebSocketConn{Conn: source},
			&boundedWebSocketConn{Conn: destination},
			ws.StateClientSide,
			ws.StateServerSide,
			&browserProtocolBudget{remaining: 10},
		)
		close(forwardDone)
	}()
	writeDone := make(chan error, 1)
	go func() {
		if err := wsutil.WriteServerMessage(sourcePeer, ws.OpText, []byte("123456")); err != nil {
			writeDone <- err
			return
		}
		writeDone <- wsutil.WriteServerMessage(sourcePeer, ws.OpText, []byte("789012"))
	}()

	payload, err := wsutil.ReadServerText(destinationPeer)
	if err != nil || string(payload) != "123456" {
		t.Fatalf("first forwarded message = %q, %v", payload, err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, errBrowserProtocolBudgetExceeded) {
			t.Fatalf("forward error = %v, want errBrowserProtocolBudgetExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("forward did not enforce the aggregate inbound budget")
	}
	select {
	case <-fatal:
	case <-time.After(time.Second):
		t.Fatal("forward did not hard-abort the browser before returning from a fatal relay error")
	}
	select {
	case <-forwardDone:
	case <-time.After(time.Second):
		t.Fatal("forward did not stop after aggregate inbound budget exhaustion")
	}
	if !proxy.terminalFailureWasObserved() {
		t.Fatal("fatal inbound budget failure was not retained for terminal result classification")
	}
	awaitTestWrite(t, writeDone)
}

func TestBoundedCDPProxyIntentionalStopDoesNotRecordTerminalFailure(t *testing.T) {
	t.Parallel()
	proxy := &boundedCDPProxy{}
	proxy.beginStop()
	proxy.recordTerminalFailure(net.ErrClosed)
	if proxy.terminalFailureWasObserved() {
		t.Fatal("intentional relay teardown was recorded as a terminal browser failure")
	}
}

func TestBoundedCDPProxyRetainsDetectedBudgetFailureAcrossConcurrentStop(t *testing.T) {
	t.Parallel()
	proxy := &boundedCDPProxy{}
	detected := make(chan struct{})
	record := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(detected)
		<-record
		proxy.recordTerminalFailure(errBrowserProtocolBudgetExceeded)
		close(done)
	}()
	<-detected
	proxy.beginStop()
	close(record)
	<-done
	if !proxy.terminalFailureWasObserved() {
		t.Fatal("detected browser protocol budget failure was erased by concurrent teardown")
	}
}

func TestBoundedWebSocketConnSerializesDataAndControlWrites(t *testing.T) {
	t.Parallel()
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	endpoint := &boundedWebSocketConn{Conn: connection}
	start := make(chan struct{})
	writes := make(chan error, 2)
	go func() {
		<-start
		writes <- endpoint.writeMessage(ws.StateServerSide, ws.OpText, []byte(strings.Repeat("x", 64<<10)))
	}()
	go func() {
		<-start
		handle := endpoint.controlFrameHandler(ws.StateServerSide)
		writes <- handle(ws.Header{Fin: true, OpCode: ws.OpPing}, strings.NewReader(""))
	}()
	close(start)

	messages, err := wsutil.ReadServerMessage(peer, nil)
	if err != nil {
		t.Fatalf("read first serialized message: %v", err)
	}
	more, err := wsutil.ReadServerMessage(peer, nil)
	if err != nil {
		t.Fatalf("read second serialized message: %v", err)
	}
	messages = append(messages, more...)
	if len(messages) != 2 {
		t.Fatalf("serialized messages = %+v", messages)
	}
	var sawData, sawPong bool
	for _, message := range messages {
		switch message.OpCode {
		case ws.OpText:
			sawData = len(message.Payload) == 64<<10
		case ws.OpPong:
			sawPong = len(message.Payload) == 0
		}
	}
	if !sawData || !sawPong {
		t.Fatalf("serialized messages omitted data or pong: %+v", messages)
	}
	awaitTestWrite(t, writes)
	awaitTestWrite(t, writes)
}

func TestBoundedCDPProxyStopUnblocksPendingAccept(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	upstream, upstreamPeer := net.Pipe()
	defer upstreamPeer.Close()
	listener := newBlockingTestListener()
	proxy := &boundedCDPProxy{
		listener: listener,
		upstream: &boundedWebSocketConn{Conn: upstream},
		cancel:   cancel,
		limit:    64,
	}
	proxy.start(ctx)

	done := make(chan struct{})
	go func() {
		proxy.stop()
		proxy.stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("boundedCDPProxy.stop() did not unblock a pending accept")
	}
}

func TestBoundedCDPProxyStopClosesConnectionReturnedAfterListenerClose(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	upstream, upstreamPeer := net.Pipe()
	downstream, downstreamPeer := net.Pipe()
	defer upstreamPeer.Close()
	defer downstreamPeer.Close()
	listener := newHeldConnectionTestListener(downstream)
	proxy := &boundedCDPProxy{
		listener: listener,
		upstream: &boundedWebSocketConn{Conn: upstream},
		cancel:   cancel,
		limit:    64,
	}
	proxy.start(ctx)
	select {
	case <-listener.accepting:
	case <-time.After(time.Second):
		t.Fatal("proxy did not enter Accept")
	}

	stopped := make(chan struct{})
	go func() {
		proxy.stop()
		close(stopped)
	}()
	select {
	case <-listener.closed:
	case <-time.After(time.Second):
		t.Fatal("proxy stop did not close listener")
	}
	close(listener.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("proxy stop did not return after held Accept completed")
	}

	_ = downstreamPeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := downstreamPeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection returned after stop remained open")
	}
}

func TestBoundedCDPProxyStopWaitsForContextAbortBeforeReturning(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	upstream, upstreamPeer := net.Pipe()
	defer upstreamPeer.Close()
	listener := newBlockingTestListener()
	abortStarted := make(chan struct{})
	releaseAbort := make(chan struct{})
	proxy := &boundedCDPProxy{
		listener: listener,
		upstream: &boundedWebSocketConn{Conn: upstream},
		cancel:   cancel,
		limit:    64,
		onFatal: func() {
			close(abortStarted)
			<-releaseAbort
		},
	}
	proxy.start(ctx)

	stopped := make(chan struct{})
	go func() {
		proxy.stop()
		close(stopped)
	}()
	select {
	case <-abortStarted:
	case <-time.After(time.Second):
		t.Fatal("proxy stop did not start the tracked process abort")
	}
	select {
	case <-stopped:
		t.Fatal("proxy stop returned before the tracked process abort completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseAbort)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("proxy stop did not return after the tracked process abort completed")
	}
}

func writeServerFrames(connection net.Conn, frames ...ws.Frame) <-chan error {
	done := make(chan error, 1)
	go func() {
		for _, frame := range frames {
			if err := ws.WriteFrame(connection, frame); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	return done
}

func awaitTestWrite(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write websocket frames: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket frame writer")
	}
}

type blockingTestListener struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingTestListener() *blockingTestListener {
	return &blockingTestListener{closed: make(chan struct{})}
}

func (l *blockingTestListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingTestListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (*blockingTestListener) Addr() net.Addr {
	return testNetworkAddress("bounded-cdp")
}

type testNetworkAddress string

func (a testNetworkAddress) Network() string { return "test" }
func (a testNetworkAddress) String() string  { return string(a) }

type heldConnectionTestListener struct {
	connection net.Conn
	accepting  chan struct{}
	release    chan struct{}
	closed     chan struct{}
	acceptOnce sync.Once
	closeOnce  sync.Once
}

func newHeldConnectionTestListener(connection net.Conn) *heldConnectionTestListener {
	return &heldConnectionTestListener{
		connection: connection,
		accepting:  make(chan struct{}),
		release:    make(chan struct{}),
		closed:     make(chan struct{}),
	}
}

func (l *heldConnectionTestListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.accepting) })
	<-l.release
	return l.connection, nil
}

func (l *heldConnectionTestListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*heldConnectionTestListener) Addr() net.Addr {
	return testNetworkAddress("held-bounded-cdp")
}
