package runtimehost

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	StateFilename = "hecate.runtime-host.json"
	idPrefix      = "runtime_"
	idRandomBytes = 12
	maxLabelRunes = 80
	defaultLabel  = "This device"
)

// Identity names one Hecate runtime installation. The opaque ID follows its
// data directory; Label describes the host running it now.
type Identity struct {
	ID    string
	Label string
}

type persistedIdentity struct {
	RuntimeHostID string `json:"runtime_host_id"`
}

// Resolve loads or creates the identity attached to one Hecate data
// directory. It is deliberately file-backed across all storage tiers because
// it identifies the runtime host, not a portable or backend-owned record.
func Resolve(dataDir, configuredLabel string) (Identity, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return Identity{}, errors.New("runtime host data directory is required")
	}
	if err := ValidateLabel(configuredLabel); err != nil {
		return Identity{}, err
	}

	path := filepath.Join(dataDir, StateFilename)
	state, err := load(path)
	if err == nil {
		return Identity{ID: state.RuntimeHostID, Label: ResolveLabel(configuredLabel)}, nil
	}
	if !os.IsNotExist(err) {
		return Identity{}, fmt.Errorf("read runtime host identity %q: %w", path, err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Identity{}, fmt.Errorf("create runtime host data directory: %w", err)
	}

	id, err := NewID()
	if err != nil {
		return Identity{}, err
	}
	state = persistedIdentity{RuntimeHostID: id}
	if err := create(path, state); err != nil {
		if os.IsExist(err) {
			state, err = load(path)
		}
		if err != nil {
			return Identity{}, fmt.Errorf("persist runtime host identity %q: %w", path, err)
		}
	}
	return Identity{ID: state.RuntimeHostID, Label: ResolveLabel(configuredLabel)}, nil
}

func load(path string) (persistedIdentity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return persistedIdentity{}, err
	}
	var state persistedIdentity
	if err := json.Unmarshal(raw, &state); err != nil {
		return persistedIdentity{}, fmt.Errorf("decode runtime host identity: %w", err)
	}
	if err := ValidateID(state.RuntimeHostID); err != nil {
		return persistedIdentity{}, err
	}
	return state, nil
}

func create(path string, state persistedIdentity) (err error) {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if !keep || err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	keep = true
	return nil
}

// NewEphemeral returns a process-scoped identity for embedders that construct
// the API without cmd/hecate's durable bootstrap path.
func NewEphemeral(configuredLabel string) Identity {
	id, err := NewID()
	if err != nil {
		// Runtime identity is informational, not an authorization credential.
		// Keep the API contract non-empty even on a failed system RNG.
		id = fmt.Sprintf("%s%024x", idPrefix, time.Now().UTC().UnixNano())
	}
	return Identity{ID: id, Label: ResolveLabel(configuredLabel)}
}

func NewID() (string, error) {
	random := make([]byte, idRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate runtime host id: %w", err)
	}
	return idPrefix + hex.EncodeToString(random), nil
}

func ValidateID(id string) error {
	if id != strings.TrimSpace(id) {
		return errors.New("runtime host id has invalid format")
	}
	encoded := strings.TrimPrefix(id, idPrefix)
	if encoded == id || len(encoded) != idRandomBytes*2 {
		return errors.New("runtime host id has invalid format")
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return errors.New("runtime host id has invalid format")
	}
	return nil
}

// ResolveLabel returns the configured operator-facing label or the machine
// hostname. Hostnames that are unsuitable for display fall back to a neutral
// label; explicitly configured invalid labels fail startup through ValidateLabel.
func ResolveLabel(configured string) string {
	if label := strings.TrimSpace(configured); label != "" {
		return label
	}
	hostname, err := os.Hostname()
	if err != nil {
		return defaultLabel
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" || ValidateLabel(hostname) != nil {
		return defaultLabel
	}
	return hostname
}

func ValidateLabel(label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil
	}
	if utf8.RuneCountInString(label) > maxLabelRunes {
		return fmt.Errorf("runtime host label must be at most %d characters", maxLabelRunes)
	}
	if strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return errors.New("runtime host label must not contain control characters")
	}
	return nil
}
