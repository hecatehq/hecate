//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package agentadapters

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func allowACPPromptStageSubstitutionForTest(t *testing.T, _ *acpPromptStage) {
	t.Helper()
}

func TestACPPromptStageUnsupportedPlatformFailsResourceLinkClosed(t *testing.T) {
	file := promptTestFile("notes.txt", "text/plain", []byte("private input"))
	blocks, stage, err := buildACPPrompt(PromptInput{Files: []PromptFile{file}}, acp.PromptCapabilities{})
	if err == nil || blocks != nil || stage != nil {
		t.Fatalf("buildACPPrompt = %#v, %#v, %v", blocks, stage, err)
	}
}
