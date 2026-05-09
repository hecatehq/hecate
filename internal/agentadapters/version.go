package agentadapters

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// semverRe matches the first semver-shaped token in a string:
// MAJOR.MINOR.PATCH with an optional pre-release / build suffix.
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z._-]+)*`)

// DetectVersion runs the adapter binary at path with --version and returns
// the first semver-shaped token found in stdout. Returns an empty string if
// the binary is not reachable, does not respond in ~5 s, or prints no
// recognisable version string.
//
// The 5 s ceiling is generous on purpose. Probe runs are pre-flight: an
// operator clicked "Test adapter" or opened the Settings tab; latency is
// surfaced in the UI as "checking…" while the request is in flight, so a
// few seconds of overhead is acceptable. The earlier 2 s cap flaked on
// CI under -race + parallel-suite load — race-mode adds 2-20× CPU
// overhead, and spawning a /bin/sh subprocess could occasionally exceed
// 2 s, returning empty and failing the assertion. Genuinely hung
// binaries still surface within the cap; healthy ones answer well below.
func DetectVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	out, _ := cmd.CombinedOutput()
	// Some CLI adapters print version text before exiting non-zero, so prefer
	// any captured semver token before treating the command as unusable.
	version := semverRe.FindString(strings.TrimSpace(string(out)))
	if version != "" {
		return version
	}
	return ""
}

// satisfiesRange reports whether version v satisfies a simple constraint of
// the form ">=MAJOR.MINOR.PATCH". Only ">=" is supported for now — the goal
// is the plumbing, not a full semver resolver.
//
// Returns true when:
//   - constraint is empty (no restriction defined)
//   - v is empty (version unknown — we cannot reject what we cannot read)
//   - v is greater than or equal to the bound embedded in the constraint
func satisfiesRange(v, constraint string) bool {
	if constraint == "" || v == "" {
		return true
	}
	constraint = strings.TrimSpace(constraint)
	if !strings.HasPrefix(constraint, ">=") {
		// Unknown operator — don't block.
		return true
	}
	bound := strings.TrimSpace(strings.TrimPrefix(constraint, ">="))
	return semverCmp(v, bound) >= 0
}

// semverCmp compares two semver strings. Numeric MAJOR.MINOR.PATCH segments
// drive ordering; when those match, a pre-release is lower than the matching
// release. Build metadata is ignored.
func semverCmp(a, b string) int {
	aParts := semverNums(a)
	bParts := semverNums(b)
	for i := 0; i < 3; i++ {
		av := semverNum(aParts, i)
		bv := semverNum(bParts, i)
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	aPre := semverPrerelease(a)
	bPre := semverPrerelease(b)
	if aPre != "" && bPre == "" {
		return -1
	}
	if aPre == "" && bPre != "" {
		return 1
	}
	if aPre < bPre {
		return -1
	}
	if aPre > bPre {
		return 1
	}
	return 0
}

func semverNums(v string) []string {
	// Strip pre-release / build suffix before splitting.
	for i, c := range v {
		if c == '-' || c == '+' {
			v = v[:i]
			break
		}
	}
	return strings.SplitN(v, ".", 3)
}

func semverPrerelease(v string) string {
	plus := strings.IndexRune(v, '+')
	if plus >= 0 {
		v = v[:plus]
	}
	dash := strings.IndexRune(v, '-')
	if dash < 0 {
		return ""
	}
	return v[dash+1:]
}

func semverNum(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n := 0
	for _, c := range parts[i] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
