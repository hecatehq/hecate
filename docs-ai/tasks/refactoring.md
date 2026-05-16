# Refactoring

Reshape code without changing behavior.

## Behavior is invariant

External observable behavior is preserved during a refactor. Tests should pass before and after the change with no rewrites. If a test needs rewriting to keep passing, the refactor changed behavior — that's no longer a refactor, it's a behavior change masquerading as one.

## Separate cleanup from behavior changes

One commit per axis. A behavior change inside a refactor commit is a regression waiting to happen — the diff makes the behavior change invisible to review. Land the refactor first; build the new behavior on top.

## Reduce risk by sequencing

For non-trivial refactors:

1. Add the new shape next to the old one.
2. Migrate callers one at a time, with tests at each step.
3. Remove the old shape only when no callers remain.

This makes every intermediate state shippable and every step independently revertible.

## "Preserve existing behavior" comments need a tracker or a fix

Comments like "preserves the existing layout", "kept on the legacy pattern", or "for backwards-compatibility" mark code as load-bearing debt — without a `TODO:` / `FIXME:` pointer, future readers can't tell intentional debt from accepted style, and the legacy state becomes invisible. Two options when you find yourself writing one:

1. **Fix it in the same PR.** Migrate callers, delete the shim, drop the comment.
2. **Tag it.** `TODO:` for a planned migration (with the condition that unblocks it — design sign-off, follow-up PR, dependency landing). `FIXME:` for a known bug whose proper fix is too large to bundle (with a pointer to the load-bearing change that retires it).

A bare "preserves existing X" comment without one of the two is the worst of both worlds: the legacy is tolerated but invisible. Reviewer push-back: if the comment doesn't tell future-you when the legacy goes away, it doesn't belong yet.

## Verify no regressions

Run the relevant verification ladder ([`../core/verification.md`](../core/verification.md)) **before** and **after** the refactor. Diff the results. For backend: race suite. For UI: typecheck plus test. The "passes before, passes after" claim is only as good as the evidence.

## Cross-ring boundary refactors

The api↔providers parallel-struct rule is not a target for unification. The duplication is the contract — see [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md). When mirroring fields across the boundary, mirror; don't share. If a refactor proposal includes "let's unify these," that's the point at which to stop and plan, not push through.

Likewise the storage tier rule: refactors that compress memory + sqlite into a single backend lose the fast-test + durable-local story. Don't.
