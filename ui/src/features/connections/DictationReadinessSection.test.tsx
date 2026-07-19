import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getDictationOptions } from "../../lib/api";
import { DictationReadinessSection } from "./DictationReadinessSection";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return { ...actual, getDictationOptions: vi.fn() };
});

const localRoute = {
  provider: "localai",
  provider_kind: "local",
  default_model: "whisper-1",
  available: true,
};

beforeEach(() => {
  vi.mocked(getDictationOptions).mockReset();
});

describe("DictationReadinessSection", () => {
  it("shows that a verified speech-to-text route works for external agents", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({
      object: "dictation_options",
      data: [
        localRoute,
        {
          provider: "openai",
          provider_kind: "cloud",
          default_model: "gpt-4o-mini-transcribe",
          available: false,
          unavailable_reason: "provider credentials are missing",
        },
      ],
    });

    render(<DictationReadinessSection />);

    const section = await screen.findByTestId("connections-dictation");
    expect(
      within(section).getByRole("heading", { level: 2, name: "Speech-to-text route readiness" }),
    ).toBeVisible();
    expect(within(section).getByLabelText("Speech-to-text route ready")).toBeVisible();
    expect(within(section).getByTestId("connections-dictation-ready")).toHaveTextContent(
      "1 speech-to-text route is ready for every chat target.",
    );
    expect(section).toHaveTextContent("Claude Code, Codex, and other External Agents");
    expect(section).toHaveTextContent("localai");
    expect(section).toHaveTextContent("local · whisper-1");
    expect(section).toHaveTextContent("provider credentials are missing");
    const routes = within(section).getByRole("list", { name: "Speech-to-text routes" });
    expect(routes).toHaveTextContent("localai");
    expect(within(routes).getAllByRole("listitem")).toHaveLength(2);
    expect(section).toHaveTextContent(
      "Hecate forwards audio only to the selected speech-to-text provider",
    );
  });

  it("guides a Claude Code or Codex-only setup to add a transcription provider", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({ object: "dictation_options", data: [] });
    const onAddProvider = vi.fn();
    const user = userEvent.setup();

    render(<DictationReadinessSection onAddProvider={onAddProvider} />);

    const section = await screen.findByTestId("connections-dictation");
    expect(within(section).getByLabelText("Speech-to-text route setup needed")).toBeVisible();
    expect(within(section).getByTestId("connections-dictation-unavailable")).toHaveTextContent(
      "No speech-to-text route is configured.",
    );
    expect(section).toHaveTextContent("their sign-ins do not provide speech-to-text");
    expect(section).toHaveTextContent("gpt-4o-mini-transcribe");
    expect(section).toHaveTextContent("whisper-large-v3-turbo");

    await user.click(within(section).getByRole("button", { name: "Add provider" }));
    expect(onAddProvider).toHaveBeenCalledOnce();
  });

  it("rechecks route readiness after an authoritative provider snapshot changes", async () => {
    vi.mocked(getDictationOptions)
      .mockResolvedValueOnce({ object: "dictation_options", data: [] })
      .mockResolvedValueOnce({ object: "dictation_options", data: [localRoute] });
    const providerConfig = { backend: "memory", providers: [], policy_rules: [], events: [] };

    const { rerender } = render(
      <DictationReadinessSection providerConfigSnapshot={providerConfig} />,
    );

    expect(await screen.findByTestId("connections-dictation-unavailable")).toHaveTextContent(
      "No speech-to-text route is configured.",
    );

    rerender(
      <DictationReadinessSection
        providerConfigSnapshot={{ ...providerConfig, providers: [...providerConfig.providers] }}
      />,
    );

    await waitFor(() => expect(getDictationOptions).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId("connections-dictation-ready")).toHaveTextContent(
      "1 speech-to-text route is ready for every chat target.",
    );
  });

  it("keeps failed readiness separate from provider setup and retries", async () => {
    vi.mocked(getDictationOptions)
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockResolvedValueOnce({ object: "dictation_options", data: [localRoute] });
    const user = userEvent.setup();

    render(<DictationReadinessSection />);

    expect(await screen.findByTestId("connections-dictation-error")).toHaveTextContent(
      "Could not load speech-to-text readiness.",
    );
    expect(screen.getByLabelText("Speech-to-text route check failed")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Add provider" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(getDictationOptions).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId("connections-dictation-ready")).toBeVisible();
  });

  it("does not recommend a local provider when setup is hosted", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({ object: "dictation_options", data: [] });

    render(<DictationReadinessSection localProviderSetupAvailable={false} />);

    const section = await screen.findByTestId("connections-dictation");
    expect(section).toHaveTextContent("Add OpenAI or Groq in Connections.");
    expect(section).not.toHaveTextContent("LocalAI");
  });

  it("aborts the readiness request when the section unmounts", () => {
    let observedSignal: AbortSignal | undefined;
    vi.mocked(getDictationOptions).mockImplementation((signal) => {
      observedSignal = signal;
      return new Promise(() => undefined);
    });

    const { unmount } = render(<DictationReadinessSection />);
    unmount();

    expect(observedSignal?.aborted).toBe(true);
  });
});
