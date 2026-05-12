package agentadapters

import (
	"os/exec"
)

type SetupCommandStatus struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
}

func DetectClaudeCodeCLI(lookup LookupFunc) SetupCommandStatus {
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup("claude")
	if err == nil {
		return SetupCommandStatus{Available: true, Path: path}
	}
	npxPath, err := lookup("npx")
	if err != nil {
		return SetupCommandStatus{}
	}
	return SetupCommandStatus{Available: true, Path: npxPath + " -y @anthropic-ai/claude-code"}
}
