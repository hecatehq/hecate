package agentadapters

type SetupCommandStatus struct {
	Available      bool   `json:"available"`
	Command        string `json:"command,omitempty"`
	ExecutablePath string `json:"executable_path,omitempty"`
}

func DetectClaudeCodeCLI(probe VersionProbe, lookup LookupFunc) SetupCommandStatus {
	path, ok := resolveVersionProbe(probe, lookup)
	if !ok {
		return SetupCommandStatus{}
	}
	return SetupCommandStatus{Available: true, Command: path, ExecutablePath: path}
}
