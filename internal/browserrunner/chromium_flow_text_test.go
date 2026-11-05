package browserrunner

import (
	"encoding/json"
	"testing"

	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/chromedp"
)

func TestFlowTextCallParamsPreserveLiteralText(t *testing.T) {
	for _, text := range []string{"", "plain text", "quotes \" and \\ with\nnewline", "'); throw new Error('not code'); //"} {
		t.Run(text, func(t *testing.T) {
			params, err := flowTextCallParams("exact-node", text)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := jsonv2.Marshal(params, chromedp.DefaultMarshalOptions)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				FunctionDeclaration string `json:"functionDeclaration"`
				ObjectID            string `json:"objectId"`
				ReturnByValue       bool   `json:"returnByValue"`
				Arguments           []struct {
					Value []string `json:"value"`
				} `json:"arguments"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.FunctionDeclaration != fillFlowTextScript || decoded.ObjectID != "exact-node" || !decoded.ReturnByValue {
				t.Fatalf("fixed function/node contract changed: %s", wire)
			}
			if len(decoded.Arguments) != 1 || len(decoded.Arguments[0].Value) != 1 || decoded.Arguments[0].Value[0] != text {
				t.Fatalf("literal argument lost or changed: %s", wire)
			}
		})
	}
}
