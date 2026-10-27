package api

import (
	"strconv"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/pkg/types"
)

func appendWorkspaceSkillContext(packet *chat.ContextPacket, skills []types.WorkspaceSkillSnapshot) {
	for _, skill := range skills {
		packet.Items = append(packet.Items, chat.ContextItem{Section: contextSectionSkills, Kind: "workspace_skill", TrustLevel: "workspace", Origin: skill.Path, Title: skill.Name, Included: true,
			InclusionReason: "Explicitly selected at creation; frozen instructions, without additional permissions",
			Metadata:        map[string]string{"sha256": skill.SHA256, "bytes": strconv.Itoa(skill.Bytes)},
		})
	}
}
