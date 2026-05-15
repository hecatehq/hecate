import { useEffect, useState } from "react";

import { ConsoleShell, getAvailableWorkspaces, type WorkspaceID } from "./AppShell";
import { useRuntimeConsole } from "./useRuntimeConsole";
import { isTauriRuntime } from "../lib/tauri";

const WORKSPACE_STORAGE_KEY = "hecate.workspace.v2";

export default function App() {
  const { state, actions } = useRuntimeConsole();
  const [preferredWorkspace, setPreferredWorkspace] = useState<WorkspaceID>(() => {
    const saved = localStorage.getItem(WORKSPACE_STORAGE_KEY);
    return (saved as WorkspaceID) ?? "chats";
  });

  const workspaces = getAvailableWorkspaces();
  const activeWorkspace: WorkspaceID =
    workspaces.some(w => w.id === preferredWorkspace) ? preferredWorkspace : "overview";

  function handleSelectWorkspace(id: WorkspaceID) {
    localStorage.setItem(WORKSPACE_STORAGE_KEY, id);
    setPreferredWorkspace(id);
  }

  useEffect(() => {
    return installTauriEditShortcutFallback();
  }, []);

  return <ConsoleShell actions={actions} activeWorkspace={activeWorkspace} onSelectWorkspace={handleSelectWorkspace} state={state} />;
}

export function installTauriEditShortcutFallback(): () => void {
  if (!isTauriRuntime()) return () => undefined;
  // Skip on macOS — the native Edit submenu (lib.rs) installs the
  // standard Cut/Copy/Paste/SelectAll items, and the OS routes
  // their canonical shortcuts to the first responder text field
  // natively. Intercepting Cmd+V ourselves used to be necessary on
  // older webviews, but now it just routes through
  // navigator.clipboard.readText() — which on macOS Sequoia
  // triggers the system "Paste" privacy prompt every time the
  // user pastes. Letting the OS handle it skips the prompt.
  if (/mac/i.test(navigator.platform)) return () => undefined;
  const handler = (event: KeyboardEvent) => {
    const editable = editableTarget(event.target);
    if (!editable) return;
    const modPressed = event.ctrlKey;
    if (!modPressed || event.altKey) return;

    const key = event.key.toLowerCase();
    if (key === "a") {
      event.preventDefault();
      editable.select();
      return;
    }
    if (key === "c" || key === "x") {
      event.preventDefault();
      document.execCommand(key === "c" ? "copy" : "cut");
      return;
    }
    if (key === "v") {
      event.preventDefault();
      void pasteIntoEditable(editable);
    }
  };
  window.addEventListener("keydown", handler);
  return () => window.removeEventListener("keydown", handler);
}

function editableTarget(target: EventTarget | null): HTMLInputElement | HTMLTextAreaElement | null {
  if (target instanceof HTMLTextAreaElement) {
    if (target.disabled || target.readOnly) {
      return null;
    }
    return target;
  }
  if (!(target instanceof HTMLInputElement) || !isTextInput(target)) {
    return null;
  }
  if (target.disabled || target.readOnly) return null;
  return target;
}

function isTextInput(input: HTMLInputElement): boolean {
  return [
    "",
    "email",
    "password",
    "search",
    "tel",
    "text",
    "url",
  ].includes(input.type);
}

async function pasteIntoEditable(target: HTMLInputElement | HTMLTextAreaElement) {
  if (target.selectionStart === null || target.selectionEnd === null) {
    return;
  }
  const text = await navigator.clipboard?.readText().catch(() => "");
  if (!text) return;
  const start = target.selectionStart;
  const end = target.selectionEnd;
  target.setRangeText(text, start, end, "end");
  target.dispatchEvent(new Event("input", { bubbles: true }));
}
