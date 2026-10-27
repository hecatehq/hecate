package browserrunner

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateFlowRequestTextEntry(t *testing.T) {
	t.Parallel()
	value := "hello\nworld"
	action := FlowAction{Kind: FlowActionFillText, Role: "textbox", Name: "Message", Text: &value}
	request := FlowRequest{URL: "https://app.example.test/", AllowedOrigin: "https://app.example.test", Actions: []FlowAction{action}}
	got, err := ValidateFlowRequest(request)
	if err != nil || got.Actions[0].Text == nil || *got.Actions[0].Text != value {
		t.Fatalf("text flow: %+v, %v", got, err)
	}
	*got.Actions[0].Text = "changed"
	if value != "hello\nworld" {
		t.Fatal("validated text aliases caller")
	}
	for name, mutate := range map[string]func(*FlowAction){
		"missing text":  func(a *FlowAction) { a.Text = nil },
		"wrong role":    func(a *FlowAction) { a.Role = "button" },
		"text on click": func(a *FlowAction) { a.Kind = FlowActionClick; a.Role = "button" },
		"text on wait":  func(a *FlowAction) { a.Kind = FlowActionWaitFor },
		"oversize":      func(a *FlowAction) { text := strings.Repeat("x", MaxFlowTextBytes+1); a.Text = &text },
		"control":       func(a *FlowAction) { text := "x\x00y"; a.Text = &text },
		"bidi":          func(a *FlowAction) { text := "x\u202ey"; a.Text = &text },
		"utf8":          func(a *FlowAction) { text := string([]byte{255}); a.Text = &text },
	} {
		t.Run(name, func(t *testing.T) {
			bad := action
			mutate(&bad)
			request.Actions = []FlowAction{bad}
			if _, err := ValidateFlowRequest(request); !errors.Is(err, ErrInvalidFlow) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	empty := ""
	request.Actions = []FlowAction{{Kind: FlowActionFillText, Role: "searchbox", Name: "Search", Text: &empty}}
	if _, err := ValidateFlowRequest(request); err != nil {
		t.Fatalf("explicit clear: %v", err)
	}
}
