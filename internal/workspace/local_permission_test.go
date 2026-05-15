package workspace

import (
	"context"
	"testing"
)

func TestLocalWorkspace_RequestPermission(t *testing.T) {
	t.Parallel()
	ws := NewLocalWorkspace()

	cases := []struct {
		name    string
		req     PermissionRequest
		wantOK  bool
		wantSub string
	}{
		{
			name: "file_write blocked by read-only policy",
			req: PermissionRequest{
				Tool:    "file_write",
				Action:  "write README.md",
				Details: map[string]any{"policy": Policy{ReadOnly: true}},
			},
			wantOK:  false,
			wantSub: "read-only",
		},
		{
			name: "file_write granted with writable policy",
			req: PermissionRequest{
				Tool:    "file_write",
				Action:  "write README.md",
				Details: map[string]any{"policy": Policy{ReadOnly: false}},
			},
			wantOK: true,
		},
		{
			name: "network_fetch blocked when network disabled",
			req: PermissionRequest{
				Tool:    "network_fetch",
				Action:  "fetch https://example.com",
				Details: map[string]any{"policy": Policy{Network: false}},
			},
			wantOK:  false,
			wantSub: "network disabled",
		},
		{
			name: "shell tool granted by default",
			req: PermissionRequest{
				Tool:   "shell",
				Action: "ls",
			},
			wantOK: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision, err := ws.RequestPermission(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("RequestPermission: %v", err)
			}
			if decision.Granted != tc.wantOK {
				t.Fatalf("Granted = %v; want %v (reason: %q)", decision.Granted, tc.wantOK, decision.Reason)
			}
			if tc.wantSub != "" && !contains(decision.Reason, tc.wantSub) {
				t.Fatalf("Reason = %q; want to contain %q", decision.Reason, tc.wantSub)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
