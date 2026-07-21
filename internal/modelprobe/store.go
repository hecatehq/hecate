// Package modelprobe owns Hecate's narrow, operator-triggered model tool
// capability verification state. It is deliberately independent of provider
// discovery: provider-native metadata remains authoritative and this package
// stores no user prompt, response text, tool arguments, endpoint, or secret.
package modelprobe

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

const (
	ProbeVersion = 1

	StatusTesting      = "testing"
	StatusSupported    = "supported"
	StatusUnsupported  = "unsupported"
	StatusInconclusive = "inconclusive"

	ReasonNone             = ""
	ReasonNoToolCall       = "no_tool_call"
	ReasonToolRejected     = "tool_rejected"
	ReasonAuthentication   = "authentication"
	ReasonRateLimited      = "rate_limited"
	ReasonTimeout          = "timeout"
	ReasonNetwork          = "network"
	ReasonProviderFailure  = "provider_failure"
	ReasonProviderChanged  = "provider_changed"
	ReasonPolicyDenied     = "policy_denied"
	ReasonConfiguration    = "configuration"
	ReasonUnexpectedResult = "unexpected_result"
)

var (
	ErrLeaseLost = errors.New("model tool probe lease was lost")
	ErrInvalid   = errors.New("invalid model tool probe record")
)

// Key is internal-only because Instance is an opaque provider-generation
// fence. It must never be emitted in HTTP responses or telemetry.
type Key struct {
	Provider string
	Model    string
	Instance types.ProviderInstanceIdentity
	Version  int
}

// Record is durable Hecate runtime state for a single provider/model
// generation. LeaseID is a private fence used only by Complete.
type Record struct {
	Key
	Status     string
	CheckedAt  time.Time
	ExpiresAt  time.Time
	LeaseUntil time.Time
	LeaseID    string
	Reason     string
}

// Store provides durable single-probe reservations. Acquire never holds the
// transaction over an upstream call; Complete must present the lease returned
// by Acquire, preventing a stale response from overwriting a newer attempt.
type Store interface {
	Backend() string
	Get(ctx context.Context, key Key) (Record, bool, error)
	Acquire(ctx context.Context, key Key, now time.Time, leaseUntil time.Time, leaseID string) (record Record, acquired bool, err error)
	Complete(ctx context.Context, record Record) (Record, error)
}

// BatchStore is an optional read extension for catalog projection. Durable
// stores implement it so listing a large provider catalog does not turn one
// model-capability request into one query per model. Callers retain the Store
// fallback for narrow test doubles and third-party in-memory implementations.
type BatchStore interface {
	GetMany(ctx context.Context, keys []Key) (map[Key]Record, error)
}

func NormalizeKey(key Key) (Key, error) {
	key.Provider = strings.TrimSpace(key.Provider)
	key.Model = strings.TrimSpace(key.Model)
	if key.Version <= 0 {
		key.Version = ProbeVersion
	}
	if key.Provider == "" || key.Model == "" || !key.Instance.Valid() {
		return Key{}, ErrInvalid
	}
	return key, nil
}

func normalizeKeys(keys []Key) ([]Key, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	normalized := make([]Key, 0, len(keys))
	seen := make(map[Key]struct{}, len(keys))
	for _, raw := range keys {
		key, err := NormalizeKey(raw)
		if err != nil {
			return nil, err
		}
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	return normalized, nil
}

func NormalizeRecord(record Record) (Record, error) {
	key, err := NormalizeKey(record.Key)
	if err != nil {
		return Record{}, err
	}
	record.Key = key
	record.Status = normalizeStatus(record.Status)
	record.Reason = normalizeReason(record.Reason)
	if record.Status == "" {
		return Record{}, ErrInvalid
	}
	if !record.CheckedAt.IsZero() {
		record.CheckedAt = record.CheckedAt.UTC()
	}
	if !record.ExpiresAt.IsZero() {
		record.ExpiresAt = record.ExpiresAt.UTC()
	}
	if !record.LeaseUntil.IsZero() {
		record.LeaseUntil = record.LeaseUntil.UTC()
	}
	record.LeaseID = strings.TrimSpace(record.LeaseID)
	if record.Status == StatusTesting && (record.LeaseID == "" || record.LeaseUntil.IsZero()) {
		return Record{}, ErrInvalid
	}
	return record, nil
}

func (record Record) Active(now time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !record.ExpiresAt.IsZero() && now.Before(record.ExpiresAt)
}

func (record Record) LeaseActive(now time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return record.Status == StatusTesting && !record.LeaseUntil.IsZero() && now.Before(record.LeaseUntil)
}

func (record Record) Public() *types.ToolCapabilityVerification {
	if record.Status == "" {
		return nil
	}
	return &types.ToolCapabilityVerification{
		Status:    record.Status,
		CheckedAt: record.CheckedAt,
		ExpiresAt: record.ExpiresAt,
		Reason:    record.Reason,
	}
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusTesting:
		return StatusTesting
	case StatusSupported:
		return StatusSupported
	case StatusUnsupported:
		return StatusUnsupported
	case StatusInconclusive:
		return StatusInconclusive
	default:
		return ""
	}
}

func normalizeReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case ReasonNone:
		return ReasonNone
	case ReasonNoToolCall:
		return ReasonNoToolCall
	case ReasonToolRejected:
		return ReasonToolRejected
	case ReasonAuthentication:
		return ReasonAuthentication
	case ReasonRateLimited:
		return ReasonRateLimited
	case ReasonTimeout:
		return ReasonTimeout
	case ReasonNetwork:
		return ReasonNetwork
	case ReasonProviderFailure:
		return ReasonProviderFailure
	case ReasonProviderChanged:
		return ReasonProviderChanged
	case ReasonPolicyDenied:
		return ReasonPolicyDenied
	case ReasonConfiguration:
		return ReasonConfiguration
	case ReasonUnexpectedResult:
		return ReasonUnexpectedResult
	default:
		return ReasonUnexpectedResult
	}
}
