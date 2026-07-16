//go:build !darwin && !linux && !windows

package agentadapters

import (
	"errors"
	"os"
)

var errACPPromptStagingUnsupported = errors.New("private ACP prompt staging is unsupported on this platform")

type acpPromptStageIdentity struct{}

func createPrivateACPPromptStageDir() (string, *acpPromptStageIdentity, error) {
	return "", nil, errACPPromptStagingUnsupported
}

func openPrivateACPPromptStageFile(*acpPromptStageIdentity, string) (*os.File, error) {
	return nil, errACPPromptStagingUnsupported
}

func sealPrivateACPPromptStageFile(*os.File) error {
	return errACPPromptStagingUnsupported
}

func retainPrivateACPPromptStageFile(*acpPromptStageIdentity, string, *os.File) error {
	return errACPPromptStagingUnsupported
}

func sealPrivateACPPromptStageDir(string, *acpPromptStageIdentity) error {
	return errACPPromptStagingUnsupported
}

func verifySealedPrivateACPPromptStageFile(string) error {
	return errACPPromptStagingUnsupported
}

func verifySealedPrivateACPPromptStageDir(string) error {
	return errACPPromptStagingUnsupported
}

func verifyPrivateACPPromptStageIdentity(string, *acpPromptStageIdentity) error {
	return errACPPromptStagingUnsupported
}

func quarantinePrivateACPPromptStage(string, *acpPromptStageIdentity) error {
	return errACPPromptStagingUnsupported
}

func currentPrivateACPPromptStageDirectory(*acpPromptStageIdentity) string { return "" }

func setPrivateACPPromptStageQuarantineObserver(*acpPromptStageIdentity, func(string)) {}

func preparePrivateACPPromptStageCleanup(*acpPromptStageIdentity) error {
	return errACPPromptStagingUnsupported
}

func privateACPPromptStageIdentityRemoved(*acpPromptStageIdentity) bool { return false }

func removePrivateACPPromptStage(*acpPromptStageIdentity, []string) error {
	return errACPPromptStagingUnsupported
}

func deletePrivateACPPromptStageChild(*acpPromptStageIdentity, string) error {
	return errACPPromptStagingUnsupported
}

func closePrivateACPPromptStageIdentity(*acpPromptStageIdentity) {}
