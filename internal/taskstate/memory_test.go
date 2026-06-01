package taskstate

import "testing"

func TestMemoryStoreConformance(t *testing.T) {
	RunConformanceTests(t, "MemoryStore", func(*testing.T) Store { return NewMemoryStore() })
}
