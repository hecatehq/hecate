package chatcontext

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

func FromSessionMessage(session chat.Session, messageID string) (chat.ContextPacket, bool) {
	messageID = strings.TrimSpace(messageID)
	for _, message := range session.Messages {
		if strings.TrimSpace(message.ID) != messageID {
			continue
		}
		if message.Context.Empty() {
			return chat.ContextPacket{}, false
		}
		return message.Context, true
	}
	return chat.ContextPacket{}, false
}

func FromSessionRun(session chat.Session, taskID, runID string) (chat.ContextPacket, bool) {
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	for _, message := range session.Messages {
		if strings.TrimSpace(message.TaskID) != taskID || strings.TrimSpace(message.RunID) != runID {
			continue
		}
		if message.Context.Empty() {
			return chat.ContextPacket{}, false
		}
		return message.Context, true
	}
	return chat.ContextPacket{}, false
}

func FromTaskRun(run types.TaskRun) (chat.ContextPacket, bool, error) {
	if len(run.ContextPacket) == 0 {
		return chat.ContextPacket{}, false, nil
	}
	var packet chat.ContextPacket
	if err := json.Unmarshal(run.ContextPacket, &packet); err != nil {
		return chat.ContextPacket{}, false, fmt.Errorf("decode task run context packet: %w", err)
	}
	if packet.Empty() {
		return chat.ContextPacket{}, false, nil
	}
	return packet, true, nil
}

func FromProjectAssignmentPayload(raw json.RawMessage) (chat.ContextPacket, bool, error) {
	if len(raw) == 0 {
		return chat.ContextPacket{}, false, nil
	}
	var packet chat.ContextPacket
	if err := json.Unmarshal(raw, &packet); err != nil {
		return chat.ContextPacket{}, false, fmt.Errorf("decode project assignment context packet: %w", err)
	}
	if packet.Empty() {
		return chat.ContextPacket{}, false, nil
	}
	return packet, true, nil
}
