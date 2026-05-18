package acp

// InitializeParams is the client→server payload of `initialize`.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ClientCaps      ClientCapabilities `json:"clientCapabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo,omitempty"`
}

// ClientCapabilities advertises which optional ACP surfaces the editor
// supports. v0 only requires the permissions capability; fs and
// terminal are consulted by the workspace-mode resolver to decide
// whether `editor-owned` mode is actually viable.
type ClientCapabilities struct {
	FS          *FSCapability         `json:"fs,omitempty"`
	Terminal    *TerminalCapability   `json:"terminal,omitempty"`
	Permissions *PermissionCapability `json:"permissions,omitempty"`
	Auth        *AuthCapabilities     `json:"auth,omitempty"`
}

// FSCapability mirrors the ACP spec's fs capability block. Both fields
// are independently advertised by the editor; Hecate's editor-owned
// mode requires both because reverse-RPC reads + writes are the whole
// point of that mode.
type FSCapability struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// TerminalCapability is present-or-absent in the ACP spec — the
// editor either supports terminal/* or it doesn't.
type TerminalCapability struct{}

// PermissionCapability is present-or-absent in the ACP spec — the
// editor either supports session/request_permission or it doesn't.
type PermissionCapability struct{}

// AuthCapabilities advertises which authentication setup helpers the
// editor can present. Terminal auth requires an explicit opt-in because
// it asks the client to launch an interactive terminal command.
type AuthCapabilities struct {
	Terminal bool `json:"terminal,omitempty"`
}

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
	AuthMethods     []AuthMethod       `json:"authMethods,omitempty"`
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

// AuthMethod describes one setup path a client can offer before the
// user starts a session. Hecate currently uses terminal auth to verify
// that the local gateway is running and has a routable model.
type AuthMethod struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// ModelDescription is one entry in the model picker.
type ModelDescription struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}
