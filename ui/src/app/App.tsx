import { useEffect, useState } from "react";

import { ConsoleShell, getAvailableWorkspaces, type WorkspaceID } from "./AppShell";
import { useRuntimeConsole } from "./useRuntimeConsole";

const WORKSPACE_STORAGE_KEY = "hecate.workspace";

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
  const handler = (event: KeyboardEvent) => {
    const editable = editableTarget(event.target);
    if (!editable) return;
    const isMac = /mac/i.test(navigator.platform);
    const modPressed = isMac ? event.metaKey : event.ctrlKey;
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

function isTauriRuntime(): boolean {
  return typeof window !== "undefined"
    && (Object.prototype.hasOwnProperty.call(window, "__TAURI_INTERNALS__")
      || Object.prototype.hasOwnProperty.call(window, "__TAURI__"));
}

function editableTarget(target: EventTarget | null): HTMLInputElement | HTMLTextAreaElement | null {
  if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) {
    return null;
  }
  if (target.disabled || target.readOnly) {
    return null;
  }
  return target;
}

async function pasteIntoEditable(target: HTMLInputElement | HTMLTextAreaElement) {
  const text = await navigator.clipboard?.readText().catch(() => "");
  if (!text) return;
  const start = target.selectionStart ?? target.value.length;
  const end = target.selectionEnd ?? target.value.length;
  target.setRangeText(text, start, end, "end");
  target.dispatchEvent(new Event("input", { bubbles: true }));
}
