package retention

import (
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestShouldRunEmptySelectionMeansAll(t *testing.T) {
	if !shouldRun(nil, "anything") {
		t.Error("nil selected should mean all subsystems run")
	}
	if !shouldRun([]string{}, "anything") {
		t.Error("empty selected should mean all subsystems run")
	}
}

func TestShouldRunFiltersBySubsystemName(t *testing.T) {
	selected := []string{SubsystemTraces, SubsystemUsageEvents}
	if !shouldRun(selected, SubsystemTraces) {
		t.Error("traces should match")
	}
	if shouldRun(selected, SubsystemAuditEvents) {
		t.Error("audit events not in selection should be skipped")
	}
}

func TestManagerEnabledHandlesNil(t *testing.T) {
	var m *Manager
	if m.Enabled() {
		t.Error("nil manager should not report enabled")
	}

	m = &Manager{cfg: config.RetentionConfig{Enabled: false}}
	if m.Enabled() {
		t.Error("manager with cfg.Enabled=false should not report enabled")
	}

	m = &Manager{cfg: config.RetentionConfig{Enabled: true}}
	if !m.Enabled() {
		t.Error("manager with cfg.Enabled=true should report enabled")
	}
}

func TestCloneHistoryRecordsIsolatesNestedSlices(t *testing.T) {
	originals := []HistoryRecord{
		{
			Trigger: "manual",
			Results: []SubsystemResult{{Name: "traces", Deleted: 5}, {Name: "usage_events", Deleted: 2}},
		},
	}
	clones := cloneHistoryRecords(originals)
	if len(clones) != 1 {
		t.Fatalf("len(clones) = %d, want 1", len(clones))
	}

	// Mutate the original results slice; clone must not change.
	originals[0].Results[0].Deleted = 9999
	if clones[0].Results[0].Deleted != 5 {
		t.Errorf("clone shared backing slice with original: got Deleted = %d, want 5", clones[0].Results[0].Deleted)
	}
}

func TestCloneHistoryRecordsEmptyReturnsNil(t *testing.T) {
	if got := cloneHistoryRecords(nil); got != nil {
		t.Errorf("nil input → got %v, want nil", got)
	}
	if got := cloneHistoryRecords([]HistoryRecord{}); got != nil {
		t.Errorf("empty input → got %v, want nil", got)
	}
}

func TestCloneSubsystemResultsEmptyReturnsNil(t *testing.T) {
	if got := cloneSubsystemResults(nil); got != nil {
		t.Errorf("nil input → got %v, want nil", got)
	}
	if got := cloneSubsystemResults([]SubsystemResult{}); got != nil {
		t.Errorf("empty input → got %v, want nil", got)
	}
}

func TestLimitHistoryRecords(t *testing.T) {
	records := []HistoryRecord{{Trigger: "a"}, {Trigger: "b"}, {Trigger: "c"}}

	cases := []struct {
		limit, wantLen int
	}{
		{0, 3},  // 0 means no limit
		{-1, 3}, // negative also no limit
		{2, 2},
		{99, 3}, // larger than slice → all
		{3, 3},
	}
	for _, tc := range cases {
		got := limitHistoryRecords(records, tc.limit)
		if len(got) != tc.wantLen {
			t.Errorf("limitHistoryRecords(limit=%d) len = %d, want %d", tc.limit, len(got), tc.wantLen)
		}
	}
}
