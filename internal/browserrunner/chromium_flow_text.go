package browserrunner

import (
	"context"
	"encoding/json"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
)

// This fixed function runs in an isolated world so page scripts cannot replace
// its DOM intrinsics. The value is a protocol argument, never executable source.
// Setting the exact node avoids keyboard focus being redirected to another field.
const fillFlowTextScript = `function(value) {
  'use strict';
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
	var resultErr error
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		resultErr = fillFlowAccessibilityTargetDirect(actionCtx, action, target)
		return nil
	})); err != nil {
		return ErrInspectionFailed
	}
	return resultErr
}

func fillFlowAccessibilityTargetDirect(ctx context.Context, action FlowAction, target *accessibility.Node) error {
	if err := accessibility.Enable().Do(ctx); err != nil {
		return ErrInspectionFailed
	}
	defer func() { _ = accessibility.Disable().Do(ctx) }()
	backendID := target.BackendDOMNodeID
	if err := dom.ScrollIntoViewIfNeeded().WithBackendNodeID(backendID).Do(ctx); err != nil {
		return ErrFlowTargetUnavailable
	}
	if _, _, err := flowSafeClickPoint(ctx, backendID); err != nil {
		return ErrFlowTargetUnavailable
	}
	// Restrict text entry to the main document. This world cannot acquire page
	// globals or silently follow a frame into a different execution context.
	frame, err := page.GetFrameTree().Do(ctx)
	if err != nil || frame == nil || frame.Frame == nil {
		return ErrInspectionFailed
	}
	world, err := page.CreateIsolatedWorld(frame.Frame.ID).WithWorldName("hecate-browser-text-entry").Do(ctx)
	if err != nil {
		return ErrInspectionFailed
	}
	object, err := dom.ResolveNode().WithBackendNodeID(backendID).WithExecutionContextID(world).Do(ctx)
	if err != nil || object == nil || object.ObjectID == "" {
		return ErrFlowTargetUnavailable
	}
	defer func() { _ = runtime.ReleaseObject(object.ObjectID).Do(ctx) }()
	rechecked, err := exactFlowAccessibilityTargetDirect(ctx, action)
	if err != nil {
		return err
	}
	if rechecked.BackendDOMNodeID != backendID {
		return ErrFlowTargetUnavailable
	}
	value, err := json.Marshal(*action.Text)
	if err != nil {
		return ErrInvalidFlow
	}
	result, exception, err := runtime.CallFunctionOn(fillFlowTextScript).
		WithObjectID(object.ObjectID).
		WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(value)}}).
		WithReturnByValue(true).Do(ctx)
	if err != nil || exception != nil || result == nil {
		return ErrInspectionFailed
	}
	if string(result.Value) != "true" {
		return ErrFlowTargetUnavailable
	}
	return nil
}
