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
				for _, want := range []string{"No write token", "no PR", "just cursor-agent-update", "human-only push allowlist"} {
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
		"Create protection reader token",
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
	reader := workflowStep(t, workflow, "Create protection reader token")
	if !strings.Contains(reader, "permission-administration: read") || strings.Contains(reader, ": write") {
		t.Fatal("protection inspection must use an explicitly read-only token")
	}
	writer := workflowStep(t, workflow, "Create updater App token")
	if strings.Contains(writer, "permission-administration:") || !strings.Contains(writer, "permission-contents: write") || !strings.Contains(writer, "permission-pull-requests: write") {
		t.Fatal("publication token must request only Contents and Pull requests write")
	}
	for _, token := range []string{reader, writer} {
		if strings.Contains(token, "owner:") || strings.Contains(token, "repositories:") || strings.Contains(token, "skip-token-revoke:") {
			t.Fatal("tokens must retain current-repository scope and automatic revocation")
		}
	}
	gate := workflowStep(t, workflow, "Require protected default branch")
	if !strings.Contains(gate, "GH_TOKEN: ${{ steps.protection-token.outputs.token }}") {
		t.Fatal("protection gate must use the read-only App token")
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
		name         string
		mutate       func(map[string]any)
		mutateApp    func(map[string]any)
		wantError    bool
		apiFailure   string
		malformedAPI string
	}{
		{name: "human maintainer with zero required approvals"},
		{name: "team allowlist", mutate: func(p map[string]any) {
			r := p["restrictions"].(map[string]any)
			r["users"] = []any{}
			r["teams"] = []any{map[string]any{"id": 42}}
		}},
		{name: "additional review policy accepted", mutate: func(p map[string]any) {
			p["required_pull_request_reviews"] = map[string]any{"required_approving_review_count": 2, "require_last_push_approval": true}
		}},
		{name: "missing deletion protection", wantError: true, mutate: func(p map[string]any) { delete(p, "allow_deletions") }},
		{name: "deletion allowed", wantError: true, mutate: func(p map[string]any) { p["allow_deletions"] = map[string]any{"enabled": true} }},
		{name: "force push allowed", wantError: true, mutate: func(p map[string]any) { p["allow_force_pushes"] = map[string]any{"enabled": true} }},
		{name: "missing force push setting", wantError: true, mutate: func(p map[string]any) { delete(p, "allow_force_pushes") }},
		{name: "administrator bypass", wantError: true, mutate: func(p map[string]any) { p["enforce_admins"] = map[string]any{"enabled": false} }},
		{name: "missing administrator setting", wantError: true, mutate: func(p map[string]any) { delete(p, "enforce_admins") }},
		{name: "malformed administrator setting", wantError: true, mutate: func(p map[string]any) { p["enforce_admins"] = map[string]any{"enabled": "true"} }},
		{name: "no required PR", wantError: true, mutate: func(p map[string]any) { p["required_pull_request_reviews"] = nil }},
		{name: "malformed PR requirement", wantError: true, mutate: func(p map[string]any) { p["required_pull_request_reviews"] = []any{} }},
		{name: "non strict checks", wantError: true, mutate: func(p map[string]any) { p["required_status_checks"].(map[string]any)["strict"] = false }},
		{name: "wrong required check", wantError: true, mutate: func(p map[string]any) { p["required_status_checks"].(map[string]any)["contexts"] = []string{"Other"} }},
		{name: "missing required checks", wantError: true, mutate: func(p map[string]any) { delete(p, "required_status_checks") }},
		{name: "no restrictions", wantError: true, mutate: func(p map[string]any) { p["restrictions"] = nil }},
		{name: "app allowed", wantError: true, mutate: func(p map[string]any) { p["restrictions"].(map[string]any)["apps"] = []any{map[string]any{"id": 77}} }},
		{name: "hidden apps", wantError: true, mutate: func(p map[string]any) { delete(p["restrictions"].(map[string]any), "apps") }},
		{name: "null apps", wantError: true, mutate: func(p map[string]any) { p["restrictions"].(map[string]any)["apps"] = nil }},
		{name: "malformed apps", wantError: true, mutate: func(p map[string]any) { p["restrictions"].(map[string]any)["apps"] = map[string]any{} }},
		{name: "no maintainers", wantError: true, mutate: func(p map[string]any) { p["restrictions"].(map[string]any)["users"] = []any{} }},
		{name: "missing users", wantError: true, mutate: func(p map[string]any) { delete(p["restrictions"].(map[string]any), "users") }},
		{name: "missing teams", wantError: true, mutate: func(p map[string]any) { delete(p["restrictions"].(map[string]any), "teams") }},
		{name: "bot user", wantError: true, mutate: func(p map[string]any) {
			p["restrictions"].(map[string]any)["users"] = []any{map[string]any{"id": 77, "type": "Bot"}}
		}},
		{name: "malformed user", wantError: true, mutate: func(p map[string]any) {
			p["restrictions"].(map[string]any)["users"] = []any{map[string]any{"type": "User"}}
		}},
		{name: "malformed team", wantError: true, mutate: func(p map[string]any) {
			p["restrictions"].(map[string]any)["teams"] = []any{map[string]any{"id": "42"}}
		}},
		{name: "app admin write", wantError: true, mutateApp: func(a map[string]any) { a["permissions"].(map[string]any)["administration"] = "write" }},
		{name: "missing app admin read", wantError: true, mutateApp: func(a map[string]any) { delete(a["permissions"].(map[string]any), "administration") }},
		{name: "extra app write", wantError: true, mutateApp: func(a map[string]any) { a["permissions"].(map[string]any)["workflows"] = "write" }},
		{name: "missing contents write", wantError: true, mutateApp: func(a map[string]any) { a["permissions"].(map[string]any)["contents"] = "read" }},
		{name: "missing PR write", wantError: true, mutateApp: func(a map[string]any) { delete(a["permissions"].(map[string]any), "pull_requests") }},
		{name: "wrong app", wantError: true, mutateApp: func(a map[string]any) { a["slug"] = "another-app" }},
		{name: "wrong client", wantError: true, mutateApp: func(a map[string]any) { a["client_id"] = "Iv.different" }},
		{name: "protection API failure", wantError: true, apiFailure: "protection"},
		{name: "app API failure", wantError: true, apiFailure: "app"},
		{name: "malformed protection JSON", wantError: true, malformedAPI: "protection"},
		{name: "malformed app JSON", wantError: true, malformedAPI: "app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			protection := map[string]any{
				"enforce_admins":                map[string]any{"enabled": true},
				"allow_deletions":               map[string]any{"enabled": false},
				"allow_force_pushes":            map[string]any{"enabled": false},
				"required_pull_request_reviews": map[string]any{"required_approving_review_count": 0},
				"required_status_checks":        map[string]any{"strict": true, "contexts": []string{"Required checks"}},
				"restrictions": map[string]any{
					"apps": []any{}, "users": []any{map[string]any{"id": 42, "type": "User"}}, "teams": []any{},
				},
			}
			app := map[string]any{
				"slug": "cursor-updater", "client_id": "Iv.test",
				"permissions": map[string]any{"administration": "read", "contents": "write", "pull_requests": "write", "metadata": "read"},
			}
			if test.mutate != nil {
				test.mutate(protection)
			}
			if test.mutateApp != nil {
				test.mutateApp(app)
			}
			root := t.TempDir()
			for name, value := range map[string]any{"protection": protection, "app": app} {
				body, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if test.malformedAPI == name {
					body = []byte("not JSON")
				}
				if err := os.WriteFile(filepath.Join(root, name+".json"), body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Replace only the read-only API client. Execute the actual workflow
			// filter so protection expectations cannot drift from the test.
			prefix := `gh() {
  case "${*: -1}" in
    apps/cursor-updater) fixture=app ;;
    repos/hecatehq/hecate/branches/master/protection) fixture=protection ;;
    *) return 99 ;;
  esac
  [ "${API_FAILURE}" != "${fixture}" ] || return 1
  cat "${RUNNER_TEMP}/${fixture}.json"
};
`
			result, err := runWorkflowShell(t, prefix+script, "API_FAILURE="+test.apiFailure, "RUNNER_TEMP="+root,
				"GITHUB_REPOSITORY=hecatehq/hecate", "APP_SLUG=cursor-updater", "APP_CLIENT_ID=Iv.test", "GITHUB_STEP_SUMMARY="+filepath.Join(root, "summary"))
			if (err != nil) != test.wantError {
				t.Fatalf("protection gate error = %v, output = %s", err, result)
			}
			if test.wantError {
				if _, err := os.Stat(filepath.Join(root, "summary")); !os.IsNotExist(err) {
					t.Fatal("failed gate must never report a verified publication boundary")
				}
			} else if !strings.Contains(readWorkflowTestFile(t, filepath.Join(root, "summary")), "cannot push or merge") {
				t.Fatal("successful gate must report the verified publication boundary")
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
