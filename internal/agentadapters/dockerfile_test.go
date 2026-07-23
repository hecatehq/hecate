package agentadapters

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDockerfilesUseEmbeddedACPAdapters(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Dockerfile", "Dockerfile.release"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(filepath.Join("..", "..", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			text := string(raw)
			for _, required := range []string{
				"@openai/codex@${OPENAI_CODEX_VERSION}",
				"@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}",
			} {
				if !strings.Contains(text, required) {
					t.Fatalf("%s is missing provider CLI contract %q", name, required)
				}
			}
			for _, forbidden := range []string{
				"adapter-downloader",
				"ACP_ADAPTER_VERSION",
				"/usr/local/bin/codex-acp-adapter",
				"/usr/local/bin/claude-code-acp-adapter",
				"@hecatehq/codex-acp-adapter",
				"@hecatehq/claude-code-acp-adapter",
				"@zed-industries/codex-acp",
				"@agentclientprotocol/claude-agent-acp",
			} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s contains standalone ACP adapter packaging %q", name, forbidden)
				}
			}
		})
	}
}

func TestDockerfilesPinAndVerifySameCursorAgentArtifacts(t *testing.T) {
	t.Parallel()

	dev := readDockerfile(t, "Dockerfile")
	release := readDockerfile(t, "Dockerfile.release")
	versionPattern := regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2}-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	checksumPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)

	for _, arg := range []string{
		"CURSOR_AGENT_VERSION",
		"CURSOR_AGENT_LINUX_X64_SHA256",
		"CURSOR_AGENT_LINUX_ARM64_SHA256",
	} {
		devValue := dockerfileArgValue(dev, arg)
		releaseValue := dockerfileArgValue(release, arg)
		if devValue == "" || releaseValue == "" {
			t.Fatalf("%s = dev:%q release:%q, want both Dockerfiles pinned", arg, devValue, releaseValue)
		}
		if devValue != releaseValue {
			t.Fatalf("%s drifted: Dockerfile=%s Dockerfile.release=%s", arg, devValue, releaseValue)
		}
		if arg == "CURSOR_AGENT_VERSION" && !versionPattern.MatchString(devValue) {
			t.Fatalf("%s = %q, want a versioned Cursor Agent artifact", arg, devValue)
		}
		if strings.HasSuffix(arg, "SHA256") && !checksumPattern.MatchString(devValue) {
			t.Fatalf("%s = %q, want a lowercase SHA-256", arg, devValue)
		}
	}

	for _, dockerfile := range []struct {
		name string
		text string
	}{
		{name: "Dockerfile", text: dev},
		{name: "Dockerfile.release", text: release},
	} {
		for _, required := range []string{
			`amd64) cursor_arch=x64`,
			`arm64) cursor_arch=arm64`,
			`https://downloads.cursor.com/lab/${CURSOR_AGENT_VERSION}/linux/${cursor_arch}/agent-cli-package.tar.gz`,
			`printf '%s  %s\n' "${cursor_sha256}" "${cursor_archive}" | sha256sum -c -`,
			`tar --no-same-owner --no-same-permissions --strip-components=1 -xzf "${cursor_archive}"`,
			`test -x "${cursor_dir}/cursor-agent"`,
			`test -x "${cursor_dir}/node"`,
		} {
			if !strings.Contains(dockerfile.text, required) {
				t.Errorf("%s is missing Cursor Agent artifact contract %q", dockerfile.name, required)
			}
		}
		for _, forbidden := range []string{
			"https://cursor.com/install",
			"CURSOR_INSTALL_SHA256",
			"CURSOR_INSTALL_URL",
			"cursor-install.sh",
		} {
			if strings.Contains(dockerfile.text, forbidden) {
				t.Errorf("%s still uses mutable Cursor installer contract %q", dockerfile.name, forbidden)
			}
		}
		checksumAt := strings.Index(dockerfile.text, "sha256sum -c -")
		extractAt := strings.Index(dockerfile.text, "tar --no-same-owner")
		if checksumAt == -1 || extractAt == -1 || checksumAt > extractAt {
			t.Errorf("%s must verify the Cursor Agent archive before extracting it", dockerfile.name)
		}
	}
}

func TestCursorAgentUpdateWorkflowIsReviewOnly(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github/workflows/cursor-agent-update.yml")
	for _, required := range []string{
		"schedule:",
		"workflow_dispatch:",
		"contents: read",
		"github.ref == 'refs/heads/master'",
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1",
		"persist-credentials: false",
		"permission-contents: write",
		"permission-pull-requests: write",
		"go run ./scripts/cursoragentupdate",
		"--existing-proposal-root",
		"automation/cursor-agent-update",
		"git ls-remote --exit-code --heads",
		"git merge-base --is-ancestor",
		"Existing automation proposal contains unrelated tracked changes",
		"EXISTING_PROPOSAL_PR",
		"cursor-agent-open-prs.json",
		`head=${GITHUB_REPOSITORY_OWNER}:${UPDATE_BRANCH}`,
		"refusing ambiguous publication",
		"refs/remotes/origin/${UPDATE_BRANCH}^",
		"git diff --quiet \"refs/remotes/origin/${UPDATE_BRANCH}\" --",
		"publish=false",
		"repos/${GITHUB_REPOSITORY}/rules/branches/master?per_page=100",
		`.type == "deletion"`,
		`.type == "non_fast_forward"`,
		`.parameters.require_last_push_approval`,
		`.context == "Required checks"`,
		`.parameters.strict_required_status_checks_policy`,
		"--force-with-lease",
		"gh pr create",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("cursor-agent-update.yml is missing automation contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"pull_request_target:",
		"actions: write",
		"gh pr merge",
		"--auto",
		"mapfile -t open_pr_numbers < <(gh pr list",
		"gh pr list",
		`git diff --name-only "${branch_parent}" "${branch_ref}" |`,
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("cursor-agent-update.yml contains unsafe automation %q", forbidden)
		}
	}
	validationAt := strings.Index(workflow, "go run ./scripts/cursoragentupdate")
	protectionAt := strings.Index(workflow, "name: Require protected default branch")
	writeTokenAt := strings.Index(workflow, "name: Create updater App token")
	if validationAt == -1 || protectionAt == -1 || writeTokenAt == -1 ||
		protectionAt < validationAt || writeTokenAt < protectionAt {
		t.Error("cursor-agent-update.yml must mint its write token only after artifact and branch-rule validation")
	}
}

func TestMainWorkflowExposesStableRequiredCheck(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github/workflows/test.yml")
	for _, required := range []string{
		"required:",
		"name: Required checks",
		"if: always()",
		"NEEDS_JSON: ${{ toJSON(needs) }}",
		`all(.[]; .result == "success" or .result == "skipped")`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("test.yml is missing stable required-check contract %q", required)
		}
	}
}

func readDockerfile(t testing.TB, name string) string {
	t.Helper()
	return readRepoFile(t, name)
}

func readRepoFile(t testing.TB, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

func dockerfileArgValue(text string, name string) string {
	prefix := "ARG " + name + "="
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
