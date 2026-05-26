import { openWorkspaceTargetViaAPI } from "./api";
import { isTauriRuntime } from "./tauri";

export type WorkspaceOpenTargetID =
  | "vscode"
  | "vscode_insiders"
  | "cursor"
  | "zed"
  | "finder"
  | "terminal"
  | "iterm2"
  | "xcode";

export type WorkspaceOpenTarget = {
  id: WorkspaceOpenTargetID;
  label: string;
  detail: string;
  kind: "editor" | "terminal" | "folder";
};

const COMMON_TARGETS: WorkspaceOpenTarget[] = [
  { id: "vscode", label: "VS Code", detail: "Visual Studio Code", kind: "editor" },
  {
    id: "vscode_insiders",
    label: "VS Code Insiders",
    detail: "Visual Studio Code - Insiders",
    kind: "editor",
  },
  { id: "cursor", label: "Cursor", detail: "Cursor editor", kind: "editor" },
  { id: "zed", label: "Zed", detail: "Zed editor", kind: "editor" },
];

const MAC_TARGETS: WorkspaceOpenTarget[] = [
  ...COMMON_TARGETS,
  { id: "finder", label: "Finder", detail: "Open the folder", kind: "folder" },
  { id: "terminal", label: "Terminal", detail: "Open a shell in the workspace", kind: "terminal" },
  { id: "iterm2", label: "iTerm2", detail: "Open a shell in the workspace", kind: "terminal" },
  { id: "xcode", label: "Xcode", detail: "Open with Xcode", kind: "editor" },
];

const OTHER_DESKTOP_TARGETS: WorkspaceOpenTarget[] = [
  ...COMMON_TARGETS,
  { id: "finder", label: "Folder", detail: "Open the folder", kind: "folder" },
  { id: "terminal", label: "Terminal", detail: "Open a shell in the workspace", kind: "terminal" },
];

export function canOpenWorkspaceFromUI(): boolean {
  return true;
}

export function workspaceOpenTargets(): WorkspaceOpenTarget[] {
  return isMacOSHost() ? MAC_TARGETS : OTHER_DESKTOP_TARGETS;
}

export async function openWorkspaceTarget(
  path: string,
  target: WorkspaceOpenTargetID,
): Promise<void> {
  if (!canOpenWorkspaceFromUI()) {
    throw new Error("Workspace launch actions are unavailable.");
  }
  if (!isTauriRuntime()) {
    await openWorkspaceTargetViaAPI(path, target);
    return;
  }
  const { invoke } = await import("@tauri-apps/api/core");
  await invoke("open_workspace_target", { path, target });
}

function isMacOSHost(): boolean {
  return typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
}
