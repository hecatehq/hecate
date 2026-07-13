package projectassistant

import (
	"testing"

	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestValidDraftDriverKind_Manual(t *testing.T) {
	if !validDraftDriverKind(projectwork.AssignmentDriverManual) {
		t.Fatal("Project Assistant should accept manual assignment drafts")
	}
}

func TestSelectDraftDriverUsesProductLanguage(t *testing.T) {
	driver, source, reason := selectDraftDriver(projectwork.AssignmentDriverManual, nil)
	if driver != projectwork.AssignmentDriverManual || source != "explicit" || reason != "Operator selected Human." {
		t.Fatalf("selectDraftDriver(manual) = %q %q %q, want Human product language", driver, source, reason)
	}
}
