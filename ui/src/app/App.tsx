import { useEffect, useLayoutEffect } from "react";

import { ConsoleShell, getAvailableWorkspaces, WORKSPACE_IDS, type WorkspaceID } from "./AppShell";
import { ApprovalsProvider } from "./state/approvals";
import { ProvidersAndModelsProvider } from "./state/providersAndModels";
import { RetentionProvider } from "./state/retention";
import { RuntimeProvider } from "./state/runtime";
import { UsageProvider } from "./state/usage";
import { useRuntimeConsole } from "./useRuntimeConsole";
import { usePersistedState } from "../lib/persistedState";
import { isTauriRuntime } from "../lib/tauri";

const WORKSPACE_STORAGE_KEY = "hecate.workspace.v2";

// Derive the validity guard from the single AppShell tuple so a new
// workspace doesn't silently fail the parse here.
const VALID_WORKSPACE_IDS = new Set<WorkspaceID>(WORKSPACE_IDS);
const parseWorkspaceID = (raw: string): WorkspaceID | null =>
  VALID_WORKSPACE_IDS.has(raw as WorkspaceID) ? (raw as WorkspaceID) : null;

// The slice providers wrap the console body so useRuntimeConsole's
// internal slice consumers (`useRetention`, …) see their context.
// As more slices are added the wrapper chain grows here; once
// prop-drill is retired and consumers read context directly, a
// single `<RuntimeConsoleProviders>` composer will collapse this
// chain.
export default function App() {
  return (
    <RuntimeProvider>
      <UsageProvider>
        <ProvidersAndModelsProvider>
          <RetentionProvider>
            <ApprovalsProvider>
              <AppConsole />
            </ApprovalsProvider>
          </RetentionProvider>
        </ProvidersAndModelsProvider>
      </UsageProvider>
    </RuntimeProvider>
  );
}

function AppConsole() {
  const { state, actions } = useRuntimeConsole();
  const [preferredWorkspace, setPreferredWorkspace] = usePersistedState<WorkspaceID>(
    WORKSPACE_STORAGE_KEY,
    parseWorkspaceID,
    "chats",
  );

  const workspaces = getAvailableWorkspaces();
  const activeWorkspace: WorkspaceID =
    workspaces.some(w => w.id === preferredWorkspace) ? preferredWorkspace : "overview";

  function handleSelectWorkspace(id: WorkspaceID) {
    setPreferredWorkspace(id);
  }

  useEffect(() => {
    return installTauriEditShortcutFallback();
  }, []);

  // useLayoutEffect (not useEffect): the marker toggles padding-top
  // and the drag-region's display in App.css via html[data-tauri].
  // Running this after first paint would leave one frame where the
  // shell renders without the 28-px titlebar inset, then jumps. A
  // layout effect runs synchronously after DOM mutations but before
  // the browser paints, so the very first frame already accounts
  // for the overlay titlebar.
  useLayoutEffect(() => {
    return installTauriDocumentMarkers();
  }, []);

  return <ConsoleShell actions={actions} activeWorkspace={activeWorkspace} onSelectWorkspace={handleSelectWorkspace} state={state} />;
}

export function installTauriDocumentMarkers(): () => void {
  if (!isTauriRuntime()) return () => undefined;
  document.documentElement.dataset.tauri = "true";
  // Surface the platform so App.css can reserve room for the macOS
  // overlay-titlebar traffic-light cluster (left 76px). On Linux /
  // Windows the native titlebar lives outside the webview and no
  // padding is needed.
  if (/mac/i.test(navigator.platform)) {
    document.documentElement.dataset.tauriOs = "macos";
  }
  return () => {
    delete document.documentElement.dataset.tauri;
    delete document.documentElement.dataset.tauriOs;
  };
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
