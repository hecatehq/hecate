package taskstate

import "testing"

func TestMemoryStoreConformance(t *testing.T) {
	RunConformanceTests(t, "MemoryStore", func(*testing.T) Store { return NewMemoryStore() })
}

func TestMemoryScheduleStoreConformance(t *testing.T) {
	RunScheduleStoreConformanceTests(t, "MemoryStore", func(*testing.T) scheduleConformanceStore { return NewMemoryStore() })
}
