# Agent Event Protocol Fixtures

These fixtures are contract examples for the draft event protocol in
[`docs/rfcs/event-protocol-v1.md`](../../../rfcs/event-protocol-v1.md).

They are not runtime output yet. Their job is to make the RFC mechanically
checkable before implementation starts.

## Layout

| Path          | Meaning                                                                                           |
| ------------- | ------------------------------------------------------------------------------------------------- |
| `core/*.json` | Candidate-core v1 event examples that frontend, CLI, and ACP prototypes may use as golden inputs. |

Experimental event ideas from
[`docs/rfcs/event-protocol-extensions.md`](../../../rfcs/event-protocol-extensions.md)
and artifact-dependent events from
[`docs/rfcs/artifact-storage-v1.md`](../../../rfcs/artifact-storage-v1.md) intentionally
do not live in `core/`.

## Validation

The Go contract test in `internal/eventprotocol` validates:

- every fixture is valid JSON;
- every event has the required envelope fields;
- `schema_version` is `"1"`;
- `occurred_at` is RFC3339/RFC3339Nano parseable;
- `data` is always a JSON object;
- event types belong to the candidate-core set;
- removed legacy event names fail fast instead of lingering in examples;
- the normalized run lifecycle fixture set covers queued, started, finished,
  failed, cancelled, resumed-from-event, and checkpoint-saved paths;
- core run lifecycle payloads include their required fields;
- sequences are strictly increasing per `run_id`.

The minimal envelope schema lives at
[`docs/schemas/events/v1/envelope.schema.json`](../../../schemas/events/v1/envelope.schema.json).
It intentionally covers only the shared envelope; payload-specific schemas
should be added as runtime emitters are implemented.
