package agentadapters

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// semverRe matches the first semver-shaped token in a string:
// MAJOR.MINOR.PATCH with an optional pre-release / build suffix.
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z._-]+)*`)

// detectVersionTimeout is the upper bound on a single DetectVersion
// probe run. Production keeps the 5 s ceiling so a genuinely hung
// adapter binary still surfaces quickly in the operator UI; the
// test binary mutates this var in `version_test.go`'s init() to
// stay independent of CI subprocess-startup jitter.
//
// 5 s is generous on purpose. Probe runs are pre-flight — an
// operator explicitly clicked the adapter's Check action;
// latency is surfaced in the UI as "checking…" while the request
// is in flight, so a few seconds of overhead is acceptable.
// Earlier values (2 s, then 5 s) flaked on CI under -race +
// parallel-suite load: race-mode adds 2-20× CPU overhead, and
// spawning a /bin/sh subprocess can occasionally exceed several
// seconds on a busy runner, returning empty output and failing
// the assertion. Healthy production adapters answer in
// milliseconds; the cap exists to bound the worst case.
var detectVersionTimeout = 5 * time.Second

// DetectVersion runs the adapter binary at path with --version and
// returns the first semver-shaped token found in combined
// stdout/stderr output. Returns an empty string if the binary is
// not reachable, does not respond within detectVersionTimeout, or
// prints no recognisable version string.
func DetectVersion(ctx context.Context, path string) string {
	return detectVersionCommand(ctx, path, "--version")
}

func DetectVersionProbe(ctx context.Context, probe VersionProbe, lookup LookupFunc) string {
	path, ok := resolveVersionProbe(probe, lookup)
	if !ok {
		return ""
	}
	return detectVersionCommand(ctx, path, probe.Args...)
}

func resolveVersionProbe(probe VersionProbe, lookup LookupFunc) (string, bool) {
	path, err := resolveInstalledCommand(probe.Command, probe.CandidatePaths, lookup)
	if err != nil {
		return "", false
	}
	return path, true
}

func detectVersionCommand(ctx context.Context, command string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, detectVersionTimeout)
	defer cancel()

	processEnv, err := prepareGenericProcessEnv(ctx, os.Environ())
	if err != nil {
		return ""
	}
	if processEnv.cleanup != nil {
		defer processEnv.cleanup()
	}
	out, _ := runAgentDiagnostic(ctx, command, args, processEnv.values)
	// Some CLI adapters print version text before exiting non-zero, so prefer
	// any captured semver token before treating the command as unusable.
	version := semverRe.FindString(strings.TrimSpace(out.combined()))
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
	if semverRe.FindString(v) == "" {
		// Development builds can identify an in-process adapter as "embedded".
		// Treat that as unknown instead of incorrectly ordering it below 0.0.0.
		return true
	}
	constraint = strings.TrimSpace(constraint)
	if !strings.HasPrefix(constraint, ">=") {
		// Unknown operator — don't block.
		return true
	}
	bound := strings.TrimSpace(strings.TrimPrefix(constraint, ">="))
	if semverRe.FindString(bound) == "" {
		return true
	}
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
	return compareSemverPrerelease(aPre, bPre)
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

func compareSemverPrerelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	maxLen := max(len(aParts), len(bParts))
	for i := 0; i < maxLen; i++ {
		if i >= len(aParts) {
			return -1
		}
		if i >= len(bParts) {
			return 1
		}
		av, aNumeric := semverPrereleaseNum(aParts[i])
		bv, bNumeric := semverPrereleaseNum(bParts[i])
		switch {
		case aNumeric && bNumeric:
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
		case aNumeric && !bNumeric:
			return -1
		case !aNumeric && bNumeric:
			return 1
		default:
			if aParts[i] < bParts[i] {
				return -1
			}
			if aParts[i] > bParts[i] {
				return 1
			}
		}
	}
	return 0
}

func semverPrereleaseNum(part string) (int64, bool) {
	if part == "" {
		return 0, false
	}
	for _, c := range part {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(part, 10, 64)
	return n, err == nil
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
