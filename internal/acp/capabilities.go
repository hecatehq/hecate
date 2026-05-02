package acp

// InitializeParams is the client→server payload of `initialize`.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ClientCaps      ClientCapabilities `json:"clientCapabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo,omitempty"`
}

// ClientCapabilities advertises which optional ACP surfaces the editor
// supports. v0 only requires the permissions capability.
type ClientCapabilities struct {
	FS          *FSCapability         `json:"fs,omitempty"`
	Terminal    *TerminalCapability   `json:"terminal,omitempty"`
	Permissions *PermissionCapability `json:"permissions,omitempty"`
}

type FSCapability struct{}
type TerminalCapability struct{}
type PermissionCapability struct{}

// ClientInfo is human-readable metadata the editor sends for
// telemetry / log lines.
type ClientInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeResult is the bridge's response to `initialize`.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	AgentCaps       AgentCapabilities  `json:"agentCapabilities"`
	AgentInfo       AgentInfo          `json:"agentInfo"`
	AvailableModels []ModelDescription `json:"availableModels"`
}

// AgentCapabilities advertises which ACP surfaces the bridge supports.
type AgentCapabilities struct {
	Prompt      bool `json:"prompt"`
	Cancel      bool `json:"cancel"`
	Permissions bool `json:"permissions"`
}

// AgentInfo is the bridge's self-description.
type AgentInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// ModelDescription is one entry in the model picker.
type ModelDescription struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}
