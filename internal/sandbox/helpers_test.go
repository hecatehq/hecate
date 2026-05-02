package sandbox

import (
	"strings"
	"testing"
)

func TestPolicyErrorErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		err  *PolicyError
		want string
	}{
		{"nil receiver returns generic", nil, "sandbox policy denied"},
		{"empty reason returns generic", &PolicyError{Reason: ""}, "sandbox policy denied"},
		{"whitespace reason returns generic", &PolicyError{Reason: "   "}, "sandbox policy denied"},
		{"reason is appended", &PolicyError{Reason: "no exec"}, "sandbox policy denied: no exec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePathRequiresTarget(t *testing.T) {
	cases := []string{"", "   "}
	for _, target := range cases {
		t.Run("empty="+target, func(t *testing.T) {
			if _, err := ResolvePath(".", target, Policy{}); err == nil {
				t.Errorf("ResolvePath(target=%q) → err = nil, want error", target)
			}
		})
	}
}

func TestResolvePathEnforcesAllowedRoot(t *testing.T) {
	root := t.TempDir()

	// Inside the root: must succeed.
	resolved, err := ResolvePath(root, "subdir/file.txt", Policy{AllowedRoot: root})
	if err != nil {
		t.Fatalf("ResolvePath inside root: %v", err)
	}
	if !strings.HasPrefix(resolved, root) {
		t.Errorf("resolved %q does not start with root %q", resolved, root)
	}

	// Escapes the root: must return PolicyError.
	if _, err := ResolvePath(root, "../escape.txt", Policy{AllowedRoot: root}); err == nil {
		t.Error("expected PolicyError on path that escapes allowed root")
	} else if !IsPolicyDenied(err) {
		t.Errorf("expected PolicyError, got %T: %v", err, err)
	}
}

func TestCommandMutatesStateDetectsRedirects(t *testing.T) {
	// Both " >" and ">>" patterns should be classified as mutating.
	mutating := []string{
		"echo hi > /tmp/out",
		"echo hi >> /tmp/out",
		"rm -rf /tmp/foo",
		"git add .",
	}
	for _, cmd := range mutating {
		if !commandMutatesState(cmd) {
			t.Errorf("commandMutatesState(%q) = false, want true", cmd)
		}
	}

	nonMutating := []string{
		"ls -la",
		"echo hi",
		"git status",
		"cat /etc/hosts",
	}
	for _, cmd := range nonMutating {
		if commandMutatesState(cmd) {
			t.Errorf("commandMutatesState(%q) = true, want false", cmd)
		}
	}
}
