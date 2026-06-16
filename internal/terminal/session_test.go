package terminal

import (
	"errors"
	"slices"
	"testing"
)

func TestTerminalEnvStripsSecrets(t *testing.T) {
	env := terminalEnv([]string{
		"PATH=/usr/bin",
		"HECATE_RUNTIME_TOKEN=secret",
		"OPENAI_API_KEY=secret",
		"ANTHROPIC_AUTH_TOKEN=secret",
		"SAFE_VALUE=visible",
	})

	for _, forbidden := range []string{
		"HECATE_RUNTIME_TOKEN=secret",
		"OPENAI_API_KEY=secret",
		"ANTHROPIC_AUTH_TOKEN=secret",
	} {
		if slices.Contains(env, forbidden) {
			t.Fatalf("terminalEnv kept secret %q in %v", forbidden, env)
		}
	}
	for _, required := range []string{
		"PATH=/usr/bin",
		"SAFE_VALUE=visible",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	} {
		if !slices.Contains(env, required) {
			t.Fatalf("terminalEnv missing %q in %v", required, env)
		}
	}
}

func TestDeviceNotConfiguredDetectsMacOSErrnoText(t *testing.T) {
	if !DeviceNotConfigured(errors.New("device not configured")) {
		t.Fatal("DeviceNotConfigured returned false for macOS ENXIO text")
	}
	if DeviceNotConfigured(errors.New("permission denied")) {
		t.Fatal("DeviceNotConfigured returned true for an unrelated error")
	}
}
