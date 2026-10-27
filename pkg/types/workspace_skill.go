package types

// WorkspaceSkillSelection is explicit operator consent to one reviewed file
// version. It is unrelated to portable role/preset skill metadata.
type WorkspaceSkillSelection struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// WorkspaceSkillSnapshot is private durable runtime input. Body must survive
// storage round trips, but API renderers expose WorkspaceSkillInfo only.
type WorkspaceSkillSnapshot struct {
	WorkspaceSkillInfo
	Body string `json:"body"`
}

type WorkspaceSkillInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func WorkspaceSkillMetadata(skills []WorkspaceSkillSnapshot) []WorkspaceSkillInfo {
	if len(skills) == 0 {
		return nil
	}
	result := make([]WorkspaceSkillInfo, 0, len(skills))
	for _, skill := range skills {
		result = append(result, skill.WorkspaceSkillInfo)
	}
	return result
}
