import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ChatActivityRecord,
  ChatContextPacketRecord,
  ChatTimingRecord,
  ChatUsageRecord,
} from "../../types/chat";
import { TranscriptMessageRow } from "./TranscriptMessageRow";

const baseProps = {
  id: "m1",
  role: "assistant" as const,
  model: "gpt-4o",
  content: "hello",
  time: "10:01",
  onCopy: () => {},
  copied: false,
};

describe("TranscriptMessageRow", () => {
  it("renders assistant content as markdown", () => {
    render(<TranscriptMessageRow {...baseProps} content="**bold** and `code`" />);
    expect(screen.getByText("bold").tagName).toBe("STRONG");
    expect(screen.getByText("code").tagName).toBe("CODE");
  });

  it("renders the badge when supplied", () => {
    render(<TranscriptMessageRow {...baseProps} badge="running" />);
    expect(screen.getByText("running")).toBeInTheDocument();
  });

  it("renders an agent run failure notice when badge=failed and an error message is present", () => {
    render(<TranscriptMessageRow {...baseProps} badge="failed" error="adapter exited 1" />);
    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.getByText("adapter exited 1")).toBeInTheDocument();
  });

  it("keeps failed run content when it differs from the error", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content="I updated the README before the tool failed."
        error="adapter exited 1"
      />,
    );

    expect(screen.getByText("I updated the README before the tool failed.")).toBeInTheDocument();
    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.getByText("adapter exited 1")).toBeInTheDocument();
  });

  it("does not duplicate failed run content when content is just the error", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content="adapter exited 1"
        error="adapter exited 1"
      />,
    );

    expect(screen.getAllByText("adapter exited 1")).toHaveLength(1);
  });

  it("does not render whitespace-only failed run content", () => {
    render(
      <TranscriptMessageRow {...baseProps} badge="failed" content="   " error="adapter exited 1" />,
    );

    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.getByText("adapter exited 1")).toBeInTheDocument();
    expect(screen.queryByText(/^\s+$/)).toBeNull();
  });

  it("hides generic failed activity rows that repeat the failure notice", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content=""
        error="launch model required: select a model for Grok Build before starting the external agent"
        activities={[
          {
            type: "failed",
            title: "Failed",
            status: "failed",
            detail:
              "launch model required: select a model for Grok Build before starting the external agent",
          },
        ]}
      />,
    );

    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.getAllByText(/launch model required/)).toHaveLength(1);
    expect(screen.queryByText("Failed")).toBeNull();
  });

  it("hides stale running placeholders after a failed run", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content=""
        error="launch model required: select a model for Grok Build before starting the external agent"
        activities={[
          {
            type: "running",
            title: "Running",
            status: "running",
            detail: "Waiting for ACP output",
          },
        ]}
      />,
    );

    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.queryByText(/working/)).toBeNull();
    expect(screen.queryByText("Running")).toBeNull();
    expect(screen.queryByText("Waiting for ACP output")).toBeNull();
  });

  it("keeps failed tool diagnostics while hiding stale running placeholders", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content=""
        error="tool failed"
        activities={[
          {
            type: "running",
            title: "Running",
            status: "running",
            detail: "Waiting for ACP output",
          },
          {
            type: "tool_call",
            title: "git_exec",
            status: "failed",
            detail: "git status failed",
          },
        ]}
      />,
    );

    expect(screen.queryByText("Running")).toBeNull();
    expect(screen.getByText(/1 failed tool/)).toBeInTheDocument();
    expect(screen.getByText("Ran git")).toBeInTheDocument();
  });

  it("hides resumed-session metadata after a cancelled run", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="cancelled"
        content=""
        activities={[
          {
            type: "resumed",
            title: "Resumed external session",
            status: "completed",
            detail: "Grok Build restored native-1",
          },
          {
            type: "cancelled",
            title: "Cancelled",
            status: "cancelled",
            detail: "stopped before the run finished",
          },
        ]}
      />,
    );

    expect(screen.getByText("agent run cancelled")).toBeInTheDocument();
    expect(screen.queryByText("Resumed external session")).toBeNull();
    expect(screen.getByText("Cancelled")).toBeInTheDocument();
  });

  it("keeps diagnostic failed activity rows with distinct details", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content=""
        error="agent run failed"
        activities={[
          {
            type: "run_result",
            title: "LLM call failed on turn 2: timeout",
            status: "failed",
            terminal: true,
            detail: "rate limit exceeded",
          },
        ]}
      />,
    );

    expect(screen.getByText("LLM call failed on turn 2: timeout")).toBeInTheDocument();
  });

  it("keeps failed tool-call rows even when their title looks generic", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        content=""
        error="tool failed"
        activities={[
          {
            type: "tool_call",
            title: "failed",
            status: "failed",
            detail: "tool failed",
          },
        ]}
      />,
    );

    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.getByText(/1 failed tool/)).toBeInTheDocument();
    expect(screen.getAllByText("failed")).toHaveLength(2);
  });

  it("strips the recovery marker from the visible failure message", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        error="Claude Code isn't signed in. Click the button below. (claude_code_auth_required)"
      />,
    );
    expect(screen.getByText(/Claude Code isn't signed in/)).toBeInTheDocument();
    expect(screen.queryByText(/claude_code_auth_required/)).toBeNull();
  });

  it("renders the setup-action button on a failed agent run", () => {
    const onClick = vi.fn();
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="failed"
        error="Claude Code isn't signed in. (claude_code_auth_required)"
        setupAction={{ label: "Open Claude Code setup", onClick }}
      />,
    );
    const button = screen.getByRole("button", { name: "Open Claude Code setup" });
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("does not render the setup-action button when the run is cancelled (only on failure)", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="cancelled"
        error="user pressed stop"
        setupAction={{ label: "Open Claude Code setup", onClick: vi.fn() }}
      />,
    );
    expect(screen.queryByRole("button", { name: "Open Claude Code setup" })).toBeNull();
  });

  it("keeps cancelled run content and appends a cancellation notice", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="cancelled"
        content="partial answer before stop"
        error="operator stopped the run"
      />,
    );
    expect(screen.getByText("agent run cancelled")).toBeInTheDocument();
    expect(screen.getByText("partial answer before stop")).toBeInTheDocument();
    expect(screen.getByText("operator stopped the run")).toBeInTheDocument();
  });

  it("shows the waiting-for-output indicator when assistant has no content but a running activity", () => {
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "running" },
    ];
    render(<TranscriptMessageRow {...baseProps} content="" activities={activities} />);
    expect(screen.getByText(/Waiting for agent output/)).toBeInTheDocument();
  });

  it("shows the waiting-for-output indicator for in-progress plan-only activity", () => {
    const activities: ChatActivityRecord[] = [
      { type: "plan", title: "Check the diff", status: "in_progress" },
    ];
    render(<TranscriptMessageRow {...baseProps} content="" activities={activities} />);
    expect(screen.getByText(/Waiting for agent output/)).toBeInTheDocument();
  });

  it("renders the user role label for role=user", () => {
    render(<TranscriptMessageRow {...baseProps} role="user" content="hi there" />);
    expect(screen.getByText("You")).toBeInTheDocument();
    expect(screen.getByText("hi there")).toBeInTheDocument();
  });

  it("shows token + cost meta when promptTokens > 0", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        promptTokens={1234}
        completionTokens={56}
        costUsd="0.00123"
      />,
    );
    expect(screen.getByText(/1234↑ 56↓/)).toBeInTheDocument();
    expect(screen.getByText(/\$0\.00123/)).toBeInTheDocument();
  });

  it("invokes onCopy with id+content when the copy button is clicked", async () => {
    const onCopy = vi.fn();
    const user = userEvent.setup();
    render(<TranscriptMessageRow {...baseProps} onCopy={onCopy} />);
    await user.click(screen.getByRole("button"));
    expect(onCopy).toHaveBeenCalledWith("m1", "hello");
  });

  it("renders task and trace header links as compact debug actions", async () => {
    const onOpenTask = vi.fn();
    const onOpenTrace = vi.fn();
    const user = userEvent.setup();
    render(
      <TranscriptMessageRow
        {...baseProps}
        runtimeMeta="Run run_123 · 2.0s"
        runtimeMetaTitle="Run run_123 · Native session native_123"
        taskLink={{ label: "Task task_123", onClick: onOpenTask }}
        traceLink={{ label: "Trace req_1234", onClick: onOpenTrace }}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Open Task task_123" }));
    await user.click(screen.getByRole("button", { name: "Open Trace req_1234" }));

    expect(onOpenTask).toHaveBeenCalledTimes(1);
    expect(onOpenTrace).toHaveBeenCalledTimes(1);
    const meta = screen.getByText("Run run_123 · 2.0s");
    expect(meta).toBeInTheDocument();
    expect(meta).toHaveAttribute("title", "Run run_123 · Native session native_123");
  });

  it("renders the changed-files chip as a compact action when it is wired", async () => {
    const onOpenWorkspaceChanges = vi.fn();
    const user = userEvent.setup();
    render(
      <TranscriptMessageRow
        {...baseProps}
        changedFilesLink={{
          label: "1 file",
          title: "Workspace changes · 1 file changed",
          onClick: onOpenWorkspaceChanges,
        }}
      />,
    );

    const chip = screen.getByRole("button", { name: "Open 1 file" });
    expect(chip).toHaveAttribute("title", "Workspace changes · 1 file changed");
    await user.click(chip);

    expect(onOpenWorkspaceChanges).toHaveBeenCalledTimes(1);
  });

  it("renders the agent usage line when reported usage is present", () => {
    const usage: ChatUsageRecord = {
      reported_cost_amount: "0.42",
      reported_cost_currency: "USD",
      context_used: 12000,
      context_size: 200000,
    };
    render(<TranscriptMessageRow {...baseProps} agentUsage={usage} />);
    expect(screen.getByText(/0\.42 USD/)).toBeInTheDocument();
    expect(screen.getByText(/12000\/200000 context/)).toBeInTheDocument();
    expect(screen.getByText(/reported usage/)).toBeInTheDocument();
  });

  it("hides the agent usage line when all usage fields are empty/zero", () => {
    const usage: ChatUsageRecord = {
      reported_cost_amount: "",
      reported_cost_currency: "",
      context_used: 0,
      context_size: 0,
    };
    render(<TranscriptMessageRow {...baseProps} agentUsage={usage} />);
    expect(screen.queryByText(/reported usage/)).toBeNull();
  });

  it("renders the Hecate Chat timing summary when timing is present", () => {
    const timing: ChatTimingRecord = {
      total_ms: 12_400,
      queue_ms: 120,
      model_ms: 8_500,
      tool_ms: 700,
      approval_wait_ms: 2_000,
      overhead_ms: 1_080,
      turn_count: 2,
      tool_count: 1,
      bottleneck: "model",
      bottleneck_ms: 8_500,
    };
    render(<TranscriptMessageRow {...baseProps} agentTiming={timing} />);
    expect(screen.getByLabelText("Hecate Chat timing summary")).toBeInTheDocument();
    expect(screen.getByText(/bottleneck · model 8\.5s/)).toBeInTheDocument();
    expect(screen.getByText(/total 12s/)).toBeInTheDocument();
    expect(screen.getByText(/2 turns · 1 tool/)).toBeInTheDocument();
  });

  it("renders a collapsed context inspector for assistant context packets", async () => {
    const user = userEvent.setup();
    const contextPacket: ChatContextPacketRecord = {
      execution_mode: "hecate_task",
      provider: "ollama",
      model: "llama3.1:8b",
      workspace: "/tmp/hecate",
      message_count: 3,
      sources: [
        {
          kind: "system_prompt",
          label: "System prompt",
          detail: "Configured for this turn",
          trust: "system",
        },
      ],
    };

    render(<TranscriptMessageRow {...baseProps} contextPacket={contextPacket} />);
    const summary = screen.getByText(/context · 3 messages · ollama · llama3\.1:8b/);
    expect(summary).toBeInTheDocument();

    await user.click(summary);

    expect(screen.getByText("Hecate task runtime")).toBeInTheDocument();
    expect(screen.getByText("/tmp/hecate")).toBeInTheDocument();
    expect(screen.getByText("System prompt")).toBeInTheDocument();
    expect(screen.getByText("Configured for this turn")).toBeInTheDocument();
  });

  it("links failed tools to related stdout and stderr artifacts", async () => {
    const onOpenTask = vi.fn();
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "git_exec (failed)",
        status: "failed",
        kind: "git",
        detail: "git_exec - failed",
      },
      {
        type: "artifact",
        title: "git-stdout.txt",
        status: "ready",
        artifact_id: "artifact_stdout",
        artifact_size_bytes: 42,
        artifact_preview: "  diff --git a/README.md b/README.md\n+hello\n",
      },
      {
        type: "artifact",
        title: "git-stderr.txt",
        status: "ready",
        artifact_id: "artifact_stderr",
        artifact_size_bytes: 19,
        artifact_preview: "fatal: not a git repository",
      },
      { type: "failed", title: "Run failed", status: "failed", terminal: true },
    ];

    const { container } = render(
      <TranscriptMessageRow
        {...baseProps}
        activities={activities}
        taskLink={{ label: "Task task_123", onClick: onOpenTask }}
      />,
    );

    await user.click(screen.getByText(/1 failed tool/));
    await user.click(screen.getByText("Advanced"));
    expect(screen.getByText(/Preview the related run output/)).toBeInTheDocument();
    expect(
      [...container.querySelectorAll("pre")].some((node) =>
        node.textContent?.startsWith("  diff --git"),
      ),
    ).toBe(true);
    expect(screen.getByText(/\+hello/)).toBeInTheDocument();
    expect(screen.getByText("fatal: not a git repository")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open task output" }));
    expect(onOpenTask).toHaveBeenCalledTimes(1);
  });

  it("does not link empty stderr artifacts from failed tools", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "git_exec (failed)",
        status: "failed",
        kind: "git",
        detail: "git_exec - failed",
      },
      {
        type: "artifact",
        title: "git-stdout.txt",
        status: "ready",
        artifact_id: "artifact_stdout",
        artifact_size_bytes: 42,
        artifact_preview: "stdout details",
      },
      {
        type: "artifact",
        title: "git-stderr.txt",
        status: "ready",
        artifact_id: "artifact_stderr",
        artifact_size_bytes: 0,
      },
    ];

    render(
      <TranscriptMessageRow
        {...baseProps}
        activities={activities}
        taskLink={{ label: "Task task_123", onClick: vi.fn() }}
      />,
    );

    await user.click(screen.getByText(/1 failed tool/));
    await user.click(screen.getByText("Advanced"));
    expect(screen.getByText("stdout details")).toBeInTheDocument();
    expect(screen.queryByText("Preview unavailable in this snapshot.")).toBeNull();
  });

  it("lets read-context activity reveal captured output without cluttering the row", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_read",
        status: "completed",
        kind: "read",
        detail: "read · output: 1>import foo from 'foo' 2>const enabled = true",
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    expect(screen.queryByText("read · output captured")).toBeNull();
    expect(screen.queryByText(/import foo/)).toBeNull();

    await user.click(screen.getByText("Output"));

    expect(screen.getByText("Read output")).toBeInTheDocument();
    expect(screen.getByText(/import foo/)).toBeInTheDocument();
    expect(screen.getByText(/const enabled = true/)).toBeInTheDocument();
    expect(screen.queryByText(/1 │/)).toBeNull();
  });

  it("shows full captured command output from artifact preview only in the output card", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_shell",
        status: "completed",
        kind: "execute",
        detail: "execute · output: total 88064 drwxr-xr-x@ 45 chicoxyzzy staff 144...",
        artifact_preview:
          "total 88064\n" +
          "drwxr-xr-x@ 45 chicoxyzzy staff 1440 May 27 17:43 .\n" +
          "drwxr-xr-x@ 20 chicoxyzzy staff 640 May 27 17:40 ..",
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    expect(screen.queryByText("execute · output captured")).toBeNull();
    expect(screen.queryByText(/total 88064/)).toBeNull();

    await user.click(screen.getByText("Output"));

    expect(screen.getByText("Tool output")).toBeInTheDocument();
    expect(screen.getByText(/drwxr-xr-x@ 45 chicoxyzzy staff 1440/)).toBeInTheDocument();
    expect(screen.getByText(/drwxr-xr-x@ 20 chicoxyzzy staff 640/)).toBeInTheDocument();
  });

  it("shows full captured command output from detail-only output-captured rows", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_shell",
        status: "completed",
        kind: "execute",
        detail:
          "execute · output captured · total 88064\n" +
          "drwxr-xr-x@ 45 chicoxyzzy staff 1440 May 27 17:43 .\n" +
          "drwxr-xr-x@ 20 chicoxyzzy staff 640 May 27 17:40 ..",
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    expect(screen.queryByText(/total 88064/)).toBeNull();
    expect(screen.queryByText(/drwxr-xr-x@ 45/)).toBeNull();

    await user.click(screen.getByText("Output"));

    expect(screen.getByText("Tool output")).toBeInTheDocument();
    expect(screen.getByText(/total 88064/)).toBeInTheDocument();
    expect(screen.getByText(/drwxr-xr-x@ 45 chicoxyzzy staff 1440/)).toBeInTheDocument();
    expect(screen.getByText(/drwxr-xr-x@ 20 chicoxyzzy staff 640/)).toBeInTheDocument();
  });

  it("strips ANSI color codes from captured command output", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_shell",
        status: "completed",
        kind: "execute",
        detail: "execute · output: done",
        artifact_preview: `${String.fromCharCode(27)}[31mfailed${String.fromCharCode(27)}[0m\nnext line`,
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    await user.click(screen.getByText("Output"));

    const output = screen.getByText(/failed/);
    expect(output.textContent).toContain("failed\nnext line");
    expect(output.textContent).not.toContain(String.fromCharCode(27));
  });

  it("renders arrow-separated read-context output with real line breaks", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_read",
        status: "completed",
        kind: "read",
        detail:
          'read · output: 1→import { parsePatchFiles } from "@pierre/diffs"; 2→import { FileDiff } from "@pierre/diffs/react";',
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    await user.click(screen.getByText("Output"));

    const output = screen.getByText(/import \{ parsePatchFiles \}/);
    expect(output.textContent).toContain(
      'import { parsePatchFiles } from "@pierre/diffs";\nimport { FileDiff } from "@pierre/diffs/react";',
    );
    expect(output).not.toHaveTextContent(/1(?:>|→|\|)/);
  });

  it("renders pipe-separated read-context output with real line breaks", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_read",
        status: "completed",
        kind: "read",
        detail:
          'read · output: 1 | <h1 align="center"> 2 | <img src="docs/assets/brand/hecate-lockup-horizontal-dark-2x.png">',
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    await user.click(screen.getByText("Output"));

    const output = screen.getByText(/<h1 align="center">/);
    expect(output.textContent).toContain(
      '<h1 align="center">\n<img src="docs/assets/brand/hecate-lockup-horizontal-dark-2x.png">',
    );
    expect(output).not.toHaveTextContent(/\d+\s*\|/);
  });

  it("strips right-aligned line gutters from read-context output", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_read",
        status: "completed",
        kind: "read",
        detail: "read · output:   41 | function run() {\n  42 |   return true;\n  43 | }",
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    await user.click(screen.getByText("Output"));

    const output = screen.getByText(/function run/);
    expect(output.textContent).toContain("function run() {\nreturn true;\n}");
    expect(output).not.toHaveTextContent(/\d+\s*\|/);
  });

  it("does not duplicate captured diffs when the workspace-changes chip is available", () => {
    const diff = [
      "diff --git a/README.md b/README.md",
      "index 1111111..2222222 100644",
      "--- a/README.md",
      "+++ b/README.md",
      "@@ -1 +1,2 @@",
      " # Hecate",
      "+Local runtime console",
    ].join("\n");

    render(
      <TranscriptMessageRow
        {...baseProps}
        diff={diff}
        diffStat="README.md | 1 +\n1 file changed, 1 insertion(+)"
        changedFilesLink={{ label: "1 file", onClick: vi.fn() }}
      />,
    );

    expect(screen.getByRole("button", { name: "Open 1 file" })).toBeInTheDocument();
    expect(screen.queryByText(/workspace changes · 1 file changed/)).toBeNull();
    expect(screen.queryByTestId("diff-viewer")).toBeNull();
  });

  it("hides backend changed-files activity rows when the workspace-changes chip is available", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "files_changed",
        title: "Workspace changes",
        status: "completed",
        detail: "README.md | 1 +\n1 file changed, 1 insertion(+)",
      },
    ];

    render(
      <TranscriptMessageRow
        {...baseProps}
        activities={activities}
        diffStat="README.md | 1 +\n1 file changed, 1 insertion(+)"
        changedFilesLink={{ label: "1 file", onClick: vi.fn() }}
      />,
    );

    expect(screen.getByRole("button", { name: "Open 1 file" })).toBeInTheDocument();
    expect(screen.queryByText("Workspace changes")).toBeNull();
    expect(screen.queryByText("Files")).toBeNull();
  });

  it("shows the rich diff viewer for file-change activity when a patch is captured", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "files_changed",
        title: "Workspace changes",
        status: "completed",
        detail: "1 file changed, 1 insertion(+)",
      },
      { type: "completed", title: "Run completed", status: "completed" },
    ];
    const diff = [
      "diff --git a/README.md b/README.md",
      "index 1111111..2222222 100644",
      "--- a/README.md",
      "+++ b/README.md",
      "@@ -1 +1,2 @@",
      " # Hecate",
      "+Local runtime console",
    ].join("\n");

    render(
      <TranscriptMessageRow
        {...baseProps}
        activities={activities}
        diff={diff}
        diffStat="README.md | 1 +\n1 file changed, 1 insertion(+)"
      />,
    );

    const changesSummary = screen.getByText(/^workspace changes/);
    expect(changesSummary.tagName).toBe("SUMMARY");
    await user.click(changesSummary);

    expect(screen.getByTestId("diff-viewer")).toBeInTheDocument();
  });

  it("lets output detail rows reveal captured previews", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "output",
        title: "stdout",
        status: "ready",
        artifact_size_bytes: 18,
        artifact_preview: "command output\n",
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    await user.click(screen.getByText("Output · 1 item"));
    await user.click(screen.getAllByText("Output")[1]);

    expect(screen.getByText("command output")).toBeInTheDocument();
  });

  it("hides output detail rows without captured previews", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "output",
        title: "ACP output",
        status: "ready",
        detail: "ACP output",
      },
    ];

    render(<TranscriptMessageRow {...baseProps} activities={activities} />);

    expect(screen.queryByText("Output · 1 item")).toBeNull();
    expect(screen.queryByText("No output preview was captured for this snapshot.")).toBeNull();
  });

  it("renders the raw agent output details when rawOutput differs from content", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        content="final answer"
        rawOutput="I'll do this. final answer"
      />,
    );
    expect(screen.getByText(/raw agent output/)).toBeInTheDocument();
  });

  it("does not render the raw agent output details when rawOutput equals content", () => {
    render(<TranscriptMessageRow {...baseProps} content="final answer" rawOutput="final answer" />);
    expect(screen.queryByText(/raw agent output/)).toBeNull();
  });

  it("does not render routine cancellation raw output", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="cancelled"
        content=""
        rawOutput="context canceled"
      />,
    );
    expect(screen.getByText("agent run cancelled")).toBeInTheDocument();
    expect(screen.queryByText(/raw agent output/)).toBeNull();
    expect(screen.queryByText("context canceled")).toBeNull();
  });

  it("keeps non-routine raw output on cancelled runs", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        badge="cancelled"
        content=""
        rawOutput="adapter refused cancellation: pending approval"
      />,
    );
    expect(screen.getByText(/raw agent output/)).toBeInTheDocument();
  });
});
