package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdaterWorkflowPublicationModes(t *testing.T) {
	workflow := updaterWorkflow(t)
	script := workflowRunScript(t, workflowStep(t, workflow, "Resolve publication mode"))
	for _, test := range []struct {
		name, clientID, privateKey, mode string
		wantError                        bool
	}{
		{name: "unconfigured", clientID: "false", privateKey: "false", mode: "check-only"},
		{name: "configured", clientID: "true", privateKey: "true", mode: "publish"},
		{name: "missing key", clientID: "true", privateKey: "false", wantError: true},
		{name: "missing id", clientID: "false", privateKey: "true", wantError: true},
		{name: "invalid flags", clientID: "unknown", privateKey: "false", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "output")
			summary := filepath.Join(root, "summary")
			result, err := runWorkflowShell(t, script,
				"HAS_APP_CLIENT_ID="+test.clientID,
				"HAS_APP_PRIVATE_KEY="+test.privateKey,
				"GITHUB_OUTPUT="+output,
				"GITHUB_STEP_SUMMARY="+summary,
			)
			if (err != nil) != test.wantError {
				t.Fatalf("publication mode error = %v, output = %s", err, result)
			}
			if test.wantError {
				if !strings.Contains(result, "::error::Incomplete updater App configuration") {
					t.Fatalf("missing actionable setup failure: %s", result)
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("partial configuration must not publish an output mode")
				}
				return
			}
			if got := readWorkflowTestFile(t, output); got != "mode="+test.mode+"\n" {
				t.Fatalf("publication output = %q", got)
			}
			if test.mode == "check-only" {
				if !strings.Contains(result, "::warning::") {
					t.Fatal("check-only mode must warn that publication is unavailable")
				}
				for _, want := range []string{"No write token", "no PR", "just cursor-agent-update", "latest-push approval"} {
					if !strings.Contains(readWorkflowTestFile(t, summary), want) {
						t.Errorf("check-only summary missing %q", want)
					}
				}
			}
		})
	}
}

func TestUpdaterWorkflowPublicationKeepsProtectionGate(t *testing.T) {
	workflow := updaterWorkflow(t)
	const condition = "if: steps.publication.outputs.mode == 'publish' && steps.update.outputs.publish == 'true'"
	for _, name := range []string{
		"Require protected default branch",
		"Create updater App token",
		"Resolve updater App identity",
		"Commit and push reviewed inputs",
		"Open or refresh review PR",
	} {
		if !strings.Contains(workflowStep(t, workflow, name), condition) {
			t.Errorf("%s must require both configured publication and a changed proposal", name)
		}
	}
	if strings.Index(workflow, "- name: Require protected default branch") > strings.Index(workflow, "- name: Create updater App token") {
		t.Fatal("protection gate must run before creating a write token")
	}
	validation := workflowStep(t, workflow, "Validate artifacts and update pins")
	if strings.Contains(validation, "        if:") || !strings.Contains(validation, "go run ./scripts/cursoragentupdate") {
		t.Fatal("artifact validation must run in both publication modes")
	}
	if !strings.Contains(validation, "if [ \"${PUBLICATION_MODE}\" = 'check-only' ]; then\n            publish=false") {
		t.Fatal("check-only must report publication disabled even when artifacts changed")
	}
	if !strings.Contains(validation, "- Publication mode:") {
		t.Fatal("validation summary must distinguish publication from check-only")
	}
	configuration := workflowStep(t, workflow, "Resolve publication mode")
	for _, want := range []string{
		"HAS_APP_CLIENT_ID: ${{ vars.CURSOR_UPDATE_APP_CLIENT_ID != '' }}",
		"HAS_APP_PRIVATE_KEY: ${{ secrets.CURSOR_UPDATE_APP_PRIVATE_KEY != '' }}",
	} {
		if !strings.Contains(configuration, want) {
			t.Errorf("mode resolution must consume presence flags, not credentials: missing %s", want)
		}
	}
}

func TestUpdaterWorkflowProtectionRulesRemainFailClosed(t *testing.T) {
	workflow := updaterWorkflow(t)
	script := workflowRunScript(t, workflowStep(t, workflow, "Require protected default branch"))
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required to execute the workflow's protection filter")
	}
	for _, test := range []struct {
		name       string
		mutate     func([]map[string]any) []map[string]any
		wantError  bool
		apiFailure bool
	}{
		{name: "fully protected"},
		{name: "current repository requires no approval", wantError: true, mutate: func(rules []map[string]any) []map[string]any {
			parameters := rules[2]["parameters"].(map[string]any)
			parameters["required_approving_review_count"] = 0
			parameters["require_last_push_approval"] = false
			return rules
		}},
		{name: "missing deletion protection", wantError: true, mutate: func(rules []map[string]any) []map[string]any { return rules[1:] }},
		{name: "missing force push protection", wantError: true, mutate: func(rules []map[string]any) []map[string]any { return append(rules[:1], rules[2:]...) }},
		{name: "stale reviews allowed", wantError: true, mutate: func(rules []map[string]any) []map[string]any {
			rules[2]["parameters"].(map[string]any)["dismiss_stale_reviews_on_push"] = false
			return rules
		}},
		{name: "last push not approved", wantError: true, mutate: func(rules []map[string]any) []map[string]any {
			rules[2]["parameters"].(map[string]any)["require_last_push_approval"] = false
			return rules
		}},
		{name: "non strict checks", wantError: true, mutate: func(rules []map[string]any) []map[string]any {
			rules[3]["parameters"].(map[string]any)["strict_required_status_checks_policy"] = false
			return rules
		}},
		{name: "wrong required check", wantError: true, mutate: func(rules []map[string]any) []map[string]any {
			rules[3]["parameters"].(map[string]any)["required_status_checks"] = []map[string]string{{"context": "unrelated"}}
			return rules
		}},
		{name: "rules API failure", wantError: true, apiFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rules := []map[string]any{
				{"type": "deletion"},
				{"type": "non_fast_forward"},
				{"type": "pull_request", "parameters": map[string]any{
					"required_approving_review_count": 1,
					"dismiss_stale_reviews_on_push":   true,
					"require_last_push_approval":      true,
				}},
				{"type": "required_status_checks", "parameters": map[string]any{
					"strict_required_status_checks_policy": true,
					"required_status_checks":               []map[string]string{{"context": "Required checks"}},
				}},
			}
			if test.mutate != nil {
				rules = test.mutate(rules)
			}
			body, err := json.Marshal(rules)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			fixture := filepath.Join(root, "rules.json")
			if err := os.WriteFile(fixture, body, 0o600); err != nil {
				t.Fatal(err)
			}
			// Replace only the read-only API client. Execute the actual workflow
			// filter so protection expectations cannot drift from the test.
			prefix := "gh() { cat \"${RULE_FIXTURE}\"; };\n"
			if test.apiFailure {
				prefix = "gh() { return 1; };\n"
			}
			result, err := runWorkflowShell(t, prefix+script, "RULE_FIXTURE="+fixture, "RUNNER_TEMP="+root, "GITHUB_REPOSITORY=hecatehq/hecate")
			if (err != nil) != test.wantError {
				t.Fatalf("protection gate error = %v, output = %s", err, result)
			}
		})
	}
}

func updaterWorkflow(t *testing.T) string {
	t.Helper()
	return readWorkflowTestFile(t, filepath.Join("..", "..", ".github", "workflows", "cursor-agent-update.yml"))
}

func workflowStep(t *testing.T, workflow, name string) string {
	t.Helper()
	_, rest, ok := strings.Cut(workflow, "      - name: "+name+"\n")
	if !ok {
		t.Fatalf("missing workflow step %q", name)
	}
	step, _, _ := strings.Cut(rest, "\n      - ")
	return step
}

func workflowRunScript(t *testing.T, step string) string {
	t.Helper()
	_, script, ok := strings.Cut(step, "        run: |\n")
	if !ok {
		t.Fatal("step has no shell script")
	}
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			break
		}
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(lines, "\n")
}

func runWorkflowShell(t *testing.T, script string, env ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to execute workflow shell regression tests")
	}
	command := exec.Command(bash, "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", script)
	command.Env = append(os.Environ(), env...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func readWorkflowTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
