package agentadapters

import (
	"os/exec"
)

type SetupCommandStatus struct {
	Available      bool   `json:"available"`
	Command        string `json:"command,omitempty"`
	ExecutablePath string `json:"executable_path,omitempty"`
}

func DetectClaudeCodeCLI(lookup LookupFunc) SetupCommandStatus {
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup("claude")
	if err == nil {
		return SetupCommandStatus{Available: true, Command: path, ExecutablePath: path}
	}
	npxPath, err := lookup("npx")
	if err != nil {
		return SetupCommandStatus{}
	}
	return SetupCommandStatus{Available: true, Command: npxPath + " -y @anthropic-ai/claude-code", ExecutablePath: npxPath}
}
