package browserrunner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func requireBrowserSmoke(t *testing.T) string {
	t.Helper()
	if os.Getenv("HECATE_BROWSER_SMOKE") != "1" {
		t.Skip("set HECATE_BROWSER_SMOKE=1 with HECATE_TASK_BROWSER_EXECUTABLE to run Chromium smoke")
	}
	executable := strings.TrimSpace(os.Getenv("HECATE_TASK_BROWSER_EXECUTABLE"))
	if executable == "" {
		t.Fatal("HECATE_TASK_BROWSER_EXECUTABLE is required when HECATE_BROWSER_SMOKE=1")
	}
	return executable
}

func browserSmokeOrigin(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse browser smoke URL: %v", err)
	}
	return parsed.Scheme + "://" + parsed.Host
}

func TestChromiumBrowserFlowSmokeAccessibleActionsAndProfileIsolation(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var (
		sawPriorCookie       atomic.Bool
		sawPriorLocalStorage atomic.Int32
		sawPriorIndexedDB    atomic.Int32
		serviceWorkerHits    atomic.Int32
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prior-local-storage":
			sawPriorLocalStorage.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		case "/prior-indexed-db":
			sawPriorIndexedDB.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		case "/sw.js":
			serviceWorkerHits.Add(1)
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = fmt.Fprint(w, `self.addEventListener('fetch', () => {})`)
			return
		case "/":
			if _, err := r.Cookie("hecate_flow_smoke"); err == nil {
				sawPriorCookie.Store(true)
			}
		default:
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "hecate_flow_smoke", Value: "fresh", Path: "/"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Flow start</title></head><body>
<div id="storage-ready" role="status" aria-label="Storage ready" hidden>Storage ready</div>
<button aria-label="Continue" onclick="localStorage.setItem('flow','used'); document.title='Flow complete'; document.getElementById('ready').hidden=false">Continue</button>
<div id="ready" role="status" aria-label="Ready" hidden>Ready</div>
<script>
if (localStorage.getItem('flow') !== null) fetch('/prior-local-storage');
localStorage.setItem('flow', 'used');
try { navigator.serviceWorker.register('/sw.js').catch(() => {}); } catch (_) {}
const markStorageReady = () => { document.getElementById('storage-ready').hidden = false; };
const databaseRequest = indexedDB.open('hecate-flow-smoke', 1);
databaseRequest.onupgradeneeded = () => databaseRequest.result.createObjectStore('state');
databaseRequest.onerror = markStorageReady;
databaseRequest.onsuccess = () => {
  const db = databaseRequest.result;
  const tx = db.transaction('state', 'readwrite');
  const store = tx.objectStore('state');
  const get = store.get('used');
  get.onsuccess = () => { if (get.result) fetch('/prior-indexed-db'); store.put(true, 'used'); };
  tx.oncomplete = () => { db.close(); markStorageReady(); };
  tx.onerror = markStorageReady;
};
</script>
</body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions: []FlowAction{
			{Kind: FlowActionWaitFor, Role: "status", Name: "Storage ready"},
			{Kind: FlowActionClick, Role: "button", Name: "Continue"},
			{Kind: FlowActionWaitFor, Role: "status", Name: "Ready"},
		},
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := inspector.RunFlow(context.Background(), request)
		if err != nil {
			t.Fatalf("RunFlow() attempt %d result=%+v error=%v", attempt+1, result, err)
		}
		if result.FinalOrigin != request.AllowedOrigin || result.Title != "Flow complete" || len(result.Actions) != 3 {
			t.Fatalf("RunFlow() attempt %d result = %+v", attempt+1, result)
		}
		for _, action := range result.Actions {
			if action.Status != FlowActionStatusCompleted {
				t.Fatalf("RunFlow() action = %+v", action)
			}
		}
		if !flowEvidenceContains(result.FinalAccessibility, "status", "Ready") {
			t.Fatalf("RunFlow() final accessibility = %+v, want Ready status", result.FinalAccessibility)
		}
	}
	if sawPriorCookie.Load() {
		t.Fatal("browser flow reused a cookie across fresh profiles")
	}
	if sawPriorLocalStorage.Load() != 0 || sawPriorIndexedDB.Load() != 0 || serviceWorkerHits.Load() != 0 {
		t.Fatalf("browser flow reused or installed browser state: localStorage=%d indexedDB=%d serviceWorker=%d", sawPriorLocalStorage.Load(), sawPriorIndexedDB.Load(), serviceWorkerHits.Load())
	}
}

func TestChromiumBrowserFlowSmokeBlocksScriptTransports(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var (
		workerHits        atomic.Int32
		webSocketHits     atomic.Int32
		foreignHits       atomic.Int32
		audioGuardEscapes atomic.Int32
	)
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		foreignHits.Add(1)
	}))
	defer foreign.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer udp.Close()
	udpHit := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, 2048)
		_ = udp.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, readErr := udp.ReadFrom(buffer); readErr == nil {
			udpHit <- struct{}{}
		}
	}()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/audio-guard-escaped":
			audioGuardEscapes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		case "/worker.js":
			workerHits.Add(1)
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = fmt.Fprint(w, `postMessage('ran')`)
			return
		case "/socket":
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				webSocketHits.Add(1)
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		webSocketURL := "ws://" + r.Host + "/socket"
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><title>Guarded transports</title></head><body>
<div role="status" aria-label="Ready">Ready</div><script>
const attempt = fn => { try { fn(); } catch (_) {} };
const mustBeBlocked = fn => { try { fn(); fetch('/audio-guard-escaped'); } catch (_) {} };
const mustUseHecateConstructorGuard = ctor => {
  try { new ctor(); } catch (error) {
    if (error instanceof DOMException && error.name === 'SecurityError' && error.message === 'Blocked by Hecate browser flow policy') return;
  }
  fetch('/audio-guard-escaped');
};
attempt(() => new Worker('/worker.js'));
attempt(() => new WebSocket(%q));
attempt(() => window.open(%q));
attempt(() => new WebTransport(%q));
attempt(() => { const pc = new RTCPeerConnection({iceServers:[{urls:%q}]}); pc.createDataChannel('x'); pc.createOffer().then(o => pc.setLocalDescription(o)); });
mustBeBlocked(() => new AudioContext());
mustBeBlocked(() => speechSynthesis.speak(new SpeechSynthesisUtterance('browser flow must stay silent')));
mustBeBlocked(() => document.createElement('audio').play());
if ('SpeechRecognition' in globalThis) mustUseHecateConstructorGuard(SpeechRecognition);
if ('webkitSpeechRecognition' in globalThis) mustUseHecateConstructorGuard(webkitSpeechRecognition);
</script></body></html>`, webSocketURL, foreign.URL, server.URL, "stun:"+udp.LocalAddr().String())
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionWaitFor, Role: "status", Name: "Ready"}},
	})
	if err != nil {
		t.Fatalf("RunFlow() result=%+v error=%v", result, err)
	}
	if workerHits.Load() != 0 || webSocketHits.Load() != 0 || foreignHits.Load() != 0 || audioGuardEscapes.Load() != 0 {
		t.Fatalf("blocked transport/audio hits: worker=%d websocket=%d foreign=%d audio=%d", workerHits.Load(), webSocketHits.Load(), foreignHits.Load(), audioGuardEscapes.Load())
	}
	select {
	case <-udpHit:
		t.Fatal("browser flow emitted WebRTC/STUN UDP traffic")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestChromiumBrowserFlowSmokeCapsOriginStorageQuota(t *testing.T) {
	executable := requireBrowserSmoke(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
<button aria-label="Test storage quota" onclick="testQuota()">Test storage quota</button>
<div id="result" role="status" hidden></div>
<script>
async function testQuota() {
  const result = document.getElementById('result');
  try {
    const root = await navigator.storage.getDirectory();
    const file = await root.getFileHandle('oversized.bin', {create:true});
    const writer = await file.createWritable();
    const chunk = new Uint8Array(1024 * 1024);
    for (let index = 0; index < 16; index++) await writer.write(chunk);
    await writer.close();
    result.setAttribute('aria-label', 'Quota escaped');
  } catch (_) {
    result.setAttribute('aria-label', 'Quota blocked');
  }
  result.hidden = false;
}
</script></body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions: []FlowAction{
			{Kind: FlowActionClick, Role: "button", Name: "Test storage quota"},
			{Kind: FlowActionWaitFor, Role: "status", Name: "Quota blocked"},
		},
	})
	if err != nil {
		t.Fatalf("RunFlow() storage quota result=%+v error=%v", result, err)
	}
	if !flowEvidenceContains(result.FinalAccessibility, "status", "Quota blocked") {
		t.Fatalf("storage quota final accessibility = %+v", result.FinalAccessibility)
	}
}

func TestChromiumBrowserFlowSmokeBlocksUnapprovedNetworkBeforeServer(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var (
		postHits    atomic.Int32
		foreignHits atomic.Int32
	)
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		foreignHits.Add(1)
	}))
	defer foreign.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mutate" {
			postHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body><button aria-label="Run requests" onclick='fetch("/mutate",{method:"POST"}); fetch(%q)'>Run requests</button></body></html>`, foreign.URL)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "button", Name: "Run requests"}},
	})
	if !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("RunFlow() result=%+v error=%v, want ErrFlowPolicyViolation", result, err)
	}
	if postHits.Load() != 0 || foreignHits.Load() != 0 {
		t.Fatalf("blocked requests reached server: post=%d foreign=%d", postHits.Load(), foreignHits.Load())
	}
}

func TestChromiumBrowserFlowSmokeBlocksExternalProtocolNavigation(t *testing.T) {
	executable := requireBrowserSmoke(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body><a href="mailto:operator@example.test">Open mail</a></body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "link", Name: "Open mail"}},
	})
	if !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("RunFlow() result=%+v error=%v, want external protocol policy failure", result, err)
	}
}

func TestChromiumBrowserFlowSmokeRevalidatesTargetAfterScrollHandlers(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var clickHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clicked" {
			clickHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<div style="height:5000px"></div>
<button id="target" aria-label="Continue" onclick="fetch('/clicked')">Continue</button>
<script>addEventListener('scroll', () => document.getElementById('target').setAttribute('aria-label', 'Changed'));</script>
</body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "button", Name: "Continue"}},
	})
	if !errors.Is(err, ErrFlowTargetNotFound) && !errors.Is(err, ErrFlowTargetUnavailable) {
		t.Fatalf("RunFlow() result=%+v error=%v, want target revalidation failure", result, err)
	}
	if clickHits.Load() != 0 {
		t.Fatalf("scroll-mutated target received %d click request(s)", clickHits.Load())
	}
}

func TestChromiumBrowserFlowSmokeRechecksHitAfterFinalSemanticQuery(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var clickHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clicked" {
			clickHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<button id="target" aria-label="Continue" onclick="fetch('/clicked')">Continue</button>
</body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	inspector.beforeFinalClickHitTest = func(ctx context.Context) error {
		_, err := chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](`(() => {
  const overlay = document.createElement('div');
  overlay.id = 'late-overlay';
  overlay.style.cssText = 'position:fixed;inset:0;z-index:2147483647;background:white';
  document.body.appendChild(overlay);
})()`))
		return err
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "button", Name: "Continue"}},
	})
	if !errors.Is(err, ErrFlowTargetUnavailable) || len(result.Actions) != 1 || result.Actions[0].ErrorKind != "target_unavailable" {
		t.Fatalf("RunFlow() result=%+v error=%v, want final hit-test rejection", result, err)
	}
	if clickHits.Load() != 0 {
		t.Fatalf("late overlay redirected %d click request(s)", clickHits.Load())
	}
}

func TestChromiumBrowserFlowSmokeRejectsAmbiguousDisabledAndObscuredTargets(t *testing.T) {
	executable := requireBrowserSmoke(t)
	for _, test := range []struct {
		name string
		html string
		err  error
	}{
		{
			name: "ambiguous",
			html: `<button aria-label="Continue">One</button><button aria-label="Continue">Two</button>`,
			err:  ErrFlowTargetAmbiguous,
		},
		{
			name: "disabled",
			html: `<button aria-label="Continue" disabled>Continue</button>`,
			err:  ErrFlowTargetUnavailable,
		},
		{
			name: "obscured",
			html: `<button style="position:absolute;left:20px;top:20px" aria-label="Continue">Continue</button><div style="position:absolute;left:0;top:0;width:300px;height:200px;z-index:10">Overlay</div>`,
			err:  ErrFlowTargetUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = fmt.Fprint(w, "<!doctype html><html><body>"+test.html+"</body></html>")
			}))
			defer server.Close()
			inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			result, err := inspector.RunFlow(context.Background(), FlowRequest{
				URL:           server.URL,
				AllowedOrigin: browserSmokeOrigin(t, server.URL),
				Actions:       []FlowAction{{Kind: FlowActionClick, Role: "button", Name: "Continue"}},
			})
			if !errors.Is(err, test.err) || len(result.Actions) != 1 || result.Actions[0].Status != FlowActionStatusFailed {
				t.Fatalf("RunFlow() result=%+v error=%v, want %v", result, err, test.err)
			}
		})
	}
}

func TestChromiumBrowserFlowSmokeRejectsNestedInteractiveClickTarget(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var childHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/child" {
			childHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<div role="button" tabindex="0" aria-label="Continue" style="position:relative;width:240px;height:80px">
  <a href="/child" aria-label="Unapproved child" style="position:absolute;inset:0;display:block">Child action</a>
</div>
</body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "button", Name: "Continue"}},
	})
	if !errors.Is(err, ErrFlowTargetUnavailable) || len(result.Actions) != 1 || result.Actions[0].ErrorKind != "target_unavailable" {
		t.Fatalf("RunFlow() result=%+v error=%v, want nested interactive target rejection", result, err)
	}
	if childHits.Load() != 0 {
		t.Fatalf("nested interactive descendant received %d request(s)", childHits.Load())
	}
}

func TestChromiumBrowserFlowSmokeBlocksDeclarativePopupTarget(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		foreignHits.Add(1)
	}))
	defer foreign.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body><a target="_blank" aria-label="Open popup" href=%q>Open popup</a></body></html>`, foreign.URL)
	}))
	defer server.Close()
	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "link", Name: "Open popup"}},
	})
	if !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("RunFlow() result=%+v error=%v, want popup policy violation", result, err)
	}
	if foreignHits.Load() != 0 {
		t.Fatalf("declarative popup reached foreign server %d time(s)", foreignHits.Load())
	}
}

func TestChromiumBrowserFlowSmokeRefreshesNavigationAfterFailedAction(t *testing.T) {
	executable := requireBrowserSmoke(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/next" {
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Next page</title></head><body><div role="status" aria-label="Arrived">Arrived</div></body></html>`)
			return
		}
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Starting page</title></head><body><script>setTimeout(() => { location.href = '/next'; }, 200)</script></body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionWaitFor, Role: "status", Name: "Never appears"}},
	})
	if !errors.Is(err, ErrFlowTargetNotFound) {
		t.Fatalf("RunFlow() result=%+v error=%v, want target not found", result, err)
	}
	if result.FinalURL != server.URL+"/next" || result.Title != "Next page" {
		t.Fatalf("partial navigation evidence = url %q title %q", result.FinalURL, result.Title)
	}
}

func TestChromiumBrowserFlowSmokeCancelsFileChooserAndDeniesDownload(t *testing.T) {
	executable := requireBrowserSmoke(t)
	var downloadHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download" {
			downloadHits.Add(1)
			w.Header().Set("Content-Disposition", `attachment; filename="blocked.txt"`)
			_, _ = fmt.Fprint(w, "must not be written")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<input id="file" type="file" hidden>
<button aria-label="Choose file" onclick="document.getElementById('file').click(); setTimeout(() => { document.getElementById('no-file').hidden=false }, 100)">Choose file</button>
<div id="no-file" role="status" aria-label="No file selected" hidden>No file selected</div>
<a id="download" href="/download" download="blocked.txt">download</a>
<button aria-label="Download file" onclick="document.getElementById('download').click()">Download file</button>
</body></html>`)
	}))
	defer server.Close()

	profileRoot := t.TempDir()
	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	inspector.profileRoot = profileRoot
	origin := browserSmokeOrigin(t, server.URL)
	chooserResult, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: origin,
		Actions: []FlowAction{
			{Kind: FlowActionClick, Role: "button", Name: "Choose file"},
			{Kind: FlowActionWaitFor, Role: "status", Name: "No file selected"},
		},
	})
	if err != nil {
		t.Fatalf("file chooser flow result=%+v error=%v", chooserResult, err)
	}

	downloadResult, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: origin,
		Actions:       []FlowAction{{Kind: FlowActionClick, Role: "button", Name: "Download file"}},
	})
	if !errors.Is(err, ErrFlowPolicyViolation) || len(downloadResult.Actions) != 1 || downloadResult.Actions[0].Status != FlowActionStatusFailed || downloadResult.Actions[0].ErrorKind != "policy_violation" {
		t.Fatalf("download-denial flow result=%+v error=%v, want audited policy violation", downloadResult, err)
	}
	entries, err := os.ReadDir(profileRoot)
	if err != nil {
		t.Fatalf("read profile root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("file chooser/download flow left profile entries: %v", entries)
	}
	// Chromium may begin the permitted GET before Browser.downloadWillBegin is
	// emitted. The security contract is no file write, not zero server reads.
	if downloadHits.Load() > 1 {
		t.Fatalf("download endpoint hit %d times, want at most one denied attempt", downloadHits.Load())
	}
}

// TestChromiumBrowserFlowSmokeCapsAggregateResponses proves that the response
// budget spans the whole approved flow. Several individually bounded GETs
// must not bypass the aggregate browser-process limit.
func TestChromiumBrowserFlowSmokeCapsAggregateResponses(t *testing.T) {
	executable := requireBrowserSmoke(t)
	const responseSize = 3 << 19 // 1.5 MiB; three responses exceed the 4 MiB cap.
	payload := strings.Repeat("x", responseSize)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/blob/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = fmt.Fprint(w, payload)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<button aria-label="Load evidence" onclick="(async () => { for (const path of ['/blob/1','/blob/2','/blob/3']) { const response = await fetch(path); await response.arrayBuffer(); } document.getElementById('done').hidden=false; })()">Load evidence</button>
<div id="done" role="status" aria-label="Loaded" hidden>Loaded</div>
</body></html>`)
	}))
	defer server.Close()

	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions: []FlowAction{
			{Kind: FlowActionClick, Role: "button", Name: "Load evidence"},
			{Kind: FlowActionWaitFor, Role: "status", Name: "Loaded"},
		},
	})
	if !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("RunFlow() result=%+v error=%v, want aggregate response limit failure", result, err)
	}
}

func TestChromiumBrowserFlowSmokeCapsProtocolEventFlood(t *testing.T) {
	executable := requireBrowserSmoke(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<div role="status" aria-label="Never reached">Never reached</div>
<script>
const payload = 'x'.repeat(900000);
for (let index = 0; index < 64; index++) console.warn(payload);
</script>
</body></html>`)
	}))
	defer server.Close()
	profileRoot := t.TempDir()
	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	inspector.profileRoot = profileRoot
	started := time.Now()
	_, err = inspector.RunFlow(context.Background(), FlowRequest{
		URL:           server.URL,
		AllowedOrigin: browserSmokeOrigin(t, server.URL),
		Actions:       []FlowAction{{Kind: FlowActionWaitFor, Role: "status", Name: "Never reached"}},
	})
	if !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("RunFlow() error = %v, want bounded protocol failure", err)
	}
	if elapsed := time.Since(started); elapsed >= 20*time.Second {
		t.Fatalf("protocol flood remained live for %v, want bounded failure before the flow deadline", elapsed)
	}
	entries, readErr := os.ReadDir(profileRoot)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("protocol-flood cleanup entries=%v error=%v, want empty profile root", entries, readErr)
	}
}

func TestChromiumBrowserFlowSmokeCancellationRemovesProfile(t *testing.T) {
	executable := requireBrowserSmoke(t)
	requestStarted := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
		<-r.Context().Done()
		requestCanceled <- struct{}{}
	}))
	defer server.Close()

	profileRoot := t.TempDir()
	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	inspector.profileRoot = profileRoot
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := inspector.RunFlow(ctx, FlowRequest{
			URL:           server.URL,
			AllowedOrigin: browserSmokeOrigin(t, server.URL),
			Actions:       []FlowAction{{Kind: FlowActionWaitFor, Role: "status", Name: "Never"}},
		})
		done <- runErr
	}()
	select {
	case <-requestStarted:
		cancel()
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("browser did not start the held request")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunFlow() returned nil after cancellation")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunFlow() did not return after cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("browser cancellation did not close the held HTTP request")
	}
	entries, err := os.ReadDir(profileRoot)
	if err != nil {
		t.Fatalf("read profile root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("browser flow left profile entries after cancellation: %v", entries)
	}
}

func TestChromiumBrowserFlowSmokeActionCancellationIsNotTargetNotFound(t *testing.T) {
	executable := requireBrowserSmoke(t)
	pageServed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><body><div role="status" aria-label="Present">Present</div></body></html>`)
		pageServed <- struct{}{}
	}))
	defer server.Close()
	inspector, err := New(Config{ExecutablePath: executable, Timeout: 30 * time.Second, AllowPrivateIPs: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := inspector.RunFlow(ctx, FlowRequest{
			URL:           server.URL,
			AllowedOrigin: browserSmokeOrigin(t, server.URL),
			Actions:       []FlowAction{{Kind: FlowActionWaitFor, Role: "status", Name: "Never"}},
		})
		done <- runErr
	}()
	select {
	case <-pageServed:
		time.Sleep(500 * time.Millisecond)
		cancel()
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("browser did not load the action-cancellation fixture")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrInspectionFailed) || errors.Is(err, ErrFlowTargetNotFound) {
			t.Fatalf("RunFlow() cancellation error = %v, want generic inspection failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunFlow() did not return after action cancellation")
	}
}

func flowEvidenceContains(nodes []AccessibilityNode, role, name string) bool {
	for _, node := range nodes {
		if node.Role == role && node.Name == name {
			return true
		}
	}
	return false
}
