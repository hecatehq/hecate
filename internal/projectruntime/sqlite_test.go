package projectruntime

import (
	"testing"
	"time"
)

func TestParseRuntimeTimePreservesTypedTimestamps(t *testing.T) {
	t.Parallel()
	value := time.Date(2026, 10, 26, 12, 0, 0, 123456789, time.UTC)
	for _, tc := range []struct {
		name  string
		input any
		want  time.Time
	}{
		{"sqlite unset", "", time.Time{}},
		{"sqlite text", "2026-10-26T14:00:00.123456789+02:00", value},
		{"postgres typed", value.In(time.FixedZone("offset", 7244)), value},
		{"postgres zero with historical offset", time.Time{}.In(time.FixedZone("historical", 44)), time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRuntimeTime(tc.input)
			if err != nil || !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("parseRuntimeTime(%v) = %v, %v; want UTC %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, invalid := range []any{"not-a-timestamp", 42, nil} {
		if _, err := parseRuntimeTime(invalid); err == nil {
			t.Fatalf("parseRuntimeTime(%v) accepted invalid timestamp", invalid)
		}
	}
}
