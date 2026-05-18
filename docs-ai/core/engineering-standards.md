# Engineering standards

Project-wide coding and style standards. Applies to backend Go and the React UI. Per-area specifics live in [`../skills/backend/SKILL.md`](../skills/backend/SKILL.md), [`../skills/ui/SKILL.md`](../skills/ui/SKILL.md), and [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md).

## Comments

Comments explain _why_, not _what_. Dense and contextual. State the trade-off in the code so the next reader does not have to `git blame` for context. Comments that paraphrase identifiers (`// increment counter`) age into noise — drop them. When the _why_ is obvious from the code, no comment is needed.

## Field shape rules (Go)

For optional config or request fields, the choice between `T` and `*T`:

- **Pointer when zero is a valid distinct value.** `Seed *int` (0 is a real seed), `ParallelToolCalls *bool` (false means "disable"), `Strict *bool` (default depends on provider).
- **Value with `omitempty`** when zero == API default. `PresencePenalty float64` (0.0 = no penalty), `Logprobs bool` (false = default off).
- **`json.RawMessage`** for forward-compat passthrough fields (`response_format`, `logit_bias`, `stream_options`, `tool_choice`). Decode lazily where the gateway needs to inspect; pass through verbatim otherwise.

State the choice in a comment so the next reader understands the constraint.

## The api↔providers parallel-struct rule

`internal/api/OpenAIChatMessage` (capital `O`) and `internal/providers/openAIChatMessage` (lowercase `o`) duplicate the JSON shape on purpose. Same JSON shape, two packages, intentional. Keeps `internal/providers/` free of `internal/api/` imports and lets the wire shapes evolve independently.

When a field is added on one side, mirror it on the other. Do not unify. The duplication is the contract. Polymorphic content types (`UnmarshalJSON` / `MarshalJSON` for string-or-array-or-null) are duplicated for the same reason — sharing them would re-introduce the import.

Full reasoning, file inventory, and the mirroring chain: [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md).

## Test naming

`TestPackage_Behavior`. Table-driven where the variant set is obvious. Pin behavior, not implementation — a refactor that does not change observable behavior should leave tests untouched.

## Type-name mirroring across the api↔UI boundary

`ui/src/types/runtime.ts` mirrors `pkg/types/` and `internal/api/` exactly. When the Go side adds a field, mirror it in the UI in the same change. Otherwise the SSE consumers and detail panels start dropping data silently.

## OpenTelemetry surface

OTel is part of the runtime contract, not a debugging afterthought. New request, provider, task, sandbox, MCP, or agent-adapter paths should add spans and metrics through `internal/telemetry` in the same change as the behavior. Metric dimensions must stay bounded: add closed-set normalizers for enum-like values, sanitize free-form labels, and keep raw diagnostics in spans, logs, persisted events, or raw-output fields instead of metric attributes.

## No emojis

No emojis in code or copy unless the operator explicitly requests them. This applies to source, tests, generated docs, log lines, and UI strings.

## Match existing patterns

Reuse the existing primitives, design tokens, and layout patterns. Do not invent new styles or wrapper abstractions when the current ones cover the case. New style islands bloat review and obscure the pattern the rest of the codebase follows.

UI specifically: design tokens are the source of truth — `var(--bg1)`, `var(--t0)`, `var(--accent)`, `var(--radius)`, `var(--font-mono)`, etc. Do not hard-code colors or radii. Reuse primitives from `ui/src/features/shared/` — `ProviderPicker`, `ModelPicker`, and the other visual atoms re-export through the `ui.tsx` barrel; `useFloatingDropdownStyle` is imported directly from its own file (it's a hook, not exported through the barrel). New code should prefer per-component imports for tighter tree-shaking.

## Anti-patterns

- Magic auto-discovery that masks misconfiguration.
- Silent fallbacks that hide upstream failure.
- ORM-style abstractions over what is a thin SQL layer.
- Generic frameworks where direct code suffices.
- Unifying the api↔providers parallel structs.
- `make([]T, 0, len(x)+N)` with arithmetic on the cap — flagged by CodeQL CWE-190 as integer-overflow risk. Use plain `len(x)` and let `append` grow once if needed.
- Mermaid sequence-diagram participants named `loop` — `loop ... end` is reserved syntax. Use `Agent` or similar.
- Drive-by formatting and renaming inside an unrelated change.
