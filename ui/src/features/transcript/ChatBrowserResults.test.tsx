import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, getTaskRunArtifact } from "../../lib/api";
import type { ChatActivityRecord } from "../../types/chat";
import type { TaskArtifactRecord, TaskArtifactResponse } from "../../types/task";
import { ChatBrowserResults } from "./ChatBrowserResults";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  getTaskRunArtifact: vi.fn(),
}));

const run = { taskID: "task-original", runID: "run-original" };
const report: TaskArtifactRecord = {
  id: "report-1",
  task_id: run.taskID,
  run_id: run.runID,
  step_id: "step-1",
  kind: "browser_evidence",
  mime_type: "text/plain",
  storage_kind: "inline",
  status: "ready",
  content_text: "A retained browser report",
};
const artifactActivity: ChatActivityRecord = {
  id: "artifact-activity",
  type: "artifact",
  artifact_id: report.id,
  step_id: report.step_id,
  kind: report.kind,
  status: "ready",
  title: "Inspection report",
};
const toolActivity: ChatActivityRecord = {
  id: "tool-activity",
  type: "tool_call",
  title: "Browser inspection",
  step_id: report.step_id,
  status: "completed",
};
const activities = [artifactActivity, toolActivity];
const response = (data: TaskArtifactRecord = report): TaskArtifactResponse => ({
  object: "task_artifact",
  data,
});
const load = vi.mocked(getTaskRunArtifact);

beforeEach(() => {
  load.mockReset();
  load.mockResolvedValue(response());
});

async function expand() {
  await userEvent.click(screen.getByText("Browser result"));
}

describe("ChatBrowserResults", () => {
  it("loads on disclosure only, keeps the exact run reference, and reuses the report across snapshots", async () => {
    const view = render(<ChatBrowserResults run={run} activities={activities} />);
    expect(load).not.toHaveBeenCalled();
    expect(screen.getByText("Completed · inspection")).toBeInTheDocument();
    await expand();
    await screen.findByText(report.content_text!);
    expect(load).toHaveBeenCalledWith(run.taskID, run.runID, report.id, expect.any(AbortSignal));
    view.rerender(
      <ChatBrowserResults run={{ ...run }} activities={activities.map((item) => ({ ...item }))} />,
    );
    await expand();
    await expand();
    await screen.findByText(report.content_text!);
    expect(load).toHaveBeenCalledTimes(1);
  });

  it.each(["failed", "cancelled"])(
    "uses the %s tool outcome, not the ready artifact status",
    async (status) => {
      load.mockResolvedValue(response({ ...report, kind: "browser_flow_evidence" }));
      render(
        <ChatBrowserResults
          run={run}
          activities={[
            { ...artifactActivity, kind: "browser_flow_evidence" },
            { ...toolActivity, status },
          ]}
        />,
      );
      expect(screen.getByText("Partial result · interaction")).toBeInTheDocument();
      await expand();
      await screen.findByText(report.content_text!);
      expect(screen.getByText(/Earlier clicks may already have changed/)).toBeVisible();
      expect(screen.queryByText(/Completed ·/)).not.toBeInTheDocument();
    },
  );

  it("leaves old reports neutral when no typed tool outcome is available", async () => {
    render(
      <ChatBrowserResults run={run} activities={[{ ...artifactActivity, step_id: undefined }]} />,
    );
    expect(screen.getByText("Saved evidence · inspection")).toBeInTheDocument();
    await expand();
    await screen.findByText(report.content_text!);
    expect(screen.getByText("Saved evidence · inspection")).toBeInTheDocument();
  });

  it("renders bounded inert text without creating page links, markup, or images", async () => {
    const unsafe =
      '<img src="https://example.com/private" onerror="alert(1)"> [click](https://example.com)';
    load.mockResolvedValue(response({ ...report, content_text: unsafe + "x".repeat(50_000) }));
    const { container } = render(<ChatBrowserResults run={run} activities={activities} />);
    await expand();
    await screen.findByText("Report display truncated.");
    expect(container.querySelector("pre")?.textContent).toHaveLength(48 * 1024);
    expect(container.querySelector("pre")?.textContent).toContain(unsafe);
    expect(container.querySelector("img, a, script")).toBeNull();
  });

  it("deduplicates artifact snapshots and excludes non-browser artifacts", () => {
    render(
      <ChatBrowserResults
        run={run}
        activities={[
          ...activities,
          { ...artifactActivity, id: "another-activity" },
          { ...artifactActivity, id: "other", artifact_id: "other", kind: "final_answer" },
        ]}
      />,
    );
    expect(screen.getAllByText("Browser result")).toHaveLength(1);
  });

  it("explains missing references without making a request", async () => {
    render(
      <ChatBrowserResults
        run={run}
        activities={[{ ...artifactActivity, artifact_id: undefined }]}
      />,
    );
    await expand();
    await screen.findByText(/no retained report reference/);
    expect(load).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Reload report" })).not.toBeInTheDocument();
  });

  it("explains retention without exposing the server error and only retries on user action", async () => {
    load.mockRejectedValueOnce(new ApiError("private server details", 404));
    const view = render(<ChatBrowserResults run={run} activities={activities} />);
    await expand();
    await screen.findByText(/no longer available/);
    expect(screen.queryByText("private server details")).not.toBeInTheDocument();
    view.rerender(<ChatBrowserResults run={{ ...run }} activities={[...activities]} />);
    expect(load).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole("button", { name: "Reload report" }));
    await screen.findByText(report.content_text!);
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("supports a safe retry after a transient failure", async () => {
    load.mockRejectedValueOnce(new Error("private network error"));
    render(<ChatBrowserResults run={run} activities={activities} />);
    await expand();
    await screen.findByText(/This does not rerun the browser/);
    await userEvent.click(screen.getByRole("button", { name: "Reload report" }));
    await screen.findByText(report.content_text!);
    expect(load).toHaveBeenCalledTimes(2);
  });

  it.each([
    { id: "other" },
    { task_id: "other" },
    { run_id: "other" },
    { step_id: "other" },
    { kind: "final_answer" },
    { mime_type: "text/html" },
    { storage_kind: "file" },
  ])("rejects a mismatched report %j", async (override) => {
    load.mockResolvedValue(response({ ...report, ...override }));
    render(<ChatBrowserResults run={run} activities={activities} />);
    await expand();
    await screen.findByText(/does not match this browser result/);
    expect(screen.queryByText(report.content_text!)).not.toBeInTheDocument();
  });

  it("aborts when closed and ignores a late response after switching run identity", async () => {
    let resolveOld!: (value: TaskArtifactResponse) => void;
    load.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve;
        }),
    );
    const view = render(<ChatBrowserResults run={run} activities={activities} />);
    await expand();
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    const signal = load.mock.calls[0][3]!;
    expect(signal.aborted).toBe(false);
    await expand();
    await waitFor(() => expect(signal.aborted).toBe(true));
    view.rerender(
      <ChatBrowserResults run={{ ...run, runID: "another-run" }} activities={activities} />,
    );
    await act(async () => resolveOld(response()));
    expect(screen.queryByText(report.content_text!)).not.toBeInTheDocument();
    expect(load).toHaveBeenCalledTimes(1);
    load.mockResolvedValue(
      response({ ...report, run_id: "another-run", content_text: "New run evidence" }),
    );
    await expand();
    await screen.findByText("New run evidence");
  });

  it("clears cached evidence when the typed step reference changes", async () => {
    const view = render(<ChatBrowserResults run={run} activities={activities} />);
    await expand();
    await screen.findByText(report.content_text!);
    view.rerender(
      <ChatBrowserResults run={run} activities={[{ ...artifactActivity, step_id: "new-step" }]} />,
    );
    expect(screen.queryByText(report.content_text!)).not.toBeInTheDocument();
    expect(screen.getByText("Saved evidence · inspection")).toBeInTheDocument();
  });
});
