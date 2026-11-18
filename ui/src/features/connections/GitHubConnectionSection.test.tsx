import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { getGitHubConnection, inspectGitHubPullRequest } from "../../lib/api";
import { GitHubConnectionSection } from "./GitHubConnectionSection";

vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  getGitHubConnection: vi.fn(),
  inspectGitHubPullRequest: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(getGitHubConnection).mockReset();
  vi.mocked(inspectGitHubPullRequest).mockReset();
  vi.mocked(getGitHubConnection).mockResolvedValue({
    object: "github_connection",
    data: {
      configured: true,
      mode: "read_only",
      repositories: ["owner/repo"],
      setup_hint: "Configure the runtime.",
    },
  });
});

it("loads passive settings without inspecting a pull request", async () => {
  render(<GitHubConnectionSection />);
  expect(await screen.findByLabelText("Pull request URL")).toBeVisible();
  expect(inspectGitHubPullRequest).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "Inspect pull request" })).toBeDisabled();
  expect(screen.getByText(/cannot change code, rerun checks, or merge/)).toBeVisible();
});

it("explains runtime setup without requesting a private key in the browser", async () => {
  vi.mocked(getGitHubConnection).mockResolvedValue({
    object: "github_connection",
    data: {
      configured: false,
      mode: "read_only",
      repositories: [],
      setup_hint: "Configure the runtime.",
    },
  });
  render(<GitHubConnectionSection />);
  expect(await screen.findByText(/Keep the App private key on the runtime host/)).toBeVisible();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "GitHub App setup guide" })).toHaveAttribute(
    "href",
    "https://github.com/hecatehq/hecate/blob/master/docs/operator/github.md",
  );
});

it("inspects only on request and distinguishes absent checks from success", async () => {
  vi.mocked(inspectGitHubPullRequest).mockResolvedValue({
    object: "github_pull_request",
    data: {
      url: "https://github.com/owner/repo/pull/7",
      repository: "owner/repo",
      number: 7,
      head_sha: "a".repeat(40),
      base_sha: "b".repeat(40),
      state: "open",
      draft: false,
      merged: false,
      complete: true,
      checks_state: "not_reported",
      checks: [],
      observed_at: "2026-10-27T12:00:00Z",
    },
  });
  const user = userEvent.setup();
  render(<GitHubConnectionSection />);
  await user.type(
    await screen.findByLabelText("Pull request URL"),
    "https://github.com/owner/repo/pull/7",
  );
  await user.click(screen.getByRole("button", { name: "Inspect pull request" }));
  expect(await screen.findByText("No checks have been reported")).toBeVisible();
  expect(inspectGitHubPullRequest).toHaveBeenCalledWith(
    "https://github.com/owner/repo/pull/7",
    expect.any(AbortSignal),
  );
  expect(screen.getByText(/not proof that required checks passed/)).toBeVisible();
  await user.clear(screen.getByLabelText("Pull request URL"));
  expect(screen.queryByText("No checks have been reported")).not.toBeInTheDocument();
});

it("aborts an inspection on unmount and ignores its late result", async () => {
  vi.mocked(inspectGitHubPullRequest).mockReturnValue(new Promise(() => {}));
  const user = userEvent.setup();
  const { unmount } = render(<GitHubConnectionSection />);
  await user.type(
    await screen.findByLabelText("Pull request URL"),
    "https://github.com/owner/repo/pull/7",
  );
  await user.click(screen.getByRole("button", { name: "Inspect pull request" }));
  await waitFor(() => expect(inspectGitHubPullRequest).toHaveBeenCalledOnce());
  const signal = vi.mocked(inspectGitHubPullRequest).mock.calls[0][1];
  unmount();
  expect(signal?.aborted).toBe(true);
});

it("keeps failures visible without retrying automatically", async () => {
  vi.mocked(inspectGitHubPullRequest).mockRejectedValue(new Error("Repository is not enabled."));
  const user = userEvent.setup();
  render(<GitHubConnectionSection />);
  await user.type(
    await screen.findByLabelText("Pull request URL"),
    "https://github.com/owner/repo/pull/7",
  );
  await user.click(screen.getByRole("button", { name: "Inspect pull request" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Repository is not enabled.");
  expect(inspectGitHubPullRequest).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: "Inspect pull request" })).toBeEnabled();
});
