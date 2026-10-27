package browserrunner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp/remote"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	// maxBrowserProtocolMessageBytes bounds each complete CDP WebSocket
	// message before chromedp can decode it in the Hecate process. Browser
	// events and command results are page-controlled (for example a huge
	// console argument or accessibility response), so evidence-level caps are
	// too late to provide this resource boundary.
	maxBrowserProtocolMessageBytes = 4 << 20
	// maxBrowserProtocolSessionBytes bounds aggregate browser-to-Hecate CDP
	// payloads for one fresh browser process. A page can emit an unbounded
	// number of individually small events, so a per-message cap alone does not
	// bound chromedp's queued input over the lifetime of an inspection.
	maxBrowserProtocolSessionBytes = 32 << 20
	maxBrowserStartupOutputBytes   = 64 << 10
)

var (
	errBrowserProtocolMessageTooLarge = errors.New("browser protocol message exceeds limit")
	errBrowserProtocolBudgetExceeded  = errors.New("browser protocol session budget exceeded")
	errBrowserProtocolEventMalformed  = errors.New("browser protocol event is malformed")
)

// boundedChromiumProcess owns a manually launched Chromium process and a
// loopback-only WebSocket relay. chromedp connects through that relay so no
// single page-controlled CDP message can allocate an unbounded buffer in the
// Hecate process.
type boundedChromiumProcess struct {
	cancel context.CancelFunc
	cmd    *exec.Cmd
	tree   chromiumProcessTree
	proxy  *boundedCDPProxy
	once   sync.Once
}

func startBoundedChromiumProcess(ctx context.Context, executablePath, profileDir string, startupTimeout time.Duration, resolverRules string, protocolError func(error)) (*boundedChromiumProcess, error) {
	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, executablePath, chromiumCommandArgs(profileDir, resolverRules)...)
	tree, err := prepareChromiumProcessTree(cmd)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		tree.close()
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		cancel()
		tree.close()
		return nil, err
	}
	if err := tree.attach(cmd); err != nil {
		_ = tree.forceKill(cmd)
		_ = cmd.Process.Kill()
		cancel()
		_ = cmd.Wait()
		tree.close()
		return nil, err
	}
	cleanup := func() {
		// Terminate the owned process unit while its leader remains unreaped. On
		// Unix this prevents numeric PID/process-group reuse from redirecting a
		// later group signal; on Windows it keeps every descendant in the Job
		// Object until termination is observed.
		_ = tree.forceKill(cmd)
		cancel()
		_ = cmd.Wait()
		tree.close()
	}

	type startupResult struct {
		url string
		err error
	}
	startup := make(chan startupResult, 1)
	go func() {
		wsURL, remainder, readErr := readChromiumDevToolsURL(stdout)
		if readErr == nil && remainder != nil {
			go func() { _, _ = io.Copy(io.Discard, remainder) }()
		}
		startup <- startupResult{url: wsURL, err: readErr}
	}()

	var wsURL string
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-timer.C:
		cleanup()
		return nil, ErrInspectionFailed
	case result := <-startup:
		if result.err != nil {
			cleanup()
			return nil, result.err
		}
		wsURL = result.url
	}

	hardAbort := func() {
		// This callback runs inside a relay forwarder, so it must not call
		// boundedChromiumProcess.stop (which waits for that forwarder). Kill the
		// process unit synchronously before the relay can detach CDP, then cancel
		// the command context to finish the leader lifecycle.
		_ = tree.forceKill(cmd)
		cancel()
	}
	proxy, err := startBoundedCDPProxy(ctx, wsURL, maxBrowserProtocolMessageBytes, protocolError, hardAbort)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &boundedChromiumProcess{cancel: cancel, cmd: cmd, tree: tree, proxy: proxy}, nil
}

func (p *boundedChromiumProcess) websocketURL() string {
	if p == nil || p.proxy == nil {
		return ""
	}
	return p.proxy.websocketURL()
}

func (p *boundedChromiumProcess) stop() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.proxy != nil {
			// Mark the relay as intentionally stopping before killing Chromium.
			// Otherwise the expected socket EOF caused by process teardown could
			// be mistaken for a page-triggered protocol failure.
			p.proxy.beginStop()
		}
		// Terminate the browser process unit before closing the DevTools relay.
		// Closing CDP first detaches the debugger and can resume a Fetch-paused
		// forbidden request during the small window before process termination.
		if p.tree != nil {
			_ = p.tree.forceKill(p.cmd)
		}
		if p.proxy != nil {
			p.proxy.stop()
		}
		p.cancel()
		if p.cmd != nil {
			_ = p.cmd.Wait()
		}
		if p.tree != nil {
			p.tree.close()
		}
	})
}

func (p *boundedChromiumProcess) terminalFailureWasObserved() bool {
	return p != nil && p.proxy != nil && p.proxy.terminalFailureWasObserved()
}

func readChromiumDevToolsURL(stdout io.Reader) (string, io.Reader, error) {
	const prefix = "DevTools listening on "
	// ReadSlice returns a view into this fixed-size buffer. Unlike ReadString,
	// it never accumulates an attacker- or process-controlled line beyond the
	// startup-output bound before reporting bufio.ErrBufferFull.
	reader := bufio.NewReaderSize(stdout, maxBrowserStartupOutputBytes+1)
	consumed := 0
	for consumed <= maxBrowserStartupOutputBytes {
		line, err := reader.ReadSlice('\n')
		consumed += len(line)
		if errors.Is(err, bufio.ErrBufferFull) || consumed > maxBrowserStartupOutputBytes {
			return "", nil, ErrInspectionFailed
		}
		lineText := string(line)
		if strings.HasPrefix(lineText, prefix) {
			wsURL := strings.TrimSpace(strings.TrimPrefix(lineText, prefix))
			if !validLoopbackDevToolsURL(wsURL) {
				return "", nil, ErrInspectionFailed
			}
			return wsURL, reader, nil
		}
		if err != nil {
			return "", nil, ErrInspectionFailed
		}
	}
	return "", nil, ErrInspectionFailed
}

func validLoopbackDevToolsURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.User != nil || parsed.Hostname() == "" || parsed.Port() == "" || !strings.HasPrefix(parsed.Path, "/devtools/browser/") {
		return false
	}
	ip := net.ParseIP(parsed.Hostname())
	return ip != nil && ip.IsLoopback()
}

func chromiumCommandArgs(profileDir, resolverRules string) []string {
	args := []string{
		"--headless",
		"--user-data-dir=" + profileDir,
		"--remote-debugging-port=0",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-sync",
		"--disable-background-networking",
		"--disable-breakpad",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--disable-component-update",
		"--disable-quic",
		"--mute-audio",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--deny-permission-prompts",
		"--no-proxy-server",
		"--metrics-recording-only",
		"--safebrowsing-disable-auto-update",
		"--enable-automation",
		"--password-store=basic",
		"--use-mock-keychain",
	}
	if resolverRules != "" {
		args = append(args, "--host-resolver-rules="+resolverRules)
	}
	return append(args, "about:blank")
}

type boundedCDPProxy struct {
	events          browserProtocolEvents
	listener        net.Listener
	upstream        *boundedWebSocketConn
	downstream      *boundedWebSocketConn
	cancel          context.CancelFunc
	mu              sync.Mutex
	wg              sync.WaitGroup
	stopOnce        sync.Once
	abortOnce       sync.Once
	closed          bool
	stopping        bool
	terminalFailure bool
	limit           int64
	onError         func(error)
	onFatal         func()
}

// boundedWebSocketConn serializes complete WebSocket writes. net.Conn permits
// concurrent Write calls, but a WebSocket message or control response may use
// multiple Write calls; locking only each call would still permit frames to
// interleave and corrupt the stream.
type boundedWebSocketConn struct {
	net.Conn
	writeMu sync.Mutex
}

func (c *boundedWebSocketConn) writeMessage(state ws.State, opcode ws.OpCode, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return wsutil.WriteMessage(c.Conn, state, opcode, payload)
}

func (c *boundedWebSocketConn) controlFrameHandler(state ws.State) wsutil.FrameHandlerFunc {
	handle := wsutil.ControlFrameHandler(c.Conn, state)
	return func(header ws.Header, payload io.Reader) error {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		return handle(header, payload)
	}
}

type browserProtocolBudget struct {
	remaining int64
}

func (b *browserProtocolBudget) consume(bytes int) error {
	if b == nil {
		return nil
	}
	if bytes < 0 || int64(bytes) > b.remaining {
		return errBrowserProtocolBudgetExceeded
	}
	b.remaining -= int64(bytes)
	return nil
}

func startBoundedCDPProxy(ctx context.Context, upstreamURL string, limit int64, onError func(error), onFatal func()) (*boundedCDPProxy, error) {
	if limit <= 0 {
		return nil, ErrInspectionFailed
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	upstream, buffered, _, err := ws.Dial(ctx, upstreamURL)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	if buffered != nil && buffered.Buffered() != 0 {
		_ = upstream.Close()
		_ = listener.Close()
		return nil, ErrInspectionFailed
	}
	proxyCtx, cancel := context.WithCancel(ctx)
	proxy := &boundedCDPProxy{
		listener: listener,
		upstream: &boundedWebSocketConn{Conn: upstream},
		cancel:   cancel,
		limit:    limit,
		onError:  onError,
		onFatal:  onFatal,
	}
	proxy.start(proxyCtx)
	return proxy, nil
}

func (p *boundedCDPProxy) start(ctx context.Context) {
	p.wg.Add(2)
	go p.serve(ctx)
	go p.watchContext(ctx)
}

func (p *boundedCDPProxy) websocketURL() string {
	return "ws://" + p.listener.Addr().String() + "/devtools/browser/hecate-bounded"
}

func (p *boundedCDPProxy) serve(ctx context.Context) {
	defer p.wg.Done()
	downstream, err := p.listener.Accept()
	if err != nil {
		p.recordTerminalFailure(err)
		return
	}
	p.mu.Lock()
	if p.closed || ctx.Err() != nil {
		p.mu.Unlock()
		_ = downstream.Close()
		return
	}
	p.downstream = &boundedWebSocketConn{Conn: downstream}
	downstreamEndpoint := p.downstream
	p.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = downstream.SetDeadline(deadline)
	}
	if _, err := ws.Upgrade(downstream); err != nil {
		p.recordTerminalFailure(err)
		p.abortProcess()
		p.closeConnections()
		return
	}
	_ = downstream.SetDeadline(time.Time{})

	done := make(chan struct{}, 2)
	go func() {
		p.forward("browser to Hecate", p.upstream, downstreamEndpoint, ws.StateClientSide, ws.StateServerSide, &browserProtocolBudget{remaining: maxBrowserProtocolSessionBytes})
		done <- struct{}{}
	}()
	go func() {
		p.forward("Hecate to browser", downstreamEndpoint, p.upstream, ws.StateServerSide, ws.StateClientSide, nil)
		done <- struct{}{}
	}()
	<-done
	p.closeConnections()
	<-done
}

func (p *boundedCDPProxy) watchContext(ctx context.Context) {
	defer p.wg.Done()
	<-ctx.Done()
	p.recordTerminalFailure(ctx.Err())
	// The watcher is part of the proxy wait group so stop cannot release the
	// process-tree handle until this final hard-abort attempt has completed.
	// That ordering prevents a late Unix process-group signal from targeting a
	// numerically reused group after the browser leader has been reaped.
	p.abortProcess()
	p.closeConnections()
}

func (p *boundedCDPProxy) forward(label string, source, destination *boundedWebSocketConn, readState, writeState ws.State, budget *browserProtocolBudget) {
	for {
		payload, opcode, err := readBoundedWebSocketMessageFromEndpoint(source, readState, p.limit)
		if err != nil {
			p.recordTerminalFailure(err)
			p.reportError(label, err)
			p.abortProcess()
			return
		}
		if err := budget.consume(len(payload)); err != nil {
			p.recordTerminalFailure(err)
			p.reportError(label, err)
			p.abortProcess()
			return
		}
		if budget != nil {
			if err := p.events.observe(payload); err != nil {
				p.recordTerminalFailure(err)
				p.abortProcess()
				return
			}
		}
		if err := destination.writeMessage(writeState, opcode, payload); err != nil {
			p.recordTerminalFailure(err)
			p.reportError(label, err)
			p.abortProcess()
			return
		}
	}
}

func (p *boundedCDPProxy) reportError(label string, err error) {
	if p.onError != nil {
		p.onError(fmt.Errorf("%s: %w", label, err))
	}
}

func (p *boundedCDPProxy) abortProcess() {
	if p == nil || p.onFatal == nil {
		return
	}
	p.abortOnce.Do(p.onFatal)
}

func (p *boundedCDPProxy) beginStop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopping = true
	p.mu.Unlock()
}

func (p *boundedCDPProxy) recordTerminalFailure(err error) {
	if p == nil || err == nil {
		return
	}
	p.mu.Lock()
	if !p.stopping || browserProtocolFailureMustPersist(err) {
		p.terminalFailure = true
	}
	p.mu.Unlock()
}

func browserProtocolFailureMustPersist(err error) bool {
	if errors.Is(err, errBrowserProtocolMessageTooLarge) ||
		errors.Is(err, errBrowserProtocolBudgetExceeded) ||
		errors.Is(err, errBrowserProtocolEventMalformed) ||
		errors.Is(err, wsutil.ErrFrameTooLarge) ||
		errors.Is(err, wsutil.ErrInvalidUTF8) {
		return true
	}
	var protocolErr ws.ProtocolError
	return errors.As(err, &protocolErr)
}

func (p *boundedCDPProxy) terminalFailureWasObserved() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminalFailure
}

func readBoundedWebSocketMessage(connection net.Conn, state ws.State, limit int64) ([]byte, ws.OpCode, error) {
	return readBoundedWebSocketMessageFromEndpoint(&boundedWebSocketConn{Conn: connection}, state, limit)
}

func readBoundedWebSocketMessageFromEndpoint(connection *boundedWebSocketConn, state ws.State, limit int64) ([]byte, ws.OpCode, error) {
	control := connection.controlFrameHandler(state)
	reader := wsutil.Reader{
		Source:         connection,
		State:          state,
		CheckUTF8:      true,
		MaxFrameSize:   limit,
		OnIntermediate: control,
	}
	for {
		header, err := reader.NextFrame()
		if err != nil {
			return nil, 0, err
		}
		if header.OpCode.IsControl() {
			if err := control(header, &reader); err != nil {
				return nil, 0, err
			}
			continue
		}
		if header.OpCode != ws.OpText && header.OpCode != ws.OpBinary {
			if err := reader.Discard(); err != nil {
				return nil, 0, err
			}
			continue
		}
		payload, err := io.ReadAll(io.LimitReader(&reader, limit+1))
		if err != nil {
			return nil, 0, err
		}
		if int64(len(payload)) > limit {
			return nil, 0, errBrowserProtocolMessageTooLarge
		}
		return payload, header.OpCode, nil
	}
}

func (p *boundedCDPProxy) closeConnections() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	_ = p.listener.Close()
	_ = p.upstream.Close()
	if p.downstream != nil {
		_ = p.downstream.Close()
	}
}

func (p *boundedCDPProxy) stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		p.beginStop()
		p.cancel()
		p.closeConnections()
		p.wg.Wait()
	})
}

func boundedBrowserAllocator(ctx context.Context, process *boundedChromiumProcess) (context.Context, context.CancelFunc, error) {
	if process == nil || process.websocketURL() == "" {
		return nil, nil, fmt.Errorf("%w: browser process", ErrInspectionFailed)
	}
	allocatorCtx, cancel := remote.NewAllocator(ctx, process.websocketURL(), remote.NoModifyURL, remote.WithDialTimeout(inspectionTimeRemaining(ctx)))
	return allocatorCtx, cancel, nil
}
