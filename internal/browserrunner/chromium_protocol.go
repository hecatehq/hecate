package browserrunner

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func protocolAction[P, R any](command cdp.Command[P, R], params P) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, session *chromedp.Target) error {
		_, err := cdp.Call(ctx, session, command, params)
		return err
	})
}

// Security observations run synchronously on the bounded relay, before a
// message reaches chromedp. Its public event iterators queue independently and
// cannot provide the ordered observation/drain boundary required at teardown.
// The relay enforces both per-message and lifetime byte limits before decoding.
type browserProtocolEvents struct {
	mu        sync.RWMutex
	listeners map[target.SessionID][]func(any)
}

func (e *browserProtocolEvents) listen(session target.SessionID, listener func(any)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listeners == nil {
		e.listeners = make(map[target.SessionID][]func(any))
	}
	e.listeners[session] = append(e.listeners[session], listener)
}

func (e *browserProtocolEvents) observe(payload []byte) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.listeners) == 0 {
		return nil
	}
	var message struct {
		Method    string           `json:"method"`
		SessionID target.SessionID `json:"sessionId"`
		Params    json.RawMessage  `json:"params"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return errBrowserProtocolEventMalformed
	}
	listeners := e.listeners[message.SessionID]
	if len(listeners) == 0 {
		return nil
	}
	var event any
	switch message.Method {
	case "Target.attachedToTarget":
		event = new(target.EventAttachedToTarget)
	case "Fetch.requestPaused":
		event = new(fetch.EventRequestPaused)
	case "Network.requestWillBeSent":
		event = new(network.EventRequestWillBeSent)
	case "Network.dataReceived":
		event = new(network.EventDataReceived)
	case "Network.responseReceived":
		event = new(network.EventResponseReceived)
	case "Network.loadingFailed":
		event = new(network.EventLoadingFailed)
	case "Runtime.consoleAPICalled":
		event = new(cdpruntime.EventConsoleAPICalled)
	case "Network.webSocketCreated":
		event = new(network.EventWebSocketCreated)
	case "Network.webTransportCreated":
		event = new(network.EventWebTransportCreated)
	case "Network.directTCPSocketCreated":
		event = new(network.EventDirectTCPSocketCreated)
	case "Network.directUDPSocketCreated":
		event = new(network.EventDirectUDPSocketCreated)
	case "Page.windowOpen":
		event = new(page.EventWindowOpen)
	case "Page.javascriptDialogOpening":
		event = new(page.EventJavascriptDialogOpening)
	case "Page.frameRequestedNavigation":
		event = new(page.EventFrameRequestedNavigation)
	case "Page.frameStartedNavigating":
		event = new(page.EventFrameStartedNavigating)
	case "Page.navigatedWithinDocument":
		event = new(page.EventNavigatedWithinDocument)
	case "Browser.downloadWillBegin":
		event = new(browser.EventDownloadWillBegin)
	default:
		return nil
	}
	if len(message.Params) == 0 || string(message.Params) == "null" {
		return errBrowserProtocolEventMalformed
	}
	if err := json.Unmarshal(message.Params, event); err != nil {
		return errBrowserProtocolEventMalformed
	}
	for _, listener := range listeners {
		listener(event)
	}
	return nil
}
