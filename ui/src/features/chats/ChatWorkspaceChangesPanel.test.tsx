import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ChatWorkspaceChangesPanel } from "./ChatWorkspaceChangesPanel";

const readmePatch = [
  "diff --git a/README.md b/README.md",
  "index 1111111..2222222 100644",
  "--- a/README.md",
  "+++ b/README.md",
  "@@ -1 +1 @@",
  "-old readme",
  "+live workspace line",
].join("\n");

describe("ChatWorkspaceChangesPanel", () => {
  it("refreshes review and loaded file tree when refresh signal changes", async () => {
    const getWorkspaceDiff = vi
      .fn()
      .mockResolvedValueOnce({
        workspace: "/tmp/hecate",
        diff_stat: "",
        diff: "",
        has_changes: false,
        files: [],
      })
      .mockResolvedValue({
        workspace: "/tmp/hecate",
        diff_stat: "README.md | 1 +\n1 file changed, 1 insertion(+)",
        diff: readmePatch,
        has_changes: true,
        files: [{ path: "README.md", additions: 1, deletions: 0, status: "modified" }],
      });
    const getWorkspaceFiles = vi
      .fn()
      .mockResolvedValueOnce({
        workspace: "/tmp/hecate",
        files: [{ path: "README.md", kind: "file", size_bytes: 12, status: "clean" }],
        truncated: false,
      })
      .mockResolvedValue({
        workspace: "/tmp/hecate",
        files: [
          { path: "README.md", kind: "file", size_bytes: 12, status: "modified" },
          { path: "docs/notes.md", kind: "file", size_bytes: 10, status: "clean" },
        ],
        truncated: false,
      });
    const getWorkspaceFileDiff = vi.fn(async () => ({
      path: "README.md",
      additions: 1,
      deletions: 0,
      status: "modified",
      diff: readmePatch,
    }));
    const revertWorkspaceFiles = vi.fn();

    const view = render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_1"
        workspace="/tmp/hecate"
        refreshSignal={0}
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    expect(await screen.findByText("The current workspace is clean.")).toBeTruthy();
    expect(getWorkspaceDiff).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByRole("tab", { name: /Files/i }));
    expect(await screen.findByText("README.md")).toBeTruthy();
    expect(getWorkspaceFiles).toHaveBeenCalledTimes(1);

    view.rerender(
      <ChatWorkspaceChangesPanel
        sessionID="chat_1"
        workspace="/tmp/hecate"
        refreshSignal={1}
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    await waitFor(() => expect(getWorkspaceDiff).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(getWorkspaceFiles).toHaveBeenCalledTimes(2));
    await userEvent.click(await screen.findByRole("button", { name: "Expand folder docs" }));
    expect(await screen.findByText("notes.md")).toBeTruthy();

    await userEvent.click(screen.getByRole("tab", { name: /Review/i }));
    expect(await screen.findByText("1 file changed, 1 insertion(+)")).toBeTruthy();
    expect(await screen.findByRole("region", { name: "Diff README.md" })).toBeTruthy();
  });
});
