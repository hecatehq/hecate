package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
)

type chatMessageRequestGuard struct {
	app                *chatapp.Application
	reservation        *chatapp.MessageRequestReservation
	keyed              bool
	committedMessageID string
}

func beginChatMessageRequest(ctx context.Context, app *chatapp.Application, sessionID string, req CreateChatMessageRequest) (*chatMessageRequestGuard, *chatapp.BeginMessageRequestResult, error) {
	guard := &chatMessageRequestGuard{app: app}
	if req.ClientRequestID == "" {
		return guard, nil, nil
	}
	guard.keyed = true
	fingerprint, err := createChatMessageFingerprint(req)
	if err != nil {
		return nil, nil, err
	}
	result, err := app.BeginMessageRequest(ctx, chatapp.BeginMessageRequestCommand{
		SessionID:       sessionID,
		ClientRequestID: req.ClientRequestID,
		Fingerprint:     fingerprint,
	})
	if err != nil {
		return nil, nil, err
	}
	guard.reservation = result.Reservation
	return guard, result, nil
}

func createChatMessageFingerprint(req CreateChatMessageRequest) (chat.MessageRequestFingerprint, error) {
	req.ClientRequestID = ""
	encoded, err := json.Marshal(req)
	if err != nil {
		return chat.MessageRequestFingerprint{}, err
	}
	return sha256.Sum256(encoded), nil
}

func (guard *chatMessageRequestGuard) appendUserMessage(ctx context.Context, sessionID string, message chat.Message) (chat.Session, error) {
	appendCtx := ctx
	appendCancel := func() {}
	if guard.keyed {
		// Once a keyed request owns its durable lease, a browser disconnect
		// cannot safely turn a successful atomic user-row commit into an
		// apparent failure: replay would observe the committed key and must not
		// dispatch the backing turn again. Unkeyed appends retain ordinary
		// request cancellation semantics.
		appendCtx, appendCancel = newAgentChatPersistenceContext(ctx)
	}
	defer appendCancel()
	result, err := guard.app.AppendUserMessage(appendCtx, chatapp.AppendUserMessageCommand{
		SessionID:   sessionID,
		Reservation: guard.reservation,
		Message:     message,
	})
	if err != nil {
		return chat.Session{}, err
	}
	guard.reservation = nil
	guard.committedMessageID = message.ID
	return result.Session, nil
}

func (guard *chatMessageRequestGuard) responseMetadata(replay bool, committedMessageID string) *ChatMessageRequestResponseItem {
	if guard == nil || !guard.keyed {
		return nil
	}
	if committedMessageID == "" {
		committedMessageID = guard.committedMessageID
	}
	return &ChatMessageRequestResponseItem{
		Replay:             replay,
		CommittedMessageID: committedMessageID,
	}
}

func (guard *chatMessageRequestGuard) release(ctx context.Context) error {
	if guard == nil || guard.reservation == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return guard.app.ReleaseMessageRequest(cleanupCtx, guard.reservation)
}

func writeChatMessageRequestError(w http.ResponseWriter, err error) bool {
	if writeChatAppError(w, err) {
		return true
	}
	if errors.Is(err, chat.ErrMessageRequestPayloadConflict) {
		WriteError(w, http.StatusConflict, errCodeClientRequestConflict, chat.ErrMessageRequestPayloadConflict.Error())
		return true
	}
	return false
}
