export type GitHubConnectionResponse = {
  object: "github_connection";
  data: { configured: boolean; mode: "read_only"; repositories: string[]; setup_hint: string };
};

export type GitHubPullRequestResponse = {
  object: "github_pull_request";
  data: {
    url: string;
    repository: string;
    number: number;
    head_sha: string;
    base_sha: string;
    state: string;
    draft: boolean;
    merged: boolean;
    complete: boolean;
    checks_state: "success" | "failure" | "pending" | "unknown" | "incomplete" | "not_reported";
    checks: { name: string; source: "check" | "status"; state: string }[];
    observed_at: string;
  };
};
