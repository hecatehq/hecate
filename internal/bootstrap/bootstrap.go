// Package bootstrap manages the gateway's first-run secret: the
// control-plane encryption key. It's auto-generated on first start and
// persisted to a JSON file under the configured data directory so
// subsequent restarts reuse it. Operators can override it through an
// environment variable; an explicit env value always wins over what's
// on disk.
package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bootstrap carries the secret the gateway needs to come up.
type Bootstrap struct {
	// ControlPlaneSecretKey is the AES-GCM key (base64 of 32 raw bytes)
	// used to encrypt persisted provider API keys at rest. base64 because
	// secrets.NewAESGCMCipher decodes its input as base64 and requires
	// exactly 32 bytes after decode.
	ControlPlaneSecretKey string `json:"control_plane_secret_key"`
}

// Resolve returns the bootstrap state to use this run, prioritizing
// the explicit env-supplied value over the persisted file. When the
// file doesn't exist and the env var is also empty, a random value is
// generated and persisted.
func Resolve(path, envSecret string) (Bootstrap, error) {
	envSecret = strings.TrimSpace(envSecret)

	var b Bootstrap
	loaded, loadErr := load(path)
	switch {
	case loadErr == nil:
		b = loaded
	case os.IsNotExist(loadErr):
		// Fresh install — fall through, we'll generate as needed.
	default:
		return Bootstrap{}, fmt.Errorf("read bootstrap file %q: %w", path, loadErr)
	}

	if envSecret != "" {
		b.ControlPlaneSecretKey = envSecret
	}

	dirty := false
	if b.ControlPlaneSecretKey == "" {
		key, err := randomBase64(32)
		if err != nil {
			return Bootstrap{}, fmt.Errorf("generate control-plane secret key: %w", err)
		}
		b.ControlPlaneSecretKey = key
		dirty = true
	}

	if dirty || envSecret != "" {
		if err := save(path, b); err != nil {
			return Bootstrap{}, fmt.Errorf("persist bootstrap file %q: %w", path, err)
		}
	}

	return b, nil
}

func load(path string) (Bootstrap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Bootstrap{}, err
	}
	var b Bootstrap
	if err := json.Unmarshal(data, &b); err != nil {
		return Bootstrap{}, fmt.Errorf("decode bootstrap file: %w", err)
	}
	return b, nil
}

func save(path string, b Bootstrap) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	// 0o600 because the file holds the encryption key. Anything more
	// permissive lets a co-located service decrypt provider credentials.
	return os.WriteFile(path, data, 0o600)
}

func randomBase64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
