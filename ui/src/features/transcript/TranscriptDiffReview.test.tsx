import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentChatChangedFileDiffRecord, AgentChatChangedFileRecord } from "../../types/runtime";
import { TranscriptDiffReview } from "./TranscriptDiffReview";

function file(overrides: Partial<AgentChatChangedFileRecord> = {}): AgentChatChangedFileRecord {
  return {
    path: "src/foo.ts",
    status: "modified",
    additions: 3,
    deletions: 1,
    ...overrides,
  };
}

function fileDiff(overrides: Partial<AgentChatChangedFileDiffRecord> = {}): AgentChatChangedFileDiffRecord {
  return {
    path: "src/foo.ts",
    additions: 1,
    deletions: 1,
    status: "modified",
    diff: "@@ -1 +1 @@\n-old\n+new\n",
    ...overrides,
  };
}

describe("TranscriptDiffReview", () => {
  it("renders the static diff stat fallback when review APIs aren't wired", () => {
    render(
      <TranscriptDiffReview
        sessionID=""
        messageID="m1"
        diffStat="src/foo.ts | 3 +-"
        diff="@@ ..."
      />,
    );
    expect(screen.getByTestId("agent-diff-review")).toBeInTheDocument();
  });

  it("loads changed files when the operator opens the section", async () => {
    const onListFiles = vi.fn(async () => [file({ path: "src/a.ts" }), file({ path: "src/b.ts" })]);
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="2 files changed"
        onListFiles={onListFiles}
        onGetFileDiff={vi.fn(async () => null)}
        onRevertFiles={vi.fn(async () => true)}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(onListFiles).toHaveBeenCalledWith("s1", "m1"));
    await waitFor(() => expect(screen.getByText("src/a.ts")).toBeInTheDocument());
    expect(screen.getByText("src/b.ts")).toBeInTheDocument();
  });

  it("surfaces a friendly error when listing changed files throws", async () => {
    const onListFiles = vi.fn(async () => {
      throw new Error("boom");
    });
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="2 files changed"
        onListFiles={onListFiles}
        onGetFileDiff={vi.fn(async () => null)}
        onRevertFiles={vi.fn(async () => true)}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(screen.getByText(/Could not load changed files/)).toBeInTheDocument());
  });

  it("loads and displays the file diff when the operator clicks Inspect", async () => {
    const onListFiles = vi.fn(async () => [file({ path: "src/foo.ts" })]);
    const onGetFileDiff = vi.fn(async () => fileDiff());
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="1 file changed"
        onListFiles={onListFiles}
        onGetFileDiff={onGetFileDiff}
        onRevertFiles={vi.fn(async () => true)}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(screen.getByText("src/foo.ts")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /^Inspect src\/foo\.ts/ }));
    await waitFor(() => expect(onGetFileDiff).toHaveBeenCalledWith("s1", "m1", "src/foo.ts"));
    await waitFor(() => expect(screen.getByText(/diff · src\/foo\.ts/)).toBeInTheDocument());
  });

  it("surfaces an error when file diff load throws and clears the loading state", async () => {
    const onListFiles = vi.fn(async () => [file()]);
    const onGetFileDiff = vi.fn(async () => {
      throw new Error("boom");
    });
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="1 file changed"
        onListFiles={onListFiles}
        onGetFileDiff={onGetFileDiff}
        onRevertFiles={vi.fn(async () => true)}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(screen.getByText("src/foo.ts")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /^Inspect src\/foo\.ts/ }));
    await waitFor(() => expect(screen.getByText(/Could not load that file diff/)).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /^Inspect src\/foo\.ts/ })).not.toBeDisabled();
  });

  it("requires a confirm step before reverting an individual file", async () => {
    const onListFiles = vi.fn(async () => [file()]);
    const onRevertFiles = vi.fn(async () => true);
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="1 file changed"
        onListFiles={onListFiles}
        onGetFileDiff={vi.fn(async () => null)}
        onRevertFiles={onRevertFiles}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(screen.getByText("src/foo.ts")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /^Revert src\/foo\.ts/ }));
    expect(onRevertFiles).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: /^Confirm revert src\/foo\.ts/ }));
    await waitFor(() => expect(onRevertFiles).toHaveBeenCalledWith("s1", "m1", ["src/foo.ts"]));
  });

  it("surfaces a friendly error when revert returns false (e.g. non-git workspace)", async () => {
    const onListFiles = vi.fn(async () => [file()]);
    const onRevertFiles = vi.fn(async () => false);
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="1 file changed"
        onListFiles={onListFiles}
        onGetFileDiff={vi.fn(async () => null)}
        onRevertFiles={onRevertFiles}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(screen.getByText("src/foo.ts")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /^Revert src\/foo\.ts/ }));
    await user.click(screen.getByRole("button", { name: /^Confirm revert src\/foo\.ts/ }));
    await waitFor(() => expect(screen.getByText(/Revert failed/)).toBeInTheDocument());
  });

  it("supports revert-all with empty paths through the same confirm flow", async () => {
    const onListFiles = vi.fn(async () => [file({ path: "a" }), file({ path: "b" })]);
    const onRevertFiles = vi.fn(async () => true);
    const user = userEvent.setup();
    render(
      <TranscriptDiffReview
        sessionID="s1"
        messageID="m1"
        diffStat="2 files changed"
        onListFiles={onListFiles}
        onGetFileDiff={vi.fn(async () => null)}
        onRevertFiles={onRevertFiles}
      />,
    );
    await user.click(screen.getByText(/^files changed · /));
    await waitFor(() => expect(screen.getByText("a")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: "Revert all" }));
    await user.click(screen.getByRole("button", { name: "Confirm revert all" }));
    await waitFor(() => expect(onRevertFiles).toHaveBeenCalledWith("s1", "m1", []));
  });
});
