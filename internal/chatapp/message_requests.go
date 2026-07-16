package chatapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
)

const MaxClientRequestIDBytes = 128

const messageRequestRenewTimeout = 3 * time.Second

type BeginMessageRequestCommand struct {
	SessionID       string
	ClientRequestID string
	Fingerprint     chat.MessageRequestFingerprint
}

type BeginMessageRequestResult struct {
	Reservation        *MessageRequestReservation
	Session            chat.Session
	Replay             bool
	CommittedMessageID string
}

type AppendUserMessageCommand struct {
	SessionID   string
	Reservation *MessageRequestReservation
	Message     chat.Message
}

// MessageRequestReservation owns the pre-commit heartbeat for one keyed chat
// message. HTTP callers can carry this opaque handle across admission work, but
// only chatapp controls renewal timing and lease failure state.
type MessageRequestReservation struct {
	messages     MessageStore
	lease        chat.MessageRequestLease
	renewEvery   time.Duration
	lifecycleCtx context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	stopOnce     sync.Once

	mu       sync.Mutex
	renewErr error
}

func (app *Application) BeginMessageRequest(ctx context.Context, cmd BeginMessageRequestCommand) (*BeginMessageRequestResult, error) {
	if app == nil || app.messages == nil {
		return nil, ErrStoreNotConfigured
	}
	sessionID := strings.TrimSpace(cmd.SessionID)
	if sessionID == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	clientRequestID := cmd.ClientRequestID
	if !validClientRequestID(clientRequestID) {
		return nil, Validation(ErrClientRequestIDInvalid)
	}
	claim, err := app.messages.ClaimMessageRequest(ctx, sessionID, clientRequestID, cmd.Fingerprint)
	if err != nil {
		return nil, err
	}
	if !claim.Replay && claim.Lease.Empty() {
		return nil, chat.ErrMessageRequestLeaseInvalid
	}
	result := &BeginMessageRequestResult{
		Session:            claim.Session,
		Replay:             claim.Replay,
		CommittedMessageID: claim.CommittedMessageID,
	}
	if !claim.Lease.Empty() {
		result.Reservation = newMessageRequestReservation(ctx, app.messages, claim.Lease, app.messageRequestRenewEvery)
	}
	return result, nil
}

func (app *Application) AppendUserMessage(ctx context.Context, cmd AppendUserMessageCommand) (*SessionResult, error) {
	if app == nil || app.messages == nil {
		return nil, ErrStoreNotConfigured
	}
	if strings.TrimSpace(cmd.SessionID) == "" {
		return nil, Validation(ErrSessionIDRequired)
	}
	if cmd.Message.Role != "user" {
		return nil, Validation(ErrUserMessageRequired)
	}
	var (
		session chat.Session
		err     error
	)
	if cmd.Reservation == nil {
		session, err = app.messages.AppendMessage(ctx, cmd.SessionID, cmd.Message)
	} else {
		if cmd.Reservation.lease.SessionID != cmd.SessionID {
			return nil, chat.ErrMessageRequestLeaseInvalid
		}
		session, err = cmd.Reservation.commit(ctx, cmd.Message)
	}
	if err != nil {
		return nil, err
	}
	return &SessionResult{Session: session}, nil
}

func (app *Application) ReleaseMessageRequest(ctx context.Context, reservation *MessageRequestReservation) error {
	if reservation == nil {
		return nil
	}
	if app == nil || app.messages == nil {
		return ErrStoreNotConfigured
	}
	return reservation.release(ctx)
}

func newMessageRequestReservation(ctx context.Context, messages MessageStore, lease chat.MessageRequestLease, renewEvery time.Duration) *MessageRequestReservation {
	lifecycleCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	reservation := &MessageRequestReservation{
		messages:     messages,
		lease:        lease,
		renewEvery:   renewEvery,
		lifecycleCtx: lifecycleCtx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	go reservation.renewLoop()
	return reservation
}

func (reservation *MessageRequestReservation) renewLoop() {
	defer close(reservation.done)
	ticker := time.NewTicker(reservation.renewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-reservation.lifecycleCtx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(reservation.lifecycleCtx, messageRequestRenewTimeout)
			err := reservation.messages.RenewMessageRequest(renewCtx, chat.RenewMessageRequestRequest{Lease: reservation.lease})
			cancel()
			if err == nil {
				continue
			}
			// A caller stopping the reservation cancels an in-flight refresh. Drop
			// only that cancellation result; an authoritative store error such as
			// lost ownership must survive even when stop races with its return.
			if reservation.lifecycleCtx.Err() != nil && errors.Is(err, context.Canceled) {
				return
			}
			reservation.mu.Lock()
			if reservation.renewErr == nil {
				reservation.renewErr = err
			}
			reservation.mu.Unlock()
			reservation.cancel()
			return
		}
	}
}

func (reservation *MessageRequestReservation) stop() error {
	reservation.stopOnce.Do(reservation.cancel)
	<-reservation.done
	reservation.mu.Lock()
	defer reservation.mu.Unlock()
	return reservation.renewErr
}

func (reservation *MessageRequestReservation) commit(ctx context.Context, message chat.Message) (chat.Session, error) {
	if err := reservation.stop(); err != nil {
		return chat.Session{}, err
	}
	// A final conditional refresh closes the ticker-to-commit gap and proves
	// ownership immediately before the atomic user-row/key write.
	if err := reservation.messages.RenewMessageRequest(ctx, chat.RenewMessageRequestRequest{Lease: reservation.lease}); err != nil {
		return chat.Session{}, err
	}
	return reservation.messages.CommitMessageRequest(ctx, reservation.lease, message)
}

func (reservation *MessageRequestReservation) release(ctx context.Context) error {
	renewErr := reservation.stop()
	releaseErr := reservation.messages.ReleaseMessageRequest(ctx, reservation.lease)
	return errors.Join(renewErr, releaseErr)
}

func validClientRequestID(value string) bool {
	if value == "" || len(value) > MaxClientRequestIDBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			continue
		}
		switch ch {
		case '.', '_', ':', '-':
			continue
		default:
			return false
		}
	}
	return true
}
