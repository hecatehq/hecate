import { useState } from "react";

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

  return <ConsoleShell actions={actions} activeWorkspace={activeWorkspace} onSelectWorkspace={handleSelectWorkspace} state={state} />;
}
