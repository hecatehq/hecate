import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TaskDetail } from "./TaskDetail";
import type { TaskRecord, TaskRunEventRecord, TaskRunRecord, TaskStepRecord } from "../../types/runtime";

function makeTask(overrides: Partial<TaskRecord> = {}): TaskRecord {
  return {
    id: "task-1",
    title: "List the working directory",
    prompt: "ls -la",
    status: "completed",
    execution_kind: "shell",
    shell_command: "ls -la",
    step_count: 2,
    latest_run_id: "run-1",
    ...overrides,
  } as TaskRecord;
}

function makeRun(overrides: Partial<TaskRunRecord> = {}): TaskRunRecord {
  return {
    id: "run-1",
    task_id: "task-1",
    number: 1,
    status: "completed",
    model: "gpt-4o-mini",
    started_at: "2026-04-27T17:00:00Z",
    finished_at: "2026-04-27T17:00:02Z",
    ...overrides,
  } as TaskRunRecord;
}

function makeStep(overrides: Partial<TaskStepRecord> = {}): TaskStepRecord {
  return {
    id: "step-1",
    task_id: "task-1",
    run_id: "run-1",
    index: 0,
    kind: "shell",
    title: "ls -la",
    status: "completed",
    started_at: "2026-04-27T17:00:00Z",
    finished_at: "2026-04-27T17:00:01Z",
    exit_code: 0,
    ...overrides,
  } as TaskStepRecord;
}

function makeEvent(overrides: Partial<TaskRunEventRecord> = {}): TaskRunEventRecord {
  return {
    schema_version: "1",
    event_id: "evt_01HX0000000000000000000001",
    task_id: "task-1",
    run_id: "run-1",
    sequence: 1,
    occurred_at: "2026-04-27T17:00:00Z",
    type: "run.started",
    data: {},
    ...overrides,
  };
}

function setup(propOverrides: Partial<React.ComponentProps<typeof TaskDetail>> = {}) {
  const task = makeTask();
  const run = makeRun();
  const props: React.ComponentProps<typeof TaskDetail> = {
    task,
    run,
    runs: [run],
    selectedRunID: run.id,
    events: [],
    steps: [],
    artifacts: [],
    approvals: [],
    streamTurnCosts: new Map(),
    streamState: "closed",
    busyAction: "",
    notice: null,
    onSelectRun: vi.fn(),
    onResolveApproval: vi.fn(),
    onCancelRun: vi.fn(),
    onRetryRun: vi.fn(),
    onResumeRun: vi.fn(),
    onRetryFromTurn: vi.fn(),
    onResumeRaisingCeiling: vi.fn(),
    ...propOverrides,
  };
  const user = userEvent.setup();
  return { props, user, render: () => render(<TaskDetail {...props} />) };
}

describe("TaskDetail run picker", () => {
  it("shows the current run number", () => {
    const { render } = setup();
    render();
    expect(screen.getByRole("button", { name: /select run/i })).toHaveTextContent("run #1");
  });

  it("renders 'of N' suffix only when there are multiple runs", () => {
    const run1 = makeRun({ id: "run-1", number: 1 });
    const run2 = makeRun({ id: "run-2", number: 2, status: "failed" });
    const { render } = setup({ runs: [run2, run1], run: run2, selectedRunID: run2.id });
    render();
    expect(screen.getByRole("button", { name: /select run/i })).toHaveTextContent("of 2");
  });

  it("opens the listbox and shows all runs when clicked", async () => {
    const run1 = makeRun({ id: "run-1", number: 1, status: "failed" });
    const run2 = makeRun({ id: "run-2", number: 2, status: "completed" });
    const { render, user } = setup({ runs: [run2, run1], run: run2, selectedRunID: run2.id });
    render();
    await user.click(screen.getByRole("button", { name: /select run/i }));
    const listbox = await screen.findByRole("listbox");
    expect(listbox).toBeTruthy();
    expect(screen.getAllByRole("option")).toHaveLength(2);
  });

  it("calls onSelectRun with the chosen run id", async () => {
    const onSelectRun = vi.fn();
    const run1 = makeRun({ id: "run-1", number: 1, status: "failed" });
    const run2 = makeRun({ id: "run-2", number: 2, status: "completed" });
    const { render, user } = setup({ runs: [run2, run1], run: run2, selectedRunID: run2.id, onSelectRun });
    render();
    await user.click(screen.getByRole("button", { name: /select run/i }));
    const options = await screen.findAllByRole("option");
    await user.click(options[1]); // run-1
    expect(onSelectRun).toHaveBeenCalledWith("run-1");
  });

  it("hides the picker when there are zero runs", () => {
    const { render } = setup({ runs: [], run: null });
    render();
    expect(screen.queryByRole("button", { name: /select run/i })).toBeNull();
  });
});

describe("TaskDetail step drill-down", () => {
  it("renders a step row with the title", () => {
    const step = makeStep({ title: "echo hello" });
    const { render } = setup({ steps: [step] });
    render();
    expect(screen.getByText("echo hello")).toBeTruthy();
  });

  it("clicking a step with detail toggles the expanded panel", async () => {
    const step = makeStep({
      title: "echo hello",
      tool_name: "shell",
      input: { command: "echo hello" },
      output_summary: { exit_code: 0, stdout_size: 6 },
    });
    const { render, user } = setup({ steps: [step] });
    render();

    expect(screen.queryByText(/^INPUT$/i)).toBeNull();

    await user.click(screen.getByRole("button", { name: /step echo hello/i }));
    expect(await screen.findByText(/^INPUT$/i)).toBeTruthy();
    expect(screen.getByText(/^OUTPUT$/i)).toBeTruthy();
    expect(screen.getByText(/"command"/)).toBeTruthy();
  });

  it("shows the error block when a step failed", async () => {
    const step = makeStep({
      title: "rm",
      status: "failed",
      exit_code: 2,
      error: "permission denied",
      input: { command: "rm /etc/passwd" },
    });
    const { render, user } = setup({ steps: [step] });
    render();
    await user.click(screen.getByRole("button", { name: /step rm/i }));
    expect(await screen.findByText(/^Error$/i)).toBeTruthy();
    // Error appears both as inline truncated tooltip and in the expanded
    // panel — use getAllByText and assert at least one occurrence renders.
    expect(screen.getAllByText(/permission denied/i).length).toBeGreaterThan(0);
  });

  it("does not make the step clickable when there is no detail to show", () => {
    const step = makeStep({
      tool_name: undefined,
      phase: undefined,
      input: undefined,
      output_summary: undefined,
      error: undefined,
    });
    const { render } = setup({ steps: [step] });
    render();
    const button = screen.getByRole("button", { name: /step/i });
    // The chevron is only rendered when hasDetail; assert no chevron path
    // shows up by checking the button does not contain an aria-expanded toggle effect.
    expect(button.getAttribute("aria-expanded")).toBe("false");
  });
});

describe("TaskDetail runtime debugging", () => {
  it("renders the run overview with request and trace ids", () => {
    const run = makeRun({
      status: "failed",
      provider: "ollama",
      provider_kind: "local",
      request_id: "req-123",
      trace_id: "trace-456",
      last_error: "context deadline exceeded",
      otel_status_message: "provider_unavailable",
    });
    const { render } = setup({ run, runs: [run] });
    render();
    expect(screen.getByText(/Run overview/i)).toBeTruthy();
    expect(screen.getByText("req-123")).toBeTruthy();
    expect(screen.getByText("trace-456")).toBeTruthy();
    expect(screen.getByText("context deadline exceeded")).toBeTruthy();
  });

  it("renders the run timeline from persisted events", () => {
    const events: TaskRunEventRecord[] = [
      makeEvent({ event_id: "evt_01HX0000000000000000000001", sequence: 1, type: "run.created", occurred_at: "2026-04-27T17:00:00Z" }),
      makeEvent({ event_id: "evt_01HX0000000000000000000002", sequence: 2, type: "run.queued", occurred_at: "2026-04-27T17:00:01Z" }),
      makeEvent({ event_id: "evt_01HX0000000000000000000003", sequence: 3, type: "run.finished", occurred_at: "2026-04-27T17:00:02Z" }),
    ];
    const { render } = setup({ events });
    render();
    expect(screen.getByText(/Run timeline/i)).toBeTruthy();
    expect(screen.getByText(/Run created/i)).toBeTruthy();
    expect(screen.getByText(/Queued/i)).toBeTruthy();
    expect(screen.getByText(/Completed/i)).toBeTruthy();
  });

  it("annotates run.resumed_from_event events with turn and reason when both are present", () => {
    const events: TaskRunEventRecord[] = [
      makeEvent({
        type: "run.resumed_from_event",
        data: { from_run_id: "run-0", reason: "wrong tool choice", retry_from_turn: 3 },
      }),
    ];
    const { render } = setup({ events });
    render();
    // Both turn and reason should appear joined by " — ".
    expect(screen.getByText("turn 3 — wrong tool choice")).toBeTruthy();
  });

  it("annotates run.resumed_from_event events with turn only when reason is absent", () => {
    const events: TaskRunEventRecord[] = [
      makeEvent({
        type: "run.resumed_from_event",
        data: { from_run_id: "run-0", retry_from_turn: 2 },
      }),
    ];
    const { render } = setup({ events });
    render();
    expect(screen.getByText("turn 2")).toBeTruthy();
  });

  it("annotates run.resumed_from_event events with reason only when retry_from_turn is absent", () => {
    const events: TaskRunEventRecord[] = [
      makeEvent({
        type: "run.resumed_from_event",
        data: { from_run_id: "run-0", reason: "continue after cancellation" },
      }),
    ];
    const { render } = setup({ events });
    render();
    expect(screen.getByText("continue after cancellation")).toBeTruthy();
  });

  it("shows no annotation on run.resumed_from_event events with no reason and no turn", () => {
    const events: TaskRunEventRecord[] = [
      makeEvent({
        type: "run.resumed_from_event",
        data: { from_run_id: "run-0" },
      }),
    ];
    const { render } = setup({ events });
    render();
    // Only "Resumed" label — no extra annotation line.
    expect(screen.getByText(/Resumed/i)).toBeTruthy();
    expect(screen.queryByText(/turn/i)).toBeNull();
  });

  it("renders approval metadata for pending approvals", () => {
    const { render } = setup({
      approvals: [{
        id: "approval-1",
        task_id: "task-1",
        run_id: "run-1",
        kind: "shell_command",
        status: "pending",
        reason: "Needs explicit shell approval",
        requested_by: "agent_loop",
        created_at: "2026-04-27T17:00:00Z",
      } as any],
      run: makeRun({ status: "awaiting_approval" }),
    });
    render();
    expect(screen.getByText(/Approval required/i)).toBeTruthy();
    expect(screen.getByText(/Shell execution/i)).toBeTruthy();
    expect(screen.getByText(/requested by/i)).toBeTruthy();
  });
});

describe("TaskDetail agent conversation viewer", () => {
  const conversation = JSON.stringify([
    { role: "user", content: "Summarize the README." },
    {
      role: "assistant",
      content: "Let me read it.",
      tool_calls: [{
        id: "call-1",
        type: "function",
        function: { name: "read_file", arguments: '{"path":"README.md"}' },
      }],
    },
    {
      role: "tool",
      content: "path=README.md size=42 bytes=42\n--- content ---\nHecate is the gateway.",
      tool_call_id: "call-1",
    },
    { role: "assistant", content: "It introduces Hecate as the gateway." },
  ]);

  function makeConvoArtifact(content = conversation) {
    return {
      id: "convo-run-1",
      task_id: "task-1",
      run_id: "run-1",
      kind: "agent_conversation",
      name: "agent-conversation.json",
      content_text: content,
      mime_type: "application/json",
    } as any;
  }

  it("renders the conversation when an agent_conversation artifact is present", () => {
    const { render } = setup({ artifacts: [makeConvoArtifact()] });
    render();
    expect(screen.getByText(/Agent conversation · 4 messages/)).toBeTruthy();
    expect(screen.getByText("Summarize the README.")).toBeTruthy();
    expect(screen.getByText("Let me read it.")).toBeTruthy();
    expect(screen.getByText(/Hecate is the gateway/)).toBeTruthy();
    expect(screen.getByText("It introduces Hecate as the gateway.")).toBeTruthy();
  });

  it("renders tool calls as chips with the function name", () => {
    const { render } = setup({ artifacts: [makeConvoArtifact()] });
    render();
    // Tool-call chip uses an arrow + function name to read fluent —
    // "→ read_file" — and includes the args inline.
    expect(screen.getByText(/→ read_file/)).toBeTruthy();
    expect(screen.getByText(/"path":"README\.md"/)).toBeTruthy();
  });

  it("does NOT render the agent_conversation as a bottom-strip badge (it's inline)", () => {
    const { render } = setup({
      artifacts: [
        makeConvoArtifact(),
        {
          id: "art-2",
          task_id: "task-1",
          run_id: "run-1",
          kind: "summary",
          name: "agent-final-answer.txt",
          content_text: "answer",
        } as any,
      ],
    });
    render();
    // The summary artifact still shows as a chip in the bottom strip.
    expect(screen.getByText("agent-final-answer.txt")).toBeTruthy();
    // The conversation artifact's filename ("agent-conversation.json")
    // must NOT appear in the bottom strip — it's already inline above.
    expect(screen.queryByText("agent-conversation.json")).toBeNull();
  });

  it("falls back to an inline error on corrupt JSON instead of crashing", () => {
    const { render } = setup({ artifacts: [makeConvoArtifact("not valid json {")] });
    render();
    expect(screen.getByText(/Could not parse agent conversation/i)).toBeTruthy();
  });

  it("renders nothing when no agent_conversation artifact exists", () => {
    const { render } = setup({ artifacts: [] });
    render();
    expect(screen.queryByText(/Agent conversation/)).toBeNull();
  });

  it("shows a 'retry from here' button on each assistant turn for terminal runs", async () => {
    const { render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "completed" }),
    });
    render();
    // Two assistant turns in the fixture — both should show the retry control.
    const retryButtons = screen.getAllByRole("button", { name: /retry from here/i });
    expect(retryButtons.length).toBe(2);
  });

  it("opens the retry modal when the retry button is clicked", async () => {
    const { user, render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "completed" }),
    });
    render();
    const retryButtons = screen.getAllByRole("button", { name: /retry from here/i });
    await user.click(retryButtons[0]);
    // Modal title includes the turn number.
    expect(screen.getByText(/Retry from turn 1/i)).toBeTruthy();
    // The reason textarea and Retry confirm button are present.
    expect(screen.getByPlaceholderText(/why are you branching/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /^retry$/i })).toBeTruthy();
  });

  it("fires onRetryFromTurn with turn and empty reason when confirmed without typing", async () => {
    const { props, user, render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "completed" }),
    });
    render();
    const retryButtons = screen.getAllByRole("button", { name: /retry from here/i });
    await user.click(retryButtons[0]);
    await user.click(screen.getByRole("button", { name: /^retry$/i }));
    expect(props.onRetryFromTurn).toHaveBeenCalledWith(1, "");
  });

  it("fires onRetryFromTurn with turn and trimmed reason when confirmed with a reason", async () => {
    const { props, user, render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "completed" }),
    });
    render();
    const retryButtons = screen.getAllByRole("button", { name: /retry from here/i });
    await user.click(retryButtons[1]);
    // Turn 2 — type a reason.
    await user.type(screen.getByPlaceholderText(/why are you branching/i), "  wrong tool choice  ");
    await user.click(screen.getByRole("button", { name: /^retry$/i }));
    expect(props.onRetryFromTurn).toHaveBeenCalledWith(2, "wrong tool choice");
  });

  it("closes the modal without firing when Cancel is clicked", async () => {
    const { props, user, render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "completed" }),
    });
    render();
    await user.click(screen.getAllByRole("button", { name: /retry from here/i })[0]);
    expect(screen.getByText(/Retry from turn 1/i)).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(screen.queryByText(/Retry from turn 1/i)).toBeNull();
    expect(props.onRetryFromTurn).not.toHaveBeenCalled();
  });

  it("hides the 'retry from here' button while the run is still active", () => {
    const { render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "running" }),
    });
    render();
    expect(screen.queryAllByRole("button", { name: /retry from here/i })).toHaveLength(0);
  });

  it("shows per-turn LLM cost next to the turn label when the model step has cost", () => {
    // Two assistant turns in the conversation fixture map to two
    // model-kind steps. We seed each step's OutputSummary with a
    // cost; the bubble must surface that cost as $X.XXX next to
    // "turn N". 1500 µUSD = $0.002 (rounded), 250000 = $0.250.
    const modelSteps: TaskStepRecord[] = [
      makeStep({ id: "s-m1", index: 1, kind: "model", title: "Agent turn 1", output_summary: { cost_micros_usd: 1500 } }),
      makeStep({ id: "s-m2", index: 2, kind: "model", title: "Agent turn 2", output_summary: { cost_micros_usd: 250000 } }),
    ];
    const { render } = setup({
      artifacts: [makeConvoArtifact()],
      steps: modelSteps,
    });
    render();
    expect(screen.getByText(/\$0\.002/)).toBeTruthy();
    expect(screen.getByText(/\$0\.250/)).toBeTruthy();
  });

  it("hides the per-turn cost when the model step lacks a cost field", () => {
    // Older runs (or resumed-after-approval steps that didn't
    // re-call the LLM) won't carry cost in OutputSummary. The
    // bubble must NOT render a misleading "$0.000" — the cost
    // chip should be absent entirely.
    const modelSteps: TaskStepRecord[] = [
      makeStep({ id: "s-m1", index: 1, kind: "model", title: "Agent turn 1", output_summary: {} }),
      makeStep({ id: "s-m2", index: 2, kind: "model", title: "Agent turn 2", output_summary: {} }),
    ];
    const { render } = setup({
      artifacts: [makeConvoArtifact()],
      steps: modelSteps,
    });
    render();
    // No "$" amount rendered inside any conversation bubble.
    // The header run-cost badge isn't present (run has no cost),
    // so any "$" anywhere would be a regression.
    expect(screen.queryByText(/\$/)).toBeNull();
  });

  it("falls back to streamTurnCosts when the model step has no cost", () => {
    // The agent.turn.completed SSE event is the canonical per-turn
    // cost ledger. When the model step's OutputSummary doesn't
    // carry the cost — historical runs, or steps that finalized
    // before the cost was attached — the conversation viewer
    // should still surface the figure from the stream.
    const modelSteps: TaskStepRecord[] = [
      makeStep({ id: "s-m1", index: 1, kind: "model", title: "Agent turn 1", output_summary: {} }),
      makeStep({ id: "s-m2", index: 2, kind: "model", title: "Agent turn 2", output_summary: {} }),
    ];
    const streamTurnCosts = new Map<number, number>([
      [1, 1500],     // $0.002
      [2, 250000],   // $0.250
    ]);
    const { render } = setup({
      artifacts: [makeConvoArtifact()],
      steps: modelSteps,
      streamTurnCosts,
    });
    render();
    expect(screen.getByText(/\$0\.002/)).toBeTruthy();
    expect(screen.getByText(/\$0\.250/)).toBeTruthy();
  });

  it("disables 'retry from here' while another action is in flight", () => {
    const { render } = setup({
      artifacts: [makeConvoArtifact()],
      run: makeRun({ status: "completed" }),
      busyAction: "cancel",
    });
    render();
    const retryButtons = screen.getAllByRole("button", { name: /retry from here/i });
    expect(retryButtons.length).toBe(2);
    retryButtons.forEach(b => expect((b as HTMLButtonElement).disabled).toBe(true));
  });
});

describe("TaskDetail cost ceiling banner", () => {
  function ceilingFailedRun(overrides: Partial<TaskRunRecord> = {}): TaskRunRecord {
    // The agent loop sets otel_status_message = "cost_ceiling_exceeded"
    // on this specific failure mode; LastError carries the human
    // breakdown. The banner gates on the otel field so a generic
    // "failed" run doesn't flash the affordance.
    return makeRun({
      status: "failed",
      otel_status_code: "error",
      otel_status_message: "cost_ceiling_exceeded",
      total_cost_micros_usd: 600_000,
      prior_cost_micros_usd: 0,
      ...overrides,
    });
  }

  it("renders the ceiling banner when otel_status_message is cost_ceiling_exceeded", () => {
    const { render } = setup({
      task: makeTask({ budget_micros_usd: 500_000 }),
      run: ceilingFailedRun(),
    });
    render();
    expect(screen.getByText(/Cost ceiling exceeded/i)).toBeTruthy();
  });

  it("does NOT render the ceiling banner for generic failures", () => {
    // The affordance is specifically for ceiling-overage failures.
    // A generic failed run (timeout, tool error, etc.) must not
    // surface it — it would invite operators to spend more on a
    // run that's blocked for unrelated reasons.
    const { render } = setup({
      task: makeTask({ budget_micros_usd: 500_000 }),
      run: makeRun({ status: "failed", otel_status_message: "tool_failed" }),
    });
    render();
    expect(screen.queryByText(/Cost ceiling exceeded/i)).toBeNull();
  });

  it("pre-fills the new ceiling at 2x the current ceiling", () => {
    const { render } = setup({
      task: makeTask({ budget_micros_usd: 250_000 }),
      run: ceilingFailedRun(),
    });
    render();
    // Default raise = 2x → 500000 µUSD = $0.500
    const input = screen.getByRole("spinbutton") as HTMLInputElement;
    expect(input.value).toBe("0.500");
  });

  it("calls onResumeRaisingCeiling with the typed value in micro-USD", async () => {
    const { props, user, render } = setup({
      task: makeTask({ budget_micros_usd: 100_000 }),
      run: ceilingFailedRun(),
    });
    render();
    const input = screen.getByRole("spinbutton") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "0.500");
    const button = screen.getByRole("button", { name: /Raise ceiling & resume/i });
    await user.click(button);
    // 0.500 USD == 500000 µUSD
    expect(props.onResumeRaisingCeiling).toHaveBeenCalledWith(500_000);
  });

  it("disables the action and shows an error when the new value is below the current ceiling", async () => {
    const { props, user, render } = setup({
      task: makeTask({ budget_micros_usd: 500_000 }),
      run: ceilingFailedRun(),
    });
    render();
    const input = screen.getByRole("spinbutton") as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "0.100"); // below 0.500
    const button = screen.getByRole("button", { name: /Raise ceiling & resume/i }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/Must be at least \$0\.500/i)).toBeTruthy();
    await user.click(button);
    expect(props.onResumeRaisingCeiling).not.toHaveBeenCalled();
  });
});

describe("TaskDetail run cost badge", () => {
  it("shows just this run's cost when there's no prior chain", () => {
    const { render } = setup({
      run: makeRun({ total_cost_micros_usd: 12_345 }),
    });
    render();
    // 12_345 µUSD ≈ $0.012; toFixed(3) = $0.012
    expect(screen.getByText(/\$0\.012/)).toBeTruthy();
    // No "/ task" suffix when prior chain is empty.
    expect(screen.queryByText(/task/)).toBeNull();
  });

  it("shows cumulative task cost when prior chain has spend", () => {
    const { render } = setup({
      run: makeRun({ total_cost_micros_usd: 250_000, prior_cost_micros_usd: 750_000 }),
    });
    render();
    // This run = $0.250, total = $1.000, with " / $1.000 task" suffix.
    expect(screen.getByText(/\$0\.250/)).toBeTruthy();
    expect(screen.getByText(/\$1\.000 task/)).toBeTruthy();
  });

  it("hides the badge entirely when both costs are zero", () => {
    const { render } = setup({
      run: makeRun({ total_cost_micros_usd: 0, prior_cost_micros_usd: 0 }),
    });
    render();
    // No "$" character anywhere from the badge — guards against an
    // empty $0.000 stub being rendered as visual noise.
    expect(screen.queryByText(/\$0\.000/)).toBeNull();
  });
});

describe("TaskDetail steps timeline — MCP tool distinction", () => {
  // makeMcpStep is a builder mirroring the wire shape the gateway
  // emits for MCP tool calls in the agent loop. tool_name is the
  // namespaced `mcp__<server>__<tool>` form; the title carries the
  // raw name with a status suffix (executor_agent_loop.go produces
  // exactly this shape).
  function makeMcpStep(overrides: Partial<TaskStepRecord> = {}): TaskStepRecord {
    return makeStep({
      id: "step-mcp",
      kind: "tool",
      tool_name: "mcp__filesystem__read_text_file",
      title: "mcp__filesystem__read_text_file (completed)",
      ...overrides,
    });
  }

  it("renders an MCP badge on namespaced tool steps", () => {
    const { render } = setup({ steps: [makeMcpStep()] });
    render();
    // The badge has aria-label "MCP tool call" so screen readers
    // announce it consistently across rows.
    expect(screen.getByLabelText(/mcp tool call/i)).toBeTruthy();
  });

  it("renders parsed server · tool instead of the raw namespaced name in the row", () => {
    const { render } = setup({ steps: [makeMcpStep()] });
    render();
    // Server and tool render as separate spans with a middle-dot
    // between them; assert each part is independently visible.
    expect(screen.getByText("filesystem")).toBeTruthy();
    expect(screen.getByText("read_text_file")).toBeTruthy();
    // The raw namespaced name must NOT appear as the row's visible
    // label — it would defeat the whole point of the parse. (It's
    // still in the row's title attribute for copy-paste.)
    expect(screen.queryByText("mcp__filesystem__read_text_file")).toBeNull();
  });

  it("does NOT render the MCP badge on built-in tool steps", () => {
    // shell_exec is a built-in — no MCP_TOOL_PREFIX, so no badge.
    // Pinning this guard keeps a future regex slip-up (matching
    // anything containing "mcp") from putting the badge on
    // unrelated rows.
    const builtin = makeStep({ id: "step-shell", kind: "tool", tool_name: "shell_exec", title: "shell_exec (completed)" });
    const { render } = setup({ steps: [builtin] });
    render();
    expect(screen.queryByLabelText(/mcp tool call/i)).toBeNull();
    // Built-in keeps its title verbatim.
    expect(screen.getByText("shell_exec (completed)")).toBeTruthy();
  });

  it("expanded StepDetail breaks out transport, server, and tool labels for MCP steps", async () => {
    const { render, user } = setup({ steps: [makeMcpStep()] });
    render();
    // Click the row to expand its detail.
    await user.click(screen.getByRole("button", { name: /step mcp__filesystem__read_text_file/i }));
    // Expanded detail must carry transport/server/tool as
    // separate labelled facts.
    expect(screen.getByText(/^transport:/)).toBeTruthy();
    expect(screen.getByText(/^server:/)).toBeTruthy();
    // "tool:" appears here too — but we don't pin its presence with
    // a generic regex because the row badge text "MCP" might match
    // separately in some CSS setups. Instead we look up the
    // surrounding label and assert the tool name appears.
    expect(screen.getAllByText("read_text_file").length).toBeGreaterThanOrEqual(1);
  });

  it("expanded StepDetail on a built-in shows the single-line tool label, not transport/server", async () => {
    const builtin = makeStep({ id: "step-shell", kind: "tool", tool_name: "shell_exec", title: "shell_exec (completed)" });
    const { render, user } = setup({ steps: [builtin] });
    render();
    await user.click(screen.getByRole("button", { name: /step shell_exec/i }));
    // The single combined "tool:" line is the existing rendering;
    // the MCP-only "transport:" / "server:" facets must NOT appear.
    expect(screen.queryByText(/^transport:/)).toBeNull();
    expect(screen.queryByText(/^server:/)).toBeNull();
  });

  it("MCP StepDetail points operators to the conversation viewer for the full result", async () => {
    // The dispatcher trims output_summary to {is_error, text_size}
    // to keep step rows small; the actual upstream text lives in
    // the agent_conversation artifact below. This hint is the
    // navigation aid that closes the loop without inventing a
    // step→message join.
    const { render, user } = setup({ steps: [makeMcpStep()] });
    render();
    await user.click(screen.getByRole("button", { name: /step mcp__filesystem__read_text_file/i }));
    expect(screen.getByText(/full upstream result rendered in the agent conversation/i)).toBeTruthy();
  });

  it("handles a tool name with embedded double-underscores in the tool segment", () => {
    // Some upstream MCP servers use `__` inside their tool names
    // (e.g. `mcp__weird__double__under` parses as server=weird,
    // tool=double__under). The Go-side SplitNamespacedToolName
    // honors the FIRST split after the server segment — pin the
    // same behavior on the UI side.
    const step = makeMcpStep({
      id: "step-weird",
      tool_name: "mcp__weird__double__under",
      title: "mcp__weird__double__under (completed)",
    });
    const { render } = setup({ steps: [step] });
    render();
    expect(screen.getByText("weird")).toBeTruthy();
    expect(screen.getByText("double__under")).toBeTruthy();
  });
});
