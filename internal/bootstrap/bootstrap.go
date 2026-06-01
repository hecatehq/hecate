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
	if err := validateControlPlaneSecretKey(b.ControlPlaneSecretKey); err != nil {
		return Bootstrap{}, err
	}

	if dirty || envSecret != "" {
		if err := save(path, b); err != nil {
			return Bootstrap{}, fmt.Errorf("persist bootstrap file %q: %w", path, err)
		}
	} else if err := secureExistingFile(path); err != nil {
		return Bootstrap{}, fmt.Errorf("secure bootstrap file %q: %w", path, err)
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

func validateControlPlaneSecretKey(key string) error {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil {
		return fmt.Errorf("control-plane secret key must be base64: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("control-plane secret key must decode to 32 bytes")
	}
	return nil
}

func save(path string, b Bootstrap) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return replaceBootstrapFile(path, data)
}

func secureExistingFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !bootstrapFileNeedsModeRepair(info.Mode().Perm()) {
		return nil
	}
	return chmodBootstrapFile(path)
}

func replaceBootstrapFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	// CreateTemp starts with 0600 on POSIX filesystems, but keep the write
	// path explicit so a future refactor cannot write key material before
	// setting the intended mode bits.
	if err := chmodBootstrapFile(tmpPath); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return err
	}
	keepTemp = true
	return chmodBootstrapFile(path)
}

func chmodBootstrapFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set bootstrap file permissions to 0600: %w", err)
	}
	return nil
}

func randomBase64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
