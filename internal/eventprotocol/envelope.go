package eventprotocol

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

const SchemaVersion = "1"

const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Envelope is the API-facing event shape for agent-runtime event protocol v1.
// Storage can keep its compact taskstate row; clients should consume this shape.
type Envelope struct {
	SchemaVersion string         `json:"schema_version"`
	EventID       string         `json:"event_id"`
	RunID         string         `json:"run_id"`
	TaskID        string         `json:"task_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	Sequence      int64          `json:"sequence"`
	OccurredAt    string         `json:"occurred_at"`
	Type          string         `json:"type"`
	Data          map[string]any `json:"data"`
}

// FromTaskRunEvent converts a persisted task run event into the v1 envelope.
func FromTaskRunEvent(event types.TaskRunEvent) Envelope {
	return Envelope{
		SchemaVersion: SchemaVersion,
		EventID:       eventIDFor(event),
		RunID:         event.RunID,
		TaskID:        event.TaskID,
		Sequence:      event.Sequence,
		OccurredAt:    occurredAt(event.CreatedAt),
		Type:          event.EventType,
		Data:          cloneData(event.Data),
	}
}

// FromTaskRunEvents converts a batch of persisted task run events.
func FromTaskRunEvents(events []types.TaskRunEvent) []Envelope {
	items := make([]Envelope, 0, len(events))
	for _, event := range events {
		items = append(items, FromTaskRunEvent(event))
	}
	return items
}

func eventIDFor(event types.TaskRunEvent) string {
	if strings.HasPrefix(event.ID, "evt_") {
		return event.ID
	}
	seed := strings.Join([]string{
		event.TaskID,
		event.RunID,
		strconv.FormatInt(event.Sequence, 10),
		event.EventType,
		event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "evt_" + crockfordEncodeTime(event.CreatedAt) + crockfordEncodeBytes(sum[:10])
}

func crockfordEncodeTime(t time.Time) string {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	millis := uint64(t.UTC().UnixMilli())
	var out [10]byte
	for i := range out {
		shift := uint((9 - i) * 5)
		out[i] = crockfordAlphabet[(millis>>shift)&0x1F]
	}
	return string(out[:])
}

func crockfordEncodeBytes(raw []byte) string {
	if len(raw) < 10 {
		panic(fmt.Sprintf("crockfordEncodeBytes requires 10 bytes, got %d", len(raw)))
	}
	var out [16]byte
	bit := 0
	for i := range out {
		var value byte
		for j := 0; j < 5; j++ {
			byteIndex := bit / 8
			bitIndex := 7 - (bit % 8)
			value <<= 1
			value |= (raw[byteIndex] >> bitIndex) & 1
			bit++
		}
		out[i] = crockfordAlphabet[value]
	}
	return string(out[:])
}

func occurredAt(t time.Time) string {
	if t.IsZero() {
		return time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func cloneData(data map[string]any) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}
