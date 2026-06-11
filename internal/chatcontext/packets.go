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

func Normalize(packet chat.ContextPacket, refs chat.ContextRefs) chat.ContextPacket {
	packet = Clone(packet)
	if packet.Refs == nil && !refsEmpty(refs) {
		packet.Refs = &chat.ContextRefs{}
	}
	if packet.Refs != nil {
		packet.Refs.SessionID = firstNonEmpty(packet.Refs.SessionID, refs.SessionID)
		packet.Refs.MessageID = firstNonEmpty(packet.Refs.MessageID, refs.MessageID)
		packet.Refs.TaskID = firstNonEmpty(packet.Refs.TaskID, refs.TaskID)
		packet.Refs.RunID = firstNonEmpty(packet.Refs.RunID, refs.RunID)
		packet.Refs.ProjectID = firstNonEmpty(packet.Refs.ProjectID, refs.ProjectID)
		packet.Refs.WorkItemID = firstNonEmpty(packet.Refs.WorkItemID, refs.WorkItemID)
		packet.Refs.AssignmentID = firstNonEmpty(packet.Refs.AssignmentID, refs.AssignmentID)
		packet.Refs.RoleID = firstNonEmpty(packet.Refs.RoleID, refs.RoleID)
		if refsEmpty(*packet.Refs) {
			packet.Refs = nil
		}
	}
	for idx := range packet.Items {
		if strings.TrimSpace(packet.Items[idx].Section) == "" {
			packet.Items[idx].Section = defaultItemSection(packet.Items[idx].Kind)
		}
	}
	return packet
}

func Clone(packet chat.ContextPacket) chat.ContextPacket {
	if packet.Refs != nil {
		refs := *packet.Refs
		packet.Refs = &refs
	}
	if len(packet.Sources) > 0 {
		packet.Sources = append([]chat.ContextSource(nil), packet.Sources...)
	}
	if len(packet.Items) > 0 {
		packet.Items = append([]chat.ContextItem(nil), packet.Items...)
	}
	return packet
}

func Marshal(packet chat.ContextPacket) json.RawMessage {
	if packet.Empty() {
		return nil
	}
	data, err := json.Marshal(packet)
	if err != nil {
		return nil
	}
	return data
}

func refsEmpty(refs chat.ContextRefs) bool {
	return refs.SessionID == "" &&
		refs.MessageID == "" &&
		refs.TaskID == "" &&
		refs.RunID == "" &&
		refs.ProjectID == "" &&
		refs.WorkItemID == "" &&
		refs.AssignmentID == "" &&
		refs.RoleID == ""
}

func defaultItemSection(kind string) string {
	switch {
	case kind == "system_prompt":
		return "instructions"
	case kind == "project_skills":
		return "skills"
	case kind == "memory":
		return "memory"
	case kind == "workspace":
		return "workspace"
	case kind == "project":
		return "project"
	case kind == "work_item" || kind == "assignment" || kind == "role" || kind == "execution_hints" || kind == "handoff" || kind == "artifact_ref":
		return "project_work"
	case kind == "transcript" || kind == "task_runtime" || kind == "external_agent_session":
		return "runtime"
	case strings.HasPrefix(kind, "workspace_") || strings.HasPrefix(kind, "project_"):
		return "sources"
	default:
		return "runtime"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
