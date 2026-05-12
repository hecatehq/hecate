package api

import (
	"errors"
	"testing"
)

func TestWorkspaceDialogCancelled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		output string
		want   bool
	}{
		{
			name:   "american apple script cancellation",
			err:    errors.New("exit status 1"),
			output: "execution error: User canceled. (-128)",
			want:   true,
		},
		{
			name:   "british apple script cancellation",
			err:    errors.New("exit status 1"),
			output: "execution error: User cancelled. (-128)",
			want:   true,
		},
		{
			name:   "apple script cancellation code",
			err:    errors.New("exit status 1"),
			output: "execution error: something localized (-128)",
			want:   true,
		},
		{
			name:   "nil error is not cancellation",
			err:    nil,
			output: "execution error: User canceled. (-128)",
			want:   false,
		},
		{
			name:   "different osascript error",
			err:    errors.New("exit status 1"),
			output: "execution error: Can't get application \"Finder\".",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isWorkspaceDialogCancelled(tt.err, tt.output); got != tt.want {
				t.Fatalf("isWorkspaceDialogCancelled() = %v, want %v", got, tt.want)
			}
		})
	}
}
