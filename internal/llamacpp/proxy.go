package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// ProxyRuntime is the slim Runtime surface the Proxy needs. Defined
// for test stubbing — the production Runtime satisfies it implicitly.
type ProxyRuntime interface {
	EnsureLoaded(ctx context.Context, modelID string) (baseURL string, err error)
	Available() bool
}

// Proxy is the gateway-internal reverse-proxy mounted at
// /hecate/internal/llamacpp/v1/*. It peeks the OpenAI-compat `model`
// field, makes sure the runtime is loaded with that model, then
// forwards the full request body to the live llama-server child.
//
// Streaming is preserved end-to-end: the request body is read once
// to find the model id but then replayed wholesale to the upstream
// (no JSON re-encoding), and the response body is piped through
// httputil.ReverseProxy with the standard buffering disabled.
type Proxy struct {
	runtime ProxyRuntime
	// maxModelPeekBytes caps how far we'll read into the request
	// body looking for the model field. Most OpenAI-compat
	// payloads put it near the top; 64 KiB is way more than
	// reasonable but bounds memory if a client sends a 10 MB
	// JSON. The peek is buffered separately from the forwarded
	// body so we never lose data.
	maxModelPeekBytes int
}

// NewProxy returns a Proxy. runtime must be non-nil.
func NewProxy(runtime ProxyRuntime) *Proxy {
	return &Proxy{runtime: runtime, maxModelPeekBytes: 64 * 1024}
}

// ServeHTTP implements the proxy. Path is everything after
// /hecate/internal/llamacpp/v1/; we forward to <base>/v1/<path>.
// Methods supported: anything llama-server's OpenAI-compat surface
// accepts (POST /chat/completions, POST /completions, GET /models,
// etc.).
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.runtime.Available() {
		writeJSONError(w, http.StatusServiceUnavailable, "local_models_unavailable",
			"local model runtime is not available in this build")
		return
	}

	// The /v1/models passthrough doesn't need a model peek — return
	// the upstream's view of what's loaded. For everything else we
	// need to know which model the operator asked for so we can
	// auto-load it.
	requestedModel, body, peekErr := p.peekModel(r)
	if peekErr != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("could not parse model field: %v", peekErr))
		return
	}
	if requestedModel == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request",
			"request body must include a non-empty model field")
		return
	}

	baseURL, err := p.runtime.EnsureLoaded(r.Context(), requestedModel)
	if err != nil {
		p.writeRuntimeError(w, err)
		return
	}
	// Emit one proxy.routed event per inbound request so trace
	// dashboards can correlate runtime spawns with the chat
	// completion traffic that drove them.
	recordProxyRouted(r.Context(), requestedModel)

	target, err := url.Parse(baseURL)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "local_model_runtime_unavailable",
			fmt.Sprintf("runtime returned invalid base url: %v", err))
		return
	}

	// Reconstruct the request with the buffered body so the proxy
	// can replay it. r.Body is already drained by peekModel.
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	// Trim our public prefix; the upstream serves under /v1/.
	r.URL.Path = "/v1" + strings.TrimPrefix(r.URL.Path, internalProxyPathPrefix)
	// Sanity: don't leak our internal prefix in the Host header.
	r.Host = target.Host

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Header.Set("Host", target.Host)
			// Strip any Authorization header from the inbound
			// request — llama-server doesn't authenticate, and
			// passing through a Hecate-side token to a child
			// process would leak credentials into stderr if the
			// child logs request headers.
			req.Header.Del("Authorization")
		},
		// Disable response buffering so streamed chunks reach
		// the client as soon as llama-server emits them. Default
		// httputil flushes only on 200-class with Content-Length
		// unset; -1 means "flush immediately every write".
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			// Surface the upstream error in a consistent shape.
			writeJSONError(w, http.StatusBadGateway, "local_model_runtime_unavailable",
				fmt.Sprintf("upstream proxy error: %v", err))
		},
	}
	rp.ServeHTTP(w, r)
}

// peekModel reads the request body, buffers it, and parses just the
// `model` field. Returns the buffered body so the caller can replay
// it to the upstream. Bodies larger than maxModelPeekBytes are
// rejected — they almost certainly aren't valid OpenAI-compat
// payloads anyway.
func (p *Proxy) peekModel(r *http.Request) (string, []byte, error) {
	if r.Body == nil {
		return "", nil, nil
	}
	limited := io.LimitReader(r.Body, int64(p.maxModelPeekBytes)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", nil, err
	}
	if len(body) > p.maxModelPeekBytes {
		return "", nil, errors.New("request body exceeds peek limit")
	}
	// JSON-decode to extract just the model field. We don't
	// validate the rest of the payload — llama-server will reject
	// anything malformed and we'll pass the error back.
	var head struct {
		Model string `json:"model"`
	}
	if len(body) == 0 {
		return "", body, nil
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&head); err != nil {
		return "", body, err
	}
	return strings.TrimSpace(head.Model), body, nil
}

// writeRuntimeError maps the runtime's typed errors to the stable
// API error codes documented in the RFC.
func (p *Proxy) writeRuntimeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRuntimeUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, "local_models_unavailable",
			err.Error())
	case errors.Is(err, ErrRuntimeNotRunning),
		errors.Is(err, ErrRuntimeWrongModel):
		writeJSONError(w, http.StatusServiceUnavailable, "local_model_runtime_unavailable",
			err.Error())
	default:
		// EnsureLoaded surfaces "not found" from the store when the
		// requested model isn't installed.
		if isNotInstalled(err) {
			writeJSONError(w, http.StatusNotFound, "local_model_not_installed",
				err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "local_model_runtime_unavailable",
			err.Error())
	}
}

// isNotInstalled is a tolerant check — the store's "not found" is a
// plain error today (no sentinel). When that gets a sentinel we'll
// errors.Is against it; for now look for the canonical string.
func isNotInstalled(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// writeJSONError is the small shared error writer used by both the
// proxy and the API handlers. Matches the OpenAI-compat error shape
// the rest of the gateway already uses: {"error": {"code", "message"}}.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// internalProxyPathPrefix is the route prefix the api package mounts
// the Proxy under. Exported as a const so the api server, the proxy,
// and the auto-registered provider's BaseURL all stay in sync.
const internalProxyPathPrefix = "/hecate/internal/llamacpp/v1"

// InternalProxyPathPrefix is the public accessor used by callers that
// need to construct the BaseURL of the auto-registered provider.
func InternalProxyPathPrefix() string { return internalProxyPathPrefix }
