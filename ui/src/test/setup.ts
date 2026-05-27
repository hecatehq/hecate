import "@testing-library/jest-dom/vitest";

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
