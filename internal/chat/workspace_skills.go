package chat

import (
	"encoding/json"
	"errors"

	"github.com/hecatehq/hecate/pkg/types"
)

func marshalWorkspaceSkills(skills []types.WorkspaceSkillSnapshot) string {
	if len(skills) == 0 {
		return "[]"
	}
	raw, _ := json.Marshal(skills)
	return string(raw)
}

func unmarshalWorkspaceSkills(raw string) ([]types.WorkspaceSkillSnapshot, error) {
	var skills []types.WorkspaceSkillSnapshot
	if err := json.Unmarshal([]byte(raw), &skills); err != nil {
		// Do not expose stored instruction bytes through a JSON decoding error,
		// or silently run without a selected skill after storage corruption.
		return nil, errors.New("invalid stored workspace skill snapshot")
	}
	return skills, nil
}
