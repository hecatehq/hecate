package browserrunner

import (
	"context"
	"encoding/json"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
)

// This fixed function runs in an isolated world so page scripts cannot replace
// its DOM intrinsics. The value is a protocol argument, never executable source.
// Setting the exact node avoids keyboard focus being redirected to another field.
const fillFlowTextScript = `function(argument) {
  'use strict';
  const value = argument[0];
  const element = this;
  const input = element instanceof HTMLInputElement;
  const textarea = element instanceof HTMLTextAreaElement;
  if ((!input && !textarea) || element.ownerDocument !== document || !element.isConnected || element.disabled || element.matches(':disabled') || element.readOnly || element.hidden || element.isContentEditable) return false;
  if (input && element.type !== 'text' && element.type !== 'search') return false;
  const autocomplete = (element.getAttribute('autocomplete') || '').trim().toLowerCase();
  const formAutocomplete = element.form ? (element.form.getAttribute('autocomplete') || '').trim().toLowerCase() : '';
  if ((autocomplete && autocomplete !== 'off') || (formAutocomplete && formAutocomplete !== 'off')) return false;
  let ancestor = element;
  for (let depth = 0; ancestor; depth++, ancestor = ancestor.parentElement) {
    if (depth >= 128 || ancestor.hidden || ancestor.inert) return false;
    const style = getComputedStyle(ancestor);
    if (style.visibility !== 'visible' || style.display === 'none' || Number(style.opacity) === 0) return false;
  }
  if (element.getClientRects().length === 0) return false;
  const prototype = input ? HTMLInputElement.prototype : HTMLTextAreaElement.prototype;
  Object.getOwnPropertyDescriptor(prototype, 'value').set.call(element, value);
  element.dispatchEvent(new Event('input', { bubbles: true, composed: true }));
  element.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
}`

func fillFlowAccessibilityTarget(ctx context.Context, action FlowAction, target *accessibility.Node) error {
	if target == nil || target.BackendDOMNodeID == 0 || action.Text == nil {
		return ErrFlowTargetUnavailable
	}
	return fillFlowAccessibilityTargetDirect(ctx, action, target)
}

func fillFlowAccessibilityTargetDirect(ctx context.Context, action FlowAction, target *accessibility.Node) error {
	if _, err := chromedp.Call(ctx, accessibility.Enable, cdp.Empty{}); err != nil {
		return ErrInspectionFailed
	}
	defer func() { _, _ = chromedp.Call(ctx, accessibility.Disable, cdp.Empty{}) }()
	backendID := target.BackendDOMNodeID
	if _, err := chromedp.Call(ctx, dom.ScrollIntoViewIfNeeded, dom.ScrollIntoViewIfNeededParams{BackendNodeID: backendID}); err != nil {
		return ErrFlowTargetUnavailable
	}
	if _, _, err := flowSafeClickPoint(ctx, backendID); err != nil {
		return ErrFlowTargetUnavailable
	}
	// Restrict text entry to the main document. This world cannot acquire page
	// globals or silently follow a frame into a different execution context.
	frame, err := chromedp.Call(ctx, page.GetFrameTree, cdp.Empty{})
	if err != nil || frame.FrameTree == nil || frame.FrameTree.Frame == nil {
		return ErrInspectionFailed
	}
	world, err := chromedp.Call(ctx, page.CreateIsolatedWorld, page.CreateIsolatedWorldParams{FrameID: frame.FrameTree.Frame.ID, WorldName: "hecate-browser-text-entry"})
	if err != nil {
		return ErrInspectionFailed
	}
	resolved, err := chromedp.Call(ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: backendID, ExecutionContextID: world.ExecutionContextID})
	if err != nil || resolved.Object == nil || resolved.Object.ObjectID == "" {
		return ErrFlowTargetUnavailable
	}
	objectID := resolved.Object.ObjectID
	defer func() {
		_, _ = chromedp.Call(ctx, runtime.ReleaseObject, runtime.ReleaseObjectParams{ObjectID: objectID})
	}()
	rechecked, err := exactFlowAccessibilityTargetDirect(ctx, action)
	if err != nil {
		return err
	}
	if rechecked.BackendDOMNodeID != backendID {
		return ErrFlowTargetUnavailable
	}
	params, err := flowTextCallParams(objectID, *action.Text)
	if err != nil {
		return ErrInvalidFlow
	}
	result, err := chromedp.Call(ctx, runtime.CallFunctionOn, params)
	if err != nil || result.ExceptionDetails != nil || result.Result == nil {
		return ErrInspectionFailed
	}
	if string(result.Result.Value) != "true" {
		return ErrFlowTargetUnavailable
	}
	return nil
}

func flowTextCallParams(objectID runtime.RemoteObjectID, text string) (runtime.CallFunctionOnParams, error) {
	// The protocol codec omits empty primitive values. A one-element array
	// preserves an explicit empty string so clearing a field never passes undefined.
	value, err := json.Marshal([]string{text})
	if err != nil {
		return runtime.CallFunctionOnParams{}, err
	}
	return runtime.CallFunctionOnParams{
		FunctionDeclaration: fillFlowTextScript,
		ObjectID:            objectID,
		Arguments:           []*runtime.CallArgument{{Value: jsontext.Value(value)}},
		ReturnByValue:       new(true),
	}, nil
}
