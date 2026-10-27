package browserrunner

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/go-json-experiment/json/jsontext"
)

func TestFlowTransportGuardBlocksBrowserManagedSpeechRecognition(t *testing.T) {
	t.Parallel()
	for _, constructor := range []string{"'SpeechRecognition'", "'webkitSpeechRecognition'"} {
		if !strings.Contains(flowTransportGuardScript, constructor) {
			t.Fatalf("flow transport guard omits %s", constructor)
		}
	}
}

func TestFlowAccessibilityEvidenceOmitsValueControlDescendants(t *testing.T) {
	t.Parallel()
	nodes := []*accessibility.Node{
		{NodeID: "input", Role: accessibilityTestValue(`"textbox"`), Name: accessibilityTestValue(`"Search"`)},
		{NodeID: "wrapper", ParentID: "input", Ignored: true},
		{NodeID: "value", ParentID: "wrapper", Role: accessibilityTestValue(`"StaticText"`), Name: accessibilityTestValue(`"private value"`)},
		{NodeID: "status", Role: accessibilityTestValue(`"status"`), Name: accessibilityTestValue(`"Ready"`)},
	}
	got, truncated, err := flowAccessibilityEvidence(nodes, 64)
	if err != nil || truncated || len(got) != 2 || got[0].Name != "Search" || got[1].Name != "Ready" {
		t.Fatalf("evidence = %+v truncated=%v err=%v", got, truncated, err)
	}
}

func TestFlowActionWaitErrorDistinguishesParentCancellation(t *testing.T) {
	t.Parallel()
	if err := flowActionWaitError(context.Background()); !errors.Is(err, ErrFlowTargetNotFound) {
		t.Fatalf("action-local timeout = %v, want target not found", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := flowActionWaitError(ctx); !errors.Is(err, ErrInspectionFailed) || errors.Is(err, ErrFlowTargetNotFound) {
		t.Fatalf("parent cancellation = %v, want generic inspection failure", err)
	}
}

func TestFlowQuadCenterRejectsMalformedCoordinates(t *testing.T) {
	t.Parallel()
	if x, y, ok := flowQuadCenter([]dom.Quad{{0, 0, 10, 0, 10, 20, 0, 20}}); !ok || x != 5 || y != 10 {
		t.Fatalf("flowQuadCenter() = %v,%v,%v, want 5,10,true", x, y, ok)
	}
	for _, quads := range [][]dom.Quad{nil, {{0, 0, 1}}, {{0, 0, 1, 0, 1, 1, 0, math.NaN()}}} {
		if _, _, ok := flowQuadCenter(quads); ok {
			t.Fatalf("flowQuadCenter(%v) accepted malformed coordinates", quads)
		}
	}
}

func TestFlowRoundedClickPointMatchesHitTestCoordinates(t *testing.T) {
	t.Parallel()
	if x, y, ok := flowRoundedClickPoint(10.49, 20.51); !ok || x != 10 || y != 21 {
		t.Fatalf("flowRoundedClickPoint() = %v,%v,%v, want 10,21,true", x, y, ok)
	}
	for _, point := range [][2]float64{{-1, 0}, {0, -1}, {math.Inf(1), 0}, {0, math.NaN()}, {float64(math.MaxInt32) + 1, 0}} {
		if _, _, ok := flowRoundedClickPoint(point[0], point[1]); ok {
			t.Fatalf("flowRoundedClickPoint(%v,%v) accepted unsafe coordinates", point[0], point[1])
		}
	}
}

func TestFlowAXHitRequiresApprovedTargetWithoutInteractiveDescendant(t *testing.T) {
	t.Parallel()
	text := &accessibility.Node{NodeID: "text", BackendDOMNodeID: cdp.BackendNodeID(7), ParentID: "approved", Role: accessibilityTestValue(`"StaticText"`)}
	approved := &accessibility.Node{NodeID: "approved", BackendDOMNodeID: cdp.BackendNodeID(9), Role: accessibilityTestValue(`"button"`)}
	if !flowAXHitBelongsToApprovedTarget([]*accessibility.Node{text, approved}, cdp.BackendNodeID(7), cdp.BackendNodeID(9)) {
		t.Fatal("flowAXHitBelongsToApprovedTarget() rejected a text descendant")
	}
	interactive := &accessibility.Node{
		NodeID:           "interactive",
		BackendDOMNodeID: cdp.BackendNodeID(8),
		ParentID:         "approved",
		Role:             accessibilityTestValue(`"link"`),
	}
	if flowAXHitBelongsToApprovedTarget([]*accessibility.Node{interactive, approved}, cdp.BackendNodeID(8), cdp.BackendNodeID(9)) {
		t.Fatal("flowAXHitBelongsToApprovedTarget() accepted an interactive descendant")
	}
	focusable := &accessibility.Node{
		NodeID:           "focusable",
		BackendDOMNodeID: cdp.BackendNodeID(10),
		ParentID:         "approved",
		Role:             accessibilityTestValue(`"generic"`),
		Properties: []*accessibility.Property{{
			Name:  accessibility.PropertyNameFocusable,
			Value: accessibilityTestValue(`true`),
		}},
	}
	if flowAXHitBelongsToApprovedTarget([]*accessibility.Node{focusable, approved}, cdp.BackendNodeID(10), cdp.BackendNodeID(9)) {
		t.Fatal("flowAXHitBelongsToApprovedTarget() accepted a focusable descendant")
	}
	if flowAXHitBelongsToApprovedTarget([]*accessibility.Node{text, approved}, cdp.BackendNodeID(7), cdp.BackendNodeID(8)) {
		t.Fatal("flowAXHitBelongsToApprovedTarget() accepted an unrelated target")
	}
}

func accessibilityTestValue(value string) *accessibility.Value {
	return &accessibility.Value{Value: jsontext.Value(value)}
}

func TestAccessibilityPropertyTrueParsesTypedBoolean(t *testing.T) {
	t.Parallel()
	node := &accessibility.Node{Properties: []*accessibility.Property{{
		Name: accessibility.PropertyNameDisabled,
		Value: &accessibility.Value{
			Type:  accessibility.ValueTypeBoolean,
			Value: jsontext.Value(`true`),
		},
	}}}
	if !accessibilityPropertyTrue(node, accessibility.PropertyNameDisabled) {
		t.Fatal("accessibilityPropertyTrue() = false, want true")
	}
	if accessibilityPropertyTrue(node, accessibility.PropertyNameFocusable) {
		t.Fatal("accessibilityPropertyTrue() accepted absent property")
	}
}

func TestFlowActionErrorKindIsStableAndNonDiagnostic(t *testing.T) {
	t.Parallel()
	for err, want := range map[error]string{
		ErrFlowTargetNotFound:                   "target_not_found",
		ErrFlowTargetAmbiguous:                  "target_ambiguous",
		ErrFlowTargetUnavailable:                "target_unavailable",
		ErrFlowPolicyViolation:                  "policy_violation",
		errors.New("private path /tmp/profile"): "flow_failed",
	} {
		if got := flowActionErrorKind(err); got != want {
			t.Fatalf("flowActionErrorKind(%v) = %q, want %q", err, got, want)
		}
	}
}
