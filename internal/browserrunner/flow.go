package browserrunner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxFlowActions keeps one operator approval understandable and bounds the
	// amount of work one browser process can perform.
	MaxFlowActions = 6
	// MaxFlowAccessibilityNodes bounds each initial/final evidence snapshot.
	MaxFlowAccessibilityNodes = 64

	FlowActionClick    = "click"
	FlowActionWaitFor  = "wait_for"
	FlowActionFillText = "fill_text"
	MaxFlowTextBytes   = 1024

	FlowActionStatusCompleted = "completed"
	FlowActionStatusFailed    = "failed"
)

var (
	// ErrInvalidFlow means the complete declarative flow was not safe to admit.
	ErrInvalidFlow = errors.New("browser flow is invalid")
	// ErrFlowTargetNotFound means an exact accessible role/name did not appear
	// within the bounded action wait.
	ErrFlowTargetNotFound = errors.New("browser flow target was not found")
	// ErrFlowTargetAmbiguous means more than one accessible node matched the
	// approved role/name. Hecate never guesses which one the model intended.
	ErrFlowTargetAmbiguous = errors.New("browser flow target is ambiguous")
	// ErrFlowTargetUnavailable means the exact target exists but cannot be
	// safely clicked (for example it is disabled or has no usable hit region).
	ErrFlowTargetUnavailable = errors.New("browser flow target is unavailable")
	// ErrFlowPolicyViolation means page behavior attempted to leave the
	// approved read-only transport boundary.
	ErrFlowPolicyViolation = errors.New("browser flow policy was violated")
)

// FlowRunner is the orchestration seam for one approved, ephemeral browser
// interaction. It is intentionally separate from Inspector: static evidence
// remains script-disabled even when a runtime also supports flows.
type FlowRunner interface {
	RunFlow(context.Context, FlowRequest) (FlowResult, error)
}

// FlowRequest declares the entire interaction before Chromium starts.
// AllowedOrigin must be one exact normalized origin and URL must begin on it.
// A flow cannot move between several origins merely because its preset allows
// each of them independently.
type FlowRequest struct {
	URL           string
	AllowedOrigin string
	Actions       []FlowAction
}

// FlowAction addresses one accessibility node by exact computed role and
// exact accessible name. Selectors, XPath, coordinates, arbitrary JavaScript,
// keyboard commands and uploaded values are deliberately absent. Text is
// supported only for separately granted, approved non-secret fill actions.
type FlowAction struct {
	Kind string
	Role string
	Name string
	// Text is ordinary, operator-reviewed application data, never credentials.
	// A pointer distinguishes an explicit clear from a missing fill value.
	Text *string
}

// FlowActionResult is safe, ordered evidence for an attempted action. A failed
// result is retained when an earlier click may already have changed the page.
type FlowActionResult struct {
	Index     int
	Kind      string
	Role      string
	Name      string
	Status    string
	ErrorKind string
}

// FlowResult contains only bounded, redacted text evidence. It excludes form
// values, console payloads, cookies, storage, screenshots, raw protocol data,
// and request/response bodies because page scripts can expose secrets there.
type FlowResult struct {
	FinalURL                      string
	FinalOrigin                   string
	Title                         string
	InitialAccessibility          []AccessibilityNode
	InitialAccessibilityTruncated bool
	FinalAccessibility            []AccessibilityNode
	FinalAccessibilityTruncated   bool
	Actions                       []FlowActionResult
	Network                       NetworkSummary
}

// ValidateFlowRequest is exported so orchestration can reject and canonicalize
// an unsafe model call before it is persisted or shown for approval.
func ValidateFlowRequest(req FlowRequest) (FlowRequest, error) {
	target, err := parseInspectionTargetURL(req.URL)
	if err != nil {
		return FlowRequest{}, fmt.Errorf("%w: start URL", ErrInvalidFlow)
	}
	origin, err := NormalizeOrigin(req.AllowedOrigin)
	if err != nil || origin != originForURL(target) {
		return FlowRequest{}, fmt.Errorf("%w: exact origin", ErrInvalidFlow)
	}
	if len(req.Actions) == 0 || len(req.Actions) > MaxFlowActions {
		return FlowRequest{}, fmt.Errorf("%w: action count", ErrInvalidFlow)
	}

	out := FlowRequest{
		URL:           target.String(),
		AllowedOrigin: origin,
		Actions:       make([]FlowAction, len(req.Actions)),
	}
	for index, action := range req.Actions {
		action.Kind = strings.TrimSpace(action.Kind)
		action.Role = strings.TrimSpace(action.Role)
		action.Name = strings.TrimSpace(action.Name)
		if !validFlowAction(action) {
			return FlowRequest{}, fmt.Errorf("%w: action %d", ErrInvalidFlow, index+1)
		}
		if action.Text != nil {
			value := *action.Text
			action.Text = &value
		}
		out.Actions[index] = action
	}
	return out, nil
}

func validFlowAction(action FlowAction) bool {
	if action.Kind != FlowActionClick && action.Kind != FlowActionWaitFor && action.Kind != FlowActionFillText {
		return false
	}
	if !validFlowSelectorText(action.Role, 64) || !validFlowSelectorText(action.Name, 256) {
		return false
	}
	if action.Kind == FlowActionClick && !clickableAccessibilityRole(action.Role) {
		return false
	}
	if action.Kind == FlowActionFillText {
		return (action.Role == "textbox" || action.Role == "searchbox") && action.Text != nil && validFlowInputText(*action.Text)
	}
	if action.Text != nil {
		return false
	}
	return true
}

func validFlowInputText(value string) bool {
	if len(value) > MaxFlowTextBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || isBidiControl(r) {
			return false
		}
	}
	return true
}

func validFlowSelectorText(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || isBidiControl(r) {
			return false
		}
	}
	return true
}

func isBidiControl(r rune) bool {
	switch r {
	case '\u061c', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}

func clickableAccessibilityRole(role string) bool {
	switch role {
	case "button", "link", "checkbox", "radio", "tab", "menuitem", "switch":
		return true
	default:
		return false
	}
}
