# Debugging

Debug deliberately. Blind edits convert a known problem into an unknown one.

## Reproduce first

A bug you can't reproduce is a bug you can't fix. Pin the conditions before changing code:

- Provider and model.
- Caller surface (provider-compatible `/v1/...`, Hecate-native `/hecate/v1/...`, native app, or ACP bridge).
- Request shape (headers, body, streaming or not).
- Environment (env vars that route the gateway differently).
- OS / runtime (especially for sandbox or subprocess issues).

If reproduction is flaky, that's a clue — usually a race, a startup-order issue, or state left over from a prior run.

## Narrow scope

Isolate the minimal failing input. Bisect the request path — `internal/api/` → governor → router → `internal/providers/` → upstream. Find the layer where the symptom first appears. Stop there.

For UI bugs: bisect by component tree. The `setup()` helper and React Testing Library's queries make this easy if the test is written right.

## Form hypotheses, don't guess

State what you think is happening, what evidence supports it, and what test would falsify it. Run the test. If it falsifies the hypothesis, form a new one. Do not jump straight to a fix.

"Try this and see" is anti-debugging. Each blind edit risks making the symptom go away without removing the cause — and now you have two bugs.

## Confirm the fix

A fix isn't done until:

- The original repro now passes.
- A regression test pins the behavior so this exact case doesn't return. Add a new test, don't just modify an existing one.
- Adjacent code paths weren't broken — run the relevant verification ladder ([`../core/verification.md`](../core/verification.md)).

## Hecate-specific debug surfaces

- **`X-Trace-Id`** header on every response. Search OTel by trace ID to see exactly what the gateway decided and why.
- **`/hecate/v1/traces`** and **`/hecate/v1/events`** for replay. The run-event log is append-only; the SSE stream replays from `after_sequence`.
- **Run state** — runs in `awaiting_approval` are blocked until resolved; check the approval lifecycle if a run is "stuck".
- **Synthetic local providers** — use `PROVIDER_FAKE_KIND=local` for e2e scenarios that should not require a real cloud provider.
- **Capability cache** — provider tests that don't seed `cachedCaps` will panic when discovery hits the test transport. See [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md) for the seeding snippet.
