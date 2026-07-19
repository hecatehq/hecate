package codeintel

import "context"

// Operation identifies one read-only code-intelligence query.
type Operation string

const (
	OpCapabilities     Operation = "capabilities"
	OpDefinition       Operation = "definition"
	OpReferences       Operation = "references"
	OpHover            Operation = "hover"
	OpDocumentSymbols  Operation = "document_symbols"
	OpWorkspaceSymbols Operation = "workspace_symbols"
	OpDiagnostics      Operation = "diagnostics"
	OpStructuralSearch Operation = "structural_search"
)

// Request contains the common inputs used by code-intelligence operations.
// Line and Column are 1-based; Column counts UTF-8 bytes, matching the file
// tools and source previews exposed to models.
type Request struct {
	Operation  Operation
	Path       string
	Language   string
	Query      string
	Line       int
	Column     int
	MaxResults int
}

// Result is both machine-readable and immediately suitable for a model tool
// response through Text. Items never contain paths outside workspaceRoot.
type Result struct {
	Operation       Operation
	Provider        string
	Items           []Item
	Capabilities    []Capability
	Text            string
	Truncated       bool
	OmittedExternal int
}

// Capability reports whether a fixed, allowlisted local provider is present.
type Capability struct {
	Language   string
	Provider   string
	Available  bool
	Status     string
	Operations []Operation
	Detail     string
}

// Item is a normalized location, symbol, hover, diagnostic, or structural
// match. Ranges use 1-based lines and UTF-8 byte columns.
type Item struct {
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	Name        string
	Kind        string
	Detail      string
	Preview     string
	Message     string
	Severity    string
	Source      string
}

// Querier is the narrow seam used by the agent-loop tool adapter. Queries do
// not request edits, but an installed language server remains trusted local
// code: OS wrappers do not provide hard filesystem confinement on every
// platform, and some servers can inspect or execute project analysis hooks.
type Querier interface {
	Query(ctx context.Context, workspaceRoot string, request Request) (Result, error)
}
