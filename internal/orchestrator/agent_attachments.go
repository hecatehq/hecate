package orchestrator

import "context"

const (
	AgentAttachmentDefaultReadBytes = 8192
	AgentAttachmentMaxReadBytes     = 32768
	AgentAttachmentMaxQueryBytes    = 256
	AgentAttachmentDefaultMatches   = 10
	AgentAttachmentMaxMatches       = 20
	AgentAttachmentMaxSearchBytes   = 16384
)

// AgentAttachmentReader is scoped to the admitted chat input by its application
// owner. References are not paths or permission to read another chat's files.
// Implementations revalidate ownership and provider identity before every read.
type AgentAttachmentReader interface {
	Read(context.Context, AgentAttachmentReadRequest) (AgentAttachmentReadResult, error)
	Search(context.Context, AgentAttachmentSearchRequest) (AgentAttachmentSearchResult, error)
	// ContextBudget validates the recorded final route and returns its current
	// private-output allowance. Initial metadata-only prompts need no lookup.
	ContextBudget(context.Context) (int, error)
}

type AgentAttachmentReadRequest struct {
	AttachmentRef string `json:"attachment_ref"`
	Offset        int64  `json:"offset,omitempty"`
	MaxBytes      int    `json:"max_bytes,omitempty"`
}

type AgentAttachmentReadResult struct {
	AttachmentRef string `json:"attachment_ref"`
	Offset        int64  `json:"offset"`
	NextOffset    int64  `json:"next_offset"`
	EOF           bool   `json:"eof"`
	Text          string `json:"text"`
}

type AgentAttachmentSearchRequest struct {
	AttachmentRef string `json:"attachment_ref"`
	Query         string `json:"query"`
	Offset        int64  `json:"offset,omitempty"`
	MaxMatches    int    `json:"max_matches,omitempty"`
}

type AgentAttachmentSearchMatch struct {
	Offset int64  `json:"offset"`
	Text   string `json:"text"`
}

type AgentAttachmentSearchResult struct {
	AttachmentRef string                       `json:"attachment_ref"`
	Matches       []AgentAttachmentSearchMatch `json:"matches"`
	NextOffset    int64                        `json:"next_offset"`
	EOF           bool                         `json:"eof"`
}
