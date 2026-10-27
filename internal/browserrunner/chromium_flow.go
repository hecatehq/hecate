package browserrunner

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpstorage "github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

const (
	flowActionWaitTimeout  = 3 * time.Second
	flowActionPollInterval = 100 * time.Millisecond
	flowActionSettleDelay  = 150 * time.Millisecond
	// browserFlowStorageQuotaBytes bounds quota-managed page state (including
	// IndexedDB, Cache Storage, and OPFS) inside the already-ephemeral profile.
	// Browser bookkeeping and an in-flight write may add a small overhead.
	browserFlowStorageQuotaBytes = 8 << 20
)

// flowTransportGuardScript is Hecate-owned policy code installed before any
// page script. It removes script APIs whose traffic is not completely governed
// by Fetch's exact-origin GET/HEAD interception. No agent-supplied code is ever
// evaluated in the browser.
const flowTransportGuardScript = `(() => {
  'use strict';
  const denied = function () { throw new DOMException('Blocked by Hecate browser flow policy', 'SecurityError'); };
	let policyViolated = false;
  const lock = (owner, name, value) => {
    if (!owner || !(name in owner)) return true;
    try {
      Object.defineProperty(owner, name, { value, writable: false, configurable: false });
      return owner[name] === value;
    } catch (_) { return false; }
  };
  let ready = true;
  for (const name of ['WebSocket', 'WebSocketStream', 'WebTransport', 'EventSource', 'Worker', 'SharedWorker', 'RTCPeerConnection', 'webkitRTCPeerConnection', 'TCPSocket', 'UDPSocket', 'PaymentRequest', 'Notification', 'EyeDropper', 'Audio', 'AudioContext', 'webkitAudioContext', 'SpeechRecognition', 'webkitSpeechRecognition', 'SpeechSynthesisUtterance']) {
    ready = lock(globalThis, name, denied) && ready;
  }
  for (const name of ['open', 'print', 'showOpenFilePicker', 'showSaveFilePicker', 'showDirectoryPicker', 'getScreenDetails']) {
    ready = lock(globalThis, name, denied) && ready;
  }
  const guardMethods = (object, names) => {
    if (!object) return;
    for (const name of names) {
      ready = lock(object, name, denied) && ready;
      try { ready = lock(Object.getPrototypeOf(object), name, denied) && ready; } catch (_) { ready = false; }
    }
  };
  guardMethods(globalThis.navigator, ['sendBeacon', 'share', 'canShare', 'getUserMedia', 'requestMIDIAccess', 'requestMediaKeySystemAccess', 'registerProtocolHandler', 'unregisterProtocolHandler', 'setAppBadge', 'clearAppBadge', 'vibrate']);
  guardMethods(globalThis.navigator && navigator.clipboard, ['read', 'readText', 'write', 'writeText']);
  guardMethods(globalThis.navigator && navigator.mediaDevices, ['getUserMedia', 'getDisplayMedia', 'selectAudioOutput', 'enumerateDevices']);
  guardMethods(globalThis.navigator && navigator.geolocation, ['getCurrentPosition', 'watchPosition', 'clearWatch']);
  guardMethods(globalThis.navigator && navigator.bluetooth, ['requestDevice', 'getDevices']);
  guardMethods(globalThis.navigator && navigator.usb, ['requestDevice', 'getDevices']);
  guardMethods(globalThis.navigator && navigator.serial, ['requestPort', 'getPorts']);
  guardMethods(globalThis.navigator && navigator.hid, ['requestDevice', 'getDevices']);
  guardMethods(globalThis.navigator && navigator.credentials, ['create', 'get', 'store', 'preventSilentAccess']);
  guardMethods(globalThis.document, ['execCommand', 'requestStorageAccess', 'requestStorageAccessForOrigin']);
  guardMethods(globalThis.speechSynthesis, ['speak', 'resume', 'pause', 'cancel']);
  guardMethods(globalThis.HTMLMediaElement && HTMLMediaElement.prototype, ['play']);
  if (globalThis.ServiceWorkerContainer && ServiceWorkerContainer.prototype) {
    ready = lock(ServiceWorkerContainer.prototype, 'register', denied) && ready;
  }
	const eventPath = globalThis.Event && Event.prototype.composedPath;
	const preventDefault = globalThis.Event && Event.prototype.preventDefault;
	const hasAttribute = globalThis.Element && Element.prototype.hasAttribute;
	addEventListener('click', event => {
		try {
			const path = eventPath.call(event);
			for (const node of path) {
				if (node && node.nodeType === 1 && hasAttribute.call(node, 'download')) {
					policyViolated = true;
					preventDefault.call(event);
					break;
				}
			}
		} catch (_) {
			policyViolated = true;
			try { preventDefault.call(event); } catch (_) {}
		}
	}, true);
  Object.defineProperty(globalThis, '__hecateBrowserFlowGuardReady', {
    value: ready, writable: false, configurable: false
  });
	Object.defineProperty(globalThis, '__hecateBrowserFlowPolicyViolated', {
		get: () => policyViolated, configurable: false
	});
})();`

// RunFlow executes one fully declared flow in a fresh Chromium process and
// profile. Scripts may run only in this method; Inspect remains explicitly
// script-disabled. No session survives the call or an approval boundary.
func (i *ChromiumInspector) RunFlow(ctx context.Context, request FlowRequest) (result FlowResult, retErr error) {
	if i == nil || i.executablePath == "" {
		return FlowResult{}, ErrUnavailable
	}
	request, err := ValidateFlowRequest(request)
	if err != nil {
		return FlowResult{}, err
	}
	policy, err := newRequestPolicy(InspectRequest{
		URL:            request.URL,
		AllowedOrigins: []string{request.AllowedOrigin},
	})
	if err != nil {
		return FlowResult{}, err
	}

	flowDeadlineCtx, cancelDeadline := context.WithTimeout(ctx, i.timeout)
	defer cancelDeadline()

	profileDir, err := i.createProfileDir("hecate-browser-flow-")
	if err != nil {
		return FlowResult{}, ErrInspectionFailed
	}
	defer func() {
		if err := i.removeProfileDir(profileDir); err != nil {
			retErr = errors.Join(retErr, ErrProfileCleanupFailed)
		}
	}()

	lookup := i.lookupIPAddrs
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	mappings, err := policy.preflightHostMappings(flowDeadlineCtx, i.allowPrivateIPs, lookup)
	if err != nil {
		return FlowResult{}, err
	}
	resolverRules, err := hostResolverRules(mappings)
	if err != nil {
		return FlowResult{}, err
	}
	remaining := inspectionTimeRemaining(flowDeadlineCtx)
	if remaining <= 0 {
		return FlowResult{}, ErrInspectionFailed
	}

	browserRootCtx, cancelBrowserRoot := context.WithCancel(flowDeadlineCtx)
	defer cancelBrowserRoot()
	startupTimeout := browserStartupTimeout(remaining)
	browserProcess, err := startBoundedChromiumProcess(browserRootCtx, i.executablePath, profileDir, startupTimeout, resolverRules, i.protocolError)
	if err != nil {
		return FlowResult{}, ErrInspectionFailed
	}
	defer browserProcess.stop()
	allocatorCtx, cancelAllocator, err := boundedBrowserAllocator(browserRootCtx, browserProcess)
	if err != nil {
		return FlowResult{}, ErrInspectionFailed
	}
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	if err := chromedp.Do(browserCtx); err != nil {
		cancelBrowser()
		cancelAllocator()
		cancelBrowserRoot()
		return FlowResult{}, ErrInspectionFailed
	}

	flowCtx, cancelFlow := context.WithCancel(browserCtx)
	browserState := chromedp.FromContext(flowCtx)
	if browserState == nil || browserState.Target == nil || browserState.Browser == nil {
		cancelFlow()
		cancelBrowser()
		cancelAllocator()
		cancelBrowserRoot()
		return FlowResult{}, ErrInspectionFailed
	}
	monitor := newFlowEventMonitor(policy)
	listenerDrain := newListenerDrain()
	hardAbortFlow := func() {
		browserProcess.proxy.abortProcess()
		cancelFlow()
	}
	monitor.startWithDrain(flowCtx, hardAbortFlow, listenerDrain, &browserProcess.proxy.events)
	childTargets := newChildTargetGuard(browserState.Target.TargetID)
	childTargets.startWithDrain(flowCtx, func() {
		monitor.markPolicyViolation()
		hardAbortFlow()
	}, listenerDrain, &browserProcess.proxy.events)
	startFlowPolicyGuards(flowCtx, monitor, hardAbortFlow, listenerDrain, &browserProcess.proxy.events)
	defer func() {
		result.Network = monitor.networkSummary()
	}()

	// Cancellation waits for the monitor and Chromium allocator before profile
	// removal, so a returned flow cannot leave a browser using the profile.
	defer func() {
		listenerDrain.stop()
		// Kill the process unit while every request interceptor is still attached.
		// Cancelling chromedp first can resume a paused POST, popup, or other
		// forbidden request during the detach-to-process-stop window.
		browserProcess.stop()
		cancelFlow()
		listenerDrain.wait()
		monitor.wait()
		cancelBrowser()
		cancelAllocator()
		cancelBrowserRoot()
		// Recheck only after every browser context has been stopped, so an event
		// already queued on Chromium's listener path cannot race a successful
		// return during teardown.
		retErr = flowTerminalError(monitor, browserProcess.terminalFailureWasObserved(), retErr)
	}()

	var guardReady bool
	err = chromedp.Do(flowCtx,
		chromedp.Func(func(actionCtx context.Context, _ *chromedp.Target) error {
			return autoAttachRelatedTargets(actionCtx, browserState.Target.TargetID)
		}),
		chromedp.Func(func(actionCtx context.Context, _ *chromedp.Target) error {
			_, err := chromedp.CallBrowser(actionCtx, browser.SetDownloadBehavior, browser.SetDownloadBehaviorParams{Behavior: browser.SetDownloadBehaviorBehaviorDeny, EventsEnabled: new(true)})
			return err
		}),
		protocolAction(page.SetInterceptFileChooserDialog, page.SetInterceptFileChooserDialogParams{Enabled: true, Cancel: new(true)}),
		protocolAction(network.Enable, network.EnableParams{MaxTotalBufferSize: new(int64(browserResponseCancellationThresholdBytes)), MaxResourceBufferSize: new(int64(browserResponseCancellationThresholdBytes))}),
		protocolAction(network.SetCacheDisabled, network.SetCacheDisabledParams{CacheDisabled: true}),
		protocolAction(network.SetBypassServiceWorker, network.SetBypassServiceWorkerParams{Bypass: true}),
		protocolAction(network.SetBlockedURLs, network.SetBlockedURLsParams{URLPatterns: browserNetworkBlockPatterns(policy)}),
		protocolAction(cdpstorage.OverrideQuotaForOrigin, cdpstorage.OverrideQuotaForOriginParams{Origin: request.AllowedOrigin, QuotaSize: new(float64(browserFlowStorageQuotaBytes))}),
		protocolAction(page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: flowTransportGuardScript, RunImmediately: new(true)}),
		protocolAction(fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{
			URLPattern:   "*",
			RequestStage: fetch.RequestStageRequest,
		}}}),
		chromedp.Navigate(policy.targetURL),
		chromedp.WaitReady(chromedp.CSS("html")),
	)
	if err == nil {
		guardReady, err = chromedp.Run(flowCtx, chromedp.Evaluate[bool](`globalThis.__hecateBrowserFlowGuardReady === true`))
	}
	if err != nil || !guardReady {
		if monitor.policyWasViolated() {
			return result, ErrFlowPolicyViolation
		}
		return result, ErrInspectionFailed
	}
	if violated, checkErr := flowScriptPolicyWasViolated(flowCtx); checkErr != nil {
		return result, ErrInspectionFailed
	} else if violated {
		monitor.markPolicyViolation()
		return result, ErrFlowPolicyViolation
	}
	if err := ensureFlowNavigation(flowCtx, policy, &result); err != nil {
		return result, err
	}
	result.InitialAccessibility, result.InitialAccessibilityTruncated, err = collectFlowAccessibility(flowCtx, MaxFlowAccessibilityNodes)
	if err != nil {
		return result, ErrInspectionFailed
	}

	result.Actions = make([]FlowActionResult, 0, len(request.Actions))
	for index, action := range request.Actions {
		actionResult := FlowActionResult{
			Index:  index,
			Kind:   action.Kind,
			Role:   action.Role,
			Name:   action.Name,
			Status: FlowActionStatusCompleted,
		}
		actionErr := runFlowAction(flowCtx, action, i.beforeFinalClickHitTest)
		if violated, checkErr := flowScriptPolicyWasViolated(flowCtx); checkErr != nil {
			if actionErr == nil {
				actionErr = ErrInspectionFailed
			}
		} else if violated {
			monitor.markPolicyViolation()
			actionErr = ErrFlowPolicyViolation
		}
		// Refresh navigation even when the action itself fails. Page scripts can
		// navigate while an exact target is being polled, and partial evidence
		// must describe the page on which the action actually terminated.
		navigationErr := ensureFlowNavigation(flowCtx, policy, &result)
		if actionErr == nil || errors.Is(navigationErr, ErrFlowPolicyViolation) {
			actionErr = navigationErr
		}
		if monitor.policyWasViolated() {
			actionErr = ErrFlowPolicyViolation
		}
		if actionErr != nil {
			actionResult.Status = FlowActionStatusFailed
			actionResult.ErrorKind = flowActionErrorKind(actionErr)
			result.Actions = append(result.Actions, actionResult)
			result.Network = monitor.networkSummary()
			if monitor.policyWasViolated() {
				return result, ErrFlowPolicyViolation
			}
			return result, actionErr
		}
		result.Actions = append(result.Actions, actionResult)
	}

	result.FinalAccessibility, result.FinalAccessibilityTruncated, err = collectFlowAccessibility(flowCtx, MaxFlowAccessibilityNodes)
	result.Network = monitor.networkSummary()
	if err != nil {
		return result, ErrInspectionFailed
	}
	if monitor.policyWasViolated() {
		return result, ErrFlowPolicyViolation
	}
	return result, nil
}

func flowScriptPolicyWasViolated(ctx context.Context) (bool, error) {
	violated, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`globalThis.__hecateBrowserFlowPolicyViolated === true`))
	if err != nil {
		return false, ErrInspectionFailed
	}
	return violated, nil
}

func flowTerminalError(monitor *eventMonitor, relayFailed bool, current error) error {
	if monitor != nil && monitor.policyWasViolated() {
		return ErrFlowPolicyViolation
	}
	if relayFailed {
		return ErrInspectionFailed
	}
	if current != nil {
		return current
	}
	if monitor != nil && monitor.failureWasObserved() {
		return ErrInspectionFailed
	}
	return nil
}

func startFlowPolicyGuards(ctx context.Context, monitor *eventMonitor, abort func(), drain *listenerDrain, events *browserProtocolEvents) {
	var once sync.Once
	fail := func() {
		if monitor != nil {
			monitor.markPolicyViolation()
		}
		once.Do(abort)
	}
	targetListener := func(event any) {
		switch event := event.(type) {
		case *network.EventWebSocketCreated,
			*network.EventWebTransportCreated,
			*network.EventDirectTCPSocketCreated,
			*network.EventDirectUDPSocketCreated,
			*page.EventWindowOpen,
			*page.EventJavascriptDialogOpening:
			fail()
		case *page.EventFrameRequestedNavigation:
			if event == nil || !monitor.policy.allowsURL(event.URL) {
				fail()
			}
		case *page.EventFrameStartedNavigating:
			if event == nil || !monitor.policy.allowsURL(event.URL) {
				fail()
			}
		case *page.EventNavigatedWithinDocument:
			if event == nil || !monitor.policy.allowsURL(event.URL) {
				fail()
			}
		}
	}
	browserListener := func(event any) {
		if _, ok := event.(*browser.EventDownloadWillBegin); ok {
			fail()
		}
	}
	if drain != nil {
		targetListener = drain.wrap(targetListener)
		browserListener = drain.wrap(browserListener)
	}
	events.listen(chromedp.FromContext(ctx).Target.SessionID, targetListener)
	events.listen("", browserListener)
}

func runFlowAction(ctx context.Context, action FlowAction, beforeFinalClickHitTest func(context.Context) error) error {
	waitCtx, cancel := context.WithTimeout(ctx, flowActionWaitTimeout)
	defer cancel()
	for {
		node, err := exactFlowAccessibilityTarget(waitCtx, action)
		if err == nil {
			if action.Kind == FlowActionWaitFor {
				return nil
			}
			return clickFlowAccessibilityTarget(waitCtx, action, node, beforeFinalClickHitTest)
		}
		if !errors.Is(err, ErrFlowTargetNotFound) {
			return err
		}
		timer := time.NewTimer(flowActionPollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return flowActionWaitError(ctx)
		case <-timer.C:
		}
	}
}

func flowActionWaitError(parent context.Context) error {
	if parent.Err() != nil {
		return ErrInspectionFailed
	}
	return ErrFlowTargetNotFound
}

func exactFlowAccessibilityTarget(ctx context.Context, action FlowAction) (*accessibility.Node, error) {
	return exactFlowAccessibilityTargetDirect(ctx, action)
}

func exactFlowAccessibilityTargetDirect(ctx context.Context, action FlowAction) (*accessibility.Node, error) {
	// Fetch one complete AX snapshot and perform exact matching in Go. Chrome's
	// filtered QueryAXTree can remain pending indefinitely on some supported
	// Chromium releases. The bounded DevTools relay caps this response before
	// chromedp decodes it, so a hostile wide tree fails closed instead of
	// allocating an unbounded protocol frame in Hecate.
	snapshot, err := chromedp.Call(ctx, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return nil, ErrInspectionFailed
	}
	byBackend := make(map[cdp.BackendNodeID]*accessibility.Node)
	for _, node := range snapshot.Nodes {
		if node == nil || node.Ignored || node.BackendDOMNodeID == 0 || accessibilityValue(node.Role) != action.Role || accessibilityValue(node.Name) != action.Name {
			continue
		}
		byBackend[node.BackendDOMNodeID] = node
	}
	if len(byBackend) == 0 {
		return nil, ErrFlowTargetNotFound
	}
	if len(byBackend) != 1 {
		return nil, ErrFlowTargetAmbiguous
	}
	for _, node := range byBackend {
		if action.Kind == FlowActionClick && (accessibilityPropertyTrue(node, accessibility.PropertyNameDisabled) || !accessibilityPropertyTrue(node, accessibility.PropertyNameFocusable)) {
			return nil, ErrFlowTargetUnavailable
		}
		return node, nil
	}
	return nil, ErrFlowTargetNotFound
}

func clickFlowAccessibilityTarget(ctx context.Context, action FlowAction, targetNode *accessibility.Node, beforeFinalClickHitTest func(context.Context) error) error {
	return clickFlowAccessibilityTargetDirect(ctx, action, targetNode, beforeFinalClickHitTest)
}

func clickFlowAccessibilityTargetDirect(ctx context.Context, action FlowAction, targetNode *accessibility.Node, beforeFinalClickHitTest func(context.Context) error) error {
	if targetNode == nil || targetNode.BackendDOMNodeID == 0 {
		return ErrFlowTargetUnavailable
	}
	// AX node ids need to remain stable across the semantic rechecks and hit
	// ancestry proof below. Keep the event-producing accessibility domain
	// enabled only for this short click-critical section; wait actions and
	// evidence snapshots use one-shot full-tree queries without subscribing to
	// page-controlled AX update floods.
	if _, err := chromedp.Call(ctx, accessibility.Enable, cdp.Empty{}); err != nil {
		return ErrInspectionFailed
	}
	defer func() { _, _ = chromedp.Call(ctx, accessibility.Disable, cdp.Empty{}) }()
	backendID := targetNode.BackendDOMNodeID
	// Re-query immediately before dispatch so a stale node cannot inherit the
	// approved role/name after page mutation.
	rechecked, err := exactFlowAccessibilityTargetDirect(ctx, action)
	if err != nil {
		return err
	}
	if rechecked.BackendDOMNodeID != backendID {
		return ErrFlowTargetUnavailable
	}
	if _, err := chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: backendID}); err != nil {
		return ErrFlowTargetUnavailable
	}
	// Scrolling executes page-controlled handlers. Revalidate the exact
	// approved role, name, enabled state, and node identity after that code has
	// run, immediately before computing the click region.
	rechecked, err = exactFlowAccessibilityTargetDirect(ctx, action)
	if err != nil {
		return err
	}
	if rechecked.BackendDOMNodeID != backendID {
		return ErrFlowTargetUnavailable
	}
	x, y, err := flowSafeClickPoint(ctx, backendID)
	if err != nil {
		return ErrFlowTargetUnavailable
	}
	// Hit testing and ancestry lookup are separate protocol round trips during
	// which page scripts can still mutate the approved node. Narrow that final
	// TOCTOU window by checking the exact semantic target and identity once more
	// immediately before dispatching the physical click.
	rechecked, err = exactFlowAccessibilityTargetDirect(ctx, action)
	if err != nil {
		return err
	}
	if rechecked.BackendDOMNodeID != backendID {
		return ErrFlowTargetUnavailable
	}
	if beforeFinalClickHitTest != nil {
		if err := beforeFinalClickHitTest(ctx); err != nil {
			return ErrInspectionFailed
		}
	}
	// The final semantic query is another page-controlled round trip. Recompute
	// the geometry and hit ancestry afterward so a newly overlaid element cannot
	// receive a click at coordinates proved only before that query.
	x, y, err = flowSafeClickPoint(ctx, backendID)
	if err != nil {
		return ErrFlowTargetUnavailable
	}
	if err := chromedp.Do(ctx, chromedp.MouseClickXY(x, y)); err != nil {
		return ErrInspectionFailed
	}
	timer := time.NewTimer(flowActionSettleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ErrInspectionFailed
	case <-timer.C:
		return nil
	}
}

func flowSafeClickPoint(ctx context.Context, backendID cdp.BackendNodeID) (float64, float64, error) {
	quads, err := chromedp.Call(ctx, dom.GetContentQuads, dom.GetContentQuadsParams{BackendNodeID: backendID})
	if err != nil || len(quads.Quads) == 0 {
		return 0, 0, ErrFlowTargetUnavailable
	}
	x, y, ok := flowQuadCenter(quads.Quads)
	if !ok {
		return 0, 0, ErrFlowTargetUnavailable
	}
	clickX, clickY, ok := flowRoundedClickPoint(x, y)
	if !ok {
		return 0, 0, ErrFlowTargetUnavailable
	}
	hit, err := chromedp.Call(ctx, dom.GetNodeForLocation, dom.GetNodeForLocationParams{X: int64(clickX), Y: int64(clickY)})
	if err != nil || hit.BackendNodeID == 0 {
		return 0, 0, ErrFlowTargetUnavailable
	}
	ancestry, err := chromedp.Call(ctx, accessibility.GetAXNodeAndAncestors, accessibility.GetAXNodeAndAncestorsParams{BackendNodeID: hit.BackendNodeID})
	if err != nil || !flowAXHitBelongsToApprovedTarget(ancestry.Nodes, hit.BackendNodeID, backendID) {
		return 0, 0, ErrFlowTargetUnavailable
	}
	return clickX, clickY, nil
}

func flowRoundedClickPoint(x, y float64) (float64, float64, bool) {
	// CDP hit testing accepts integer coordinates while mouse dispatch accepts
	// floats. Bind both operations to the exact same point and reject values
	// outside a conservative viewport-sized range before integer conversion.
	const maxCoordinate = float64(math.MaxInt32)
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || x < 0 || y < 0 || x > maxCoordinate || y > maxCoordinate {
		return 0, 0, false
	}
	return math.Round(x), math.Round(y), true
}

func flowQuadCenter(quads []dom.Quad) (float64, float64, bool) {
	for _, quad := range quads {
		if len(quad) != 8 {
			continue
		}
		var x, y float64
		for index := 0; index < len(quad); index += 2 {
			x += quad[index]
			y += quad[index+1]
		}
		x /= 4
		y /= 4
		if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			continue
		}
		return x, y, true
	}
	return 0, 0, false
}

func flowAXHitBelongsToApprovedTarget(nodes []*accessibility.Node, hitBackendID, approvedBackendID cdp.BackendNodeID) bool {
	if hitBackendID == 0 || approvedBackendID == 0 {
		return false
	}
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(nodes))
	var hit *accessibility.Node
	for _, node := range nodes {
		if node == nil {
			continue
		}
		byID[node.NodeID] = node
		if node.BackendDOMNodeID == hitBackendID {
			if hit != nil {
				// Ambiguous AX projections for the physical hit cannot prove which
				// semantic path will receive the browser event.
				return false
			}
			hit = node
		}
	}
	seen := make(map[accessibility.NodeID]struct{}, len(nodes))
	for node := hit; node != nil; node = byID[node.ParentID] {
		if _, duplicate := seen[node.NodeID]; duplicate {
			return false
		}
		seen[node.NodeID] = struct{}{}
		if node.BackendDOMNodeID == approvedBackendID {
			return true
		}
		// Text and presentational descendants are normal hit targets inside an
		// approved control. A separately interactive descendant is not: clicking
		// it can perform an action that was never named in the approved flow.
		if !node.Ignored && (accessibilityPropertyTrue(node, accessibility.PropertyNameFocusable) || clickableAccessibilityRole(accessibilityValue(node.Role))) {
			return false
		}
		if node.ParentID == "" {
			return false
		}
	}
	return false
}

func accessibilityPropertyTrue(node *accessibility.Node, name accessibility.PropertyName) bool {
	if node == nil {
		return false
	}
	for _, property := range node.Properties {
		if property != nil && property.Name == name && accessibilityValue(property.Value) == "true" {
			return true
		}
	}
	return false
}

func ensureFlowNavigation(ctx context.Context, policy requestPolicy, result *FlowResult) error {
	history, err := chromedp.Call(ctx, page.GetNavigationHistory, cdp.Empty{})
	if err != nil {
		return ErrInspectionFailed
	}
	finalURL, title, ok := currentNavigation(history.Entries, history.CurrentIndex)
	if !ok || !policy.allowsURL(finalURL) {
		return ErrFlowPolicyViolation
	}
	if result != nil {
		result.FinalURL = RedactURL(finalURL)
		result.FinalOrigin = originFromRawURL(finalURL)
		result.Title = SanitizeEvidenceText(title)
	}
	return nil
}

func collectFlowAccessibility(ctx context.Context, limit int) ([]AccessibilityNode, bool, error) {
	return collectFlowAccessibilityDirect(ctx, limit)
}

func collectFlowAccessibilityDirect(ctx context.Context, limit int) ([]AccessibilityNode, bool, error) {
	if limit <= 0 {
		return nil, true, nil
	}
	snapshot, err := chromedp.Call(ctx, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return nil, false, err
	}
	out := make([]AccessibilityNode, 0, limit)
	truncated := false
	for _, node := range snapshot.Nodes {
		if node == nil {
			continue
		}
		if !node.Ignored {
			item := AccessibilityNode{
				Role:        SanitizeEvidenceText(accessibilityValue(node.Role)),
				Name:        SanitizeEvidenceText(accessibilityValue(node.Name)),
				Description: SanitizeEvidenceText(accessibilityValue(node.Description)),
			}
			if item.Role != "" || item.Name != "" || item.Description != "" {
				if len(out) >= limit {
					truncated = true
					break
				}
				out = append(out, item)
			}
		}
	}
	return out, truncated, nil
}

func flowActionErrorKind(err error) string {
	switch {
	case errors.Is(err, ErrFlowTargetNotFound):
		return "target_not_found"
	case errors.Is(err, ErrFlowTargetAmbiguous):
		return "target_ambiguous"
	case errors.Is(err, ErrFlowTargetUnavailable):
		return "target_unavailable"
	case errors.Is(err, ErrFlowPolicyViolation):
		return "policy_violation"
	default:
		return "flow_failed"
	}
}
