package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	maxInferenceRequestBodyBytes           = int64(32 << 20)
	defaultInferenceRequestBodyReadTimeout = 60 * time.Second
)

type inferenceErrorEnvelope uint8

const (
	inferenceErrorOpenAI inferenceErrorEnvelope = iota
	inferenceErrorAnthropic
)

// inferenceBodyReadGuard keeps the route-local read deadline alive until the
// request body has either been consumed or explicitly closed. This matters on
// early rejects: net/http may otherwise drain unread request bytes after the
// handler returns, which must not become an unbounded slow-client wait.
type inferenceBodyReadGuard struct {
	body            io.ReadCloser
	timeout         time.Duration
	deadline        time.Time
	controller      *http.ResponseController
	readDeadlineSet bool
	protocolMajor   int
	readCtx         context.Context
	cancel          context.CancelFunc
	stopClose       func() bool
	closeDone       chan struct{}

	finishOnce sync.Once
	timedOut   bool
}

func (h *Handler) beginInferenceBodyRead(w http.ResponseWriter, r *http.Request) *inferenceBodyReadGuard {
	r.Body = http.MaxBytesReader(w, r.Body, maxInferenceRequestBodyBytes)
	readTimeout := h.inferenceRequestBodyReadTimeout
	if readTimeout <= 0 {
		readTimeout = defaultInferenceRequestBodyReadTimeout
	}
	return startRequestBodyReadDeadline(r.Context(), w, r.Body, readTimeout, r.ProtoMajor)
}

func (h *Handler) checkInferenceRateLimit(
	w http.ResponseWriter,
	keyID string,
	bodyRead *inferenceBodyReadGuard,
	envelope inferenceErrorEnvelope,
) bool {
	err := h.consumeRateLimit(w, keyID)
	if err == nil {
		return true
	}
	writeRejectedInferenceResponse(w, bodyRead, func() {
		writeInferenceError(w, envelope, http.StatusTooManyRequests, errCodeRateLimitExceeded, err.Error(), ErrorDetails{})
	})
	return false
}

// decodeInferenceJSON bounds provider-compatible request bodies before JSON
// decoding. Rich-content clients may inline base64 images, so the encoded
// transport limit intentionally exceeds Hecate Chat's retained-image limits
// without leaving compatibility ingress open to unbounded allocation or a
// connection that can trickle a body indefinitely.
func (h *Handler) decodeInferenceJSON(
	w http.ResponseWriter,
	r *http.Request,
	v any,
	bodyRead *inferenceBodyReadGuard,
	envelope inferenceErrorEnvelope,
) bool {
	if r.ContentLength > maxInferenceRequestBodyBytes {
		writeRejectedInferenceResponse(w, bodyRead, func() {
			writeInferenceRequestTooLarge(w, envelope)
		})
		return false
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(v); err != nil {
		return writeInferenceJSONReadError(w, err, bodyRead, envelope)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return writeInferenceJSONReadError(w, err, bodyRead, envelope)
		}
		writeRejectedInferenceResponse(w, bodyRead, func() {
			if bodyRead.Expired() {
				writeInferenceRequestBodyTimeout(w, bodyRead.Timeout(), envelope)
				return
			}
			writeInferenceError(w, envelope, http.StatusBadRequest, errCodeInvalidRequest, "request body must contain exactly one JSON value", ErrorDetails{})
		})
		return false
	}
	if bodyRead.Finish() {
		writeRejectedInferenceResponse(w, bodyRead, func() {
			writeInferenceRequestBodyTimeout(w, bodyRead.Timeout(), envelope)
		})
		return false
	}
	return true
}

func writeRejectedInferenceResponse(w http.ResponseWriter, bodyRead *inferenceBodyReadGuard, write func()) {
	writeRejectedRequestBodyResponse(w, bodyRead, write)
}

// writeRejectedRequestBodyResponse commits the route-shaped error before it
// aborts unread bytes. HTTP/1 can then expire its connection read deadline to
// prevent net/http from draining a slow body after the handler returns. HTTP/2
// has no connection-close response header and must flush the response first so
// closing the request stream cannot hide the error envelope from the client.
func writeRejectedRequestBodyResponse(w http.ResponseWriter, bodyRead *inferenceBodyReadGuard, write func()) {
	if bodyRead.protocolMajor < 2 {
		w.Header().Set("Connection", "close")
	}
	write()
	if bodyRead.protocolMajor >= 2 {
		_ = bodyRead.controller.Flush()
	}
	bodyRead.CloseAndFinish()
}

func writeInferenceJSONReadError(w http.ResponseWriter, err error, bodyRead *inferenceBodyReadGuard, envelope inferenceErrorEnvelope) bool {
	writeRejectedInferenceResponse(w, bodyRead, func() {
		if bodyRead.Expired() {
			writeInferenceRequestBodyTimeout(w, bodyRead.Timeout(), envelope)
			return
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeInferenceRequestTooLarge(w, envelope)
			return
		}
		writeInferenceError(w, envelope, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON", ErrorDetails{})
	})
	return false
}

func writeInferenceRequestTooLarge(w http.ResponseWriter, envelope inferenceErrorEnvelope) {
	writeInferenceError(w, envelope, http.StatusRequestEntityTooLarge, errCodeRequestTooLarge, "inference request body exceeds the 32 MiB limit", ErrorDetails{})
}

func writeInferenceRequestBodyTimeout(w http.ResponseWriter, timeout time.Duration, envelope inferenceErrorEnvelope) {
	writeInferenceError(w, envelope, http.StatusRequestTimeout, errCodeRequestBodyTimeout, "inference request body read timed out", ErrorDetails{
		Fields: map[string]any{"read_timeout_ms": timeout.Milliseconds()},
	})
}

func writeInferenceError(
	w http.ResponseWriter,
	envelope inferenceErrorEnvelope,
	status int,
	code string,
	message string,
	details ErrorDetails,
) {
	if envelope == inferenceErrorOpenAI {
		WriteErrorDetails(w, status, code, message, details)
		return
	}

	details = enrichErrorDetails(code, details)
	errorObject := map[string]any{
		"type":    anthropicInferenceErrorType(code),
		"message": message,
	}
	if details.UserMessage != "" {
		errorObject["user_message"] = details.UserMessage
	}
	if details.OperatorAction != "" {
		errorObject["operator_action"] = details.OperatorAction
	}
	if details.RequestID != "" {
		errorObject["request_id"] = details.RequestID
	}
	if details.TraceID != "" {
		errorObject["trace_id"] = details.TraceID
	}
	for key, value := range details.Fields {
		if key == "" || !isSafeErrorField(value) || isReservedErrorField(key) {
			continue
		}
		errorObject[key] = value
	}
	WriteJSON(w, status, map[string]any{
		"type":  "error",
		"error": errorObject,
	})
}

func anthropicInferenceErrorType(code string) string {
	switch code {
	case errCodeRateLimitExceeded:
		return "rate_limit_error"
	case errCodeRequestTooLarge:
		return "request_too_large"
	default:
		return "invalid_request_error"
	}
}

func startRequestBodyReadDeadline(
	ctx context.Context,
	w http.ResponseWriter,
	body io.ReadCloser,
	timeout time.Duration,
	protocolMajor int,
) *inferenceBodyReadGuard {
	deadline := time.Now().Add(timeout)
	controller := http.NewResponseController(w)
	// ResponseController.SetReadDeadline is connection-scoped for HTTP/2. Using
	// it for one upload can reset unrelated streams and can prevent this stream's
	// 408 envelope from being delivered. The route timer still closes the
	// request body at the same deadline and unblocks a stalled handler read.
	readDeadlineSet := false
	if protocolMajor < 2 {
		readDeadlineSet = controller.SetReadDeadline(deadline) == nil
	}
	readCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	closeDone := make(chan struct{})
	stopClose := context.AfterFunc(readCtx, func() {
		_ = body.Close()
		close(closeDone)
	})
	return &inferenceBodyReadGuard{
		body:            body,
		timeout:         timeout,
		deadline:        deadline,
		controller:      controller,
		readDeadlineSet: readDeadlineSet,
		protocolMajor:   protocolMajor,
		readCtx:         readCtx,
		cancel:          cancel,
		stopClose:       stopClose,
		closeDone:       closeDone,
	}
}

func (g *inferenceBodyReadGuard) Timeout() time.Duration {
	return g.timeout
}

func (g *inferenceBodyReadGuard) Expired() bool {
	return errors.Is(g.readCtx.Err(), context.DeadlineExceeded) || !time.Now().Before(g.deadline)
}

func (g *inferenceBodyReadGuard) Finish() bool {
	return g.finish(false)
}

func (g *inferenceBodyReadGuard) CloseAndFinish() bool {
	return g.finish(true)
}

func (g *inferenceBodyReadGuard) finish(closeBody bool) bool {
	g.finishOnce.Do(func() {
		stopped := g.stopClose()
		if closeBody && stopped {
			// Abort unread transport bytes immediately on an early reject. The
			// response already carries Connection: close, so preserving an expired
			// read deadline prevents net/http from starting a post-handler drain.
			if g.readDeadlineSet && g.protocolMajor < 2 {
				_ = g.controller.SetReadDeadline(time.Now())
			}
			_ = g.body.Close()
		}
		g.timedOut = g.Expired()
		g.cancel()
		if !stopped {
			<-g.closeDone
		}
		if g.readDeadlineSet && !g.timedOut && !closeBody {
			_ = g.controller.SetReadDeadline(time.Time{})
		}
	})
	return g.timedOut
}
