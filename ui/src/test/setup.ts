import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";

// usePersistedState writes localStorage on every state change; without a
// clean slate between tests a slice that toggled a persisted field in
// one test would bleed its value into the next test's mount-time read
// and the sync layer's effect would race the first user interaction.
// Reset both web-storage backings after every test so the order of
// `it()` blocks within a file never affects outcomes.
afterEach(() => {
  if (typeof globalThis.localStorage !== "undefined") {
    globalThis.localStorage.clear();
  }
  if (typeof globalThis.sessionStorage !== "undefined") {
    globalThis.sessionStorage.clear();
  }
});

// jsdom doesn't implement scrollIntoView; ChatView uses it heavily.
if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {};
}

// jsdom does not provide ResizeObserver; rich diff custom elements observe
// themselves after render, so tests only need a no-op implementation.
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

// jsdom exposes requestSubmit but routes it through an unimplemented path.
if (typeof HTMLFormElement !== "undefined") {
  Object.defineProperty(HTMLFormElement.prototype, "requestSubmit", {
    configurable: true,
    value(this: HTMLFormElement, submitter?: HTMLElement | null) {
      if (submitter) {
        submitter.click();
        return;
      }
      this.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    },
  });
}

if (typeof document !== "undefined") {
  // jsdom does not perform the browser's native "click submit button
  // submits enclosing form" behavior reliably. Keep this broad shim so
  // tests exercise the same path as users; tests that need custom
  // submit-button clicks should preventDefault explicitly.
  document.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    const control = target.closest("button, input");
    if (!isSubmitControl(control)) return;
    const form = control.form;
    if (!form) return;

    event.preventDefault();
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
}

function isSubmitControl(control: Element | null): control is HTMLButtonElement | HTMLInputElement {
  if (control instanceof HTMLButtonElement) return control.type === "submit";
  if (control instanceof HTMLInputElement) return control.type === "submit";
  return false;
}
