package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Call sends an outbound JSON-RPC request to the connected ACP client
// and blocks until the response arrives (or ctx is cancelled).
// Used by the bridge to issue reverse-RPC calls — `fs/read`,
// `fs/write`, `terminal/create`, `terminal/output`, etc. — back to
// the editor when the operator opts into editor-owned workspace mode.
//
// The existing permission-request path (session/request_permission)
// stays unchanged: it's tracked through pendingPermissions and
// resolved server-side against the gateway's approval queue. Call
// is the general-purpose primitive for everything else — its
// response routes to a per-call channel that this function blocks
// on, so the caller's goroutine model is "just await the result."
//
// Concurrency: Call may be invoked from any goroutine. Each
// outstanding call holds one slot in pendingCalls keyed by request
// id; HandleResponse delivers the matching Response and removes
// the slot. ctx.Done() releases the caller and removes the slot
// without waiting for a response that may never arrive.
func (d *Dispatcher) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if d.emit == nil {
		return nil, errors.New("acp: dispatcher has no emitter; outbound RPC not possible")
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal %s params: %w", method, err)
	}

	id, idRaw := d.nextCallID()
	wait := make(chan *Response, 1)
	d.registerPendingCall(id, wait)

	req := &Request{
		JSONRPC: JSONRPCVersion,
		ID:      &idRaw,
		Method:  method,
		Params:  rawParams,
	}
	d.emit(req)

	select {
	case resp := <-wait:
		if resp == nil {
			return nil, fmt.Errorf("acp: %s: nil response", method)
		}
		if resp.Error != nil {
			return nil, &CallError{Method: method, Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
		}
		return resp.Result, nil
	case <-ctx.Done():
		d.cancelPendingCall(id)
		return nil, ctx.Err()
	}
}

// CallError is the typed error returned by Call when the remote side
// answered with a JSON-RPC error envelope. Callers can errors.As() to
// inspect Code (e.g. method-not-found vs editor-denied).
type CallError struct {
	Method  string
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *CallError) Error() string {
	return fmt.Sprintf("acp: %s rejected (code %d): %s", e.Method, e.Code, e.Message)
}

// nextCallID allocates the next outbound-call request id. Format
// "call-N" is namespaced away from the permission flow's "permission-N"
// so the two pending maps stay non-overlapping by construction.
func (d *Dispatcher) nextCallID() (string, json.RawMessage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextCallSequence++
	id := fmt.Sprintf("call-%d", d.nextCallSequence)
	// Marshal once; the raw bytes ride along on the outbound
	// envelope. Quote-escape via json.Marshal so the id renders as
	// a JSON string in the wire envelope.
	raw, _ := json.Marshal(id)
	return id, raw
}

func (d *Dispatcher) registerPendingCall(id string, wait chan *Response) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pendingCalls == nil {
		d.pendingCalls = make(map[string]chan *Response)
	}
	d.pendingCalls[id] = wait
}

func (d *Dispatcher) cancelPendingCall(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pendingCalls, id)
}

// deliverPendingCall returns the channel for an outstanding outbound
// call and removes it from the pending map. Used by HandleResponse
// to route reply payloads. Returns nil when the id doesn't match an
// outstanding call (i.e. the response is for the permission flow or
// for a call we've already cancelled).
func (d *Dispatcher) deliverPendingCall(id string) chan *Response {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch, ok := d.pendingCalls[id]
	if !ok {
		return nil
	}
	delete(d.pendingCalls, id)
	return ch
}
