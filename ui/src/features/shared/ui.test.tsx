import { useRef, useState } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  AgentAdapterPicker,
  Badge,
  BrandAvatar,
  ChipInput,
  CodeBlock,
  ConfirmModal,
  CopyableID,
  CopyBtn,
  Dot,
  Icon,
  Icons,
  InlineError,
  Modal,
  ModelPicker,
  ProviderPicker,
  SlideOver,
  Toggle,
} from "./ui";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ModelRecord } from "../../types/model";

describe("Toggle", () => {
  it("renders with role=switch and aria-checked", () => {
    render(<Toggle on onChange={() => {}} ariaLabel="enable feature" />);
    const sw = screen.getByRole("switch", { name: "enable feature" });
    expect(sw.getAttribute("aria-checked")).toBe("true");
  });

  it("invokes onChange with negation on click", async () => {
    const onChange = vi.fn();
    render(<Toggle on={false} onChange={onChange} ariaLabel="x" />);
    await userEvent.setup().click(screen.getByRole("switch"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("supports being clicked multiple times", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(<Toggle on={false} onChange={onChange} ariaLabel="x" />);
    await user.click(screen.getByRole("switch"));
    expect(onChange).toHaveBeenLastCalledWith(true);
    rerender(<Toggle on={true} onChange={onChange} ariaLabel="x" />);
    await user.click(screen.getByRole("switch"));
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("supports Enter and Space keyboard activation", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(<Toggle on={false} onChange={onChange} ariaLabel="x" />);
    const firstSwitch = screen.getByRole("switch", { name: "x" });
    firstSwitch.focus();
    await user.keyboard("{Enter}");
    expect(onChange).toHaveBeenLastCalledWith(true);

    rerender(<Toggle on={true} onChange={onChange} ariaLabel="x" />);
    const secondSwitch = screen.getByRole("switch", { name: "x" });
    secondSwitch.focus();
    await user.keyboard(" ");
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("falls back to label when ariaLabel not given", () => {
    render(<Toggle on onChange={() => {}} label="Enabled" />);
    expect(screen.getByRole("switch", { name: "Enabled" })).toBeTruthy();
  });
});

describe("Badge", () => {
  it("renders the configured label per status", () => {
    const { rerender } = render(<Badge status="enabled" />);
    expect(screen.getByText("enabled")).toBeTruthy();
    rerender(<Badge status="error" />);
    expect(screen.getByText("error")).toBeTruthy();
    rerender(<Badge status="healthy" />);
    expect(screen.getByText("healthy")).toBeTruthy();
  });

  it("uses the provided label override", () => {
    render(<Badge status="enabled" label="active" />);
    expect(screen.getByText("active")).toBeTruthy();
  });
});

describe("InlineError", () => {
  it("renders the message inside an error block", () => {
    render(<InlineError message="something broke" />);
    expect(screen.getByRole("alert")).toHaveTextContent("something broke");
  });

  it("renders nothing when message is empty", () => {
    const { container } = render(<InlineError message="" />);
    expect(container.firstChild).toBeNull();
  });
});

describe("Dot", () => {
  it("renders a circle with the requested colour", () => {
    const { container } = render(<Dot color="green" />);
    expect(container.firstChild).toBeTruthy();
  });
});

describe("Icon", () => {
  it("renders an SVG with a single path", () => {
    const { container } = render(<Icon d={Icons.chat} />);
    const svg = container.querySelector("svg");
    expect(svg).toBeTruthy();
    expect(svg).toHaveAttribute("aria-hidden", "true");
    expect(svg).toHaveAttribute("focusable", "false");
    expect(container.querySelectorAll("path").length).toBeGreaterThan(0);
  });

  it("renders multiple paths when given an array", () => {
    const { container } = render(<Icon d={Icons.providers} />);
    expect(container.querySelectorAll("path").length).toBeGreaterThan(1);
  });
});

describe("BrandAvatar", () => {
  it("renders a known Devicons brand icon", () => {
    const { container } = render(<BrandAvatar brand="claude_code" title="Claude Code" />);
    expect(screen.getByLabelText("Claude Code")).toBeTruthy();
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("falls back to a compact letter tile for unknown brands", () => {
    render(<BrandAvatar brand="unknown-provider" fallback="Unknown provider" />);
    expect(screen.getByText("U")).toBeTruthy();
  });

  it("keeps unknown unboxed brand fallbacks aligned", () => {
    render(
      <BrandAvatar brand="unknown-provider" fallback="Unknown provider" boxed={false} size={32} />,
    );
    const fallback = screen.getByText("U").parentElement;
    expect(fallback).toHaveStyle({ height: "32px", width: "32px" });
  });

  it("renders the Hecate mark as a branded image", () => {
    const { container } = render(<BrandAvatar brand="hecate" fallback="Hecate" />);
    expect(container.querySelector("img")?.getAttribute("src")).toContain("hecate-mark");
    expect(container.querySelector("img")?.getAttribute("aria-hidden")).toBe("true");
    expect(container.querySelector("img")).toHaveStyle({
      filter: "var(--mono-icon-filter)",
    });
  });

  it("uses the Meta icon for llama.cpp providers", () => {
    const { container } = render(<BrandAvatar brand="llamacpp" title="llama.cpp" />);
    expect(screen.getByLabelText("llama.cpp")).toBeTruthy();
    expect(container.querySelector("svg")).toBeTruthy();
    expect(screen.queryByText("L")).toBeNull();
  });

  it("renders monochrome devicons with theme-controlled color", () => {
    const { container } = render(<BrandAvatar brand="openai" title="OpenAI" />);
    expect(screen.getByLabelText("OpenAI")).toBeTruthy();
    expect(container.querySelector("path")?.getAttribute("fill")).toBe("currentColor");
    expect(container.querySelector("svg")).toHaveStyle({
      color: "var(--mono-icon)",
    });
  });

  it("uses the Vercel icon for Vercel AI Gateway", () => {
    const { container } = render(
      <BrandAvatar brand="vercel_ai_gateway" title="Vercel AI Gateway" />,
    );
    expect(screen.getByLabelText("Vercel AI Gateway")).toBeTruthy();
    expect(container.querySelector("svg")).toBeTruthy();
    expect(screen.queryByText("V")).toBeNull();
  });
});

describe("CopyBtn", () => {
  // Direct navigator.clipboard integration test — userEvent.setup() installs its
  // own clipboard mock that would intercept our spy, so we use the lower-level
  // fireEvent here. This is the documented approach for clipboard interop tests.
  it("invokes navigator.clipboard.writeText on click", () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    render(<CopyBtn text="hello" />);
    fireEvent.click(screen.getByRole("button"));
    expect(writeText).toHaveBeenCalledWith("hello");
  });
});

describe("CopyableID", () => {
  it("renders a compact label while copying the full id", async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    render(<CopyableID text="run_full_identifier_123" compact />);
    expect(screen.getByText("run_full_i…er_123")).toBeTruthy();
    const button = screen.getByRole("button", {
      name: /copy run_full_identifier_123/i,
    });
    fireEvent.click(button);
    expect(writeText).toHaveBeenCalledWith("run_full_identifier_123");
    await waitFor(() => expect(button).toHaveStyle({ color: "var(--green)" }));
  });

  it("keeps short ids unmodified", () => {
    render(<CopyableID text="run_123" compact />);
    expect(screen.getByText("run_123")).toBeTruthy();
  });

  it("does not mark copied when the clipboard write fails", async () => {
    const writeText = vi.fn(() => Promise.reject(new Error("clipboard denied")));
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    render(<CopyableID text="run_full_identifier_123" compact />);
    const button = screen.getByRole("button", {
      name: /copy run_full_identifier_123/i,
    });
    expect(button).toHaveStyle({ color: "var(--teal)" });
    fireEvent.click(button);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("run_full_identifier_123"));
    expect(button).toHaveStyle({ color: "var(--teal)" });
  });

  it("does not mark copied when clipboard access is unavailable", () => {
    Object.defineProperty(navigator, "clipboard", {
      value: undefined,
      configurable: true,
    });
    render(<CopyableID text="run_full_identifier_123" compact />);
    const button = screen.getByRole("button", {
      name: /copy run_full_identifier_123/i,
    });
    expect(() => fireEvent.click(button)).not.toThrow();
    expect(button).toHaveStyle({ color: "var(--teal)" });
  });
});

// ─── Modal / SlideOver / ConfirmModal ──────────────────────────────────
// Shared dialog primitives. The chrome contract — Escape closes, the
// Close button closes, backdrop click closes, in-content click does NOT
// close — is the same across all three. A regression here breaks every
// settings form and confirm dialog.

describe("Modal", () => {
  function renderModal(onClose = vi.fn()) {
    render(
      <Modal title="Test modal" footer={<button>OK</button>} onClose={onClose}>
        <div data-testid="content">body content</div>
      </Modal>,
    );
    return { onClose };
  }

  it("renders title, body, and footer with role=dialog", () => {
    renderModal();
    const dialog = screen.getByRole("dialog", { name: "Test modal" });
    expect(dialog).toBeTruthy();
    expect(dialog.style.maxWidth).toBe("calc(100vw - 24px)");
    expect(dialog.style.maxHeight).toBe("min(80vh, calc(100dvh - 24px))");
    const content = within(dialog).getByTestId("content");
    expect(content).toBeTruthy();
    expect(content.parentElement?.style.overscrollBehavior).toBe("contain");
    expect(within(dialog).getByRole("button", { name: "OK" })).toBeTruthy();
  });

  it("Escape key fires onClose", async () => {
    const { onClose } = renderModal();
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("Close button fires onClose", async () => {
    const { onClose } = renderModal();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("clicking content does NOT close (stopPropagation guards body)", async () => {
    const { onClose } = renderModal();
    await userEvent.click(screen.getByTestId("content"));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("moves focus into the dialog and traps Tab navigation", async () => {
    const user = userEvent.setup();
    renderModal();
    const close = screen.getByRole("button", { name: "Close" });
    const ok = screen.getByRole("button", { name: "OK" });

    await waitFor(() => expect(close).toHaveFocus());
    await user.tab();
    expect(ok).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.tab({ shift: true });
    expect(ok).toHaveFocus();
  });

  it("skips controls inside closed disclosures when trapping focus", async () => {
    const user = userEvent.setup();
    render(
      <Modal title="Evidence modal" footer={<button disabled>Save</button>} onClose={() => {}}>
        <input aria-label="Title" />
        <details>
          <summary>Advanced details</summary>
          <input aria-label="Hidden provider" />
        </details>
        <textarea aria-label="Summary" />
      </Modal>,
    );
    const close = screen.getByRole("button", { name: "Close" });
    const title = screen.getByLabelText("Title");
    const disclosure = screen.getByText("Advanced details");
    const summary = screen.getByLabelText("Summary");

    await waitFor(() => expect(close).toHaveFocus());
    await user.tab({ shift: true });
    expect(summary).toHaveFocus();
    close.focus();
    await user.tab();
    expect(title).toHaveFocus();
    await user.tab();
    expect(disclosure).toHaveFocus();
    await user.tab();
    expect(summary).toHaveFocus();

    (disclosure.parentElement as HTMLDetailsElement).open = true;
    disclosure.focus();
    await user.tab();
    expect(screen.getByLabelText("Hidden provider")).toHaveFocus();
  });

  it("blocks every dismiss path while dismissal is disabled", async () => {
    const onClose = vi.fn();
    render(
      <Modal title="Pending decision" dismissible={false} footer={<span />} onClose={onClose}>
        <div>Recording the decision.</div>
      </Modal>,
    );
    const dialog = screen.getByRole("dialog", { name: "Pending decision" });
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    fireEvent.click(dialog.parentElement as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("restores focus to the previously focused element on close", async () => {
    const user = userEvent.setup();

    function Harness() {
      const [open, setOpen] = useState(false);
      const fieldRef = useRef<HTMLInputElement>(null);
      return (
        <>
          <button onClick={() => setOpen(true)}>Open modal</button>
          {open && (
            <Modal
              title="Test modal"
              footer={<button>OK</button>}
              initialFocusRef={fieldRef}
              onClose={() => setOpen(false)}
            >
              <label>
                Modal field
                <input ref={fieldRef} />
              </label>
            </Modal>
          )}
        </>
      );
    }

    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Open modal" });
    await user.click(opener);
    await waitFor(() => expect(screen.getByLabelText("Modal field")).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it("preserves an explicit focus handoff when a dialog closes", async () => {
    const user = userEvent.setup();

    function Harness() {
      const [open, setOpen] = useState(false);
      const nextRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button onClick={() => setOpen(true)}>Open modal</button>
          <button ref={nextRef}>Next action</button>
          {open && (
            <Modal
              title="Test modal"
              footer={
                <button
                  onClick={() => {
                    nextRef.current?.focus();
                    setOpen(false);
                  }}
                >
                  Continue
                </button>
              }
              onClose={() => setOpen(false)}
            >
              Ready to continue.
            </Modal>
          )}
        </>
      );
    }

    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Open modal" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Next action" })).toHaveFocus());
  });

  it("restores focus to a stable fallback when the opener is removed", async () => {
    const user = userEvent.setup();

    function Harness() {
      const [open, setOpen] = useState(false);
      const fallbackRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          {!open && <button onClick={() => setOpen(true)}>Open disposable modal</button>}
          <button ref={fallbackRef}>Stable fallback</button>
          {open && (
            <Modal
              title="Disposable modal"
              footer={<button>OK</button>}
              onClose={() => setOpen(false)}
              returnFocusRef={fallbackRef}
            >
              Modal content
            </Modal>
          )}
        </>
      );
    }

    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Open disposable modal" }));
    await user.click(screen.getByRole("button", { name: "Close" }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Stable fallback" })).toHaveFocus(),
    );
  });

  it("restores focus to a stable fallback when the opener becomes hidden", async () => {
    const user = userEvent.setup();

    function Harness() {
      const [open, setOpen] = useState(false);
      const [openerHidden, setOpenerHidden] = useState(false);
      const fallbackRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          <div style={{ visibility: openerHidden ? "hidden" : "visible" }}>
            <button
              onClick={() => {
                setOpenerHidden(true);
                setOpen(true);
              }}
            >
              Open row action
            </button>
          </div>
          <button ref={fallbackRef}>Stable row fallback</button>
          {open && (
            <Modal
              title="Row action modal"
              footer={<button>Confirm</button>}
              onClose={() => setOpen(false)}
              returnFocusRef={fallbackRef}
            >
              Modal content
            </Modal>
          )}
        </>
      );
    }

    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Open row action" }));
    await user.keyboard("{Escape}");

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Stable row fallback" })).toHaveFocus(),
    );
  });
});

describe("SlideOver", () => {
  // SlideOver shares chrome with Modal — only the surface positioning
  // differs. One sanity test confirms the dialog role + Escape close.
  it("renders as a dialog and closes on Escape", async () => {
    const onClose = vi.fn();
    render(
      <SlideOver title="Side panel" footer={<span />} onClose={onClose}>
        <div>panel body</div>
      </SlideOver>,
    );
    expect(screen.getByRole("dialog", { name: "Side panel" })).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("ConfirmModal", () => {
  it("calls onConfirm when the confirm button is clicked", async () => {
    const onConfirm = vi.fn();
    render(
      <ConfirmModal
        title="Delete?"
        message="This is irreversible."
        confirmLabel="Delete"
        danger
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("disables confirm and shows 'Working…' while pending", () => {
    const onClose = vi.fn();
    render(
      <ConfirmModal
        title="Delete?"
        message="x"
        confirmLabel="Delete"
        pending
        onConfirm={() => {}}
        onClose={onClose}
      />,
    );
    // Pending replaces the label — operator can't accidentally
    // double-fire the action while the request is in flight.
    const btn = screen.getByRole("button", { name: /Working/i });
    expect((btn as HTMLButtonElement).disabled).toBe(true);
    const dialog = screen.getByRole("dialog", { name: "Delete?" });
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(dialog.parentElement as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("restores confirm focus when a pending confirmation fails", async () => {
    const props = {
      title: "Delete?",
      message: "x",
      confirmLabel: "Delete",
      onConfirm: vi.fn(),
      onClose: vi.fn(),
    };
    const { rerender } = render(
      <>
        <button>Outside</button>
        <ConfirmModal {...props} />
      </>,
    );
    screen.getByRole("button", { name: "Delete" }).focus();

    rerender(
      <>
        <button>Outside</button>
        <ConfirmModal {...props} pending />
      </>,
    );
    screen.getByRole("button", { name: "Outside" }).focus();
    expect(screen.getByRole("button", { name: "Outside" })).toHaveFocus();

    rerender(
      <>
        <button>Outside</button>
        <ConfirmModal {...props} />
      </>,
    );
    await waitFor(() => expect(screen.getByRole("button", { name: "Delete" })).toHaveFocus());
  });

  it("uses btn-primary by default and btn-danger when danger=true", () => {
    const { rerender } = render(
      <ConfirmModal
        title="t"
        message="m"
        confirmLabel="OK"
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: "OK" }).className).toContain("btn-primary");
    rerender(
      <ConfirmModal
        title="t"
        message="m"
        confirmLabel="OK"
        danger
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: "OK" }).className).toContain("btn-danger");
  });
});

// ─── ModelPicker ──────────────────────────────────────────────────────

describe("ModelPicker", () => {
  const models: ModelRecord[] = [
    {
      id: "gpt-4o-mini",
      owned_by: "openai",
      metadata: { provider: "openai", provider_kind: "cloud", default: true },
    },
    {
      id: "gpt-4o",
      owned_by: "openai",
      metadata: { provider: "openai", provider_kind: "cloud", default: false },
    },
    {
      id: "claude-sonnet-4-6",
      owned_by: "anthropic",
      metadata: {
        provider: "anthropic",
        provider_kind: "cloud",
        default: false,
      },
    },
  ];

  it("opens on trigger click and lists models flat (no section headers)", async () => {
    const user = userEvent.setup();
    render(<ModelPicker value="gpt-4o-mini" onChange={() => {}} models={models} />);
    // Trigger reads the selected model id (rendered in both the
    // visible label span and as the title attribute on the inner
    // span). getAllByText accommodates both occurrences.
    expect(screen.getAllByText("gpt-4o-mini").length).toBeGreaterThan(0);
    expect(document.querySelector(".dropdown-menu")).toBeNull();
    await user.click(screen.getByRole("button"));
    expect(document.querySelector(".dropdown-menu")).toBeTruthy();
    // Each row carries the provider name as a per-row suffix
    // (showProvider defaults to true) — but the picker no longer
    // groups under section headers like before. We verify by
    // confirming each model id renders as its own row.
    const menu = document.querySelector(".dropdown-menu")!;
    expect(within(menu as HTMLElement).getByText("gpt-4o-mini")).toBeTruthy();
    expect(within(menu as HTMLElement).getByText("gpt-4o")).toBeTruthy();
    expect(within(menu as HTMLElement).getByText("claude-sonnet-4-6")).toBeTruthy();
  });

  it("calls onChange with the picked id and closes the menu", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ModelPicker value="gpt-4o-mini" onChange={onChange} models={models} />);
    await user.click(screen.getByRole("button"));

    const menu = document.querySelector(".dropdown-menu")!;
    await user.click(within(menu as HTMLElement).getByText("claude-sonnet-4-6"));

    expect(onChange).toHaveBeenCalledWith("claude-sonnet-4-6");
    // Menu closes after selection.
    expect(document.querySelector(".dropdown-menu")).toBeNull();
  });

  it("shows account-scoped models as short names while selecting the full id", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    const fireworksModels: ModelRecord[] = [
      {
        id: "accounts/fireworks/models/deepseek-v3p1",
        owned_by: "fireworks",
        metadata: {
          provider: "fireworks",
          provider_kind: "cloud",
          default: true,
        },
      },
      {
        id: "accounts/fireworks/models/llama-v3p1",
        owned_by: "fireworks",
        metadata: {
          provider: "fireworks",
          provider_kind: "cloud",
          default: false,
        },
      },
    ];
    render(
      <ModelPicker
        value="accounts/fireworks/models/deepseek-v3p1"
        onChange={onChange}
        models={fireworksModels}
      />,
    );

    expect(screen.getByRole("button")).toHaveTextContent("deepseek-v3p1");
    await user.click(screen.getByRole("button"));

    const input = screen.getByRole("textbox", { name: /filter models/i });
    await user.type(input, "llama");
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    expect(within(menu).getByText("llama-v3p1")).toBeTruthy();
    expect(within(menu).getAllByText("Fireworks AI").length).toBeGreaterThan(0);

    await user.click(within(menu).getByText("llama-v3p1"));
    expect(onChange).toHaveBeenCalledWith("accounts/fireworks/models/llama-v3p1");
  });

  it("renders configured provider aliases with their preset display name", async () => {
    const user = userEvent.setup();
    const aliasedModels: ModelRecord[] = [
      {
        id: "accounts/fireworks/models/deepseek-v3p1",
        owned_by: "fireworks-prod",
        metadata: {
          provider: "fireworks-prod",
          provider_kind: "cloud",
          default: true,
        },
      },
    ];
    render(
      <ModelPicker
        value=""
        onChange={() => {}}
        models={aliasedModels}
        presets={[
          {
            id: "fireworks",
            name: "Fireworks AI",
            kind: "cloud",
            protocol: "openai",
            base_url: "https://api.fireworks.ai/inference/v1",
          },
        ]}
        configuredProviders={[
          {
            id: "fireworks-prod",
            name: "fireworks-prod",
            preset_id: "fireworks",
            kind: "cloud",
            protocol: "openai",
            base_url: "https://api.fireworks.ai/inference/v1",
            credential_configured: true,
          },
        ]}
      />,
    );

    await user.click(screen.getByRole("button"));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    expect(within(menu).getAllByText("Fireworks AI").length).toBeGreaterThan(0);
    expect(within(menu).queryByText("fireworks-prod")).toBeNull();
  });

  it("supports filtering, arrow-key navigation, and Enter selection", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ModelPicker value="" onChange={onChange} models={models} />);
    await user.click(screen.getByRole("button"));
    const input = screen.getByRole("textbox", { name: /filter models/i });
    await user.type(input, "gpt");
    await user.keyboard("{ArrowDown}");
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    const selected = within(menu).getByText("gpt-4o-mini").closest("button");
    expect(selected).toHaveFocus();
    await user.keyboard("{ArrowDown}{Enter}");
    expect(onChange).toHaveBeenCalledWith("gpt-4o");
  });

  it("selects the first enabled model when Enter is pressed with an empty filter", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ModelPicker value="claude-sonnet-4-6" onChange={onChange} models={models} />);
    await user.click(screen.getByRole("button"));
    await user.keyboard("{Enter}");

    expect(onChange).toHaveBeenCalledWith("gpt-4o-mini");
    expect(onChange).not.toHaveBeenCalledWith("");
  });

  it("disables the trigger when the model list is empty", async () => {
    // Selecting a provider whose runtime isn't running (Ollama / LM Studio
    // not started, llamacpp credentials missing) yields a 0-model list
    // for that scope. Opening a dropdown to see "no models" is worse UX
    // than a clearly-disabled trigger — operators either start the
    // runtime, switch back to Auto, or pick a different provider, all
    // of which are clearer next steps than scanning an empty list.
    const user = userEvent.setup();
    render(<ModelPicker value="" onChange={() => {}} models={[]} />);
    const trigger = screen.getByRole("button");
    expect(trigger).toBeDisabled();
    expect(trigger.getAttribute("title")).toMatch(/no discovered models/i);
    expect(trigger.textContent).toMatch(/no models available/i);

    // Clicking a disabled button does nothing. The dropdown menu stays
    // unmounted and the test confirms the empty-state messaging lives
    // on the trigger, not behind a click.
    await user.click(trigger);
    expect(document.querySelector(".dropdown-menu")).toBeNull();
  });

  it("highlights the selected row with the 'selected' class", async () => {
    const user = userEvent.setup();
    render(<ModelPicker value="gpt-4o" onChange={() => {}} models={models} />);
    await user.click(screen.getByRole("button"));
    const selectedRow = document.querySelector(".dropdown-item.selected");
    expect(selectedRow?.textContent).toContain("gpt-4o");
  });

  it("type-to-filter narrows the visible rows", async () => {
    const user = userEvent.setup();
    render(<ModelPicker value="" onChange={() => {}} models={models} />);
    await user.click(screen.getByRole("button"));
    const input = screen.getByPlaceholderText(/Filter models/i);
    await user.type(input, "claude");
    const menu = document.querySelector(".dropdown-menu")!;
    expect(within(menu as HTMLElement).getByText("claude-sonnet-4-6")).toBeTruthy();
    expect(within(menu as HTMLElement).queryByText("gpt-4o")).toBeNull();
  });

  it("shows an empty-state message for no-match filters when the all-models sentinel is enabled", async () => {
    const user = userEvent.setup();
    render(<ModelPicker value="" onChange={() => {}} models={models} includeAll />);
    await user.click(screen.getByRole("button"));
    const input = screen.getByPlaceholderText(/Filter models/i);
    await user.type(input, "not-a-model");
    const menu = document.querySelector(".dropdown-menu")!;
    expect(within(menu as HTMLElement).getByText("All models")).toBeTruthy();
    expect(within(menu as HTMLElement).getByText("No models match")).toBeTruthy();
  });

  it("greys out and blocks selection of disabled-provider rows", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    const disabled = new Map<string, string>([
      ["anthropic", "Add an API key for Anthropic in Connections"],
    ]);
    render(
      <ModelPicker value="" onChange={onChange} models={models} disabledProviders={disabled} />,
    );
    await user.click(screen.getByRole("button"));
    const menu = document.querySelector(".dropdown-menu")!;
    // Click attempt on the disabled row — onChange must not fire and
    // the menu stays open (lets the operator notice the tooltip).
    await user.click(within(menu as HTMLElement).getByText("claude-sonnet-4-6"));
    expect(onChange).not.toHaveBeenCalled();
    expect(document.querySelector(".dropdown-menu")).toBeTruthy();
  });
});

// ─── ProviderPicker ───────────────────────────────────────────────────

describe("ProviderPicker", () => {
  const options = [
    { id: "openai", name: "OpenAI", configured: true, kind: "cloud" as const },
    {
      id: "anthropic",
      name: "Anthropic",
      configured: false,
      kind: "cloud" as const,
    },
    { id: "ollama", name: "Ollama", configured: true, kind: "local" as const },
  ];

  it("shows the selected option's display name", () => {
    // Auto-size mode renders the label twice — once visible, once
    // hidden (aria-hidden) as the widest-label spacer that pins the
    // trigger width across selections. So getAllByText finds 2 with
    // matching content; we just verify the trigger button contains
    // the label text.
    render(<ProviderPicker value="anthropic" onChange={() => {}} options={options} />);
    expect(screen.getByRole("button").textContent).toContain("Anthropic");
  });

  it("opens, shows All providers row when includeAuto, and emits the autoValue", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <ProviderPicker
        value="openai"
        onChange={onChange}
        options={options}
        includeAuto
        autoValue=""
        autoLabel="All providers"
      />,
    );
    await user.click(screen.getByRole("button"));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    expect(within(menu).getByText("All providers")).toBeTruthy();
    await user.click(within(menu).getByText("All providers"));
    // autoValue here is "" — pin the contract so a regression to "auto"
    // surfaces clearly. Different callers configure the sentinel.
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("opens on the floating overlay layer", async () => {
    const user = userEvent.setup();
    render(<ProviderPicker value="" onChange={() => {}} options={options} />);
    await user.click(screen.getByRole("button"));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    await waitFor(() => expect(menu.style.position).toBe("fixed"));
    expect(menu.style.zIndex).toBe("1000");
  });

  it("emits the option id (not the display name) when a row is clicked", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ProviderPicker value="" onChange={onChange} options={options} />);
    await user.click(screen.getByRole("button"));
    // The trigger spacer also contains every option name (widest-label
    // pin) — scope to the menu so we click the dropdown item, not the
    // hidden trigger span.
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    await user.click(within(menu).getByText("Anthropic"));
    expect(onChange).toHaveBeenCalledWith("anthropic");
  });

  it("closes the menu after a selection", async () => {
    const user = userEvent.setup();
    render(<ProviderPicker value="" onChange={() => {}} options={options} />);
    await user.click(screen.getByRole("button"));
    expect(document.querySelector(".dropdown-menu")).toBeTruthy();
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    await user.click(within(menu).getByText("Anthropic"));
    expect(document.querySelector(".dropdown-menu")).toBeNull();
  });

  it("renders emptyLabel when value is the empty string and no autoValue match", () => {
    // The previous fallback chain was `selected?.name ?? value ?? emptyLabel`.
    // `??` treats "" as defined, so an empty value rendered a blank trigger.
    // Pin emptyLabel as the fallback so the trigger never goes blank.
    render(
      <ProviderPicker
        value=""
        onChange={() => {}}
        options={options}
        emptyLabel="select provider"
      />,
    );
    expect(screen.getByRole("button").textContent).toContain("select provider");
  });

  it("renders the default emptyLabel ('select provider') when no prop is passed", () => {
    render(<ProviderPicker value="" onChange={() => {}} options={options} />);
    expect(screen.getByRole("button").textContent).toContain("select provider");
  });

  it("renders emptyLabel when value points to a removed/stale option", () => {
    // localStorage may persist a provider id from an earlier build that
    // no longer exists in the current options list. Showing the raw
    // stale id ("stale-anthropic-id") looks like a bug; emptyLabel is
    // the honest state — pick again.
    render(
      <ProviderPicker
        value="stale-anthropic-id"
        onChange={() => {}}
        options={options}
        emptyLabel="pick one"
      />,
    );
    const trigger = screen.getByRole("button").textContent || "";
    expect(trigger).toContain("pick one");
    expect(trigger).not.toContain("stale-anthropic-id");
  });

  it("renders emptyLabel when options is empty", () => {
    render(
      <ProviderPicker
        value=""
        onChange={() => {}}
        options={[]}
        emptyLabel="no providers configured"
      />,
    );
    expect(screen.getByRole("button").textContent).toContain("no providers configured");
  });

  it("shows a visible empty row when opening an empty provider menu", async () => {
    const user = userEvent.setup();
    render(
      <ProviderPicker
        value=""
        onChange={() => {}}
        options={[]}
        emptyLabel="no providers configured"
      />,
    );

    await user.click(screen.getByRole("button"));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    expect(within(menu).getByRole("option", { name: /no providers configured/i })).toBeTruthy();
  });

  it("autoLabel still wins over emptyLabel when value matches autoValue", () => {
    render(
      <ProviderPicker
        value=""
        onChange={() => {}}
        options={options}
        includeAuto
        autoValue=""
        autoLabel="All providers"
        emptyLabel="select provider"
      />,
    );
    const trigger = screen.getByRole("button").textContent || "";
    expect(trigger).toContain("All providers");
    expect(trigger).not.toContain("select provider");
  });

  it("supports arrow-key navigation and Enter selection", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ProviderPicker value="" onChange={onChange} options={options} />);
    await user.click(screen.getByRole("button"));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    const openai = within(menu).getByText("OpenAI").closest("button");
    const anthropic = within(menu).getByText("Anthropic").closest("button");

    await waitFor(() => expect(openai).toHaveFocus());
    await user.keyboard("{ArrowDown}");
    expect(anthropic).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(onChange).toHaveBeenCalledWith("anthropic");
  });
});

// ─── AgentAdapterPicker ───────────────────────────────────────────────

describe("AgentAdapterPicker", () => {
  const adapters: AgentAdapterRecord[] = [
    {
      id: "codex",
      name: "Codex",
      kind: "acp",
      command: "codex-acp-adapter",
      available: true,
      status: "available",
      cost_mode: "external",
      supports_authenticate: true,
      supports_logout: true,
    },
    {
      id: "claude_code",
      name: "Claude Code",
      kind: "acp",
      command: "claude-code-acp-adapter",
      available: true,
      status: "available",
      cost_mode: "external",
      supports_authenticate: true,
      supports_logout: true,
    },
  ];

  it("supports arrow-key navigation and Enter selection", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<AgentAdapterPicker value="" onChange={onChange} adapters={adapters} />);
    await user.click(screen.getByRole("button", { name: "External agent" }));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    const codex = within(menu).getByText("Codex").closest("button");
    const claude = within(menu).getByText("Claude Code").closest("button");

    await waitFor(() => expect(codex).toHaveFocus());
    await user.keyboard("{ArrowDown}");
    expect(claude).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(onChange).toHaveBeenCalledWith("claude_code");
  });

  it("shows unverified auth as a non-blocking check state", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <AgentAdapterPicker
        value=""
        onChange={onChange}
        adapters={[
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-code-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
            supports_authenticate: true,
            supports_logout: true,
            auth_status: "unknown",
            auth_error: "Claude Code config is present on disk.",
          },
        ]}
      />,
    );

    await user.click(screen.getByRole("button", { name: "External agent" }));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    const claude = within(menu).getByText("Claude Code").closest("button") as HTMLElement;
    expect(within(claude).getByText("check")).toBeTruthy();
    expect(within(claude).queryByText("auth")).toBeNull();
    expect(claude).not.toHaveAttribute("aria-disabled");

    await user.click(claude);
    expect(onChange).toHaveBeenCalledWith("claude_code");
  });

  it("shows missing adapters as setup instead of errors", async () => {
    const user = userEvent.setup();
    render(
      <AgentAdapterPicker
        value=""
        onChange={() => {}}
        adapters={[
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            cost_mode: "external",
            supports_authenticate: false,
            supports_logout: false,
            auth_status: "unknown",
          },
        ]}
        healthByID={
          new Map([
            [
              "cursor_agent",
              {
                adapter_id: "cursor_agent",
                status: "error",
                stage: "ready",
                error: "forced app CLI missing by HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
                hint: "Install Cursor with Agent support, then sign in with Cursor Agent.",
                duration_ms: 0,
              },
            ],
          ])
        }
      />,
    );

    await user.click(screen.getByRole("button", { name: "External agent" }));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    const cursor = within(menu).getByText("Cursor Agent").closest("button") as HTMLElement;
    expect(within(cursor).getByText("setup")).toBeTruthy();
    expect(within(cursor).queryByText("error")).toBeNull();
    expect(cursor.title).toContain("Install Cursor with Agent support");
    expect(cursor).not.toHaveAttribute("aria-disabled");
  });

  it("uses useful ready tooltips instead of showing only the executable path", async () => {
    const user = userEvent.setup();
    render(
      <AgentAdapterPicker
        value=""
        onChange={() => {}}
        adapters={[
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            path: "/Users/test/.local/bin/cursor-agent",
            available: true,
            status: "available",
            cost_mode: "external",
            supports_authenticate: false,
            supports_logout: false,
            auth_status: "ok",
          },
        ]}
        healthByID={
          new Map([
            [
              "cursor_agent",
              {
                adapter_id: "cursor_agent",
                status: "ready",
                stage: "ready",
                path: "/Users/test/.local/bin/cursor-agent",
                duration_ms: 80,
              },
            ],
          ])
        }
      />,
    );

    await user.click(screen.getByRole("button", { name: "External agent" }));
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    const cursor = within(menu).getByText("Cursor Agent").closest("button") as HTMLElement;
    expect(cursor.title).toContain("Cursor Agent is ready");
    expect(cursor.title).toContain("verified agent startup, auth, and ACP session creation");
    expect(cursor.title).toContain("/Users/test/.local/bin/cursor-agent");
  });
});

// ─── CodeBlock ────────────────────────────────────────────────────────

// ─── ChipInput ──────────────────────────────────────────────────────

describe("ChipInput", () => {
  const options = [
    { id: "openai", label: "OpenAI" },
    { id: "anthropic", label: "Anthropic" },
    { id: "ollama", label: "Ollama" },
  ];

  it("renders chips for the current values and an empty placeholder when none selected", () => {
    const { rerender } = render(
      <ChipInput values={[]} onChange={() => {}} options={options} placeholder="all" />,
    );
    expect((screen.getByPlaceholderText("all") as HTMLInputElement).value).toBe("");
    rerender(<ChipInput values={["openai"]} onChange={() => {}} options={options} />);
    expect(screen.getByText("OpenAI")).toBeTruthy();
  });

  it("typing filters the suggestion dropdown; clicking a suggestion adds a chip", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ChipInput values={[]} onChange={onChange} options={options} ariaLabel="providers" />);
    const input = screen.getByLabelText("providers");
    await user.click(input);
    await user.type(input, "anth");
    // Only the matching option should be visible in the dropdown.
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    expect(within(menu).getByText("Anthropic")).toBeTruthy();
    expect(within(menu).queryByText("OpenAI")).toBeNull();
    // mousedown — not click — because the dropdown closes on the
    // input's blur (which fires before click). The component uses
    // onMouseDown for selection to dodge that race.
    fireEvent.mouseDown(within(menu).getByText("Anthropic"));
    expect(onChange).toHaveBeenCalledWith(["anthropic"]);
  });

  it("Enter on the highlighted suggestion adds a chip", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<ChipInput values={[]} onChange={onChange} options={options} ariaLabel="providers" />);
    const input = screen.getByLabelText("providers");
    await user.click(input);
    // First suggestion is highlighted by default — Enter commits it.
    await user.keyboard("{Enter}");
    expect(onChange).toHaveBeenCalledWith(["openai"]);
  });

  it("Backspace on empty input removes the last chip", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <ChipInput
        values={["openai", "anthropic"]}
        onChange={onChange}
        options={options}
        ariaLabel="providers"
      />,
    );
    const input = screen.getByLabelText("providers");
    await user.click(input);
    await user.keyboard("{Backspace}");
    // Removes the last chip (anthropic), keeps openai.
    expect(onChange).toHaveBeenCalledWith(["openai"]);
  });

  it("clicking the × on a chip removes that specific chip", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <ChipInput
        values={["openai", "anthropic"]}
        onChange={onChange}
        options={options}
        ariaLabel="providers"
      />,
    );
    await user.click(screen.getByRole("button", { name: "Remove OpenAI" }));
    expect(onChange).toHaveBeenCalledWith(["anthropic"]);
  });

  it("excludes already-chipped values from the suggestion list", async () => {
    const user = userEvent.setup();
    render(
      <ChipInput values={["openai"]} onChange={() => {}} options={options} ariaLabel="providers" />,
    );
    const input = screen.getByLabelText("providers");
    await user.click(input);
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    // openai is already chipped — shouldn't appear in the suggestion
    // list (the dropdown should only show anthropic + ollama).
    expect(within(menu).queryByText("OpenAI")).toBeNull();
    expect(within(menu).getByText("Anthropic")).toBeTruthy();
    expect(within(menu).getByText("Ollama")).toBeTruthy();
  });

  it("freeText mode commits a typed value not in options on Enter", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <ChipInput values={[]} onChange={onChange} options={options} freeText ariaLabel="reasons" />,
    );
    const input = screen.getByLabelText("reasons");
    await user.click(input);
    await user.type(input, "custom_reason");
    // The dropdown shows an "add 'custom_reason'" hint — Enter commits.
    // Highlight=0 points to the (no-match) freeText fallback row in
    // this case; we look for the hint and mouse-down it.
    const menu = document.querySelector(".dropdown-menu") as HTMLElement;
    fireEvent.mouseDown(within(menu).getByText(/add "custom_reason"/));
    expect(onChange).toHaveBeenCalledWith(["custom_reason"]);
  });
});

describe("CodeBlock", () => {
  it("renders the code text and the language tag", () => {
    render(<CodeBlock code="hecate --help" lang="bash" />);
    // The header carries the lang label uppercased per CSS, but the DOM
    // text is the raw lowercase. Match either via case-insensitive text.
    expect(screen.getByText(/bash/i)).toBeTruthy();
    expect(screen.getByText(/hecate --help/)).toBeTruthy();
  });

  it("highlights diff lines by semantic type", () => {
    render(
      <CodeBlock lang="diff" code={"diff --git a/a b/a\n@@ -1 +1 @@\n-old\n+new\n context"} />,
    );

    expect(screen.getByText("diff --git a/a b/a")).toHaveClass("diff-line-meta");
    expect(screen.getByText("@@ -1 +1 @@")).toHaveClass("diff-line-hunk");
    expect(screen.getByText("-old")).toHaveClass("diff-line-remove");
    expect(screen.getByText("+new")).toHaveClass("diff-line-add");
    expect(document.querySelector(".diff-line-context")).toHaveTextContent("context");
  });

  it("Copy button copies the code to the clipboard", () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    render(<CodeBlock code="echo hi" />);
    // The copy button has no accessible label of its own — just an icon.
    // Use the closest button and click it.
    const btn = document.querySelector(".code-copy-btn") as HTMLButtonElement;
    fireEvent.click(btn);
    expect(writeText).toHaveBeenCalledWith("echo hi");
  });
});
