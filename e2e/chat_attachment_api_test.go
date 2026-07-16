//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
)

func TestChatAttachmentPublicAPIE2E(t *testing.T) {
	baseURL := gatewayServer(t, "HECATE_BACKEND=sqlite")
	first := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", `{
		"agent_id": "hecate",
		"title": "attachment owner"
	}`)
	second := postJSONDecode[e2eChatSessionResponse](t, baseURL+"/hecate/v1/chat/sessions", `{
		"agent_id": "hecate",
		"title": "attachment isolation"
	}`)

	imageBytes := e2eAttachmentPNG(t)
	upload, uploadBody := e2eUploadChatAttachment(t, baseURL, first.Data.ID, "pixel.png", imageBytes)
	if upload.Object != "chat_attachment" {
		t.Fatalf("upload object = %q, want chat_attachment", upload.Object)
	}
	digest := sha256.Sum256(imageBytes)
	wantContentURL := "/hecate/v1/chat/sessions/" + first.Data.ID + "/attachments/" + upload.Data.ID + "/content"
	if upload.Data.ID == "" || upload.Data.SessionID != first.Data.ID || upload.Data.Filename != "pixel.png" ||
		upload.Data.MediaType != "image/png" || upload.Data.SizeBytes != int64(len(imageBytes)) ||
		upload.Data.SHA256 != hex.EncodeToString(digest[:]) || upload.Data.ContentURL != wantContentURL {
		t.Fatalf("upload metadata = %+v", upload.Data)
	}
	e2eAssertAttachmentBodyAbsent(t, uploadBody, imageBytes)

	sessionResponse := e2eGetRaw(t, baseURL+"/hecate/v1/chat/sessions/"+first.Data.ID, http.StatusOK)
	e2eAssertAttachmentBodyAbsent(t, sessionResponse, imageBytes)
	var session struct {
		Data struct {
			ID       string            `json:"id"`
			Messages []json.RawMessage `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(sessionResponse, &session); err != nil {
		t.Fatalf("decode session response: %v; body=%s", err, sessionResponse)
	}
	if session.Data.ID != first.Data.ID || len(session.Data.Messages) != 0 {
		t.Fatalf("draft attachment changed transcript session = %+v", session.Data)
	}

	isolatedURL := baseURL + "/hecate/v1/chat/sessions/" + second.Data.ID + "/attachments/" + upload.Data.ID + "/content"
	isolationBody := e2eGetRaw(t, isolatedURL, http.StatusNotFound)
	if !bytes.Contains(isolationBody, []byte(`"type":"chat.attachment_not_found"`)) {
		t.Fatalf("cross-session content error = %s", isolationBody)
	}
	e2eDeleteChatAttachment(t, baseURL, second.Data.ID, upload.Data.ID, http.StatusNotFound)

	contentURL := baseURL + upload.Data.ContentURL
	contentResponse, err := http.Get(contentURL) //nolint:noctx
	if err != nil {
		t.Fatalf("GET attachment content: %v", err)
	}
	contentBody, err := io.ReadAll(contentResponse.Body)
	contentResponse.Body.Close()
	if err != nil {
		t.Fatalf("read attachment content: %v", err)
	}
	if contentResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET attachment content status = %d, want 200; body=%s", contentResponse.StatusCode, contentBody)
	}
	if !bytes.Equal(contentBody, imageBytes) {
		t.Fatalf("attachment content bytes differ: got %d bytes, want %d", len(contentBody), len(imageBytes))
	}
	if got := contentResponse.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type = %q, want image/png", got)
	}
	if got := contentResponse.Header.Get("Content-Length"); got != strconv.Itoa(len(imageBytes)) {
		t.Fatalf("content length = %q, want %d", got, len(imageBytes))
	}
	if got := contentResponse.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := contentResponse.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
	if got := contentResponse.Header.Get("Content-Disposition"); !strings.Contains(got, "inline") || !strings.Contains(got, "pixel.png") {
		t.Fatalf("Content-Disposition = %q, want inline pixel.png", got)
	}

	e2eDeleteChatAttachment(t, baseURL, first.Data.ID, upload.Data.ID, http.StatusNoContent)
	_ = e2eGetRaw(t, contentURL, http.StatusNotFound)
}

type e2eChatAttachmentResponse struct {
	Object string                    `json:"object"`
	Data   e2eChatAttachmentMetadata `json:"data"`
}

type e2eChatAttachmentMetadata struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	ContentURL string `json:"content_url"`
}

func e2eUploadChatAttachment(t *testing.T, baseURL, sessionID, filename string, data []byte) (e2eChatAttachmentResponse, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart image part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart image: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart image: %v", err)
	}

	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseURL+"/hecate/v1/chat/sessions/"+sessionID+"/attachments", &body)
	if err != nil {
		t.Fatalf("new attachment upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("upload attachment: %v", err)
	}
	responseBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read attachment upload response: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201; body=%s", response.StatusCode, responseBody)
	}
	var decoded e2eChatAttachmentResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		t.Fatalf("decode attachment upload response: %v; body=%s", err, responseBody)
	}
	return decoded, responseBody
}

func e2eDeleteChatAttachment(t *testing.T, baseURL, sessionID, attachmentID string, wantStatus int) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		baseURL+"/hecate/v1/chat/sessions/"+sessionID+"/attachments/"+attachmentID, nil)
	if err != nil {
		t.Fatalf("new attachment delete request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("DELETE attachment: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read attachment delete response: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("DELETE attachment for session %s status = %d, want %d; body=%s", sessionID, response.StatusCode, wantStatus, body)
	}
}

func e2eGetRaw(t *testing.T, url string, wantStatus int) []byte {
	t.Helper()
	response, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read GET %s response: %v", url, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d; body=%s", url, response.StatusCode, wantStatus, body)
	}
	return body
}

func e2eAttachmentPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	canvas.Set(0, 0, color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff})
	canvas.Set(1, 0, color.NRGBA{R: 0xa0, G: 0xb0, B: 0xc0, A: 0xff})
	var data bytes.Buffer
	if err := png.Encode(&data, canvas); err != nil {
		t.Fatalf("encode PNG fixture: %v", err)
	}
	return data.Bytes()
}

func e2eAssertAttachmentBodyAbsent(t *testing.T, payload, imageBytes []byte) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	if bytes.Contains(payload, []byte(encoded)) || bytes.Contains(payload, []byte("data:image/")) || bytes.Contains(payload, []byte(`"data_base64"`)) {
		t.Fatalf("attachment response exposed binary body: %s", payload)
	}
}
