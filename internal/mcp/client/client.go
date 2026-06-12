package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/hecatehq/hecate/internal/mcp"
)

// Client is one MCP-client connection to a single external server.
// Lifecycle: New → Initialize → ListTools / CallTool (any number of
// times) → Close.
//
// Threading: CallTool / ListTools / Initialize are safe to call
// concurrently from multiple goroutines — request/response
// correlation is via JSON-RPC ID and a thread-safe pending-request
// map. The transport itself is single-reader / single-writer; the
// client serializes writes via Transport's contract and runs a
// single read loop.
type Client struct {
	transport  Transport
	clientInfo mcp.ClientInfo

	// nextID generates monotonic JSON-RPC request IDs. Atomic so
	// concurrent senders don't collide. We use integer IDs (encoded
	// as JSON numbers); the spec also allows strings but numbers
	// keep correlation cheap.
	nextID atomic.Int64

	// pending maps request ID → response channel. The reader loop
	// looks up by ID and delivers the Response (or error) to the
	// waiting caller. Buffered chans so we never block the reader.
	pendingMu sync.Mutex
	pending   map[int64]chan responseOrError

	// initResult is filled by Initialize() and used by callers that
	// want to inspect the server's declared capabilities or info.
	initOnce sync.Once
	initRes  mcp.InitializeResult

	// closed signals the read loop to stop and prevents further
	// Sends. Set by Close().
	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error

	// readDone is closed when the read loop exits. Close() waits on
	// it to ensure the transport isn't being read while we tear it
	// down.
	readDone chan struct{}
}

// responseOrError is what the read loop delivers to a pending
// caller. We carry the error here (rather than failing the
// caller's parse) so JSON-RPC errors and transport errors land
// in the same place.
type responseOrError struct {
	resp *mcp.Response
	err  error
}

// New wraps a Transport with the client request/response state
// machine. The caller passes ClientInfo (name + version) which we
// send during initialize so the server can log who connected.
//
// New starts the read loop synchronously; the caller MUST call
// Close to stop it (a deferred Close right after New is the safe
// pattern).
func New(transport Transport, info mcp.ClientInfo) *Client {
	c := &Client{
		transport:  transport,
		clientInfo: info,
		pending:    make(map[int64]chan responseOrError),
		closed:     make(chan struct{}),
		readDone:   make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Initialize performs the MCP handshake: sends `initialize` with
// our protocol version and ClientInfo, awaits the server's
// InitializeResult, then sends `notifications/initialized` to
// confirm. Per spec, no other requests may be sent before this
// completes.
//
// Calling Initialize more than once returns the cached result of
// the first call — re-handshaking on a live connection is not
// supported by the protocol.
func (c *Client) Initialize(ctx context.Context) (mcp.InitializeResult, error) {
	var firstErr error
	c.initOnce.Do(func() {
		params := mcp.InitializeParams{
			ProtocolVersion: declaredClientProtocolVersion,
			ClientInfo:      c.clientInfo,
			// Advertise the MCP Apps extension so servers can include
			// ui:// resource links and _meta.ui visibility on tools.
			Capabilities: mcp.AppsClientCapabilities(),
		}
		raw, err := json.Marshal(params)
		if err != nil {
			firstErr = fmt.Errorf("marshal initialize params: %w", err)
			return
		}
		resp, err := c.call(ctx, "initialize", raw)
		if err != nil {
			firstErr = fmt.Errorf("initialize: %w", err)
			return
		}
		var res mcp.InitializeResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			firstErr = fmt.Errorf("decode initialize result: %w", err)
			return
		}
		c.initRes = res
		// Per spec, the client sends `notifications/initialized`
		// (a notification, no ID, no response expected) after
		// processing InitializeResult. Servers that don't get this
		// will refuse subsequent requests.
		if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
			firstErr = fmt.Errorf("initialized notification: %w", err)
			return
		}
	})
	return c.initRes, firstErr
}

// declaredClientProtocolVersion mirrors the server-side constant.
// Defined here (not imported) because the server-side const is
// unexported. Bumping both in lockstep is the contract.
const declaredClientProtocolVersion = "2025-11-25"

// ListTools fetches the server's tools/list. The result is the
// authoritative tool catalog for this connection — agent_loop
// turns each Tool into a tools entry for the LLM (with the
// server's name as a prefix to avoid collisions across multiple
// servers).
func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	var res mcp.ListToolsResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	return res.Tools, nil
}

// ListResources fetches resources/list. MCP Apps servers may omit
// UI-only resources from this list, but when they do expose resources
// this method preserves descriptor metadata for host review.
func (c *Client) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	resp, err := c.call(ctx, "resources/list", nil)
	if err != nil {
		return nil, fmt.Errorf("resources/list: %w", err)
	}
	var res mcp.ListResourcesResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return nil, fmt.Errorf("decode resources/list result: %w", err)
	}
	return res.Resources, nil
}

// ReadResource fetches a resource by URI. MCP Apps uses this for the
// raw HTML ui:// resource referenced from tool _meta.ui.resourceUri.
func (c *Client) ReadResource(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
	params := mcp.ReadResourceParams{URI: uri}
	raw, err := json.Marshal(params)
	if err != nil {
		return mcp.ReadResourceResult{}, fmt.Errorf("marshal resources/read params: %w", err)
	}
	resp, err := c.call(ctx, "resources/read", raw)
	if err != nil {
		return mcp.ReadResourceResult{}, fmt.Errorf("resources/read %q: %w", uri, err)
	}
	var res mcp.ReadResourceResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return mcp.ReadResourceResult{}, fmt.Errorf("decode resources/read result: %w", err)
	}
	return res, nil
}

// CallTool invokes a tool by name with the given JSON arguments.
// Returns the CallToolResult straight from the server — including
// IsError=true for tool-level failures. JSON-RPC protocol errors
// (the call itself failed to dispatch) come back as a non-nil error
// with an *mcp.RPCError chain (use errors.As to inspect Code).
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (mcp.CallToolResult, error) {
	params := mcp.CallToolParams{Name: name, Arguments: args}
	raw, err := json.Marshal(params)
	if err != nil {
		return mcp.CallToolResult{}, fmt.Errorf("marshal tools/call params: %w", err)
	}
	resp, err := c.call(ctx, "tools/call", raw)
	if err != nil {
		return mcp.CallToolResult{}, fmt.Errorf("tools/call %q: %w", name, err)
	}
	var res mcp.CallToolResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return mcp.CallToolResult{}, fmt.Errorf("decode tools/call result: %w", err)
	}
	return res, nil
}

// Ping sends an MCP `ping` request and waits for the response. The
// MCP spec defines ping as a bidirectional liveness probe — servers
// answer with an empty result, clients can answer the same. This
// method is the client→server direction, used by the cache's
// health-check loop to detect subprocesses that are still
// connected but wedged (event loop deadlock, tight CPU loop, etc.)
// before a real tool call hits the wall.
//
// Bounded by ctx — callers pass a tight deadline (a few seconds)
// because the whole point is to fail fast on a non-responsive
// peer. Reactive eviction in Pool.Call already covers the
// transport-closed case; ping covers the more pernicious "alive
// but not answering" failure mode.
func (c *Client) Ping(ctx context.Context) error {
	if _, err := c.call(ctx, "ping", nil); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}

// Close shuts the client down: marks the connection closed,
// terminates the transport, and waits for the read loop to exit.
// Idempotent. After Close, all method calls return ErrClientClosed.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		// Closing the transport surfaces io.EOF / ErrTransportClosed
		// in the read loop, which then exits.
		c.closeErr = c.transport.Close()
		// Drain any callers blocked on responses. The read loop
		// is also draining — we close pending channels here as a
		// safety net for callers that arrive after the loop exits.
		<-c.readDone
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
	})
	return c.closeErr
}

// ErrClientClosed is returned by Initialize / ListTools / CallTool
// after Close has been called. Distinguishing this from a transport
// error keeps shutdown paths cleaner — callers can ignore
// ErrClientClosed but should log other errors.
var ErrClientClosed = errors.New("mcp client: closed")

// call issues a JSON-RPC request and blocks for the response.
// Internal — public methods marshal params first, then call this.
func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (*mcp.Response, error) {
	select {
	case <-c.closed:
		return nil, ErrClientClosed
	default:
	}

	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	idJSON := json.RawMessage(idRaw)

	req := mcp.Request{
		JSONRPC: "2.0",
		ID:      &idJSON,
		Method:  method,
		Params:  params,
	}
	frame, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Register before sending so the read loop never sees a
	// response with no waiter. Buffered chan means the reader
	// never blocks, even if the caller has already cancelled.
	ch := make(chan responseOrError, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.transport.Send(ctx, frame); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, ErrClientClosed
	case r, ok := <-ch:
		if !ok {
			return nil, ErrClientClosed
		}
		if r.err != nil {
			return nil, r.err
		}
		if r.resp.Error != nil {
			return nil, r.resp.Error
		}
		return r.resp, nil
	}
}

// notify sends a JSON-RPC notification (no ID, no response). Used
// for `notifications/initialized` and any future fire-and-forget
// messages.
func (c *Client) notify(ctx context.Context, method string, params json.RawMessage) error {
	select {
	case <-c.closed:
		return ErrClientClosed
	default:
	}
	req := mcp.Request{JSONRPC: "2.0", Method: method, Params: params}
	frame, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	return c.transport.Send(ctx, frame)
}

// readLoop pulls envelopes off the transport and demuxes them by
// JSON-RPC ID. Notifications (no ID) are dropped silently — when
// we add list_changed handling, this is where it'll plug in.
//
// The loop exits when:
//   - Recv returns io.EOF (peer hung up cleanly)
//   - Recv returns ErrTransportClosed (we Closed)
//   - Any other read error (logged via the response channels;
//     pending callers see the error)
func (c *Client) readLoop() {
	defer close(c.readDone)

	// Use a context tied to closure so transport.Recv exits
	// promptly when Close runs.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-c.closed
		cancel()
	}()
	defer cancel()

	for {
		frame, err := c.transport.Recv(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, ErrTransportClosed) || errors.Is(err, context.Canceled) {
				// Graceful shutdown. Surface ErrClientClosed to
				// any pending callers.
				c.failPending(ErrClientClosed)
				return
			}
			// Unexpected error — fail every pending caller with
			// it so they don't hang on the response chan.
			c.failPending(fmt.Errorf("transport recv: %w", err))
			return
		}
		if len(frame) == 0 {
			continue
		}

		var resp mcp.Response
		if err := json.Unmarshal(frame, &resp); err != nil {
			// Malformed frame. Log via failing the SHAPE — but
			// since we don't know which request this was for,
			// we drop it. A subsequent valid response will still
			// reach its waiter.
			continue
		}
		// Notifications come through here too (no ID). They have
		// no waiter — drop. If we ever care about server-pushed
		// notifications we'd dispatch on resp.JSONRPC == "2.0"
		// && resp.ID == nil.
		if resp.ID == nil {
			continue
		}
		var id int64
		if err := json.Unmarshal(*resp.ID, &id); err != nil {
			continue
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[id]
		c.pendingMu.Unlock()
		if !ok {
			// Late response (caller cancelled, took the entry
			// out). Drop.
			continue
		}
		// Buffered chan; never blocks.
		ch <- responseOrError{resp: &resp}
	}
}

// failPending notifies every waiter of a fatal error. Used when
// the read loop exits unexpectedly so callers don't hang.
func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		select {
		case ch <- responseOrError{err: err}:
		default:
		}
		delete(c.pending, id)
	}
}
