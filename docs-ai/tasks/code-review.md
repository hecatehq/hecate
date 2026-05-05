# Code review

Structured rubric for reviewing changes — your own or another agent's.

## Rubric

For each pass, ask:

1. **Correctness.** Does the change do what it claims? Are edge cases handled? Are errors classified correctly (`IsClientError`, `IsBudgetExceeded`, `IsRateLimited`, `IsDenied`, or a new class if needed)?
2. **Maintainability.** Is the change readable? Do comments explain *why*, not *what*? Is naming consistent with the surrounding code? Will the next reader understand the trade-off without `git blame`?
3. **Architecture fit.** Does it respect the rings (`pkg/types/` → `internal/api/` → `internal/providers/`, with imports inward only)? Does it mirror the storage tier rule (memory + sqlite)? Does it duplicate what should be shared, or share what should be duplicated (the api↔providers parallel-struct rule)?
4. **Test coverage.** Does it test the seam? The error path? The cross-boundary wire shape? For UI: data-to-UI transformation and conditional rendering of critical states? Streaming wire shape? Storage scoping?
5. **Security risk.** Local-only assumptions; loopback/same-origin behavior; sandbox boundary; SSRF guards on `http_request`; secrets in env vs in code.
6. **Operational risk.** Env-var changes (synced to `.env.example` and `docs/<feature>.md`?); schema migrations across storage tiers; retention worker impact; OTel surface; log volume.
7. **Follow-ups.** What's left? What's known to be incomplete? What needs a separate change?

## Output format

Structure the review so the recipient can act on it without re-reading the diff:

1. **Findings.** Concrete observations with `file:line` references. One bullet per finding.
2. **Open questions.** Things the reviewer can't decide without product or operator input.
3. **Suggested fixes.** Specific edits, not vague directions. "Replace `float64` with `int64` micro-USD on line 42" beats "consider integer math".
4. **Verification notes.** What was tested. What wasn't, and why. What manual smoke would catch what's missing.
