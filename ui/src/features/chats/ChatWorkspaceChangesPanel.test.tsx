import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ChatChangedFileDiffRecord,
  ChatWorkspaceDiffRecord,
  ChatWorkspaceFilesRecord,
  ChatWorkspaceLayeredDiffRecord,
  ChatWorkspaceReviewFileRecord,
  ChatWorkspaceReviewLayerKind,
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

function legacyChangedWorkspace(
  workspace: string,
  path: string,
  diff = "",
): ChatWorkspaceDiffRecord {
  return {
    workspace,
    revision: `revision:${workspace}:${path}:${diff.length}`,
    diff_stat: `${path} | 1 +\n1 file changed, 1 insertion(+)`,
    diff,
    has_changes: true,
    files: [{ path, additions: 1, deletions: 0, status: "modified" }],
  };
}

function reviewFile(
  id: string,
  layer: ChatWorkspaceReviewLayerKind,
  path: string,
  preview: ChatWorkspaceReviewFileRecord["preview"] = { kind: "unavailable" },
): ChatWorkspaceReviewFileRecord {
  return {
    id,
    layer,
    path,
    additions: preview.kind === "text_diff" ? 1 : 0,
    deletions: 0,
    status: layer === "untracked" ? "untracked" : "modified",
    preview,
  };
}

function layeredWorkspace(
  workspace: string,
  files: ChatWorkspaceReviewFileRecord[] = [],
  options: Partial<
    Pick<
      ChatWorkspaceLayeredDiffRecord,
      | "discard"
      | "has_changes"
      | "review_complete"
      | "review_issues"
      | "review_issues_omitted_count"
    >
  > = {},
): ChatWorkspaceLayeredDiffRecord {
  const layers = (["staged", "working_tree", "untracked"] as const).map((kind) => ({
    kind,
    complete: true,
    files: files.filter((file) => file.layer === kind),
  }));
  return {
    workspace,
    diff_stat: "",
    diff: "",
    has_changes: options.has_changes ?? files.length > 0,
    files: files
      .filter((file) => file.layer === "working_tree")
      .map(({ path, additions, deletions, status }) => ({ path, additions, deletions, status })),
    review_complete: options.review_complete ?? true,
    review_issues: options.review_issues,
    review_issues_omitted_count: options.review_issues_omitted_count,
    layers,
    discard: options.discard ?? {
      available: files.some((file) => file.layer === "working_tree"),
      revision: files.some((file) => file.layer === "working_tree")
        ? `discard:${workspace}`
        : undefined,
      reason: files.some((file) => file.layer === "working_tree")
        ? undefined
        : "no_working_tree_changes",
    },
  };
}

function changedWorkspace(workspace: string, path: string, diff = ""): ChatWorkspaceDiffRecord {
  const snapshot = layeredWorkspace(workspace, [
    reviewFile(
      `working:${path}`,
      "working_tree",
      path,
      diff ? { kind: "text_diff", content: diff } : { kind: "unavailable" },
    ),
  ]);
  snapshot.discard = {
    available: true,
    revision: `revision:${workspace}:${path}:${diff.length}`,
  };
  return snapshot;
}

function cleanWorkspace(workspace: string): ChatWorkspaceDiffRecord {
  return layeredWorkspace(workspace);
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
        ? legacyChangedWorkspace("/workspace/a", "a-only.ts", patchA)
        : legacyChangedWorkspace("/workspace/b", "b-only.ts"),
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

  it("moves focus into discard confirmation, restores it on cancel, and preserves it on submit", async () => {
    const pendingRevert = deferred<ChatWorkspaceDiffRecord | null>();
    const revertWorkspaceFiles = vi.fn(() => pendingRevert.promise);
    const user = userEvent.setup();
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

    const discard = await screen.findByRole("button", { name: "Discard README.md" });
    await user.click(discard);
    const confirm = screen.getByRole("button", { name: "Confirm discard README.md" });
    expect(confirm).toHaveFocus();

    await user.click(screen.getByRole("button", { name: "Cancel discard README.md" }));
    expect(discard).toHaveFocus();

    await user.click(discard);
    await user.click(screen.getByRole("button", { name: "Confirm discard README.md" }));
    expect(screen.getByRole("tab", { name: "Review" })).toHaveFocus();

    await act(async () => {
      pendingRevert.resolve(cleanWorkspace("/workspace/a"));
      await pendingRevert.promise;
    });
  });

  it("disables every layered row action while a discard request is pending", async () => {
    const pendingRevert = deferred<ChatWorkspaceDiffRecord | null>();
    const snapshot = layeredWorkspace(
      "/workspace/a",
      [
        reviewFile("entry-a", "working_tree", "a.txt", {
          kind: "text_diff",
          content: filePatch("a.txt", "a line"),
        }),
        reviewFile("entry-b", "working_tree", "b.txt", {
          kind: "text_diff",
          content: filePatch("b.txt", "b line"),
        }),
      ],
      { discard: { available: true, revision: "discard:two-files" } },
    );
    const revertWorkspaceFiles = vi.fn(() => pendingRevert.promise);
    const user = userEvent.setup();
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

    await user.click(await screen.findByRole("button", { name: "Discard a.txt" }));
    await user.click(screen.getByRole("button", { name: "Confirm discard a.txt" }));
    await waitFor(() => expect(revertWorkspaceFiles).toHaveBeenCalledTimes(1));

    const otherDiscard = screen.getByRole("button", { name: "Discard b.txt" });
    const otherCopy = screen.getByRole("button", { name: "Copy diff b.txt" });
    expect(otherDiscard).toBeDisabled();
    expect(otherCopy).toBeDisabled();
    await user.click(otherDiscard);
    expect(screen.queryByRole("button", { name: "Confirm discard b.txt" })).toBeNull();

    await act(async () => {
      pendingRevert.resolve(cleanWorkspace("/workspace/a"));
      await pendingRevert.promise;
    });
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

  it("fails closed when the layered discard capability has no revision", async () => {
    const snapshot = changedWorkspace("/workspace/a", "README.md");
    if ("discard" in snapshot && snapshot.discard) {
      snapshot.discard = { available: true, revision: "" };
    }
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

    expect(
      await screen.findByText("Discard is unavailable until the workspace is reviewed again."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Discard README.md" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Discard all working-tree changes" })).toBeNull();
    expect(revertWorkspaceFiles).not.toHaveBeenCalled();
  });

  it("renders fixed layers with independent opaque identities and discards only working-tree entries", async () => {
    const spoofingCharacters = `${String.fromCharCode(0x07)}${String.fromCharCode(0x061c)}${String.fromCharCode(0x200e)}${String.fromCharCode(0x200f)}${String.fromCharCode(0x202e)}`;
    const stagedPatch = filePatch("src/shared.ts", `staged line${spoofingCharacters}`);
    const staged = reviewFile("entry-staged", "staged", "src/shared.ts", {
      kind: "text_diff",
      content: stagedPatch,
    });
    const working = reviewFile("entry-working", "working_tree", "src/shared.ts", {
      kind: "text_diff",
      content: filePatch("src/shared.ts", "working line"),
    });
    const untracked = reviewFile("entry-untracked", "untracked", "notes.txt", {
      kind: "text",
      content: "untracked notes",
    });
    const snapshot = layeredWorkspace("/workspace/a", [staged, working, untracked], {
      discard: { available: true, revision: "discard:exact-working-tree" },
    });
    const getWorkspaceFileDiff = vi.fn(async () => null);
    const revertWorkspaceFiles = vi.fn(async () => layeredWorkspace("/workspace/a"));
    const writeText = vi.fn(async () => undefined);
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });

    render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={vi.fn(async () => snapshot)}
        onGetWorkspaceFiles={vi.fn(async () => null)}
        onGetWorkspaceFileDiff={getWorkspaceFileDiff}
        onRevertWorkspaceFiles={revertWorkspaceFiles}
      />,
    );

    const stagedRegion = await screen.findByRole("region", { name: "Staged changes" });
    const workingRegion = screen.getByRole("region", { name: "Working tree changes" });
    const untrackedRegion = screen.getByRole("region", { name: "Untracked changes" });
    expect(stagedRegion.compareDocumentPosition(workingRegion)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(workingRegion.compareDocumentPosition(untrackedRegion)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(screen.getAllByText("Read only")).toHaveLength(2);
    expect(getWorkspaceFileDiff).not.toHaveBeenCalled();
    const richDiff = document.querySelector(".diff-viewer-file");
    expect(richDiff).toBeTruthy();
    const displayedDiff =
      richDiff?.shadowRoot?.textContent ??
      screen.getByTestId("workspace-review-diff-preview").textContent ??
      "";
    expect(displayedDiff).toContain("\\u0007\\u061C\\u200E\\u200F\\u202E");
    for (const character of Array.from(spoofingCharacters)) {
      expect(displayedDiff).not.toContain(character);
    }
    await user.click(screen.getAllByRole("button", { name: "Copy diff src/shared.ts" })[0]);
    expect(writeText).toHaveBeenCalledWith(stagedPatch);

    const sharedToggles = screen
      .getAllByRole("button", { name: /diff src\/shared\.ts/i })
      .filter((button) => button.hasAttribute("aria-expanded"));
    expect(sharedToggles).toHaveLength(2);
    expect(sharedToggles[0]).toHaveAttribute("aria-expanded", "true");
    expect(sharedToggles[0]).toHaveAccessibleName(
      "Collapse diff src/shared.ts; Modified file; 1 addition, 0 deletions",
    );
    expect(
      document.getElementById(sharedToggles[0].getAttribute("aria-controls") ?? ""),
    ).toBeTruthy();
    expect(sharedToggles[1]).toHaveAttribute("aria-expanded", "false");
    expect(sharedToggles[1]).not.toHaveAttribute("aria-controls");
    await user.click(sharedToggles[1]);
    expect(sharedToggles[0]).toHaveAttribute("aria-expanded", "true");
    expect(sharedToggles[1]).toHaveAttribute("aria-expanded", "true");
    expect(
      document.getElementById(sharedToggles[1].getAttribute("aria-controls") ?? ""),
    ).toBeTruthy();

    const discardButtons = screen.getAllByRole("button", { name: "Discard src/shared.ts" });
    expect(discardButtons).toHaveLength(1);
    await user.click(discardButtons[0]);
    await user.click(screen.getByRole("button", { name: "Confirm discard src/shared.ts" }));
    await waitFor(() =>
      expect(revertWorkspaceFiles).toHaveBeenCalledWith(
        "chat_a",
        ["src/shared.ts"],
        "discard:exact-working-tree",
      ),
    );
    expect(getWorkspaceFileDiff).not.toHaveBeenCalled();
  });

  it("uses the nested discard revision for the working-tree bulk action", async () => {
    const snapshot = {
      ...layeredWorkspace("/workspace/a", [
        reviewFile("entry-working", "working_tree", "README.md", {
          kind: "text_diff",
          content: readmePatch,
        }),
      ]),
      revision: "legacy-revision-must-not-authorize",
      discard: { available: true, revision: "discard:nested-authority" },
    } satisfies ChatWorkspaceLayeredDiffRecord;
    const revertWorkspaceFiles = vi.fn(async () => layeredWorkspace("/workspace/a"));
    const user = userEvent.setup();

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

    await user.click(
      await screen.findByRole("button", { name: "Discard all working-tree changes" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Confirm discard all working-tree changes" }),
    );
    await waitFor(() =>
      expect(revertWorkspaceFiles).toHaveBeenCalledWith("chat_a", [], "discard:nested-authority"),
    );
  });

  it("renders every bounded inline preview state and visibly escapes unsafe path characters", async () => {
    const unsafePath = `notes/${String.fromCharCode(0x0a)}${String.fromCharCode(0x202e)}cod.exe`;
    const files = [
      reviewFile("entry-diff", "staged", "src/app.ts", {
        kind: "text_diff",
        content: filePatch("src/app.ts", "safe line"),
      }),
      reviewFile("entry-text", "untracked", unsafePath, {
        kind: "text",
        content: "<script>not markup</script>",
      }),
      reviewFile("entry-binary", "untracked", "asset.bin", { kind: "binary" }),
      reviewFile("entry-large", "untracked", "large.txt", {
        kind: "too_large",
        reason: "file_limit",
      }),
      reviewFile("entry-symlink", "untracked", "shortcut", { kind: "symlink" }),
      reviewFile("entry-special", "untracked", "pipe", { kind: "special" }),
      reviewFile("entry-repo", "untracked", "vendor/repo", { kind: "nested_repository" }),
      reviewFile("entry-conflict", "working_tree", "conflicted.ts", {
        kind: "conflict",
        reason: "unmerged",
      }),
      reviewFile("entry-unavailable", "untracked", "changing.txt", {
        kind: "unavailable",
        reason: "changed_during_read",
      }),
      reviewFile("entry-timeout", "untracked", "slow.txt", {
        kind: "unavailable",
        reason: "read_timeout",
      }),
    ];
    const snapshot = layeredWorkspace("/workspace/a", files, {
      discard: { available: false, reason: "working_tree_preview_incomplete" },
      review_complete: false,
      review_issues: [{ kind: "assume_unchanged", path: unsafePath }],
      review_issues_omitted_count: 3,
    });
    snapshot.layers = snapshot.layers.map((layer) =>
      layer.kind === "untracked" ? { ...layer, complete: false, omitted_count: 2 } : layer,
    );
    const user = userEvent.setup();

    render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={vi.fn(async () => snapshot)}
        onGetWorkspaceFiles={vi.fn(async () => null)}
        onGetWorkspaceFileDiff={vi.fn(async () => null)}
        onRevertWorkspaceFiles={vi.fn(async () => null)}
      />,
    );

    expect(await screen.findByText("Review incomplete.")).toBeTruthy();
    expect(screen.getAllByText(/notes\/\\u000A\\u202Ecod\.exe/).length).toBeGreaterThan(0);
    expect(screen.queryByText(unsafePath)).toBeNull();
    expect(screen.getByText("2 additional entries were omitted by the review limit.")).toBeTruthy();
    expect(
      screen.getByText("3 additional review issues were omitted by the response limit."),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Discard is unavailable because one or more working-tree previews are incomplete. Refresh or inspect Git directly.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Discard conflicted.ts" })).toBeNull();

    const cases = [
      ["preview notes/\\u000A\\u202Ecod.exe", "<script>not markup</script>"],
      ["preview asset.bin", "Binary content is not displayed."],
      [
        "preview large.txt",
        "Preview omitted because this entry exceeds the per-file content limit.",
      ],
      ["preview shortcut", "Symlink targets are not opened from workspace review."],
      ["preview pipe", "This non-regular file is not opened from workspace review."],
      ["preview vendor/repo", "Nested repository contents are not opened from workspace review."],
      [
        "preview conflicted.ts",
        "Git reports an unresolved conflict. Inspect and resolve it directly in the workspace.",
      ],
      ["preview changing.txt", "The file changed while it was being read. Refresh to try again."],
      ["preview slow.txt", "The file preview timed out. Refresh to try again."],
    ] as const;
    for (const [accessibleName, expected] of cases) {
      await user.click(
        screen.getByRole("button", {
          name: (name) => name.startsWith(`Expand ${accessibleName};`),
        }),
      );
      expect(screen.getByText(expected)).toBeTruthy();
    }
    expect(document.querySelector("script")).toBeNull();
  });

  it("supports arrow-key tab navigation with linked tab panels", async () => {
    const user = userEvent.setup();
    render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={vi.fn(async () => layeredWorkspace("/workspace/a"))}
        onGetWorkspaceFiles={vi.fn(async () => ({ workspace: "/workspace/a", files: [] }))}
        onGetWorkspaceFileDiff={vi.fn(async () => null)}
        onRevertWorkspaceFiles={vi.fn(async () => null)}
      />,
    );

    const reviewTab = await screen.findByRole("tab", { name: "Review" });
    reviewTab.focus();
    await user.keyboard("{ArrowRight}");
    const filesTab = screen.getByRole("tab", { name: "Files" });
    expect(filesTab).toHaveFocus();
    expect(filesTab).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", filesTab.id);

    await user.keyboard("{Home}");
    expect(reviewTab).toHaveFocus();
    expect(reviewTab).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", reviewTab.id);
    expect(document.getElementById(reviewTab.getAttribute("aria-controls") ?? "")).toBeTruthy();
    expect(filesTab).not.toHaveAttribute("aria-controls");

    await user.keyboard("{ArrowLeft}");
    expect(filesTab).toHaveFocus();
    expect(filesTab).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowRight}");
    expect(reviewTab).toHaveFocus();
    expect(reviewTab).toHaveAttribute("aria-selected", "true");
  });

  it.each([
    [
      "cleanup_failed",
      "Git cleanup did not finish after discard. Refresh and check Git before continuing.",
    ],
    [
      "mutation_outcome_unknown",
      "The discard command did not finish cleanly. Refresh and verify every selected file before continuing.",
    ],
  ])("explains the %s post-discard state", async (reason, message) => {
    render(
      <ChatWorkspaceChangesPanel
        sessionID="chat_a"
        workspace="/workspace/a"
        onGetWorkspaceDiff={vi.fn(async () =>
          layeredWorkspace("/workspace/a", [], {
            discard: { available: false, reason },
            has_changes: true,
            review_complete: false,
          }),
        )}
        onGetWorkspaceFiles={vi.fn(async () => null)}
        onGetWorkspaceFileDiff={vi.fn(async () => null)}
        onRevertWorkspaceFiles={vi.fn(async () => null)}
      />,
    );

    expect(await screen.findByText(message)).toBeTruthy();
  });
});
