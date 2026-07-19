import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { NewTaskSlideOver } from "./NewTaskSlideOver";

const agentLoopModelFixtures = [
  {
    id: "model-a",
    owned_by: "test",
    metadata: { provider: "test", provider_kind: "local", default: false },
  },
];

function setup(propOverrides: Partial<React.ComponentProps<typeof NewTaskSlideOver>> = {}) {
  const props: React.ComponentProps<typeof NewTaskSlideOver> = {
    open: true,
    models: [],
    busyAction: "",
    onClose: vi.fn(),
    onCreate: vi.fn(),
    ...propOverrides,
  };
  const user = userEvent.setup();
  return { props, user, render: () => render(<NewTaskSlideOver {...props} />) };
}

describe("NewTaskSlideOver visibility", () => {
  it("renders nothing when open is false", () => {
    const { render } = setup({ open: false });
    const { container } = render();
    expect(container.firstChild).toBeNull();
  });

  it("renders the panel when open is true", () => {
    const { render } = setup();
    render();
    expect(screen.getByRole("dialog", { name: /new task/i })).toBeTruthy();
  });

  it("starts on the shell tab and shows the shell command field", () => {
    const { render } = setup();
    render();
    expect(screen.getByPlaceholderText(/ls -la/i)).toBeTruthy();
  });
});

describe("NewTaskSlideOver kind switching", () => {
  it("switching to git swaps in the git command field", async () => {
    const { render, user } = setup();
    render();
    await user.click(screen.getByRole("button", { name: "Git" }));
    expect(screen.getByPlaceholderText(/status \/ log/i)).toBeTruthy();
    expect(screen.queryByPlaceholderText(/ls -la/i)).toBeNull();
  });

  it("switching to file shows path and content inputs", async () => {
    const { render, user } = setup();
    render();
    await user.click(screen.getByRole("button", { name: "File" }));
    expect(screen.getByPlaceholderText(/\/path\/to\/file/i)).toBeTruthy();
    expect(screen.getByPlaceholderText(/file content/i)).toBeTruthy();
  });

  it("switching to agent loop shows the prompt textarea", async () => {
    const { render, user } = setup();
    render();
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    expect(screen.getByPlaceholderText(/describe the task/i)).toBeTruthy();
    expect(screen.getByRole("combobox", { name: /workflow mode/i })).toBeTruthy();
  });

  it("hides the workspace input for file kind, shows it for shell/git/agent_loop", async () => {
    const { render, user } = setup();
    render();
    // Default kind shell — workspace is visible.
    expect(screen.getByPlaceholderText("/Users/alice/dev/project")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "File" }));
    // File tasks have their own file_path field, no separate workspace.
    expect(screen.queryByPlaceholderText("/Users/alice/dev/project")).toBeNull();
    // Agent_loop tasks DO show the workspace input — needed
    // for direct workspace mode (target the operator's real
    // repo). Switching to agent_loop should re-show it.
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    expect(screen.getByPlaceholderText("/Users/alice/dev/project")).toBeTruthy();
  });

  it("hides the description input on the agent_loop tab (the prompt IS the description)", async () => {
    const { render, user } = setup();
    render();
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    expect(screen.queryByPlaceholderText(/human-readable description/i)).toBeNull();
  });
});

describe("NewTaskSlideOver submit", () => {
  it("disables 'Create task & start run' until the required field is filled", async () => {
    const { render, user } = setup();
    render();
    const queueBtn = screen.getByRole("button", {
      name: /create task & start run/i,
    }) as HTMLButtonElement;
    expect(queueBtn.disabled).toBe(true);
    await user.type(screen.getByPlaceholderText(/ls -la/i), "echo hi");
    expect(queueBtn.disabled).toBe(false);
  });

  it("submits a shell payload with the trimmed command", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate });
    render();
    await user.type(screen.getByPlaceholderText(/ls -la/i), "echo hi");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        execution_kind: "shell",
        shell_command: "echo hi",
        prompt: "echo hi",
      }),
    );
  });

  it("submits a git payload with `git ${command}` as the fallback prompt", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate });
    render();
    await user.click(screen.getByRole("button", { name: "Git" }));
    await user.type(screen.getByPlaceholderText(/status/i), "log --oneline");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        execution_kind: "git",
        git_command: "log --oneline",
        prompt: "git log --oneline",
      }),
    );
  });

  it("submits a file payload with the chosen operation and content", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate });
    render();
    await user.click(screen.getByRole("button", { name: "File" }));
    await user.type(screen.getByPlaceholderText(/\/path\/to\/file/i), "/tmp/note.txt");
    await user.type(screen.getByPlaceholderText(/file content/i), "hello");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        execution_kind: "file",
        file_path: "/tmp/note.txt",
        file_content: "hello",
        file_operation: "write",
      }),
    );
  });

  it("includes working_directory only when filled", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate });
    render();
    await user.type(screen.getByPlaceholderText(/ls -la/i), "echo hi");
    await user.type(screen.getByPlaceholderText("/Users/alice/dev/project"), "/tmp");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        working_directory: "/tmp",
      }),
    );
  });

  it("omits working_directory when blank — the gateway treats absence as 'workspace root'", async () => {
    // Sending an empty string lands as a literal "" working_directory at
    // the gateway, which is not the same as omission. The optional-spread
    // pattern in submit() guards against this; the test asserts it.
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate });
    render();
    await user.type(screen.getByPlaceholderText(/ls -la/i), "echo hi");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    const payload = onCreate.mock.calls[0][0];
    expect(payload.working_directory).toBeUndefined();
  });

  it("submits the visible provider-scoped model when the operator pins a provider", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({
      onCreate,
      models: [
        {
          id: "gpt-5.4-mini",
          owned_by: "openai",
          metadata: { provider: "openai", provider_kind: "cloud", default: true },
        },
        {
          id: "ministral-3:latest",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local", default: true },
        },
      ],
      providers: [
        { name: "openai", kind: "cloud", healthy: true, status: "ready" },
        { name: "ollama", kind: "local", healthy: true, status: "ready" },
      ],
      providerPresets: [
        {
          id: "openai",
          name: "OpenAI",
          kind: "cloud",
          protocol: "openai",
          base_url: "https://api.openai.com/v1",
        },
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
        },
      ],
    });
    render();
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    await user.type(screen.getByPlaceholderText(/describe the task/i), "show git diff");
    await user.click(screen.getByRole("button", { name: /Any provider/i }));
    await user.click(screen.getByRole("option", { name: /Ollama/i }));
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        execution_kind: "agent_loop",
        requested_provider: "ollama",
        requested_model: "ministral-3:latest",
      }),
    );
  });

  it("submits the explicitly selected model from the model picker", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({
      onCreate,
      models: [
        {
          id: "gpt-5.4-mini",
          owned_by: "openai",
          metadata: { provider: "openai", provider_kind: "cloud", default: true },
        },
        {
          id: "ministral-3:latest",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local", default: false },
        },
      ],
      providerPresets: [
        {
          id: "openai",
          name: "OpenAI",
          kind: "cloud",
          protocol: "openai",
          base_url: "https://api.openai.com/v1",
        },
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
        },
      ],
    });
    render();
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    await user.type(screen.getByPlaceholderText(/describe the task/i), "show git diff");
    await user.click(screen.getByRole("button", { name: /Model picker:/i }));
    await user.click(screen.getByRole("option", { name: /ministral-3:latest/i }));
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));

    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        execution_kind: "agent_loop",
        requested_model: "ministral-3:latest",
      }),
    );
  });

  it("submits report-only QA as a bounded agent-loop workflow", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({
      onCreate,
      models: [
        {
          ...agentLoopModelFixtures[0],
          metadata: { ...agentLoopModelFixtures[0].metadata, default: true },
        },
      ],
    });
    render();
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    await user.selectOptions(screen.getByRole("combobox", { name: /workflow mode/i }), "qa");
    await user.type(screen.getByPlaceholderText(/describe the task/i), "inspect for regressions");

    expect(screen.getByText(/does not run shell tests/i)).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: /system prompt/i })).toBeNull();
    expect(screen.queryByText("MCP SERVERS")).toBeNull();
    expect(screen.queryByLabelText(/run directly in this workspace/i)).toBeNull();

    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    const payload = onCreate.mock.calls[0][0];
    expect(payload).toMatchObject({
      execution_kind: "agent_loop",
      workflow_mode: "qa",
      prompt: "inspect for regressions",
    });
    expect(payload.mcp_servers).toBeUndefined();
    expect(payload.workspace_mode).toBeUndefined();
    expect(payload.system_prompt).toBeUndefined();
  });

  it("prefills workspace from the shared agent workspace default", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, defaultWorkspace: "/Users/me/dev/hecate" });
    render();
    expect(screen.getByPlaceholderText("/Users/alice/dev/project")).toHaveValue(
      "/Users/me/dev/hecate",
    );
    await user.type(screen.getByPlaceholderText(/ls -la/i), "echo hi");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        working_directory: "/Users/me/dev/hecate",
      }),
    );
  });

  it("Enter key in shell command field submits when valid", async () => {
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate });
    render();
    const input = screen.getByPlaceholderText(/ls -la/i);
    await user.type(input, "echo hi{Enter}");
    expect(onCreate).toHaveBeenCalled();
  });

  it("exposes labelled controls for keyboard and screen-reader users", async () => {
    const { render, user } = setup();
    render();
    expect(screen.getByRole("textbox", { name: /shell command/i })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: /workspace path/i })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "File" }));
    expect(screen.getByRole("radio", { name: /file operation: write/i })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: /file path/i })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: /file content/i })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    expect(screen.getByRole("combobox", { name: /workflow mode/i })).toBeTruthy();
  });
});

describe("NewTaskSlideOver close behavior", () => {
  it("clicking the X button calls onClose", async () => {
    const onClose = vi.fn();
    const { render, user } = setup({ onClose });
    render();
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("clicking 'Cancel' calls onClose", async () => {
    const onClose = vi.fn();
    const { render, user } = setup({ onClose });
    render();
    await user.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(onClose).toHaveBeenCalled();
  });

  it("clicking the backdrop calls onClose", async () => {
    const onClose = vi.fn();
    const { render, user } = setup({ onClose });
    const { container } = render();
    // The outermost div is the backdrop. Click it directly.
    await user.click(container.firstChild as Element);
    expect(onClose).toHaveBeenCalled();
  });
});

describe("NewTaskSlideOver feedback", () => {
  it("renders the errorMessage prop when provided", () => {
    const { render } = setup({ errorMessage: "rate limited" });
    render();
    expect(screen.getByText(/rate limited/i)).toBeTruthy();
  });

  it("shows the busy label on the queue button while creating", () => {
    const { render } = setup({ busyAction: "create" });
    render();
    expect(screen.getByRole("button", { name: /creating/i })).toBeTruthy();
  });
});

describe("NewTaskSlideOver workspace preview", () => {
  it("shows the isolated-clone preview by default for shell tasks", () => {
    const { render } = setup();
    render();
    // Default kind is shell — workspace is visible, so the
    // preview line should render with the temp-dir clone pattern.
    expect(screen.getByText(/isolated clone at/i)).toBeTruthy();
    expect(screen.getByText(/hecate-workspaces/)).toBeTruthy();
  });

  it("switches to direct-workspace messaging when enabled", async () => {
    const { render, user } = setup();
    render();
    // Need an absolute path for the in-place preview to validate.
    const wdInput = screen.getByPlaceholderText("/Users/alice/dev/project");
    await user.type(wdInput, "/Users/me/project");
    const inPlace = screen.getByLabelText(/Run directly in this workspace/i);
    await user.click(inPlace);
    expect(screen.getByText(/writes land here directly/i)).toBeTruthy();
    // The isolated-clone path text must NOT appear once in-place is on.
    expect(screen.queryByText(/isolated clone at/i)).toBeNull();
  });

  it("flags missing path when direct workspace mode is on but workspace is empty", async () => {
    const { render, user } = setup();
    render();
    await user.click(screen.getByLabelText(/Run directly in this workspace/i));
    expect(screen.getByText(/needs an absolute workspace path/i)).toBeTruthy();
  });

  it("flags relative path when in-place is on but path isn't absolute", async () => {
    const { render, user } = setup();
    render();
    const wdInput = screen.getByPlaceholderText("/Users/alice/dev/project");
    await user.type(wdInput, "./relative/path");
    await user.click(screen.getByLabelText(/Run directly in this workspace/i));
    expect(screen.getByText(/needs an absolute path/i)).toBeTruthy();
  });
});

describe("NewTaskSlideOver model warnings (agent_loop)", () => {
  // Models known to lack tool-calling. The picker flags them with
  // a non-blocking ⚠ — operators can still pick if they know what
  // they're doing, but the visual cue saves a wasted run.
  const mixedModels = [
    {
      id: "gpt-4o-mini",
      owned_by: "openai",
      metadata: { provider: "openai", provider_kind: "cloud", default: true },
    },
    {
      id: "smollm2:135m",
      owned_by: "ollama",
      metadata: { provider: "ollama", provider_kind: "local", default: false },
    },
    {
      id: "qwen2.5-coder:7b",
      owned_by: "ollama",
      metadata: { provider: "ollama", provider_kind: "local", default: false },
    },
    {
      id: "nomic-embed-text",
      owned_by: "ollama",
      metadata: { provider: "ollama", provider_kind: "local", default: false },
    },
  ];

  it("flags non-tool-capable models on the agent_loop tab", async () => {
    const { render, user } = setup({ models: mixedModels });
    render();
    await user.click(screen.getByRole("button", { name: "Agent loop" }));
    // Open the model picker so rows render.
    const modelTrigger = screen
      .getAllByRole("button")
      .find((b) => b.textContent?.includes("model") || b.textContent?.includes("gpt-4o-mini"));
    if (modelTrigger) await user.click(modelTrigger);
    // smollm2 + nomic-embed should each have an aria-labeled
    // warning icon; gpt-4o-mini and qwen2.5-coder must not.
    const warnings = screen.getAllByLabelText(/Likely doesn't support tool-calling/i);
    expect(warnings.length).toBeGreaterThanOrEqual(2);
  });

  it("does NOT flag any models on the shell tab (warnings only matter for agent_loop)", async () => {
    const { render, user } = setup({ models: mixedModels });
    render();
    // Default kind is shell. Open the model picker.
    const modelTrigger = screen
      .getAllByRole("button")
      .find((b) => b.textContent?.includes("model") || b.textContent?.includes("gpt-4o-mini"));
    if (modelTrigger) await user.click(modelTrigger);
    // No warning icons should render for non-agent_loop tasks.
    expect(screen.queryAllByLabelText(/Likely doesn't support tool-calling/i)).toHaveLength(0);
  });
});

// gotoAgentLoopAndAddMCPRow flips the form to the agent_loop tab,
// fills the prompt (so submit isn't blocked), and clicks the
// "Add MCP server" button. Returns the test handles for further
// assertions / interactions on the row that just got added.
async function gotoAgentLoopAndAddMCPRow(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Agent loop" }));
  await user.type(screen.getByPlaceholderText(/describe the task/i), "do the thing");
  const modelTrigger = screen.queryByRole("button", {
    name: /Model picker:/i,
  }) as HTMLButtonElement | null;
  if (
    modelTrigger &&
    !modelTrigger.disabled &&
    /pick a model/i.test(modelTrigger.textContent ?? "")
  ) {
    await user.click(modelTrigger);
    await user.click(screen.getByRole("option", { name: /model-a/i }));
  }
  await user.click(screen.getByRole("button", { name: /add mcp server/i }));
}

describe("NewTaskSlideOver MCP servers — transport toggle", () => {
  // Default transport is stdio: command/args/env fields are visible,
  // url/headers fields are NOT in the DOM. Switching the per-row
  // transport pill to HTTP swaps the field set in place.
  it("starts a new row in stdio mode with command/args/env visible", async () => {
    const { render, user } = setup();
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    expect(screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i)).toBeTruthy();
    expect(screen.getByPlaceholderText(/^args \(space-separated/i)).toBeTruthy();
    expect(screen.getByPlaceholderText(/^env \(KEY=VALUE per line/i)).toBeTruthy();
    // HTTP-only fields must not render in the DOM yet.
    expect(screen.queryByPlaceholderText(/^url \(e\.g\. https/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/^headers \(KEY=VALUE/i)).toBeNull();
  });

  it("clicking the HTTP pill swaps to url + headers fields", async () => {
    const { render, user } = setup();
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    // The transport pill has aria-label "Server 1 transport" via PillToggle.
    const transportGroup = screen.getByRole("group", { name: /server 1 transport/i });
    const httpBtn = within(transportGroup).getByRole("button", { name: /^HTTP$/i });
    await user.click(httpBtn);
    // After the swap the HTTP fields must be in the DOM and the
    // stdio fields must NOT.
    expect(screen.getByPlaceholderText(/^url \(e\.g\. https/i)).toBeTruthy();
    expect(screen.getByPlaceholderText(/^headers \(KEY=VALUE/i)).toBeTruthy();
    expect(screen.queryByPlaceholderText(/^command \(e\.g\. npx\)/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/^args \(space-separated/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/^env \(KEY=VALUE per line/i)).toBeNull();
  });

  it("toggling transport back to stdio retains the previously-typed command", async () => {
    // The form keeps both sides' state so a flip-flop doesn't lose
    // the operator's typing — pinning that here so we don't
    // accidentally regress to a "clear on switch" pattern.
    const { render, user } = setup();
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    const cmdInput = screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i);
    await user.type(cmdInput, "npx -y @scope/server");
    const transportGroup = screen.getByRole("group", { name: /server 1 transport/i });
    await user.click(within(transportGroup).getByRole("button", { name: /^HTTP$/i }));
    await user.click(within(transportGroup).getByRole("button", { name: /^stdio$/i }));
    // Same field by placeholder, but now the value should still be
    // there because the form preserved stdio state across the flip.
    const cmdAgain = screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i) as HTMLInputElement;
    expect(cmdAgain.value).toBe("npx -y @scope/server");
  });
});

describe("NewTaskSlideOver MCP servers — submit payload", () => {
  it("submits a stdio entry with parsed args + env", async () => {
    // Pins backwards-compat with the pre-HTTP MCP shape: a stdio
    // server with command/args/env still sends only those fields,
    // does NOT send url/headers, and omits approval_policy when
    // it's left at the default (auto).
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, models: agentLoopModelFixtures });
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    await user.type(screen.getByPlaceholderText(/^name \(e\.g\. filesystem\)/i), "fs");
    await user.type(screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i), "npx");
    await user.type(
      screen.getByPlaceholderText(/^args \(space-separated/i),
      "-y @mcp/server-fs /workspace",
    );
    await user.type(
      screen.getByPlaceholderText(/^env \(KEY=VALUE per line/i),
      "TOKEN=abc{enter}DEBUG=1",
    );
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate).toHaveBeenCalled();
    const payload = onCreate.mock.calls[0][0];
    expect(payload.mcp_servers).toEqual([
      {
        name: "fs",
        command: "npx",
        args: ["-y", "@mcp/server-fs", "/workspace"],
        env: { TOKEN: "abc", DEBUG: "1" },
      },
    ]);
  });

  it("submits an HTTP entry with url + headers and no command/args/env", async () => {
    // Pins the new HTTP transport shape: url + headers are present;
    // command/args/env are absent (NOT empty strings — the gateway's
    // mutual-exclusion check would reject "" as a present-but-empty
    // command).
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, models: agentLoopModelFixtures });
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    await user.type(screen.getByPlaceholderText(/^name \(e\.g\. filesystem\)/i), "remote");
    const transportGroup = screen.getByRole("group", { name: /server 1 transport/i });
    await user.click(within(transportGroup).getByRole("button", { name: /^HTTP$/i }));
    await user.type(
      screen.getByPlaceholderText(/^url \(e\.g\. https/i),
      "https://api.example.com/mcp",
    );
    await user.type(
      screen.getByPlaceholderText(/^headers \(KEY=VALUE/i),
      "Authorization=Bearer xyz{enter}X-Trace=on",
    );
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    const payload = onCreate.mock.calls[0][0];
    expect(payload.mcp_servers).toEqual([
      {
        name: "remote",
        url: "https://api.example.com/mcp",
        headers: { Authorization: "Bearer xyz", "X-Trace": "on" },
      },
    ]);
    expect(payload.mcp_servers[0].command).toBeUndefined();
    expect(payload.mcp_servers[0].args).toBeUndefined();
    expect(payload.mcp_servers[0].env).toBeUndefined();
  });

  it("omits approval_policy when the operator leaves it at the auto default", async () => {
    // The gateway treats absence of approval_policy as auto, so the
    // form's "send only when non-default" rule keeps API responses
    // free of redundant fields. Default = auto = omitted.
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, models: agentLoopModelFixtures });
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    await user.type(screen.getByPlaceholderText(/^name \(e\.g\. filesystem\)/i), "github");
    await user.type(screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i), "npx");
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate.mock.calls[0][0].mcp_servers[0].approval_policy).toBeUndefined();
  });

  it("emits approval_policy = require_approval when the operator picks it", async () => {
    // The flip from auto to require_approval is the headline gating
    // workflow — the operator wants the run to pause before any tool
    // call to this server. Pin that the wire shape carries the
    // string verbatim so the gateway's per-server policy lookup
    // matches.
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, models: agentLoopModelFixtures });
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    await user.type(screen.getByPlaceholderText(/^name \(e\.g\. filesystem\)/i), "github");
    await user.type(screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i), "npx");
    const policyGroup = screen.getByRole("group", { name: /server 1 approval policy/i });
    await user.click(within(policyGroup).getByRole("button", { name: /^require approval$/i }));
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate.mock.calls[0][0].mcp_servers[0].approval_policy).toBe("require_approval");
  });

  it("emits approval_policy = block when the operator picks block", async () => {
    // Block is the third state — easy to forget if we only test
    // auto + require_approval, so we pin it explicitly.
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, models: agentLoopModelFixtures });
    render();
    await gotoAgentLoopAndAddMCPRow(user);
    await user.type(screen.getByPlaceholderText(/^name \(e\.g\. filesystem\)/i), "github");
    await user.type(screen.getByPlaceholderText(/^command \(e\.g\. npx\)/i), "npx");
    const policyGroup = screen.getByRole("group", { name: /server 1 approval policy/i });
    await user.click(within(policyGroup).getByRole("button", { name: /^block$/i }));
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    expect(onCreate.mock.calls[0][0].mcp_servers[0].approval_policy).toBe("block");
  });

  it("drops a row that has neither name, command, nor url before submit", async () => {
    // The submit-time filter matches {name OR command OR url} — a
    // row the operator clicked Add on but never filled must NOT
    // travel as a phantom entry, otherwise the gateway returns a
    // 400 the operator can't easily map back to a UI mistake.
    const onCreate = vi.fn();
    const { render, user } = setup({ onCreate, models: agentLoopModelFixtures });
    render();
    // Add a row, leave it empty.
    await gotoAgentLoopAndAddMCPRow(user);
    await user.click(screen.getByRole("button", { name: /create task & start run/i }));
    const payload = onCreate.mock.calls[0][0];
    // mcp_servers should either be undefined OR an empty array; both
    // are acceptable since the form's optional-spread treats an
    // empty array specially.
    expect(payload.mcp_servers ?? []).toHaveLength(0);
  });
});
