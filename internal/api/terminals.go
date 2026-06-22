package api

import "time"

type TerminalStartRequest struct {
	Workspace        string            `json:"workspace"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	OutputByteLimit  int               `json:"output_byte_limit,omitempty"`
}

type TerminalInputRequest struct {
	Input string `json:"input"`
}

type TerminalResponse struct {
	Object string               `json:"object"`
	Data   TerminalResponseItem `json:"data"`
}

type TerminalResponseItem struct {
	ID               string    `json:"id"`
	Workspace        string    `json:"workspace"`
	WorkingDirectory string    `json:"working_directory"`
	Command          string    `json:"command,omitempty"`
	Args             []string  `json:"args,omitempty"`
	Output           string    `json:"output"`
	Truncated        bool      `json:"truncated"`
	Running          bool      `json:"running"`
	ExitCode         *int      `json:"exit_code,omitempty"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
