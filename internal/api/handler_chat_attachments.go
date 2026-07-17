package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/internal/chatattachments"
	_ "golang.org/x/image/webp"
)

const (
	maxChatImageAttachmentBytes              = int64(5 << 20)
	maxChatImageUploadBodyBytes              = maxChatImageAttachmentBytes + int64(1<<20)
	maxChatImageDimension                    = 8000
	maxChatImagePixels                       = int64(16_000_000)
	maxChatAttachmentNameBytes               = 128
	maxConcurrentChatImageUploads            = 2
	chatImageUploadRetryAfter                = 1
	defaultChatImageUploadReadTimeout        = 60 * time.Second
	maxConcurrentChatAttachmentContentReads  = 4
	chatAttachmentContentRetryAfter          = 1
	defaultChatAttachmentContentWriteTimeout = 30 * time.Second

	chatAttachmentUploadFailureMessage   = "failed to store chat attachment"
	chatAttachmentContentFailureMessage  = "failed to load chat attachment"
	chatAttachmentDeleteFailureMessage   = "failed to delete chat attachment"
	chatAttachmentClaimFailureMessage    = "failed to prepare chat attachments"
	chatAttachmentFinalizeFailureMessage = "failed to finalize chat attachments"
)

var supportedChatImageFormats = map[string]string{
	"image/jpeg": "jpeg",
	"image/png":  "png",
	"image/webp": "webp",
}

type chatImageUploadAdmission interface {
	TryAcquire() bool
	Release()
}

type chatAttachmentContentAdmission interface {
	TryAcquire() bool
	Release()
}

// The permit spans body read through storage because the bounded upload buffer
// remains resident while the image is decoded and the draft is persisted.
type fixedChatImageUploadAdmission struct {
	permits chan struct{}
}

func newChatImageUploadAdmission(capacity int) *fixedChatImageUploadAdmission {
	if capacity <= 0 {
		panic("chat image upload admission capacity must be positive")
	}
	return &fixedChatImageUploadAdmission{permits: make(chan struct{}, capacity)}
}

func (g *fixedChatImageUploadAdmission) TryAcquire() bool {
	select {
	case g.permits <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *fixedChatImageUploadAdmission) Release() {
	<-g.permits
}

type fixedChatAttachmentContentAdmission struct {
	permits chan struct{}
}

func newChatAttachmentContentAdmission(capacity int) *fixedChatAttachmentContentAdmission {
	if capacity <= 0 {
		panic("chat attachment content admission capacity must be positive")
	}
	return &fixedChatAttachmentContentAdmission{permits: make(chan struct{}, capacity)}
}

func (g *fixedChatAttachmentContentAdmission) TryAcquire() bool {
	select {
	case g.permits <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *fixedChatAttachmentContentAdmission) Release() {
	<-g.permits
}

func (h *Handler) HandleCreateChatAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatImageUploadBodyBytes)
	readTimeout := h.chatImageUploadReadTimeout
	if readTimeout <= 0 {
		readTimeout = defaultChatImageUploadReadTimeout
	}
	bodyRead := startRequestBodyReadDeadline(r.Context(), w, r.Body, readTimeout, r.ProtoMajor)
	defer bodyRead.Finish()
	writeRejected := func(write func()) {
		writeRejectedRequestBodyResponse(w, bodyRead, write)
	}

	sessionID := strings.TrimSpace(r.PathValue("id"))
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	defer lifecycle.release()
	sessionResult, err := h.chatApplication().GetSession(r.Context(), sessionID)
	if err != nil {
		writeRejected(func() {
			if writeChatAppError(w, err) {
				return
			}
			writeChatAttachmentAppError(w, err, chatAttachmentUploadFailureMessage)
		})
		return
	}
	if h.chatImageUploadAdmission == nil || !h.chatImageUploadAdmission.TryAcquire() {
		writeRejected(func() { writeChatImageUploadBusy(w) })
		return
	}
	defer h.chatImageUploadAdmission.Release()

	reader, err := r.MultipartReader()
	if err != nil {
		writeRejected(func() {
			writeChatAttachmentReadError(w, err, bodyRead.Expired(), readTimeout)
		})
		return
	}

	var (
		data              []byte
		filename          string
		declaredMediaType string
		fileSeen          bool
	)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeRejected(func() {
				writeChatAttachmentReadError(w, nextErr, bodyRead.Expired(), readTimeout)
			})
			return
		}
		if fileSeen || part.FormName() != "file" {
			_ = part.Close()
			writeRejected(func() {
				if bodyRead.Expired() {
					writeChatImageUploadTimeout(w, readTimeout)
					return
				}
				WriteError(w, http.StatusUnprocessableEntity, errCodeAttachmentInvalid, "multipart request must contain exactly one file field named file")
			})
			return
		}
		fileSeen = true
		filename = part.FileName()
		declaredMediaType = part.Header.Get("Content-Type")
		data, err = io.ReadAll(io.LimitReader(part, maxChatImageAttachmentBytes+1))
		closeErr := part.Close()
		if err != nil {
			writeRejected(func() {
				writeChatAttachmentReadError(w, err, bodyRead.Expired(), readTimeout)
			})
			return
		}
		if closeErr != nil {
			writeRejected(func() {
				writeChatAttachmentReadError(w, closeErr, bodyRead.Expired(), readTimeout)
			})
			return
		}
		if int64(len(data)) > maxChatImageAttachmentBytes {
			writeRejected(func() {
				if bodyRead.Expired() {
					writeChatImageUploadTimeout(w, readTimeout)
					return
				}
				WriteError(w, http.StatusRequestEntityTooLarge, errCodeAttachmentTooLarge, "attachment exceeds the 5 MiB limit")
			})
			return
		}
	}
	// multipart.Reader reports EOF when it reaches the final boundary, not
	// necessarily when the request transport reaches EOF. Drain the wrapped body
	// while the route deadline and MaxBytesReader are still active so an epilogue
	// or an unterminated chunked request cannot escape the upload bounds and leave
	// net/http draining a slow body after the handler returns.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		writeRejected(func() {
			writeChatAttachmentReadError(w, err, bodyRead.Expired(), readTimeout)
		})
		return
	}
	if bodyRead.Finish() {
		writeRejected(func() { writeChatImageUploadTimeout(w, readTimeout) })
		return
	}
	if !fileSeen || len(data) == 0 {
		WriteError(w, http.StatusUnprocessableEntity, errCodeAttachmentInvalid, "attachment is required")
		return
	}

	mediaType, err := validateChatAttachmentUpload(data, declaredMediaType, isExternalChatSession(sessionResult.Session))
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, errCodeAttachmentUnsupported, err.Error())
		return
	}
	filename = normalizeChatAttachmentFilename(filename, mediaType)
	digest := sha256.Sum256(data)
	releaseOperation, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
	if !accepted {
		writeChatSessionStopping(w)
		return
	}
	defer releaseOperation()
	attachment, err := h.chatApplication().CreateAttachment(r.Context(), chatapp.CreateAttachmentCommand{
		Attachment: chatattachments.StoredAttachment{
			Attachment: chatattachments.Attachment{
				ID:        newChatID("att"),
				SessionID: sessionResult.Session.ID,
				Filename:  filename,
				MediaType: mediaType,
				SizeBytes: int64(len(data)),
				SHA256:    hex.EncodeToString(digest[:]),
				CreatedAt: time.Now().UTC(),
			},
			Data: data,
		},
	})
	if err != nil {
		writeChatAttachmentAppError(w, err, chatAttachmentUploadFailureMessage)
		return
	}
	WriteJSON(w, http.StatusCreated, ChatAttachmentResponse{
		Object: "chat_attachment",
		Data:   renderChatAttachment(attachment.Attachment),
	})
}

func writeChatImageUploadBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(chatImageUploadRetryAfter))
	WriteErrorDetails(w, http.StatusTooManyRequests, errCodeAttachmentUploadBusy, "attachment upload validation capacity is busy", ErrorDetails{
		Fields: map[string]any{
			"max_concurrent_uploads": maxConcurrentChatImageUploads,
			"retry_after_seconds":    chatImageUploadRetryAfter,
		},
	})
}

func writeChatImageUploadTimeout(w http.ResponseWriter, timeout time.Duration) {
	WriteErrorDetails(w, http.StatusRequestTimeout, errCodeAttachmentUploadTimeout, "attachment upload body read timed out", ErrorDetails{
		Fields: map[string]any{
			"read_timeout_ms": timeout.Milliseconds(),
		},
	})
}

func writeChatAttachmentContentBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(chatAttachmentContentRetryAfter))
	WriteErrorDetails(w, http.StatusTooManyRequests, errCodeAttachmentContentBusy, "attachment download capacity is busy", ErrorDetails{
		Fields: map[string]any{
			"max_concurrent_downloads": maxConcurrentChatAttachmentContentReads,
			"retry_after_seconds":      chatAttachmentContentRetryAfter,
		},
	})
}

func (h *Handler) HandleChatAttachmentContent(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	defer lifecycle.release()
	if h.chatAttachmentContentAdmission == nil || !h.chatAttachmentContentAdmission.TryAcquire() {
		writeChatAttachmentContentBusy(w)
		return
	}
	defer h.chatAttachmentContentAdmission.Release()
	releaseOperation, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
	if !accepted {
		writeChatSessionStopping(w)
		return
	}
	defer releaseOperation()

	attachment, err := h.chatApplication().GetAttachment(r.Context(), chatapp.AttachmentCommand{
		SessionID:    sessionID,
		AttachmentID: r.PathValue("attachment_id"),
	})
	if err != nil {
		writeChatAttachmentAppError(w, err, chatAttachmentContentFailureMessage)
		return
	}
	if err := validateStoredChatAttachment(attachment); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "stored chat attachment failed integrity validation")
		return
	}
	writeTimeout := h.chatAttachmentContentWriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultChatAttachmentContentWriteTimeout
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		w.Header().Set("Connection", "close")
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, chatAttachmentContentFailureMessage)
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	w.Header().Set("Content-Type", attachment.MediaType)
	w.Header().Set("Content-Length", formatInt64(attachment.SizeBytes))
	disposition := "attachment"
	if _, image := supportedChatImageFormats[attachment.MediaType]; image {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(attachment.Data)
}

func (h *Handler) HandleDeleteChatAttachment(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	lifecycle := h.agentChatLive.snapshotLifecycle(sessionID)
	defer lifecycle.release()
	releaseOperation, accepted := h.agentChatLive.beginLifecycleOperation(lifecycle)
	if !accepted {
		writeChatSessionStopping(w)
		return
	}
	defer releaseOperation()

	err := h.chatApplication().DeleteAttachment(r.Context(), chatapp.AttachmentCommand{
		SessionID:    sessionID,
		AttachmentID: r.PathValue("attachment_id"),
	})
	if err != nil {
		writeChatAttachmentAppError(w, err, chatAttachmentDeleteFailureMessage)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateChatImage(data []byte, declared string) (string, error) {
	detected := http.DetectContentType(data)
	expectedFormat, ok := supportedChatImageFormats[detected]
	if !ok {
		return "", errors.New("only PNG, JPEG, and WebP image attachments are supported")
	}
	if declared = strings.TrimSpace(declared); declared != "" {
		declaredType, _, err := mime.ParseMediaType(declared)
		if err != nil {
			return "", errors.New("image attachment has an invalid content type")
		}
		if declaredType != "application/octet-stream" && declaredType != detected {
			return "", errors.New("image attachment content does not match its declared content type")
		}
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != expectedFormat {
		return "", errors.New("image attachment is malformed")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxChatImageDimension || config.Height > maxChatImageDimension {
		return "", errors.New("image attachment dimensions must be between 1 and 8000 pixels")
	}
	if int64(config.Width)*int64(config.Height) > maxChatImagePixels {
		return "", errors.New("image attachment exceeds the 16 megapixel limit")
	}
	if _, decodedFormat, err := image.Decode(bytes.NewReader(data)); err != nil || decodedFormat != expectedFormat {
		return "", errors.New("image attachment is malformed")
	}
	return detected, nil
}

func validateChatAttachmentUpload(data []byte, declared string, external bool) (string, error) {
	if !external {
		return validateChatImage(data, declared)
	}
	detected, _, detectedErr := mime.ParseMediaType(http.DetectContentType(data))
	if detectedErr != nil || detected == "" {
		detected = "application/octet-stream"
	}
	declared = strings.TrimSpace(declared)
	declaredType := ""
	if declared != "" {
		var err error
		declaredType, _, err = mime.ParseMediaType(declared)
		if err != nil || declaredType == "" || len(declaredType) > 128 {
			return "", errors.New("attachment has an invalid content type")
		}
		declaredType = strings.ToLower(declaredType)
	}
	_, detectedImage := supportedChatImageFormats[detected]
	_, declaredImage := supportedChatImageFormats[declaredType]
	if detectedImage {
		// External Agent inputs are opaque files first. Promote a detected
		// supported raster to image handling only after it passes the same
		// bounded full decode used by Hecate direct-model turns. Malformed or
		// over-dimension image-like bytes remain sendable as an inert file.
		if imageType, err := validateChatImage(data, ""); err == nil {
			return imageType, nil
		}
		return "application/octet-stream", nil
	}
	if declaredImage {
		// A browser or caller may label a diagnostic/corrupt file as an image.
		// Do not reject an External Agent file or preserve an unsafe inline
		// image type when its bytes do not identify a supported raster.
		return strings.ToLower(detected), nil
	}
	if declaredType != "" && declaredType != "application/octet-stream" {
		return declaredType, nil
	}
	return strings.ToLower(detected), nil
}

func normalizeChatAttachmentFilename(filename, mediaType string) string {
	filename = strings.ToValidUTF8(filename, "")
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	filename = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, filename)
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." {
		filename = defaultChatAttachmentFilename(mediaType)
	}
	for len(filename) > maxChatAttachmentNameBytes {
		_, size := utf8.DecodeLastRuneInString(filename)
		filename = filename[:len(filename)-size]
	}
	if filename == "" {
		return defaultChatAttachmentFilename(mediaType)
	}
	return filename
}

func defaultChatAttachmentFilename(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "image.jpg"
	case "image/png":
		return "image.png"
	case "image/webp":
		return "image.webp"
	case "application/json":
		return "attachment.json"
	case "text/plain":
		return "attachment.txt"
	default:
		return "attachment.bin"
	}
}

func writeChatAttachmentReadError(w http.ResponseWriter, err error, timedOut bool, timeout time.Duration) {
	if timedOut {
		writeChatImageUploadTimeout(w, timeout)
		return
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		WriteError(w, http.StatusRequestEntityTooLarge, errCodeAttachmentTooLarge, "attachment upload request exceeds the allowed size")
		return
	}
	WriteError(w, http.StatusUnprocessableEntity, errCodeAttachmentInvalid, "invalid multipart attachment upload")
}

func writeChatAttachmentAppError(w http.ResponseWriter, err error, internalFailureMessage string) {
	var rollbackErr *chatapp.AttachmentRollbackError
	switch {
	case errors.As(err, &rollbackErr):
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, chatapp.ErrAttachmentRollback.Error())
	case errors.Is(err, chatapp.ErrAttachmentNotFound), errors.Is(err, chatapp.ErrSessionNotFound):
		WriteError(w, http.StatusNotFound, errCodeAttachmentNotFound, "chat attachment not found")
	case errors.Is(err, chatapp.ErrAttachmentInUse):
		WriteError(w, http.StatusConflict, errCodeAttachmentInUse, err.Error())
	case errors.Is(err, chatapp.ErrAttachmentDraftQuota):
		WriteErrorDetails(w, http.StatusConflict, errCodeAttachmentDraftQuota, err.Error(), ErrorDetails{Fields: map[string]any{
			"max_draft_attachments": chatattachments.MaxDraftAttachmentsPerSession,
			"max_draft_bytes":       chatattachments.MaxDraftBytesPerSession,
			"draft_ttl_seconds":     int64(chatattachments.DraftTTL / time.Second),
		}})
	case errors.Is(err, chatapp.ErrAttachmentSessionQuota):
		WriteErrorDetails(w, http.StatusConflict, errCodeAttachmentSessionQuota, err.Error(), ErrorDetails{Fields: map[string]any{
			"max_session_attachment_bytes": chatattachments.MaxStoredBytesPerSession,
		}})
	case errors.Is(err, chatapp.ErrAttachmentTotalQuota):
		limit := chatattachments.MaxDurableStoredBytesTotal
		var quota chatapp.AttachmentTotalQuotaError
		if errors.As(err, &quota) && quota.LimitBytes > 0 {
			limit = quota.LimitBytes
		}
		WriteErrorDetails(w, http.StatusConflict, errCodeAttachmentTotalQuota, err.Error(), ErrorDetails{Fields: map[string]any{
			"max_total_attachment_bytes": limit,
		}})
	case errors.Is(err, chatapp.ErrAttachmentMessageBytes):
		WriteErrorDetails(w, http.StatusRequestEntityTooLarge, errCodeAttachmentTooLarge, err.Error(), ErrorDetails{
			UserMessage:    "The selected attachments are too large to send together.",
			OperatorAction: "Keep the combined files at or below 12 MiB, or send them in separate messages.",
			Fields: map[string]any{
				"max_message_attachment_bytes": chatapp.MaxMessageAttachmentBytes,
			},
		})
	case errors.Is(err, chatapp.ErrTooManyAttachments), errors.Is(err, chatapp.ErrDuplicateAttachmentID), errors.Is(err, chatapp.ErrAttachmentIDRequired):
		WriteError(w, http.StatusUnprocessableEntity, errCodeAttachmentInvalid, err.Error())
	case chatapp.IsValidationError(err):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, internalFailureMessage)
	}
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
