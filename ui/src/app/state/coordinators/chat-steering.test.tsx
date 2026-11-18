import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatActions } from "./chat";
import { ChatProvider, useChat, type ChatState } from "../chat";
import { RuntimeProvider, useRuntime } from "../runtime";
import { ProvidersAndModelsProvider } from "../providersAndModels";
import { ProjectsProvider } from "../projects";
import { SettingsProvider } from "../settings";
import { ApprovalsProvider } from "../approvals";
import { UsageProvider } from "../usage";
import { ApiError, steerTaskRun } from "../../../lib/api";

vi.mock("../../../lib/api", async (original) => ({
  ...(await original<typeof import("../../../lib/api")>()),
  steerTaskRun: vi.fn(),
}));

function setup(overrides: Partial<ChatState> = {}) {
  const session = {
    id: "chat",
    agent_id: "hecate",
    workspace: "/tmp/work",
    title: "Work",
    task_id: "task",
    latest_run_id: "run",
    status: "running",
    messages: [],
  };
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <RuntimeProvider initialState={{ message: "Inspect first" }}>
        <UsageProvider>
          <ProvidersAndModelsProvider>
            <ProjectsProvider>
              <ChatProvider
                initialState={{
                  activeChatSessionID: "chat",
                  activeChatSession: session,
                  ...overrides,
                }}
              >
                <SettingsProvider>
                  <ApprovalsProvider>{children}</ApprovalsProvider>
                </SettingsProvider>
              </ChatProvider>
            </ProjectsProvider>
          </ProvidersAndModelsProvider>
        </UsageProvider>
      </RuntimeProvider>
    );
  }
  return renderHook(
    () => ({
      actions: useChatActions({ chatTarget: "agent", setNoticeMessage: vi.fn() }),
      runtime: useRuntime(),
      chat: useChat(),
    }),
    { wrapper: Wrapper },
  );
}

describe("live guidance coordinator", () => {
  beforeEach(() => {
    vi.mocked(steerTaskRun).mockReset();
    sessionStorage.clear();
  });
  it("retries an ambiguous response with the same identity and preserves the draft", async () => {
    vi.mocked(steerTaskRun)
      .mockRejectedValueOnce(new Error("Connection lost"))
      .mockResolvedValueOnce({
        object: "task.run.steering",
        data: {
          id: "guidance",
          content: "Inspect first",
          status: "pending",
          sequence: 1,
          created_at: "now",
          replay: true,
        },
      });
    const { result } = setup();
    await act(async () => {
      expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
    });
    expect(result.current.runtime.state.message).toBe("Inspect first");
    await act(async () => {
      expect(await result.current.actions.steerActiveRun("task", "run")).toBe(true);
    });
    expect(vi.mocked(steerTaskRun).mock.calls[0][3]).toBe(vi.mocked(steerTaskRun).mock.calls[1][3]);
    expect(result.current.runtime.state.message).toBe("");
  });
  it.each([
    [429, "task.steering_full"],
    [409, "task.steering_unavailable"],
  ])(
    "retires a typed non-admission response %s/%s without losing the draft",
    async (status, code) => {
      vi.mocked(steerTaskRun).mockRejectedValue(new ApiError("Not admitted", status, code));
      const { result } = setup();
      await act(async () => {
        expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
      });
      expect(result.current.runtime.state.message).toBe("Inspect first");
      expect(sessionStorage.length).toBe(0);
      await act(async () => {
        expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
      });
      expect(vi.mocked(steerTaskRun).mock.calls[0][3]).not.toBe(
        vi.mocked(steerTaskRun).mock.calls[1][3],
      );
    },
  );
  it.each([
    [500, "gateway_error"],
    [429, ""],
    [409, "task.steering_request_conflict"],
    [500, "task.steering_full"],
    [429, "task.steering_unavailable"],
  ])("keeps retry identity for an unproven failure %s/%s", async (status, code) => {
    vi.mocked(steerTaskRun).mockRejectedValue(new ApiError("Unknown outcome", status, code));
    const { result } = setup();
    for (let attempt = 0; attempt < 2; attempt++) {
      await act(async () => {
        expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
      });
    }
    expect(result.current.runtime.state.message).toBe("Inspect first");
    expect(vi.mocked(steerTaskRun).mock.calls[0][3]).toBe(vi.mocked(steerTaskRun).mock.calls[1][3]);
  });
  it.each([
    {
      object: "unknown",
      data: { id: "guidance", content: "Inspect first", status: "pending", replay: false },
    },
    {
      object: "task.run.steering",
      data: { id: "guidance", content: "Inspect first", status: "unknown", replay: false },
    },
    {
      object: "task.run.steering",
      data: { id: "guidance", content: "Different text", status: "pending", replay: false },
    },
    { object: "task.run.steering", data: {} },
    {
      object: "task.run.steering",
      data: { id: 123, content: "Inspect first", status: "pending", replay: true },
    },
  ])(
    "preserves the draft and retry identity for malformed acknowledgments %#",
    async (response) => {
      vi.mocked(steerTaskRun).mockResolvedValue(
        response as unknown as Awaited<ReturnType<typeof steerTaskRun>>,
      );
      const { result } = setup();
      for (let attempt = 0; attempt < 2; attempt++) {
        await act(async () => {
          expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
        });
      }
      expect(result.current.runtime.state.message).toBe("Inspect first");
      expect(vi.mocked(steerTaskRun).mock.calls[0][3]).toBe(
        vi.mocked(steerTaskRun).mock.calls[1][3],
      );
    },
  );
  it("preserves edits made while a delivery acknowledgment is pending", async () => {
    let acknowledge!: (value: Awaited<ReturnType<typeof steerTaskRun>>) => void;
    vi.mocked(steerTaskRun).mockImplementation(
      () =>
        new Promise((resolve) => {
          acknowledge = resolve;
        }),
    );
    const { result } = setup();
    let delivery!: Promise<boolean>;
    act(() => {
      delivery = result.current.actions.steerActiveRun("task", "run");
    });
    await act(async () => {
      expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
    });
    act(() => {
      result.current.runtime.actions.setMessage("New draft");
    });
    await act(async () => {
      acknowledge({
        object: "task.run.steering",
        data: {
          id: "guidance",
          content: "Inspect first",
          status: "pending",
          sequence: 1,
          created_at: "now",
          replay: false,
        },
      });
      await delivery;
    });
    expect(result.current.runtime.state.message).toBe("New draft");
    expect(steerTaskRun).toHaveBeenCalledOnce();
  });
  it("does not dispatch into an approval pause or a different run", async () => {
    const { result } = setup({
      activeChatSession: {
        id: "chat",
        agent_id: "hecate",
        workspace: "/tmp/work",
        title: "Work",
        task_id: "task",
        latest_run_id: "run",
        status: "awaiting_approval",
        messages: [],
      },
    });
    await act(async () => {
      expect(await result.current.actions.steerActiveRun("task", "run")).toBe(false);
    });
    expect(steerTaskRun).not.toHaveBeenCalled();
  });
  it("keeps the replay identity when acknowledgment arrives after switching chats", async () => {
    let acknowledge!: (value: Awaited<ReturnType<typeof steerTaskRun>>) => void;
    vi.mocked(steerTaskRun).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          acknowledge = resolve;
        }),
    );
    const { result } = setup();
    let delivery!: Promise<boolean>;
    act(() => {
      delivery = result.current.actions.steerActiveRun("task", "run");
    });
    act(() => {
      result.current.chat.actions.setActiveChatSessionID("other-chat");
      result.current.runtime.actions.setMessage("Other chat draft");
    });
    const response = {
      object: "task.run.steering",
      data: {
        id: "guidance",
        content: "Inspect first",
        status: "pending" as const,
        sequence: 1,
        created_at: "now",
        replay: true,
      },
    };
    await act(async () => {
      acknowledge(response);
      await delivery;
    });
    expect(result.current.runtime.state.message).toBe("Other chat draft");
    act(() => {
      result.current.chat.actions.setActiveChatSessionID("chat");
      result.current.runtime.actions.setMessage("Inspect first");
    });
    vi.mocked(steerTaskRun).mockResolvedValueOnce(response);
    await act(async () => {
      expect(await result.current.actions.steerActiveRun("task", "run")).toBe(true);
    });
    expect(vi.mocked(steerTaskRun).mock.calls[0][3]).toBe(vi.mocked(steerTaskRun).mock.calls[1][3]);
    expect(result.current.runtime.state.message).toBe("");
  });
});
