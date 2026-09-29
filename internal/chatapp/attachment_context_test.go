package chatapp

import "testing"

func TestNativeTextContextBudget(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		window, ordinary, want int
	}{
		{"unknown", 0, 1024, (64 << 10) - 1024},
		{"negative unknown", -1, 0, 64 << 10},
		{"small model", 8192, 1024, 1024},
		{"large model", 200000, 10000, 135904},
		{"context occupied", 8192, 3000, 0},
		{"bounded", 1 << 30, 0, int(MaxMessageAttachmentBytes)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NativeTextContextBudget(tt.window, tt.ordinary); got != tt.want {
				t.Fatalf("budget=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestNativeAttachmentToolContextBytes(t *testing.T) {
	for _, tt := range []struct{ window, want int }{{0, 64 << 10}, {8192, 2048}, {200000, 50000}, {1000000, 64 << 10}} {
		if got := NativeAttachmentToolContextBytes(tt.window); got != tt.want {
			t.Fatalf("window=%d budget=%d want=%d", tt.window, got, tt.want)
		}
	}
}
