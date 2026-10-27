package browserrunner

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChromiumBrowserFlowSmokeTextEntry(t *testing.T) {
	executable := requireBrowserSmoke(t)
	for _, test := range []struct {
		name, field, role string
		allowed           bool
	}{
		{"text", `<input aria-label="Field" type="text">`, "textbox", true},
		{"search", `<input aria-label="Field" type="search">`, "searchbox", true},
		{"textarea", `<textarea aria-label="Field"></textarea>`, "textbox", true},
		{"password", `<input aria-label="Field" type="password">`, "textbox", false},
		{"credential", `<input aria-label="Field" autocomplete="username">`, "textbox", false},
		{"autofill", `<input aria-label="Field" autocomplete="on">`, "textbox", false},
		{"disabled", `<input aria-label="Field" disabled>`, "textbox", false},
		{"disabled_fieldset", `<fieldset disabled><input aria-label="Field"></fieldset>`, "textbox", false},
		{"hidden", `<input aria-label="Field" hidden>`, "textbox", false},
		{"invisible_ancestor", `<div style="opacity:0"><input aria-label="Field"></div>`, "textbox", false},
		{"inert_ancestor", `<div inert><input aria-label="Field"></div>`, "textbox", false},
		{"readonly", `<input aria-label="Field" readonly>`, "textbox", false},
		{"contenteditable", `<div role="textbox" aria-label="Field" contenteditable="true">old</div>`, "textbox", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprintf(w, `<!doctype html><html><body><form onsubmit="document.title='submitted';return false">%s</form><div role="status" aria-label="Filled" hidden></div><script>
document.querySelector('form').addEventListener('input', e => { if (e.target.value === 'sample text') document.querySelector('[role=status]').hidden = false; });
</script></body></html>`, test.field)
			}))
			defer server.Close()
			runner, err := New(Config{ExecutablePath: executable, Timeout: 20 * time.Second, AllowPrivateIPs: true})
			if err != nil {
				t.Fatal(err)
			}
			value := "sample text"
			actions := []FlowAction{{Kind: FlowActionFillText, Role: test.role, Name: "Field", Text: &value}}
			if test.allowed {
				actions = append(actions, FlowAction{Kind: FlowActionWaitFor, Role: "status", Name: "Filled"})
			}
			result, err := runner.RunFlow(t.Context(), FlowRequest{URL: server.URL, AllowedOrigin: browserSmokeOrigin(t, server.URL), Actions: actions})
			if test.allowed {
				if err != nil || len(result.Actions) != 2 || result.Title == "submitted" {
					t.Fatalf("fill = %+v %v", result, err)
				}
				for _, node := range result.FinalAccessibility {
					if strings.Contains(node.Name+node.Description, value) {
						t.Fatal("form value retained in evidence")
					}
				}
			} else if !errors.Is(err, ErrFlowTargetUnavailable) && !errors.Is(err, ErrFlowTargetNotFound) {
				t.Fatalf("unsafe fill = %+v %v", result, err)
			}
		})
	}
}
