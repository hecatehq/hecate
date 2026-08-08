# Code-Intelligence Model Dogfood

This opt-in scorecard checks whether a real tool-capable model discovers and
uses Hecate's native code-intelligence path through the same Project assignment
flow an operator uses. It exercises Go and TypeScript semantic inspection,
Python and Rust structural search, restricted-policy behavior, and a deliberately
unavailable Go language server. Existing bounded text search remains the
expected fallback when a more precise route is unavailable or forbidden.

The run uses isolated Hecate-managed workspaces below a temporary gateway data
directory. It never asks the model to edit files or run commands. Network tools
are disabled, while shell, terminal, broad Git, and write tools remain
approval-gated even in the write-capable preset needed for semantic LSP on some
hosts. The harness rejects any such approval and scores the proposal as a
failure; the model's prompt is not treated as a security boundary. The report
records only whether a workspace changed, not changed paths or patch content.
The test still sends fixed code-navigation questions, tool results, and the
managed workspace's absolute path from the agent-loop prelude to the configured
model provider, so use only a provider and repository you are allowed to test.
The sanitized scorecard itself does not retain that path.

## Prerequisites

Run the scorecard from the Hecate repository version you want to evaluate. The
harness requires a clean, committed worktree so the managed Project workspaces
contain exactly the source revision recorded in the report. It also compares a
bounded digest of the source checkout's ignored path set before and after the
run; ignored paths themselves are not written to the scorecard.

1. Install Go and `just`, then inspect the optional code-intelligence providers:

   ```bash
   just doctor
   ```

2. For the full matrix, install trusted global copies of:

   - `gopls` for Go semantic navigation;
   - TypeScript 7 or newer's `tsc` for native TypeScript LSP support; and
   - `ast-grep` for structural search.

   An unavailable provider does not prevent the run. Its affected scenario is
   reported as inconclusive or as a fallback observation instead of pretending
   the preferred capability was tested.

3. Configure a tool-capable model using the normal provider environment. The
   provider name selects the matching `PROVIDER_<NAME>_*` variables. For
   example:

   ```bash
   export HECATE_CODEINTEL_DOGFOOD_PROVIDER=openai
   export HECATE_CODEINTEL_DOGFOOD_MODEL='<tool-capable-model-id>'
   export PROVIDER_OPENAI_API_KEY='<provider-secret>'
   ```

   Custom compatible endpoints may also use the provider's normal
   `PROVIDER_<NAME>_BASE_URL` and `PROVIDER_<NAME>_KIND` variables. The harness
   supplies its temporary gateway with the matching preconfigured-provider and
   model-catalog entries. Never put provider secrets in command arguments or
   commit them to the repository.

## Run the scorecard

```bash
just test-codeintel-dogfood
```

The recipe intentionally clears `E2E_HECATE_BIN`, builds Hecate from the current
source, enables the live test with `HECATE_CODEINTEL_AGENT_DOGFOOD=1`, and runs
only `TestCodeIntelligenceModelDogfood`. It may consume provider quota. Normal
test and e2e runs compile the harness but skip real model calls unless that
explicit opt-in flag is set.

Optional controls are environment variables:

| Variable                                   | Effect                                                                                        |
| ------------------------------------------ | --------------------------------------------------------------------------------------------- |
| `HECATE_CODEINTEL_DOGFOOD_TIMEOUT_SECONDS` | Sets the terminal wait allowed for each model-backed Run, from 1 to 300 seconds.              |
| `HECATE_CODEINTEL_DOGFOOD_REPORT_DIR`      | Writes both reports below this directory instead of the default timestamped directory.        |
| `HECATE_CODEINTEL_DOGFOOD_REPEATS`         | Repeats each scenario to expose model variability; the default is `1` and the maximum is `5`. |
| `HECATE_CODEINTEL_DOGFOOD_STRICT=1`        | Fails the Go test when any model-behavior scenario is scored `fail`.                          |

Harness and API invariant failures always fail the test. Without strict mode,
stochastic model misses remain visible in the scorecard but do not make this
expensive, provider-dependent check a flaky CI gate. Expected `inconclusive`
results, such as a host posture that cannot represent one scenario, do not fail
strict mode. Use strict mode only for a provider/model combination whose
behavior you intentionally want to gate.

## Read the result

By default, each run writes:

```text
.dogfood/code-intelligence/<UTC timestamp>-<unique suffix>/scorecard.json
.dogfood/code-intelligence/<UTC timestamp>-<unique suffix>/scorecard.md
```

`.dogfood/` is ignored by Git. On POSIX hosts, newly created report directories
use mode `0700` and report files use mode `0600`. On Windows, filesystem ACLs
and inherited permissions apply instead of POSIX mode bits. JSON is the
machine-readable record; Markdown is a projection of the same allowlisted
fields for human review. Each scenario is classified as `pass`, `fail`,
`inconclusive`, or `skipped`. Review the individual reason codes alongside the
aggregate rate: a useful final answer does not erase generic browsing before
capability discovery, an avoidable failed semantic call, a forbidden route, or
an unexpected workspace change.

Capability discovery reports providers as `installed_unverified` until a real
query starts them. If that query exposes a provider startup or protocol failure
and the model then uses a successful bounded fallback, the scenario is
`inconclusive` rather than charging the infrastructure failure to tool
selection. A missing or unsuccessful fallback still fails the scenario.

The report allowlist is deliberately narrow. It may contain:

- schema and source revision metadata, operating system, architecture, sandbox
  wrapper, and source-dirty state;
- the configured and verified model route, plus code-intelligence provider
  names and bounded capability version and availability status;
- opaque Project task, Run, and trace identifiers;
- fixed scenario, language, route, posture, verdict, and reason-code values;
- tool names, code-intelligence operation names, counts, durations, cost in
  micro-USD, and workspace-change counts.

It must not contain prompts, final answers, tool input or output, search
queries or patterns, workspace paths, changed filenames, patches, provider
secrets, credential-bearing or non-allowlisted environment values, raw stderr,
or raw errors. The harness may inspect some of that runtime evidence in memory
to derive booleans, but the report writer does not accept those fields.

Treat even the sanitized scorecard as local evidence by default. Before sharing
or checking a curated result into `docs/design/proposals/evidence/`, review both
files, confirm the allowlist still matches the intended audience, remove opaque
runtime identifiers if they are not needed, and state the exact source revision,
provider/model, repeat count, host posture, and limitations. Never publish the
temporary gateway data directory or raw Run artifacts.

## Scope and interpretation

This is a model-level product scorecard, not the provider lifecycle test suite.
In particular, process cleanup is reported as `not_measured`: a global process
scan would confuse Hecate-started servers with an editor's existing language
servers and still would not prove ownership. Deterministic code-intelligence
unit and race tests remain the evidence for cancellation and process-tree
cleanup.

Use repeated scorecards to decide whether the current guidance reliably causes
models to discover capabilities first, select semantic or structural inspection
when available, and fall back cleanly. Only then use the evidence to prioritize
an operator-facing readiness view, per-Run language-server pooling, additional
languages, direct Tree-sitter embedding, or write-capable refactoring. A single
passing run is not a graduation result.
