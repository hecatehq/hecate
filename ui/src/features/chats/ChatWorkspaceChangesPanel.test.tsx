import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ChatChangedFileDiffRecord,
  ChatWorkspaceDiffRecord,
  ChatWorkspaceFilesRecord,
} from "../../types/chat";

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

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

function changedWorkspace(workspace: string, path: string, diff = ""): ChatWorkspaceDiffRecord {
  return {
    workspace,
    revision: `revision:${workspace}:${path}:${diff.length}`,
    diff_stat: `${path} | 1 +\n1 file changed, 1 insertion(+)`,
    diff,
    has_changes: true,
    files: [{ path, additions: 1, deletions: 0, status: "modified" }],
  };
}

function cleanWorkspace(workspace: string): ChatWorkspaceDiffRecord {
  return {
    workspace,
    revision: `revision:${workspace}:clean`,
    diff_stat: "",
    diff: "",
    has_changes: false,
    files: [],
  };
}

function filePatch(path: string, line: string) {
  return [
    `diff --git a/${path} b/${path}`,
    "index 1111111..2222222 100644",
    `--- a/${path}`,
    `+++ b/${path}`,
    "@@ -1 +1 @@",
    "-old line",
    `+${line}`,
  ].join("\n");
}

describe("ChatWorkspaceChangesPanel", () => {
  it("refreshes review and loaded file tree when refresh signal changes", async () => {
    const getWorkspaceDiff = vi
      .fn()
      .mockResolvedValueOnce({
        workspace: "/tmp/hecate",
        revision: "revision:clean",
        diff_stat: "",
        diff: "",
        has_changes: false,
        files: [],
      })
      .mockResolvedValue({
        workspace: "/tmp/hecate",
        revision: "revision:changed",
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

  it("ignores a late review response when the workspace changes for the same session", async () => {
    const lateA = deferred<ChatWorkspaceDiffRecord | null>();
    const getWorkspaceDiff = vi
      .fn<(sessionID: string) => Promise<ChatWorkspaceDiffRecord | null>>()
      .mockImplementationOnce(() => lateA.promise)
      .mockResolvedValueOnce(changedWorkspace("/workspace/b", "b-only.txt"));
    const getWorkspaceFiles = vi.fn(async () => null);
    const getWorkspaceFileDiff = vi.fn(async () => null);
    const revertWorkspaceFiles = vi.fn(async () => null);

    const view = render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_shared"
        workspace="/workspace/a"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );
    await waitFor(() => expect(getWorkspaceDiff).toHaveBeenCalledWith("chat_shared"));

    view.rerender(
      <ChatWorkspaceChangesPanel
        sessionID="chat_shared"
        workspace="/workspace/b"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    expect(await screen.findByText("b-only.txt")).toBeTruthy();

    await act(async () => {
      lateA.resolve(changedWorkspace("/workspace/a", "a-late.txt"));
      await lateA.promise;
    });

    await waitFor(() => expect(screen.queryByText("a-late.txt")).toBeNull());
    expect(screen.getByText("b-only.txt")).toBeTruthy();
  });

  it("ignores a late workspace tree from the previous session and workspace owner", async () => {
    const lateA = deferred<ChatWorkspaceFilesRecord | null>();
    const getWorkspaceDiff = vi.fn(async (sessionID: string) =>
      cleanWorkspace(sessionID === "chat_a" ? "/workspace/a" : "/workspace/b"),
    );
    const getWorkspaceFiles = vi.fn((sessionID: string) => {
      if (sessionID === "chat_a") {
        return lateA.promise;
      }
      return Promise.resolve<ChatWorkspaceFilesRecord>({
        workspace: "/workspace/b",
        files: [{ path: "b-only.txt", name: "b-only.txt", kind: "file", status: "clean" }],
        truncated: false,
      });
    });
    const getWorkspaceFileDiff = vi.fn(async () => null);
    const revertWorkspaceFiles = vi.fn(async () => null);
    const user = userEvent.setup();

    const view = render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );
    expect(await screen.findByText("The current workspace is clean.")).toBeTruthy();
    await user.click(screen.getByRole("tab", { name: /Files/i }));
    await waitFor(() => expect(getWorkspaceFiles).toHaveBeenCalledWith("chat_a"));

    view.rerender(
      <ChatWorkspaceChangesPanel
        sessionID="chat_b"
        workspace="/workspace/b"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    expect(await screen.findByText("b-only.txt")).toBeTruthy();

    await act(async () => {
      lateA.resolve({
        workspace: "/workspace/a",
        files: [{ path: "a-late.txt", name: "a-late.txt", kind: "file", status: "clean" }],
        truncated: false,
      });
      await lateA.promise;
    });

    await waitFor(() => expect(screen.queryByText("a-late.txt")).toBeNull());
    expect(screen.getByText("b-only.txt")).toBeTruthy();
  });

  it("ignores a late nested file diff from the previous snapshot owner", async () => {
    const lateFileA = deferred<ChatChangedFileDiffRecord | null>();
    const patchA = filePatch("a-only.ts", "late A nested content");
    const getWorkspaceDiff = vi.fn(async (sessionID: string) =>
      sessionID === "chat_a"
        ? changedWorkspace("/workspace/a", "a-only.ts", patchA)
        : changedWorkspace("/workspace/b", "b-only.ts"),
    );
    const getWorkspaceFiles = vi.fn(async () => null);
    const getWorkspaceFileDiff = vi.fn((sessionID: string) => {
      if (sessionID === "chat_a") {
        return lateFileA.promise;
      }
      return Promise.resolve(null);
    });
    const revertWorkspaceFiles = vi.fn(async () => null);

    const view = render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );
    await waitFor(() => expect(getWorkspaceFileDiff).toHaveBeenCalledWith("chat_a", "a-only.ts"));

    view.rerender(
      <ChatWorkspaceChangesPanel
        sessionID="chat_b"
        workspace="/workspace/b"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    expect(await screen.findByText("b-only.ts")).toBeTruthy();

    await act(async () => {
      lateFileA.resolve({
        path: "a-only.ts",
        additions: 1,
        deletions: 1,
        status: "modified",
        diff: patchA,
      });
      await lateFileA.promise;
    });

    await waitFor(() => expect(screen.queryByText("late A nested content")).toBeNull());
    expect(screen.getByText("b-only.ts")).toBeTruthy();
    expect(screen.queryByText("Could not load that file diff.")).toBeNull();
  });

  it("clears an old confirmation before a new owner can submit its path", async () => {
    const pendingB = deferred<ChatWorkspaceDiffRecord | null>();
    const getWorkspaceDiff = vi.fn((sessionID: string) =>
      sessionID === "chat_a"
        ? Promise.resolve(changedWorkspace("/workspace/a", "a-only.txt"))
        : pendingB.promise,
    );
    const getWorkspaceFiles = vi.fn(async () => null);
    const getWorkspaceFileDiff = vi.fn(async () => null);
    const revertWorkspaceFiles = vi.fn(async (sessionID: string) =>
      cleanWorkspace(sessionID === "chat_a" ? "/workspace/a" : "/workspace/b"),
    );
    const user = userEvent.setup();

    const view = render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );
    expect(await screen.findByText("a-only.txt")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Discard a-only.txt" }));
    expect(screen.getByRole("button", { name: "Confirm discard a-only.txt" })).toBeTruthy();

    view.rerender(
      <ChatWorkspaceChangesPanel
        sessionID="chat_b"
        workspace="/workspace/b"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Confirm discard a-only.txt" })).toBeNull(),
    );
    expect(revertWorkspaceFiles).not.toHaveBeenCalled();

    await act(async () => {
      pendingB.resolve(changedWorkspace("/workspace/b", "b-only.txt"));
      await pendingB.promise;
    });
    expect(await screen.findByText("b-only.txt")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Discard b-only.txt" }));
    await user.click(screen.getByRole("button", { name: "Confirm discard b-only.txt" }));

    await waitFor(() =>
      expect(revertWorkspaceFiles).toHaveBeenCalledWith(
        "chat_b",
        ["b-only.txt"],
        "revision:/workspace/b:b-only.txt:0",
      ),
    );
    expect(revertWorkspaceFiles).not.toHaveBeenCalledWith(
      "chat_b",
      ["a-only.txt"],
      expect.any(String),
    );
  });

  it("keeps a late revert result bound to the session that submitted it", async () => {
    const lateRevertA = deferred<ChatWorkspaceDiffRecord | null>();
    const getWorkspaceDiff = vi.fn(async (sessionID: string) =>
      sessionID === "chat_a"
        ? changedWorkspace("/workspace/a", "a-only.txt")
        : changedWorkspace("/workspace/b", "b-only.txt"),
    );
    const getWorkspaceFiles = vi.fn(async () => null);
    const getWorkspaceFileDiff = vi.fn(async () => null);
    const revertWorkspaceFiles = vi.fn((sessionID: string) =>
      sessionID === "chat_a"
        ? lateRevertA.promise
        : Promise.resolve(cleanWorkspace("/workspace/b")),
    );
    const user = userEvent.setup();

    const view = render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );
    expect(await screen.findByText("a-only.txt")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Discard a-only.txt" }));
    await user.click(screen.getByRole("button", { name: "Confirm discard a-only.txt" }));
    await waitFor(() =>
      expect(revertWorkspaceFiles).toHaveBeenCalledWith(
        "chat_a",
        ["a-only.txt"],
        "revision:/workspace/a:a-only.txt:0",
      ),
    );

    view.rerender(
      <ChatWorkspaceChangesPanel
        sessionID="chat_b"
        workspace="/workspace/b"
        onGetWorkspaceDiff={getWorkspaceDiff}
        onGetWorkspaceFiles={getWorkspaceFiles}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );
    expect(await screen.findByText("b-only.txt")).toBeTruthy();

    await act(async () => {
      lateRevertA.resolve(cleanWorkspace("/workspace/a"));
      await lateRevertA.promise;
    });

    await waitFor(() => expect(screen.getByText("b-only.txt")).toBeTruthy());
    expect(screen.queryByText("The current workspace is clean.")).toBeNull();
    expect(revertWorkspaceFiles).not.toHaveBeenCalledWith(
      "chat_b",
      ["a-only.txt"],
      expect.any(String),
    );
  });

  it("keeps discard disabled while agent work is active", async () => {
    const getWorkspaceDiff = vi.fn(async () => changedWorkspace("/workspace/a", "README.md"));
    const revertWorkspaceFiles = vi.fn(async () => cleanWorkspace("/workspace/a"));
    const common = {
      sessionID: "chat_a",
      workspace: "/workspace/a",
      onGetWorkspaceDiff: getWorkspaceDiff,
      onGetWorkspaceFiles: vi.fn(async () => null),
      onGetWorkspaceFileDiff: vi.fn(async () => null),
      onRevertWorkspaceFiles: revertWorkspaceFiles,
    };
    const view = render(<ChatWorkspaceChangesPanel {...common} revertDisabled />);

    const discard = await screen.findByRole("button", { name: "Discard README.md" });
    expect(discard).toBeDisabled();

    view.rerender(<ChatWorkspaceChangesPanel {...common} revertDisabled={false} />);
    await userEvent.click(screen.getByRole("button", { name: "Discard README.md" }));
    view.rerender(<ChatWorkspaceChangesPanel {...common} revertDisabled />);

    const confirm = screen.getByRole("button", { name: "Confirm discard README.md" });
    expect(confirm).toBeDisabled();
    await userEvent.click(confirm);
    expect(revertWorkspaceFiles).not.toHaveBeenCalled();
  });

  it("invalidates reviewed mutation authority when discard returns no snapshot", async () => {
    const revertWorkspaceFiles = vi.fn(async () => null);
    render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={vi.fn(async () => changedWorkspace("/workspace/a", "README.md"))}
        onGetWorkspaceFiles={vi.fn(async () => null)}
        onGetWorkspaceFileDiff={vi.fn(async () => null)}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    await userEvent.click(await screen.findByRole("button", { name: "Discard README.md" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm discard README.md" }));

    expect(await screen.findByText("Could not discard those workspace changes.")).toBeTruthy();
    expect(screen.getByText("Could not load the current workspace diff.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Discard README.md" })).toBeNull();
    expect(screen.getByRole("button", { name: "Refresh" })).not.toBeDisabled();
  });

  it("fails closed when a workspace snapshot has no revision", async () => {
    const snapshot = { ...changedWorkspace("/workspace/a", "README.md"), revision: "" };
    const revertWorkspaceFiles = vi.fn(async () => cleanWorkspace("/workspace/a"));
    render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={vi.fn(async () => snapshot)}
        onGetWorkspaceFiles={vi.fn(async () => null)}
        onGetWorkspaceFileDiff={vi.fn(async () => null)}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    const discard = await screen.findByRole("button", { name: "Discard README.md" });
    expect(discard).toBeDisabled();
    await userEvent.click(discard);
    expect(revertWorkspaceFiles).not.toHaveBeenCalled();
  });
});
