package browserrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/hecatehq/hecate/internal/localfs"
	"github.com/hecatehq/hecate/internal/safetext"
)

const (
	maxAccessibilityNodes = 64
	maxConsoleMessages    = 16
	maxEvidenceTextBytes  = 1 << 10
	// Page-controlled strings are clipped before UTF-8 repair and sanitization,
	// so a hostile accessibility name cannot make evidence processing scan an
	// otherwise unbounded JavaScript-generated value.
	maxEvidenceInputBytes = 4 * maxEvidenceTextBytes
	maxPausedRequests     = 256
	maxObservedRequests   = 256
	// browserResponseCancellationThresholdBytes is the amount of response data
	// CDP may observe before Hecate cancels the capture. It is not a hard wire
	// byte cap: browser, socket, and peer buffers can already contain more data
	// when the cancellation reaches Chromium.
	browserResponseCancellationThresholdBytes = 4 << 20
	browserSettleDelay                        = 150 * time.Millisecond
)

// ChromiumInspector launches an explicitly configured Chromium-compatible
// executable for each inspection. It never attaches to the operator's browser
// or reuses a profile between calls.
type ChromiumInspector struct {
	executablePath  string
	timeout         time.Duration
	allowPrivateIPs bool
	lookupIPAddrs   lookupIPAddrs
	// profileRoot is empty in production (the OS-private temp root). Tests set
	// it to assert that cancellation removes every ephemeral profile.
	profileRoot string
	// removeProfile is a deterministic failure seam. Production uses
	// os.RemoveAll with a small bounded retry after Chromium has stopped.
	removeProfile func(string) error
	// protocolError is a test-only diagnostic seam. Production never reflects
	// raw relay errors because they can encode browser/runtime details.
	protocolError func(error)
	// beforeFinalClickHitTest is a test-only race seam used to prove that the
	// final geometry/ancestry check follows the last semantic target query.
	beforeFinalClickHitTest func(context.Context) error
}

// New validates the explicit local browser runtime. The caller is expected to
// omit this inspector entirely when no executable has been configured.
func New(cfg Config) (*ChromiumInspector, error) {
	path := strings.TrimSpace(cfg.ExecutablePath)
	if path == "" {
		return nil, ErrUnavailable
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: browser executable must be an absolute path", ErrUnavailable)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("%w: browser executable is not available", ErrUnavailable)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("%w: browser executable is not executable", ErrUnavailable)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &ChromiumInspector{
		executablePath:  path,
		timeout:         cfg.Timeout,
		allowPrivateIPs: cfg.AllowPrivateIPs,
		lookupIPAddrs:   net.DefaultResolver.LookupIPAddr,
	}, nil
}

// Inspect loads a single allowed page in a temporary profile, returns bounded
// text evidence, and removes the profile before returning. It does not expose
// click, typing, upload, download, clipboard, or arbitrary JavaScript actions.
func (i *ChromiumInspector) Inspect(ctx context.Context, req InspectRequest) (result InspectResult, retErr error) {
	if i == nil || i.executablePath == "" {
		return InspectResult{}, ErrUnavailable
	}
	// One deadline covers validation, Chromium startup, and page capture. In
	// particular, a slow DNS lookup must not earn the browser a fresh timeout
	// after it returns.
	inspectionDeadlineCtx, cancelDeadline := context.WithTimeout(ctx, i.timeout)
	defer cancelDeadline()
	policy, err := newRequestPolicy(req)
	if err != nil {
		return InspectResult{}, err
	}

	profileDir, err := i.createProfileDir("hecate-browser-")
	if err != nil {
		return InspectResult{}, ErrInspectionFailed
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
	mappings, err := policy.preflightHostMappings(inspectionDeadlineCtx, i.allowPrivateIPs, lookup)
	if err != nil {
		return InspectResult{}, err
	}
	resolverRules, err := hostResolverRules(mappings)
	if err != nil {
		return InspectResult{}, err
	}

	remaining := inspectionTimeRemaining(inspectionDeadlineCtx)
	if remaining <= 0 {
		return InspectResult{}, ErrInspectionFailed
	}
	// The first chromedp.Run owns the Chromium process. Its context inherits
	// the inspection deadline, but is not cancelled when bootstrap succeeds, so
	// the same browser remains usable for the remaining inspection budget.
	browserRootCtx, cancelBrowserRoot := context.WithCancel(inspectionDeadlineCtx)
	defer cancelBrowserRoot()

	startupTimeout := browserStartupTimeout(remaining)
	browserProcess, err := startBoundedChromiumProcess(browserRootCtx, i.executablePath, profileDir, startupTimeout, resolverRules, i.protocolError)
	if err != nil {
		return InspectResult{}, ErrInspectionFailed
	}
	defer browserProcess.stop()
	allocatorCtx, cancelAllocator, err := boundedBrowserAllocator(browserRootCtx, browserProcess)
	if err != nil {
		return InspectResult{}, ErrInspectionFailed
	}
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx,
		chromedp.WithBrowserOption(chromedp.WithDialTimeout(startupTimeout)),
	)
	defer cancelBrowser()
	if err := chromedp.Run(browserCtx); err != nil {
		return InspectResult{}, ErrInspectionFailed
	}

	inspectionCtx, cancelInspection := context.WithCancel(browserCtx)
	defer cancelInspection()
	// Stop the owned browser process tree before chromedp detaches its target.
	// Detaching first can resume a Fetch-paused request for a brief window and
	// let forbidden page traffic escape before the later process teardown.
	defer browserProcess.stop()
	browserState := chromedp.FromContext(inspectionCtx)
	if browserState == nil || browserState.Target == nil || browserState.Browser == nil {
		return InspectResult{}, ErrInspectionFailed
	}
	monitor := newEventMonitor(policy)
	listenerDrain := newListenerDrain()
	hardAbortInspection := func() {
		browserProcess.stop()
		cancelInspection()
	}
	monitor.startWithDrain(inspectionCtx, hardAbortInspection, listenerDrain)
	childTargets := newChildTargetGuard(browserState.Target.TargetID)
	childTargets.startWithDrain(inspectionCtx, func() {
		monitor.failClosed(hardAbortInspection)
	}, listenerDrain)

	var (
		currentIndex int64
		entries      []*page.NavigationEntry
	)
	err = chromedp.Run(inspectionCtx,
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return autoAttachRelatedTargets(actionCtx, browserState.Target.TargetID)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny).Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return network.Enable().
				WithMaxTotalBufferSize(browserResponseCancellationThresholdBytes).
				WithMaxResourceBufferSize(browserResponseCancellationThresholdBytes).
				Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return network.SetCacheDisabled(true).Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return network.SetBypassServiceWorker(true).Do(actionCtx)
		}),
		// Browser evidence is deliberately static. Disabling page scripts before
		// navigation prevents JavaScript-only transports (WebSocket,
		// WebTransport, WebRTC, workers, and similar APIs) from bypassing the
		// URL-loader interception below. It also keeps this slice from becoming
		// a browser automation surface.
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return emulation.SetScriptExecutionDisabled(true).Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return network.SetBlockedURLs().WithURLPatterns(browserNetworkBlockPatterns(policy)).Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return cdpruntime.Enable().Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return accessibility.Enable().Do(actionCtx)
		}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return fetch.Enable().WithPatterns([]*fetch.RequestPattern{{
				URLPattern:   "*",
				RequestStage: fetch.RequestStageRequest,
			}}).Do(actionCtx)
		}),
		chromedp.Navigate(policy.targetURL),
		chromedp.WaitReady("html", chromedp.ByQuery),
		// Keep the fresh browser alive briefly after the DOM is ready so
		// asynchronous console output and related targets are observed before
		// we form evidence. It is bounded by the inspection deadline.
		chromedp.Sleep(browserSettleDelay),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			var historyErr error
			currentIndex, entries, historyErr = page.GetNavigationHistory().Do(actionCtx)
			return historyErr
		}),
	)
	var accessibilityEvidence []AccessibilityNode
	var accessibilityTruncated bool
	if err == nil {
		accessibilityEvidence, accessibilityTruncated, err = collectFlowAccessibility(inspectionCtx, maxAccessibilityNodes)
	}
	listenerDrain.stop()
	browserProcess.stop()
	cancelInspection()
	listenerDrain.wait()
	monitor.wait()
	if err != nil || monitor.failureWasObserved() || browserProcess.terminalFailureWasObserved() {
		return InspectResult{}, ErrInspectionFailed
	}

	finalURL, title, ok := currentNavigation(entries, currentIndex)
	if !ok || !policy.allowsURL(finalURL) {
		return InspectResult{}, ErrOriginNotAllowed
	}
	return InspectResult{
		FinalURL:               RedactURL(finalURL),
		FinalOrigin:            originFromRawURL(finalURL),
		Title:                  SanitizeEvidenceText(title),
		Accessibility:          accessibilityEvidence,
		AccessibilityTruncated: accessibilityTruncated,
		Console:                monitor.consoleMessages(),
		Network:                monitor.networkSummary(),
	}, nil
}

func (i *ChromiumInspector) removeProfileDir(path string) error {
	remove := os.RemoveAll
	if i != nil && i.removeProfile != nil {
		remove = i.removeProfile
	}
	const attempts = 3
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if err = remove(path); err == nil {
			return nil
		}
		if attempt+1 < attempts {
			time.Sleep(25 * time.Millisecond)
		}
	}
	return err
}

func (i *ChromiumInspector) createProfileDir(prefix string) (string, error) {
	base := ""
	if i != nil {
		base = i.profileRoot
	}
	if strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", ErrInspectionFailed
	}
	absBase = filepath.Clean(absBase)
	inspector, err := localfs.NewInspector()
	if err != nil || inspector.EnsurePath(absBase) != nil {
		return "", ErrInspectionFailed
	}
	resolvedBase, err := filepath.EvalSymlinks(absBase)
	if err != nil || inspector.EnsurePath(resolvedBase) != nil {
		return "", ErrInspectionFailed
	}
	profileDir, err := os.MkdirTemp(resolvedBase, prefix)
	if err != nil {
		return "", ErrInspectionFailed
	}
	cleanup := func() (string, error) {
		_ = os.RemoveAll(profileDir)
		return "", ErrInspectionFailed
	}
	if err := os.Chmod(profileDir, 0o700); err != nil {
		return cleanup()
	}
	handle, err := os.Open(profileDir)
	if err != nil {
		return cleanup()
	}
	info, statErr := handle.Stat()
	filesystemErr := localfs.EnsureBoundedFile(handle)
	closeErr := handle.Close()
	if statErr != nil || filesystemErr != nil || closeErr != nil || info == nil || !info.IsDir() {
		return cleanup()
	}
	return profileDir, nil
}

// browserNetworkBlockPatterns provides a second browser-level origin gate in
// addition to Fetch interception. Explicit allowed-origin patterns precede a
// catch-all block, so URL-loader traffic that is not part of the selected
// inspection cannot start while a request-paused event is waiting for its
// bounded handler. Script execution is disabled separately because not every
// browser transport goes through the URL loader.
func browserNetworkBlockPatterns(policy requestPolicy) []*network.BlockPattern {
	origins := make([]string, 0, len(policy.allowed))
	for origin := range policy.allowed {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	patterns := make([]*network.BlockPattern, 0, len(origins)+1)
	for _, origin := range origins {
		patterns = append(patterns, &network.BlockPattern{
			URLPattern: strings.TrimSuffix(origin, "/") + "/*",
			Block:      false,
		})
	}
	return append(patterns, &network.BlockPattern{
		URLPattern: "*://*/*",
		Block:      true,
	})
}

// childTargetGuard fails an inspection closed when Chromium reports a related
// popup, prerender, OOPIF, or worker. Fetch interception is target-scoped; it
// is safer to stop the evidence capture than let a child target run outside
// the original page's policy.
type childTargetGuard struct {
	primary target.ID
	once    sync.Once
}

func newChildTargetGuard(primary target.ID) *childTargetGuard {
	return &childTargetGuard{primary: primary}
}

func (g *childTargetGuard) start(ctx context.Context, abort func()) {
	g.startWithDrain(ctx, abort, nil)
}

func (g *childTargetGuard) startWithDrain(ctx context.Context, abort func(), drain *listenerDrain) {
	listener := func(event any) {
		if g.observe(event) {
			// Browser listeners run on Chromium's event path. Cancellation is
			// non-blocking and keeps the paused child from receiving any CDP work.
			g.once.Do(abort)
		}
	}
	if drain != nil {
		listener = drain.wrap(listener)
	}
	chromedp.ListenBrowser(ctx, listener)
}

func (g *childTargetGuard) observe(event any) bool {
	attached, ok := event.(*target.EventAttachedToTarget)
	return ok && attached.TargetInfo != nil && attached.TargetInfo.TargetID != g.primary
}

func autoAttachRelatedTargets(ctx context.Context, primary target.ID) error {
	browserState := chromedp.FromContext(ctx)
	if browserState == nil || browserState.Browser == nil {
		return ErrInspectionFailed
	}
	return target.AutoAttachRelated(primary, true).Do(cdp.WithExecutor(ctx, browserState.Browser))
}

func browserStartupTimeout(timeout time.Duration) time.Duration {
	// Cold Chromium launches can take more than ten seconds. Startup shares
	// the already-bounded inspection/flow deadline; a second, shorter ceiling
	// would fail before the operator's configured budget is exhausted.
	return timeout
}

// inspectionTimeRemaining returns the remaining wall-clock budget inherited
// by every Chromium phase. Inspect always creates that deadline itself; a
// missing deadline is treated as exhausted rather than accidentally starting
// an unbounded browser process.
func inspectionTimeRemaining(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

func currentNavigation(entries []*page.NavigationEntry, index int64) (string, string, bool) {
	if index < 0 || index >= int64(len(entries)) || entries[index] == nil {
		return "", "", false
	}
	return entries[index].URL, entries[index].Title, true
}

func originFromRawURL(raw string) string {
	origin, _ := OriginForURL(raw)
	return origin
}

func accessibilityValue(value *accessibility.Value) string {
	if value == nil {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(value.Value), &decoded); err != nil {
		return ""
	}
	switch value := decoded.(type) {
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

// SanitizeEvidenceText makes page-controlled text safe for bounded task
// evidence. It removes common credential-bearing URL fragments and oversized
// binary-looking payloads before they reach a model, artifact, or trace.
func SanitizeEvidenceText(value string) string {
	if len(value) > maxEvidenceInputBytes {
		value = value[:maxEvidenceInputBytes]
	}
	value = normalizeEvidenceLine(strings.ToValidUTF8(value, "�"))
	if value == "" {
		return ""
	}
	value = normalizeEvidenceLine(safetext.SanitizeErrorMessage(value))
	if len(value) <= maxEvidenceTextBytes {
		return value
	}
	const ellipsis = "…"
	value = value[:maxEvidenceTextBytes-len(ellipsis)]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + ellipsis
}

// normalizeEvidenceLine removes terminal/HTML direction controls and other
// format/control code points from page-owned text. Whitespace, including line
// breaks, is collapsed to one ASCII space so a title or accessibility label
// cannot forge a new evidence record when rendered as plain text.
func normalizeEvidenceLine(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	pendingSpace := false
	for _, r := range value {
		switch {
		case unicode.Is(unicode.Cf, r):
			// Drop bidi and other invisible format controls rather than retaining
			// page-selected display direction in operator/model evidence.
			continue
		case unicode.IsSpace(r):
			pendingSpace = out.Len() > 0
			continue
		case unicode.IsControl(r):
			continue
		}
		if pendingSpace {
			out.WriteByte(' ')
			pendingSpace = false
		}
		out.WriteRune(r)
	}
	return strings.TrimSpace(out.String())
}

// listenerDrain makes the terminal security decision ordered after every
// flow listener that was admitted before teardown. stop closes admission
// under the same mutex used by wrap; callers then cancel Chromium contexts,
// wait, and only afterwards read policy/failure state.
type listenerDrain struct {
	mu        sync.Mutex
	accepting bool
	wg        sync.WaitGroup
}

func newListenerDrain() *listenerDrain {
	return &listenerDrain{accepting: true}
}

func (d *listenerDrain) wrap(listener func(any)) func(any) {
	return func(event any) {
		d.mu.Lock()
		if !d.accepting {
			d.mu.Unlock()
			return
		}
		d.wg.Add(1)
		d.mu.Unlock()
		defer d.wg.Done()
		listener(event)
	}
}

func (d *listenerDrain) stop() {
	d.mu.Lock()
	d.accepting = false
	d.mu.Unlock()
}

func (d *listenerDrain) wait() {
	d.wg.Wait()
}

type pausedRequest struct {
	id    fetch.RequestID
	allow bool
}

type eventMonitor struct {
	policy requestPolicy
	paused chan pausedRequest
	mu     sync.Mutex
	wg     sync.WaitGroup
	abort  sync.Once
	// abortOnBlocked is enabled only for interactive flows. Static inspection
	// may report blocked passive subresources, while a flow must stop as soon as
	// page behavior attempts an unapproved origin or method.
	abortOnBlocked bool

	network               NetworkSummary
	console               []ConsoleMessage
	responseBytes         int64
	responseLimitExceeded bool
	policyViolated        bool
	failureObserved       bool
	blockedRequestIDs     map[network.RequestID]struct{}
}

func newEventMonitor(policy requestPolicy) *eventMonitor {
	return &eventMonitor{policy: policy, paused: make(chan pausedRequest, maxPausedRequests), blockedRequestIDs: make(map[network.RequestID]struct{})}
}

func newFlowEventMonitor(policy requestPolicy) *eventMonitor {
	return &eventMonitor{policy: policy, paused: make(chan pausedRequest, maxPausedRequests), abortOnBlocked: true, blockedRequestIDs: make(map[network.RequestID]struct{})}
}

func (m *eventMonitor) start(ctx context.Context, abort func()) {
	m.startWithDrain(ctx, abort, nil)
}

func (m *eventMonitor) startWithDrain(ctx context.Context, abort func(), drain *listenerDrain) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case request := <-m.paused:
				err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
					if request.allow {
						return fetch.ContinueRequest(request.id).Do(actionCtx)
					}
					return fetch.FailRequest(request.id, network.ErrorReasonBlockedByClient).Do(actionCtx)
				}))
				if err != nil && ctx.Err() == nil {
					// A paused request that cannot be answered otherwise causes
					// Page.navigate to hang until its deadline. Abort the bounded
					// inspection instead; callers receive only the generic error.
					m.failClosed(abort)
				}
			}
		}
	}()
	listener := func(event any) {
		m.observeEvent(ctx, event, abort)
	}
	if drain != nil {
		listener = drain.wrap(listener)
	}
	chromedp.ListenTarget(ctx, listener)
}

func (m *eventMonitor) wait() {
	m.wg.Wait()
}

func (m *eventMonitor) observeEvent(ctx context.Context, event any, abort func()) {
	switch event := event.(type) {
	case *fetch.EventRequestPaused:
		m.onRequestPaused(ctx, event, abort)
	case *network.EventRequestWillBeSent:
		if m.onRequestWillBeSent(event) {
			m.failClosed(abort)
		}
	case *network.EventDataReceived:
		if m.onDataReceived(event) {
			m.failClosed(abort)
		}
	case *network.EventResponseReceived:
		if m.onResponseReceived(event) {
			m.failClosed(abort)
		} else if m.onDownloadResponse(event) {
			m.failClosed(abort)
		}
	case *network.EventLoadingFailed:
		if m.onLoadingFailed(event) {
			m.failClosed(abort)
		}
	case *cdpruntime.EventConsoleAPICalled:
		m.onConsole(event)
	}
}

func (m *eventMonitor) onDownloadResponse(event *network.EventResponseReceived) bool {
	if event == nil || event.Response == nil || !m.abortOnBlocked || !responseDeclaresDownload(event.Response.Headers) {
		return false
	}
	m.mu.Lock()
	m.policyViolated = true
	m.mu.Unlock()
	return true
}

func responseDeclaresDownload(headers network.Headers) bool {
	for name, raw := range headers {
		if !strings.EqualFold(name, "Content-Disposition") {
			continue
		}
		var value string
		switch raw := raw.(type) {
		case string:
			value = raw
		case []string:
			value = strings.Join(raw, ",")
		default:
			value = fmt.Sprint(raw)
		}
		return strings.Contains(strings.ToLower(value), "attachment")
	}
	return false
}

func (m *eventMonitor) failClosed(abort func()) {
	m.mu.Lock()
	m.failureObserved = true
	m.mu.Unlock()
	if abort == nil {
		return
	}
	m.abort.Do(abort)
}

func (m *eventMonitor) onRequestPaused(ctx context.Context, event *fetch.EventRequestPaused, abort func()) {
	if event == nil || event.Request == nil {
		return
	}
	allow := m.policy.allowsRequest(event.Request.URL, event.Request.Method)
	m.mu.Lock()
	if !allow {
		if event.NetworkID == "" {
			m.network.BlockedRequests++
		} else if _, seen := m.blockedRequestIDs[event.NetworkID]; !seen {
			m.blockedRequestIDs[event.NetworkID] = struct{}{}
			m.network.BlockedRequests++
		}
		if m.abortOnBlocked {
			m.policyViolated = true
		}
	}
	abortOnBlocked := !allow && m.abortOnBlocked
	m.mu.Unlock()
	if abortOnBlocked {
		m.failClosed(abort)
	}
	// chromedp dispatches listeners synchronously. Queue the reply for one
	// bounded worker rather than blocking the CDP event loop or spawning an
	// unbounded goroutine for hostile pages. A full queue intentionally leaves
	// the new request paused until the inspection timeout (fail closed).
	select {
	case m.paused <- pausedRequest{id: event.RequestID, allow: allow}:
	case <-ctx.Done():
	default:
		m.mu.Lock()
		m.network.BlockedRequests++
		m.mu.Unlock()
	}
}

func (m *eventMonitor) onLoadingFailed(event *network.EventLoadingFailed) bool {
	if event == nil || event.BlockedReason != network.BlockedReasonInspector {
		return false
	}
	m.mu.Lock()
	if _, seen := m.blockedRequestIDs[event.RequestID]; !seen {
		m.blockedRequestIDs[event.RequestID] = struct{}{}
		m.network.BlockedRequests++
	}
	if m.abortOnBlocked {
		m.policyViolated = true
	}
	abort := m.abortOnBlocked
	m.mu.Unlock()
	return abort
}

func (m *eventMonitor) onRequestWillBeSent(event *network.EventRequestWillBeSent) bool {
	if event == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.network.Requests++
	if event.Type == network.ResourceTypeDocument {
		m.network.Navigations++
	}
	return m.network.Requests > maxObservedRequests
}

// onDataReceived accounts every response chunk instead of trusting a
// Content-Length header. Chunked and otherwise unknown-length responses must
// reach the same observed-data cancellation threshold as fixed-size responses.
func (m *eventMonitor) onDataReceived(event *network.EventDataReceived) bool {
	if event == nil {
		return false
	}
	return m.reachesResponseCancellationThreshold(event.DataLength)
}

// onResponseReceived rejects a known oversized body before Chromium receives
// its first chunk. It is intentionally only an early fail-closed optimization:
// onDataReceived remains authoritative for unknown, malformed, compressed, or
// streaming response lengths.
func (m *eventMonitor) onResponseReceived(event *network.EventResponseReceived) bool {
	if event == nil || event.Response == nil {
		return false
	}
	contentLength, ok := responseContentLength(event.Response.Headers)
	return ok && m.declaredResponseReachesCancellationThreshold(contentLength)
}

func (m *eventMonitor) reachesResponseCancellationThreshold(bytes int64) bool {
	if bytes <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.responseLimitExceeded {
		return true
	}
	if m.responseBytes > browserResponseCancellationThresholdBytes-bytes {
		m.responseLimitExceeded = true
		return true
	}
	m.responseBytes += bytes
	return false
}

func (m *eventMonitor) declaredResponseReachesCancellationThreshold(bytes int64) bool {
	if bytes <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.responseLimitExceeded {
		return true
	}
	if m.responseBytes > browserResponseCancellationThresholdBytes-bytes {
		m.responseLimitExceeded = true
		return true
	}
	return false
}

func responseContentLength(headers network.Headers) (int64, bool) {
	for name, value := range headers {
		if !strings.EqualFold(name, "Content-Length") {
			continue
		}
		return parseResponseContentLength(value)
	}
	return 0, false
}

func parseResponseContentLength(value any) (int64, bool) {
	var raw string
	switch value := value.(type) {
	case string:
		raw = value
	case []string:
		if len(value) != 1 {
			return 0, false
		}
		raw = value[0]
	default:
		raw = fmt.Sprint(value)
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func (m *eventMonitor) onConsole(event *cdpruntime.EventConsoleAPICalled) {
	if event == nil || (event.Type != cdpruntime.APITypeError && event.Type != cdpruntime.APITypeWarning) {
		return
	}
	parts := make([]string, 0, len(event.Args))
	for _, arg := range event.Args {
		if arg == nil {
			continue
		}
		value := SanitizeEvidenceText(arg.Description)
		if value == "" {
			value = SanitizeEvidenceText(arg.Value.String())
		}
		if value != "" {
			parts = append(parts, value)
		}
	}
	text := SanitizeEvidenceText(strings.Join(parts, " "))
	if text == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.console) >= maxConsoleMessages {
		return
	}
	m.console = append(m.console, ConsoleMessage{Level: string(event.Type), Text: text})
}

func (m *eventMonitor) networkSummary() NetworkSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.network
}

func (m *eventMonitor) consoleMessages() []ConsoleMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ConsoleMessage(nil), m.console...)
}

func (m *eventMonitor) markPolicyViolation() {
	m.mu.Lock()
	m.policyViolated = true
	m.mu.Unlock()
}

func (m *eventMonitor) policyWasViolated() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policyViolated
}

func (m *eventMonitor) failureWasObserved() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failureObserved
}
