import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { getGitHubConnection, inspectGitHubPullRequest } from "../../lib/api";
import type { GitHubConnectionResponse, GitHubPullRequestResponse } from "../../types/github";

const labels = {
  success: "Reported checks passed",
  failure: "Checks need attention",
  pending: "Checks are still running",
  unknown: "Check results need inspection",
  incomplete: "Only part of the check results was returned",
  not_reported: "No checks have been reported",
};

export function GitHubConnectionSection() {
  const id = useId();
  const [connection, setConnection] = useState<GitHubConnectionResponse["data"] | null>(null);
  const [url, setURL] = useState("");
  const [result, setResult] = useState<GitHubPullRequestResponse["data"] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const inspection = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void getGitHubConnection(controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) setConnection(response.data);
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setError("GitHub connection settings could not be loaded. Reopen Connections to retry.");
      });
    return () => {
      controller.abort();
      inspection.current?.abort();
    };
  }, []);

  async function inspect(event: FormEvent) {
    event.preventDefault();
    if (inspection.current || !connection?.configured || !url.trim()) return;
    const controller = new AbortController();
    inspection.current = controller;
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const response = await inspectGitHubPullRequest(url.trim(), controller.signal);
      if (!controller.signal.aborted) setResult(response.data);
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(cause instanceof Error ? cause.message : "GitHub inspection failed. Try again.");
    } finally {
      if (inspection.current === controller) inspection.current = null;
      if (!controller.signal.aborted) setBusy(false);
    }
  }

  return (
    <section
      className="card"
      aria-labelledby={`${id}-heading`}
      style={{
        marginBottom: 24,
        padding: "14px 16px",
        fontSize: 12,
        lineHeight: 1.5,
        overflowWrap: "anywhere",
      }}
    >
      <h2
        id={`${id}-heading`}
        style={{ fontSize: 13, fontWeight: 600, color: "var(--t0)", margin: "0 0 4px" }}
      >
        GitHub
      </h2>
      <p>
        Inspect pull requests through a repository-scoped GitHub App. This connection cannot change
        code, rerun checks, or merge.
      </p>
      {!connection && !error && <p role="status">Loading connection settings…</p>}
      {connection && !connection.configured && (
        <>
          <p>{connection.setup_hint} Keep the App private key on the runtime host, not in chat.</p>
          <p>
            <a
              href="https://github.com/hecatehq/hecate/blob/master/docs/operator/github.md"
              target="_blank"
              rel="noopener noreferrer"
            >
              GitHub App setup guide
            </a>
          </p>
        </>
      )}
      {connection?.configured && (
        <>
          <p>
            Configured for {connection.repositories.join(", ")}. Access is verified only when you
            inspect a pull request.
          </p>
          <form onSubmit={(event) => void inspect(event)} aria-busy={busy}>
            <label htmlFor={`${id}-url`}>Pull request URL</label>
            <input
              id={`${id}-url`}
              className="input"
              style={{ width: "100%", marginTop: 4 }}
              type="url"
              value={url}
              required
              disabled={busy}
              placeholder="https://github.com/owner/repository/pull/123"
              aria-describedby={`${id}-help${error ? ` ${id}-error` : ""}`}
              onChange={(event) => {
                setURL(event.target.value);
                setResult(null);
                setError("");
              }}
            />
            <p id={`${id}-help`}>
              Reads the selected pull request and its current checks from GitHub. Results are not
              saved.
            </p>
            <button className="btn btn-primary btn-sm" type="submit" disabled={busy || !url.trim()}>
              {busy ? "Inspecting…" : "Inspect pull request"}
            </button>
          </form>
        </>
      )}
      {error && (
        <p id={`${id}-error`} role="alert">
          {error}
        </p>
      )}
      {result && (
        <div>
          <p role="status">{labels[result.checks_state] ?? labels.unknown}</p>
          <p>
            {result.repository} #{result.number} ·{" "}
            {result.merged ? "Merged" : result.draft ? "Draft" : result.state} · commit{" "}
            {result.head_sha.slice(0, 12)}
          </p>
          <p>
            This is a snapshot of reported checks, not proof that required checks passed or the pull
            request is mergeable.
          </p>
          <ul aria-label="Pull request checks">
            {result.checks.map((check, index) => (
              <li key={`${check.source}-${index}`}>
                {check.name}: {check.state}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
