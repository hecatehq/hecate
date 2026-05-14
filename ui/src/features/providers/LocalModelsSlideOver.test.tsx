import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { LocalModelsSlideOver } from "./LocalModelsSlideOver";
import {
  cancelLocalModelInstall,
  getLocalModelsCatalog,
  getLocalModelsInstalled,
  getLocalModelsRuntime,
  installLocalModel,
  listHuggingFaceRepoFiles,
  searchHuggingFaceModels,
  startLocalModel,
  stopLocalModel,
  subscribeLocalModelInstallEvents,
  uninstallLocalModel,
} from "../../lib/api";
import type {
  LocalModelCatalogEntry,
  LocalModelInstalled,
  LocalModelProgressEvent,
  LocalModelRuntimeResponse,
} from "../../types/runtime";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getLocalModelsCatalog: vi.fn(),
    getLocalModelsInstalled: vi.fn(),
    getLocalModelsRuntime: vi.fn(),
    installLocalModel: vi.fn(),
    cancelLocalModelInstall: vi.fn(),
    uninstallLocalModel: vi.fn(),
    startLocalModel: vi.fn(),
    stopLocalModel: vi.fn(),
    subscribeLocalModelInstallEvents: vi.fn(),
    searchHuggingFaceModels: vi.fn(),
    listHuggingFaceRepoFiles: vi.fn(),
  };
});

const m = {
  catalog: vi.mocked(getLocalModelsCatalog),
  installed: vi.mocked(getLocalModelsInstalled),
  runtime: vi.mocked(getLocalModelsRuntime),
  install: vi.mocked(installLocalModel),
  cancel: vi.mocked(cancelLocalModelInstall),
  uninstall: vi.mocked(uninstallLocalModel),
  start: vi.mocked(startLocalModel),
  stop: vi.mocked(stopLocalModel),
  subscribe: vi.mocked(subscribeLocalModelInstallEvents),
  hfSearch: vi.mocked(searchHuggingFaceModels),
  hfFiles: vi.mocked(listHuggingFaceRepoFiles),
};

function catalogEntry(overrides: Partial<LocalModelCatalogEntry> = {}): LocalModelCatalogEntry {
  return {
    id: "qwen-tiny",
    display_name: "Qwen Tiny",
    description: "Tiny test model for smoke checks.",
    huggingface_url: "https://huggingface.co/foo/repo/resolve/main/qwen-tiny.gguf",
    sha256: "abc",
    size_bytes: 1_000_000,
    recommended_context: 2048,
    capabilities: { streaming: true, tool_calling: "none" },
    license: "apache-2.0",
    installed: false,
    ...overrides,
  };
}

function installedRow(overrides: Partial<LocalModelInstalled> = {}): LocalModelInstalled {
  return {
    id: "qwen-tiny",
    display_name: "Qwen Tiny",
    file_path: "models/qwen-tiny.gguf",
    sha256: "abc",
    size_bytes: 1_000_000,
    ...overrides,
  };
}

function runtime(overrides: Partial<LocalModelRuntimeResponse> = {}): LocalModelRuntimeResponse {
  return {
    object: "local_models.runtime",
    state: "idle",
    available: true,
    availability: { available: true, binary_path: "/fake/llama-server" },
    ...overrides,
  };
}

beforeEach(() => {
  Object.values(m).forEach((fn) => fn.mockReset());
  // Default load — empty everything. Specific tests override before
  // render() to inject the scenario they need.
  m.catalog.mockResolvedValue({ object: "local_models.catalog", data: [] });
  m.installed.mockResolvedValue({ object: "local_models.installed", data: [] });
  m.runtime.mockResolvedValue(runtime());
  m.start.mockResolvedValue(runtime({ state: "running" }));
  m.stop.mockResolvedValue(runtime({ state: "idle" }));
  m.install.mockResolvedValue({
    object: "local_models.install",
    install_id: "install-123",
    model_id: "qwen-tiny",
  });
  m.cancel.mockResolvedValue();
  m.uninstall.mockResolvedValue();
  m.subscribe.mockReturnValue(() => undefined);
  m.hfSearch.mockResolvedValue({ object: "local_models.huggingface.search", data: [] });
  m.hfFiles.mockResolvedValue({ object: "local_models.huggingface.files", data: [] });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("LocalModelsSlideOver", () => {
  it("renders Runtime / Installed / Catalog sections on first load", async () => {
    m.catalog.mockResolvedValue({
      object: "local_models.catalog",
      data: [catalogEntry()],
    });
    m.installed.mockResolvedValue({
      object: "local_models.installed",
      data: [installedRow()],
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);

    // The three required headers + the count badges. Asserting the
    // shape so a future re-ordering doesn't quietly drop a section.
    await screen.findByText("Runtime");
    await screen.findByText(/Installed \(1\)/);
    await screen.findByText(/Catalog \(1\)/);
    // Every section ships even when empty, so this is also covered:
    expect(screen.getByText(/Custom HuggingFace URL/i)).toBeInTheDocument();
  });

  it("renders the runtime pill + Stop button only while a model is loaded", async () => {
    m.runtime.mockResolvedValue(
      runtime({
        state: "running",
        active: {
          state: "running",
          active_model_id: "qwen-tiny",
          port: 8765,
          pid: 42,
        },
      }),
    );
    m.installed.mockResolvedValue({
      object: "local_models.installed",
      data: [installedRow()],
    });
    render(<LocalModelsSlideOver onClose={() => undefined} />);

    // Loopback port shows so the operator can curl it directly.
    // Using the port string as the wait anchor — it's unique to
    // the Runtime section, whereas the model id appears twice
    // (Runtime section + InstalledList subtitle).
    await screen.findByText(/loopback :8765/);
    // And confirm the active model id is in the Runtime section
    // — at least one instance must be present.
    expect(screen.getAllByText("qwen-tiny").length).toBeGreaterThan(0);
    // Stop button visible.
    expect(screen.getByRole("button", { name: /Stop/ })).toBeInTheDocument();
  });

  it("hides the Start button on the currently-loaded model row", async () => {
    m.runtime.mockResolvedValue(
      runtime({
        state: "running",
        active: { state: "running", active_model_id: "qwen-tiny" },
      }),
    );
    m.installed.mockResolvedValue({
      object: "local_models.installed",
      data: [installedRow()],
    });
    render(<LocalModelsSlideOver onClose={() => undefined} />);

    // The "loaded" badge is what tells the operator this row is
    // active without scrolling to the Runtime section.
    await screen.findByText("loaded");
    // No Start button for the loaded row — only Uninstall.
    expect(screen.queryAllByRole("button", { name: /^Start/i })).toHaveLength(0);
    expect(screen.getByRole("button", { name: /Uninstall/i })).toBeInTheDocument();
  });

  it("calls installLocalModel and subscribes to SSE when Install is clicked", async () => {
    const user = userEvent.setup();
    m.catalog.mockResolvedValue({
      object: "local_models.catalog",
      data: [catalogEntry()],
    });
    let publishEvent: ((ev: LocalModelProgressEvent) => void) | undefined;
    m.subscribe.mockImplementation((_id, onEvent) => {
      publishEvent = onEvent;
      return () => undefined;
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);

    // Wait for the catalog section to populate before picking
    // buttons by name — otherwise findByRole races against the
    // paste-URL Install button and clicks the wrong one.
    await screen.findByText(/Catalog \(1\)/);
    const installButtons = screen.getAllByRole("button", { name: /^Install$/i });
    // [0] catalog Install, [1] paste-URL submit.
    await user.click(installButtons[0]);

    // installLocalModel must be invoked with the catalog id, not
    // the URL — UI bug regression guard.
    await waitFor(() =>
      expect(m.install).toHaveBeenCalledWith({ catalog_id: "qwen-tiny" }),
    );
    // SSE subscription kicks off immediately so the operator sees
    // progress on the first chunk, not after a refresh.
    await waitFor(() => expect(m.subscribe).toHaveBeenCalled());

    // Push a progress event through the subscribe callback and
    // confirm the UI shows the % bar.
    await act(async () => {
      publishEvent?.({
        kind: "progress",
        model_id: "qwen-tiny",
        bytes_downloaded: 500_000,
        bytes_total: 1_000_000,
        emitted_at: "2026-05-15T10:00:00Z",
      });
    });
    expect(await screen.findByText("50%")).toBeInTheDocument();
  });

  it("flips the active install row to completed and triggers a refresh", async () => {
    const user = userEvent.setup();
    m.catalog.mockResolvedValue({
      object: "local_models.catalog",
      data: [catalogEntry()],
    });
    let publishEvent: ((ev: LocalModelProgressEvent) => void) | undefined;
    m.subscribe.mockImplementation((_id, onEvent) => {
      publishEvent = onEvent;
      return () => undefined;
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    await screen.findByText(/Catalog \(1\)/);
    const installButtons = screen.getAllByRole("button", { name: /^Install$/i });
    const initialFetches = m.installed.mock.calls.length;
    await user.click(installButtons[0]);
    await waitFor(() => expect(m.subscribe).toHaveBeenCalled());

    await act(async () => {
      publishEvent?.({
        kind: "completed",
        model_id: "qwen-tiny",
        bytes_downloaded: 1_000_000,
        bytes_total: 1_000_000,
        sha256: "abc",
        emitted_at: "2026-05-15T10:00:01Z",
      });
    });

    // The active install row should transition to a terminal
    // state. The "Dismiss" button only renders post-terminal.
    await screen.findByRole("button", { name: /Dismiss/i });
    // And a refresh fires to pull the new /installed row.
    await waitFor(() => {
      expect(m.installed.mock.calls.length).toBeGreaterThan(initialFetches);
    });
  });

  it("cancels an in-flight install when Cancel is clicked", async () => {
    const user = userEvent.setup();
    m.catalog.mockResolvedValue({
      object: "local_models.catalog",
      data: [catalogEntry()],
    });
    m.subscribe.mockImplementation(() => () => undefined);

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    await screen.findByText(/Catalog \(1\)/);
    const installButtons = screen.getAllByRole("button", { name: /^Install$/i });
    await user.click(installButtons[0]);

    const cancelBtn = await screen.findByRole("button", { name: /Cancel/i });
    await user.click(cancelBtn);
    await waitFor(() =>
      expect(m.cancel).toHaveBeenCalledWith("install-123"),
    );
  });

  it("disables catalog Install buttons while another install is running", async () => {
    const user = userEvent.setup();
    m.catalog.mockResolvedValue({
      object: "local_models.catalog",
      data: [
        catalogEntry({ id: "model-a", display_name: "Model A", installed: false }),
        catalogEntry({ id: "model-b", display_name: "Model B", installed: false }),
      ],
    });
    m.subscribe.mockImplementation(() => () => undefined);
    // Two distinct install IDs so the test doesn't accidentally
    // re-trigger on the same one.
    m.install
      .mockResolvedValueOnce({ object: "x", install_id: "i-1", model_id: "model-a" })
      .mockResolvedValueOnce({ object: "x", install_id: "i-2", model_id: "model-b" });

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    // Wait until the catalog section header reflects the loaded
    // entries (avoids racing on the async refresh()).
    await screen.findByText(/Catalog \(2\)/);

    const installButtons = screen.getAllByRole("button", { name: /^Install$/i });
    // 2 catalog Install buttons + 1 paste-URL Install button.
    expect(installButtons).toHaveLength(3);

    await user.click(installButtons[0]);
    await waitFor(() => expect(m.install).toHaveBeenCalledTimes(1));

    // Catalog Install buttons must now be disabled; the paste-URL
    // Install also disables. Operators can't kick off two parallel
    // downloads in v1.
    await waitFor(() => {
      const remaining = screen.queryAllByRole("button", { name: /^Install$/i });
      const enabled = remaining.filter((b) => !b.hasAttribute("disabled"));
      expect(enabled).toHaveLength(0);
    });
  });

  it("opens the confirmation modal before uninstalling a model", async () => {
    const user = userEvent.setup();
    m.installed.mockResolvedValue({
      object: "local_models.installed",
      data: [installedRow()],
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    const uninstallBtn = await screen.findByRole("button", { name: /Uninstall/i });
    await user.click(uninstallBtn);

    // Confirm modal renders with the model name; uninstall hasn't
    // fired yet — single-click safety.
    await screen.findByText(/Uninstall model\?/i);
    expect(m.uninstall).not.toHaveBeenCalled();

    // The ConfirmModal's confirm button is a "Uninstall" with
    // danger styling; click it.
    const confirmBtn = (await screen.findAllByRole("button", { name: /^Uninstall$/i })).pop()!;
    await user.click(confirmBtn);
    await waitFor(() => expect(m.uninstall).toHaveBeenCalledWith("qwen-tiny"));
  });

  it("calls startLocalModel when a non-active row's Start button is clicked", async () => {
    const user = userEvent.setup();
    m.installed.mockResolvedValue({
      object: "local_models.installed",
      data: [installedRow({ id: "llama-1b", display_name: "Llama 1B" })],
    });
    render(<LocalModelsSlideOver onClose={() => undefined} />);
    const startBtn = await screen.findByRole("button", { name: /^Start/i });
    await user.click(startBtn);
    await waitFor(() => expect(m.start).toHaveBeenCalledWith("llama-1b"));
  });

  it("submits the paste-URL flow with the entered URL", async () => {
    const user = userEvent.setup();
    m.subscribe.mockImplementation(() => () => undefined);
    render(<LocalModelsSlideOver onClose={() => undefined} />);
    const urlInput = await screen.findByPlaceholderText(/huggingface\.co/i);

    const fixtureURL =
      "https://huggingface.co/bartowski/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/Qwen2.5-0.5B-Instruct-Q4_K_M.gguf";
    await user.type(urlInput, fixtureURL);
    const submitBtn = within(urlInput.parentElement!).getByRole("button", {
      name: /^Install$/i,
    });
    await user.click(submitBtn);
    await waitFor(() => expect(m.install).toHaveBeenCalledWith({ url: fixtureURL }));
  });

  it("surfaces an inline error when /catalog or /installed throws", async () => {
    m.catalog.mockRejectedValue(new Error("catalog 500"));
    render(<LocalModelsSlideOver onClose={() => undefined} />);
    // InlineError renders the message; assert it lands so a fetch
    // regression doesn't quietly hide the operator's feedback.
    await screen.findByText(/catalog 500/i);
  });

  it("renders the HuggingFace browse section with a search input", async () => {
    render(<LocalModelsSlideOver onClose={() => undefined} />);
    await screen.findByText(/Browse HuggingFace/i);
    expect(screen.getByPlaceholderText(/qwen, llama, gemma/i)).toBeInTheDocument();
    // The search input + token input + search button — search is
    // not auto-fired on render, only on click/Enter.
    expect(m.hfSearch).not.toHaveBeenCalled();
  });

  it("searches HuggingFace and shows result rows with download counts + gated badge", async () => {
    const user = userEvent.setup();
    m.hfSearch.mockResolvedValue({
      object: "local_models.huggingface.search",
      data: [
        {
          id: "bartowski/Qwen2.5-GGUF",
          author: "bartowski",
          downloads: 12345,
          likes: 67,
          tags: ["gguf"],
          gated: false,
        },
        {
          id: "meta-llama/Llama-3.2-Instruct-GGUF",
          author: "meta-llama",
          downloads: 999_999,
          gated: true,
        },
      ],
    });
    render(<LocalModelsSlideOver onClose={() => undefined} />);
    const searchInput = await screen.findByPlaceholderText(/qwen, llama, gemma/i);
    await user.type(searchInput, "qwen");
    const searchBtn = screen.getByRole("button", { name: /^Search$/i });
    await user.click(searchBtn);

    await waitFor(() =>
      expect(m.hfSearch).toHaveBeenCalledWith("qwen", { limit: 20, token: "" }),
    );
    await screen.findByText("bartowski/Qwen2.5-GGUF");
    await screen.findByText("meta-llama/Llama-3.2-Instruct-GGUF");
    // The gated badge marks repos requiring an HF token.
    expect(screen.getByText(/^gated$/i)).toBeInTheDocument();
    // Download count formatting kicks in for >1000.
    expect(screen.getByText(/↓ 12\.3k/)).toBeInTheDocument();
  });

  it("expands a repo and shows .gguf files with size + Install button", async () => {
    const user = userEvent.setup();
    m.hfSearch.mockResolvedValue({
      object: "local_models.huggingface.search",
      data: [
        { id: "bartowski/Qwen-GGUF", author: "bartowski", downloads: 1000, gated: false },
      ],
    });
    m.hfFiles.mockResolvedValue({
      object: "local_models.huggingface.files",
      data: [
        {
          path: "Qwen-Q4_K_M.gguf",
          size: 4_000_000_000,
          sha256: "deadbeef",
          download_url: "https://huggingface.co/bartowski/Qwen-GGUF/resolve/main/Qwen-Q4_K_M.gguf",
        },
      ],
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    const searchBtn = await screen.findByRole("button", { name: /^Search$/i });
    await user.click(searchBtn);
    await screen.findByText("bartowski/Qwen-GGUF");

    const showFiles = screen.getByRole("button", { name: /Show files/i });
    await user.click(showFiles);

    await waitFor(() =>
      expect(m.hfFiles).toHaveBeenCalledWith("bartowski/Qwen-GGUF", { token: "" }),
    );
    await screen.findByText("Qwen-Q4_K_M.gguf");
    // Size formatted as GB.
    expect(screen.getByText(/3\.7. GB/)).toBeInTheDocument();
  });

  it("installs the selected HF file via the URL+sha256 path", async () => {
    const user = userEvent.setup();
    m.hfSearch.mockResolvedValue({
      object: "local_models.huggingface.search",
      data: [{ id: "owner/repo", author: "owner", downloads: 1, gated: false }],
    });
    const fileURL = "https://huggingface.co/owner/repo/resolve/main/model.gguf";
    m.hfFiles.mockResolvedValue({
      object: "local_models.huggingface.files",
      data: [
        { path: "model.gguf", size: 1_000_000, sha256: "abc123", download_url: fileURL },
      ],
    });
    m.subscribe.mockImplementation(() => () => undefined);

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: /^Search$/i }));
    await screen.findByText("owner/repo");
    await user.click(screen.getByRole("button", { name: /Show files/i }));
    await screen.findByText("model.gguf");

    // The file row's Install button — disambiguate from the
    // paste-URL Install at the bottom.
    const installButtons = screen.getAllByRole("button", { name: /^Install$/i });
    // [0] file Install (catalog is empty in this test), [1] paste-URL.
    await user.click(installButtons[0]);

    await waitFor(() =>
      expect(m.install).toHaveBeenCalledWith({
        url: fileURL,
        sha256: "abc123",
      }),
    );
  });

  it("marks already-installed HF files with an 'installed' badge instead of Install", async () => {
    const user = userEvent.setup();
    const fileURL = "https://huggingface.co/owner/repo/resolve/main/model.gguf";
    m.installed.mockResolvedValue({
      object: "local_models.installed",
      data: [installedRow({ id: "owner-repo", source_url: fileURL })],
    });
    m.hfSearch.mockResolvedValue({
      object: "local_models.huggingface.search",
      data: [{ id: "owner/repo", author: "owner", downloads: 1, gated: false }],
    });
    m.hfFiles.mockResolvedValue({
      object: "local_models.huggingface.files",
      data: [
        { path: "model.gguf", size: 1_000_000, sha256: "abc123", download_url: fileURL },
      ],
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: /^Search$/i }));
    await screen.findByText("owner/repo");
    await user.click(screen.getByRole("button", { name: /Show files/i }));

    await screen.findByText("model.gguf");
    // The file row should show the "installed" badge instead of an
    // Install button — confirm the badge is present and no Install
    // button exists inside the file row.
    expect(screen.getByText(/^installed$/i)).toBeInTheDocument();
    // Only the paste-URL Install button should remain.
    expect(screen.getAllByRole("button", { name: /^Install$/i })).toHaveLength(1);
  });

  it("surfaces InlineError when HF search fails", async () => {
    const user = userEvent.setup();
    m.hfSearch.mockRejectedValue(new Error("HTTP 502: upstream"));
    render(<LocalModelsSlideOver onClose={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: /^Search$/i }));
    await screen.findByText(/HTTP 502/);
  });

  it("forwards the HF token from the shared input on both search and file list", async () => {
    const user = userEvent.setup();
    m.hfSearch.mockResolvedValue({
      object: "local_models.huggingface.search",
      data: [{ id: "owner/gated", author: "owner", downloads: 1, gated: true }],
    });
    m.hfFiles.mockResolvedValue({
      object: "local_models.huggingface.files",
      data: [],
    });

    render(<LocalModelsSlideOver onClose={() => undefined} />);
    // The slide-over has two HF-token inputs (one in the browse
    // section, one in the paste-URL section). They share state so
    // typing in either fills both. Grab the browse-section one by
    // its aria-label.
    const tokenInput = await screen.findByLabelText(/HuggingFace access token for search/i);
    await user.type(tokenInput, "hf-token-xyz");

    await user.click(screen.getByRole("button", { name: /^Search$/i }));
    await waitFor(() =>
      expect(m.hfSearch).toHaveBeenCalledWith("", { limit: 20, token: "hf-token-xyz" }),
    );
    await screen.findByText("owner/gated");

    await user.click(screen.getByRole("button", { name: /Show files/i }));
    await waitFor(() =>
      expect(m.hfFiles).toHaveBeenCalledWith("owner/gated", { token: "hf-token-xyz" }),
    );
  });
});
