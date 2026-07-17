package browserrunner

import (
	"context"
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
	"github.com/hecatehq/hecate/internal/safetext"
)

const (
	maxAccessibilityNodes = 1
	maxConsoleMessages    = 16
	maxEvidenceTextBytes  = 1 << 10
	maxPausedRequests     = 256
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
func (i *ChromiumInspector) Inspect(ctx context.Context, req InspectRequest) (InspectResult, error) {
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

	profileDir, err := os.MkdirTemp("", "hecate-browser-")
	if err != nil {
		return InspectResult{}, ErrInspectionFailed
	}
	if err := os.Chmod(profileDir, 0o700); err != nil {
		_ = os.RemoveAll(profileDir)
		return InspectResult{}, ErrInspectionFailed
	}
	defer os.RemoveAll(profileDir)

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
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(browserRootCtx, chromiumAllocatorOptions(i.executablePath, profileDir, startupTimeout, resolverRules)...)
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
	browserState := chromedp.FromContext(inspectionCtx)
	if browserState == nil || browserState.Target == nil || browserState.Browser == nil {
		return InspectResult{}, ErrInspectionFailed
	}
	childTargets := newChildTargetGuard(browserState.Target.TargetID)
	childTargets.start(inspectionCtx, cancelInspection)

	monitor := newEventMonitor(policy)
	monitor.start(inspectionCtx, cancelInspection)

	var (
		currentIndex int64
		entries      []*page.NavigationEntry
		nodes        []*accessibility.Node
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
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			root, treeErr := accessibility.GetRootAXNode().Do(actionCtx)
			if treeErr != nil {
				return treeErr
			}
			if root != nil {
				nodes = []*accessibility.Node{root}
			}
			return nil
		}),
	)
	cancelInspection()
	monitor.wait()
	if err != nil {
		return InspectResult{}, ErrInspectionFailed
	}

	finalURL, title, ok := currentNavigation(entries, currentIndex)
	if !ok || !policy.allowsURL(finalURL) {
		return InspectResult{}, ErrOriginNotAllowed
	}
	return InspectResult{
		FinalURL:      RedactURL(finalURL),
		FinalOrigin:   originFromRawURL(finalURL),
		Title:         SanitizeEvidenceText(title),
		Accessibility: summarizeAccessibility(nodes),
		Console:       monitor.consoleMessages(),
		Network:       monitor.networkSummary(),
	}, nil
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
	chromedp.ListenBrowser(ctx, func(event any) {
		if g.observe(event) {
			// Browser listeners run on Chromium's event path. Cancellation is
			// non-blocking and keeps the paused child from receiving any CDP work.
			g.once.Do(abort)
		}
	})
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

func chromiumAllocatorOptions(executablePath, profileDir string, startupTimeout time.Duration, resolverRules string) []chromedp.ExecAllocatorOption {
	// Do not add --no-sandbox. Hecate's local runtime must preserve Chromium's
	// normal process sandbox when the host platform supports it.
	options := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(executablePath),
		chromedp.UserDataDir(profileDir),
		chromedp.WSURLReadTimeout(startupTimeout),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("deny-permission-prompts", true),
		chromedp.Flag("no-proxy-server", true),
		// chromedp otherwise adds --no-sandbox automatically when its host
		// process is root. Suppress that fallback explicitly; the runtime must
		// not weaken Chromium's normal sandbox.
		chromedp.Flag("no-sandbox", false),
		chromedp.Flag("use-mock-keychain", true),
	}
	if resolverRules != "" {
		options = append(options, chromedp.Flag("host-resolver-rules", resolverRules))
	}
	return options
}

func browserStartupTimeout(timeout time.Duration) time.Duration {
	const maximum = 10 * time.Second
	if timeout < maximum {
		return timeout
	}
	return maximum
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

func summarizeAccessibility(nodes []*accessibility.Node) []AccessibilityNode {
	out := make([]AccessibilityNode, 0, min(len(nodes), maxAccessibilityNodes))
	for _, node := range nodes {
		if node == nil || node.Ignored || len(out) >= maxAccessibilityNodes {
			continue
		}
		item := AccessibilityNode{
			Role:        accessibilityValue(node.Role),
			Name:        SanitizeEvidenceText(accessibilityValue(node.Name)),
			Description: SanitizeEvidenceText(accessibilityValue(node.Description)),
			Value:       SanitizeEvidenceText(accessibilityValue(node.Value)),
		}
		if item.Role == "" && item.Name == "" && item.Description == "" && item.Value == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func accessibilityValue(value *accessibility.Value) string {
	if value == nil {
		return ""
	}
	return value.Value.String()
}

// SanitizeEvidenceText makes page-controlled text safe for bounded task
// evidence. It removes common credential-bearing URL fragments and oversized
// binary-looking payloads before they reach a model, artifact, or trace.
func SanitizeEvidenceText(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if value == "" {
		return ""
	}
	value = safetext.SanitizeErrorMessage(value)
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

	network               NetworkSummary
	console               []ConsoleMessage
	responseBytes         int64
	responseLimitExceeded bool
}

func newEventMonitor(policy requestPolicy) *eventMonitor {
	return &eventMonitor{policy: policy, paused: make(chan pausedRequest, maxPausedRequests)}
}

func (m *eventMonitor) start(ctx context.Context, abort func()) {
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
	chromedp.ListenTarget(ctx, func(event any) {
		m.observeEvent(ctx, event, abort)
	})
}

func (m *eventMonitor) wait() {
	m.wg.Wait()
}

func (m *eventMonitor) observeEvent(ctx context.Context, event any, abort func()) {
	switch event := event.(type) {
	case *fetch.EventRequestPaused:
		m.onRequestPaused(ctx, event)
	case *network.EventRequestWillBeSent:
		m.onRequestWillBeSent(event)
	case *network.EventDataReceived:
		if m.onDataReceived(event) {
			m.failClosed(abort)
		}
	case *network.EventResponseReceived:
		if m.onResponseReceived(event) {
			m.failClosed(abort)
		}
	case *cdpruntime.EventConsoleAPICalled:
		m.onConsole(event)
	}
}

func (m *eventMonitor) failClosed(abort func()) {
	if abort == nil {
		return
	}
	m.abort.Do(abort)
}

func (m *eventMonitor) onRequestPaused(ctx context.Context, event *fetch.EventRequestPaused) {
	if event == nil || event.Request == nil {
		return
	}
	allow := m.policy.allowsRequest(event.Request.URL, event.Request.Method)
	m.mu.Lock()
	if !allow {
		m.network.BlockedRequests++
	}
	m.mu.Unlock()
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

func (m *eventMonitor) onRequestWillBeSent(event *network.EventRequestWillBeSent) {
	if event == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.network.Requests++
	if event.Type == network.ResourceTypeDocument {
		m.network.Navigations++
	}
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
