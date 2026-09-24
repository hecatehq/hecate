package agentadapters

import (
	"context"
	"errors"
	"time"
)

const ExecutableIdentitySchemaVersion = "hecate.external-agent-executable.v1"

const (
	ExecutableTrustStateUnavailable = "unavailable"
	ExecutableTrustStateUnapproved  = "unapproved"
	ExecutableTrustStateApproved    = "approved"
	ExecutableTrustStateChanged     = "changed"
)

const (
	ExecutableCoverageBinary       = "binary"
	ExecutableCoverageLauncherOnly = "launcher_only"
)

const (
	ExecutablePublisherUnavailable = "unavailable"
)

var (
	ErrExecutableTrustNotFound       = errors.New("external agent executable trust not found")
	ErrExecutableTrustRequired       = errors.New("external agent executable approval required")
	ErrExecutableIdentityChanged     = errors.New("external agent executable identity changed")
	ErrExecutableIdentityRaced       = errors.New("external agent executable changed while it was measured")
	ErrExecutableTrustConflict       = errors.New("external agent executable approval is stale")
	ErrExecutableIdentityUnavailable = errors.New("external agent executable identity unavailable")
)

// ExecutablePublisherEvidence is best-effort platform evidence. An unavailable
// publisher never means that an executable is unsafe; it means Hecate has no
// authenticated publisher claim to show for this identity.
type ExecutablePublisherEvidence struct {
	Status     string `json:"status"`
	Platform   string `json:"platform,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	TeamID     string `json:"team_id,omitempty"`
}

// ExecutableIdentity describes the exact filesystem object Hecate measured.
// IdentityToken is a digest over the security-relevant fields below; SHA256 is
// the digest of the executable or launcher bytes themselves.
type ExecutableIdentity struct {
	SchemaVersion  string                      `json:"schema_version"`
	IdentityToken  string                      `json:"identity_token"`
	InvocationPath string                      `json:"invocation_path"`
	CanonicalPath  string                      `json:"canonical_path"`
	SHA256         string                      `json:"sha256"`
	Coverage       string                      `json:"coverage"`
	LauncherChain  []string                    `json:"launcher_chain,omitempty"`
	FileID         string                      `json:"file_id,omitempty"`
	Mode           uint32                      `json:"mode"`
	SizeBytes      int64                       `json:"size_bytes"`
	Publisher      ExecutablePublisherEvidence `json:"publisher"`
}

type ExecutableTrustRecord struct {
	RuntimeHostID string             `json:"runtime_host_id"`
	AdapterID     string             `json:"adapter_id"`
	Identity      ExecutableIdentity `json:"identity"`
	ApprovedBy    string             `json:"approved_by"`
	ApprovedAt    time.Time          `json:"approved_at"`
}

type ExecutableTrustStatus struct {
	SchemaVersion string              `json:"schema_version"`
	State         string              `json:"state"`
	Reason        string              `json:"reason,omitempty"`
	Current       *ExecutableIdentity `json:"current,omitempty"`
	Approved      *ExecutableIdentity `json:"approved,omitempty"`
	ApprovedBy    string              `json:"approved_by,omitempty"`
	ApprovedAt    *time.Time          `json:"approved_at,omitempty"`
}

type ExecutableTrustStore interface {
	Backend() string
	Get(ctx context.Context, runtimeHostID, adapterID string) (ExecutableTrustRecord, error)
	Approve(ctx context.Context, record ExecutableTrustRecord) (ExecutableTrustRecord, error)
	Revoke(ctx context.Context, runtimeHostID, adapterID string) error
}

func cloneExecutableIdentity(identity ExecutableIdentity) ExecutableIdentity {
	identity.LauncherChain = append([]string(nil), identity.LauncherChain...)
	return identity
}

func cloneExecutableTrustRecord(record ExecutableTrustRecord) ExecutableTrustRecord {
	record.Identity = cloneExecutableIdentity(record.Identity)
	return record
}
