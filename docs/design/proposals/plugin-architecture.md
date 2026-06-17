# Plugin Architecture

> **Status:** proposal.
> **Current source of truth:** [MCP integration](../../runtime/mcp.md),
> [External Agent integrations](../accepted/external-agent-integrations.md),
> [Workspace instructions, skills, and profiles](workspace-instructions-skills-and-profiles.md),
> and [Projects](../accepted/projects.md) for today's shipped seams.
> **Next action:** agree the plugin vocabulary and manifest/capability model,
> then add a registry-only implementation before installing or executing plugin
> code.

Hecate needs a general extension model that can cover GitHub, Linear, Slack,
Jira, custom local tools, UI affordances, MCP servers, reusable skills, and
future workflow packages without turning every integration into a bespoke
runtime path.

The product word should be **plugin**. A plugin can contain one or more
capabilities. A **connector** is a plugin capability that links Hecate to an
external service such as GitHub or Linear.

## External Alignment

Facts, accessed 2026-06-17:

- MCP describes itself as an open standard for connecting AI applications to
  external systems, including data sources, tools, prompts, and workflows.
  Source: <https://modelcontextprotocol.io/docs/getting-started/intro>.
- The MCP specification defines hosts, clients, and servers; servers can expose
  resources, prompts, and tools; clients can expose roots, sampling, and
  elicitation. Source:
  <https://modelcontextprotocol.io/specification/2025-06-18>.
- The MCP specification's trust and safety section says users must explicitly
  consent to and understand data access and operations, retain control over
  what is shared and executed, and treat tool descriptions from untrusted
  servers with appropriate caution. Source:
  <https://modelcontextprotocol.io/specification/2025-06-18>.
- Hecate already uses MCP in both directions: as a local MCP server exposing
  Hecate surfaces, and as an MCP client that can attach external MCP tools to
  `agent_loop` tasks. Source: [MCP integration](../../runtime/mcp.md).
- Hecate already models External Agents separately from providers and MCP
  servers. External Agents are supervised local runtimes, not plugins or model
  providers. Source:
  [External Agent integrations](../accepted/external-agent-integrations.md).
- Hecate already tracks portable `SKILL.md` packages as reusable procedures and
  distinguishes them from workspace instructions, profiles, runbooks, memory,
  and project context sources. Source:
  [Workspace instructions, skills, and profiles](workspace-instructions-skills-and-profiles.md).

The conclusion: Hecate should use MCP as the broadest compatibility layer for
tool/resource/prompt interop, but Hecate's plugin contract should not be the
private plugin API of any single host. Claude Code, Codex, Cursor, and other
hosts can be compatibility sources; Hecate should adapt their packages into
Hecate plugin metadata instead of making their formats the canonical ABI.

## Problem

"Plugin" currently means several different things in the broader agent
ecosystem:

- An MCP server exposing tools/resources/prompts.
- A host-specific bundle for Claude Code, Codex, Cursor, or another agent.
- A reusable `SKILL.md` package.
- A local hook or script.
- A third-party service connector such as GitHub or Linear.
- A UI extension or mini app.
- An External Agent adapter.

If Hecate treats all of these as interchangeable, the operator loses the most
important thing Hecate provides: clear authority, provenance, approvals, and
runtime boundaries.

## Goals

- Define one Hecate plugin vocabulary that can grow beyond Projects.
- Let a plugin expose multiple capabilities: MCP servers, skills, slash
  commands, connectors, evidence providers, project mappers, UI surfaces, and
  workflow templates.
- Reuse MCP where it is strong: tools, resources, prompts, and external
  ecosystem compatibility.
- Allow compatibility import/adaptation from Claude/Codex-style packages
  without making those host-specific APIs canonical.
- Keep plugin actions inside Hecate's existing approval, sandbox, proposal,
  and audit model.
- Support GitHub and Linear as examples of general plugins, not just Project
  integrations.

## Non-goals

- Do not build a broad plugin marketplace in the alpha line.
- Do not run arbitrary plugin hooks, post-install scripts, or browser/WASM code
  as part of the first registry slice.
- Do not let plugin tools mutate durable Hecate project state directly.
- Do not make External Agent adapters into plugins. They may be distributed by
  plugins later, but the runtime concept remains separate.
- Do not make MCP the only Hecate plugin format. MCP is the interop substrate;
  Hecate still needs durable metadata, permissions, UI placement, auth state,
  and project/evidence semantics.

## Vocabulary

| Concept                | Owns                                                                                                  | Does not own                                                                                        |
| ---------------------- | ----------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Plugin                 | A versioned extension package with metadata, capabilities, permissions, and optional assets.          | Runtime authority by itself; every capability still routes through its own Hecate boundary.         |
| Connector              | Service integration capability: auth, external refs, issue/PR/task search, sync, comments.            | Hecate project/work mutation without proposal/apply or explicit API validation.                     |
| MCP server capability  | Tools, resources, and prompts Hecate can mount into tasks/chats or expose as Hecate surfaces.         | Hecate-specific auth state, UI placement, project mapping, or durable external-ref semantics alone. |
| Skill capability       | A reusable `SKILL.md` procedure package.                                                              | Tool grants, sandbox bypasses, memory promotion, or autonomous execution.                           |
| Slash command          | Local composer or command-palette shortcut.                                                           | A separate execution channel; it routes to normal Hecate APIs or inserts text for an agent.         |
| Evidence provider      | External artifacts/links such as GitHub PRs, CI runs, Linear issues, support tickets.                 | The truth of project completion by itself; evidence remains trust-labelled and inspectable.         |
| Project mapper         | Mapping external work into Hecate proposals, work items, assignments, handoffs, or memory candidates. | Direct durable project mutation without operator review/apply.                                      |
| UI surface             | Optional Hecate-rendered panel/action/modal declared by a plugin.                                     | Arbitrary unreviewed web execution in the alpha line.                                               |
| External Agent adapter | A supervised native agent runtime such as Claude Code ACP or Cursor Agent ACP.                        | Plugin behavior; agent adapters stay under `internal/agentadapters` and their runtime contract.     |

Short version:

```text
Plugins package capabilities.
Connectors are service-facing plugin capabilities.
MCP servers are compatibility/runtime tool capabilities.
Skills are procedure/context capabilities.
External Agents are supervised runtimes, not plugins.
```

## Recommended Shape

Use a Hecate-native manifest as the product contract, with MCP as a first-class
capability type:

```text
hecate-plugin.json
README.md
skills/
  github-pr-review/SKILL.md
mcp/
  github-server.json
commands/
  slash-commands.json
ui/
  surfaces.json
```

Sketch:

```json
{
  "schema_version": "hecate.plugin.v0",
  "id": "github",
  "name": "GitHub",
  "version": "0.1.0",
  "description": "GitHub issues, pull requests, reviews, and evidence links.",
  "homepage_url": "https://github.com/hecatehq/hecate",
  "permissions": [
    "network:github.com",
    "secret:github_token",
    "connector:read",
    "connector:write_comment",
    "evidence:provide"
  ],
  "capabilities": {
    "connectors": [
      {
        "id": "github",
        "kinds": ["issue", "pull_request", "review", "commit", "branch"]
      }
    ],
    "mcp_servers": [
      {
        "id": "github",
        "transport": "stdio",
        "command": "github-mcp-server"
      }
    ],
    "slash_commands": [
      {
        "name": "github",
        "scope": "hecate",
        "description": "Open GitHub actions and linked refs."
      }
    ],
    "skills": [
      {
        "id": "github-pr-review",
        "path": "skills/github-pr-review/SKILL.md"
      }
    ],
    "project_mappers": [
      {
        "id": "github_issue_to_work",
        "source_kind": "github_issue",
        "target": "project_assistant_proposal"
      }
    ],
    "evidence_providers": [
      {
        "id": "github_pull_request",
        "kinds": ["pull_request", "review"]
      }
    ]
  }
}
```

This manifest is descriptive. Installing a plugin does not automatically grant
network, secrets, tool execution, or durable mutation authority. Each capability
has to be enabled or used through the relevant Hecate surface.

## Capability Boundaries

### MCP servers

A plugin can ship or reference MCP server configs. Hecate may use them in two
ways:

- Mount into Hecate-owned task/chat runs through the existing `mcp_servers`
  path.
- Expose as a connector-backed resource/tool surface in Hecate UI after an
  operator enables it.

MCP tool descriptions and metadata must be treated as external input. Hecate
should preserve MCP annotations, but approval policy remains Hecate-owned:
read-only hints can improve UX, while writes/destructive operations still need
the configured Hecate gate.

### Connectors

Connectors provide durable integration state:

```text
provider: github | linear | ...
kind: issue | pull_request | review | cycle | team | ...
external_id: provider-native id
url: canonical URL
title/status snapshot: cached operator display
sync cursor: optional provider-specific state
```

Connectors are useful outside Projects:

- Chats can open/search/link external refs.
- Tasks can attach evidence.
- Project Assistant can draft proposals from external work.
- Context Inspector can show which external refs were visible.
- Usage/observability can group plugin calls.

Connectors should start read-first. Writes such as comments, issue creation, or
status updates require explicit operator action or a Hecate approval gate.

### Project Assistant integration

Project Assistant consumes normalized connector data, not provider-specific
API payloads. A GitHub or Linear plugin may propose:

- Create a Hecate work item linked to external issue `X`.
- Create a queued assignment based on external issue labels/status.
- Attach a GitHub PR as evidence to a handoff.
- Create a memory candidate from a linked issue discussion.

It must not directly create/start chats, tasks, runs, External Agent sessions,
or promoted memory. Durable project changes still go through proposal
validation plus explicit operator apply.

### Slash commands

Plugin slash commands are local Hecate commands unless they are explicitly
External Agent commands advertised by ACP. The composer should label
provenance:

| Label                          | Meaning                                                                                           |
| ------------------------------ | ------------------------------------------------------------------------------------------------- |
| Hecate                         | Local navigation/runtime shortcut owned by Hecate or a Hecate-native plugin.                      |
| Project                        | Hecate-owned project/proposal shortcut.                                                           |
| External Agent: `<agent name>` | ACP-advertised command sent as ordinary prompt text to the active External Agent adapter/session. |
| Plugin: `<name>`               | Future UI label when a Hecate plugin owns a local command outside built-in surfaces.              |

Hecate should group External Agent commands by their originating adapter and
session. "External Agent" is the capability class, not a shared command
namespace. The visible label, routing key, telemetry, and command-picker
deduplication should preserve the specific agent identity because `/review`,
`/plan`, or another command may have different semantics in different agents.

Plugin command labels are display strings, not routing identities. Internally,
local plugin commands should be addressed by a plugin-scoped key such as
`plugin_id:command_name` or `plugin_id:capability_id:command_name`. If two
plugins declare the same short command name, the command picker should
disambiguate with plugin identity instead of choosing one by install order.

### UI surfaces

UI surfaces should be conservative in alpha:

- Built-in Hecate-rendered panels from typed plugin metadata.
- No arbitrary remote iframe/plugin webview as a first slice.
- No unreviewed browser automation plugin model.
- Prefer "open details", "review proposal", "link external ref", and
  "inspect evidence" surfaces before rich app embedding.

## Compatibility Strategy

### MCP first

MCP is the most portable current integration contract. Hecate should support
plugin-declared MCP servers and should keep improving Hecate-as-MCP-server and
Hecate-as-MCP-client behavior.

Compatibility rule:

```text
If a third-party integration can be represented as MCP tools/resources/prompts,
prefer mounting it as MCP capability inside a Hecate plugin.
```

### Host-specific package importers

Hecate may later import host-specific packages:

- Claude Code skills/plugins.
- Codex skills/plugins.
- Cursor/OpenCode/Gemini command or skill packages.
- Internal company plugin folders.

Import means "read metadata and adapt into Hecate plugin records." It does not
mean "execute the other host's lifecycle hooks" or "inherit that host's
permission model."

Host-specific permission declarations are hints. Hecate policy, sandbox,
approvals, provider routing, and project proposal validation remain
authoritative.

### Native Hecate plugins

Native Hecate plugins should exist because Hecate has product concepts MCP does
not cover by itself:

- Operator installation/enablement state.
- Secrets and auth health.
- Project Assistant proposal mappers.
- Evidence trust labels.
- UI placement.
- Remote-runtime safety classification.
- Storage across memory/sqlite/postgres.
- Hecate audit and OTel attributes.

## GitHub Plugin Example

The GitHub plugin should be general:

- Connector: issues, pull requests, reviews, commits, branches, checks.
- MCP: optional GitHub MCP server mounted into task/chat profiles.
- Slash commands: `/github issue`, `/github pr`, or command-palette actions.
- Project Assistant: import issues as proposal actions; attach PRs as handoff
  evidence; draft follow-up work from review comments.
- Task evidence: link PR/check/run evidence to task runs and handoffs.
- Chat: summarize linked PR or issue without creating project records unless
  the operator requests a proposal.

GitHub should not be "Projects only" and should not be only "agent tool." It
is both an operator connector and an optional agent capability source.

## Linear Plugin Example

The Linear plugin should start read-first:

- Connector: teams, projects, issues, cycles, statuses.
- Project Assistant: draft Hecate work items/assignments from selected Linear
  issues.
- Evidence: link Linear issues/cycles/statuses to project work and handoffs.
- Sync: optionally comment back or update Linear status after explicit
  operator confirmation.

Linear remains an external planning system. Hecate Projects remains the local
execution/review cockpit.

## Storage And API Sketch

First implementation should be registry-only. The record is a catalog and
policy-review object, not a runtime grant.

Registry-only means Hecate can:

- Read a Hecate-native manifest from a built-in source or local path.
- Validate schema, IDs, capability declarations, and requested permissions.
- Persist the raw manifest plus a normalized projection for listing/search.
- Show requested capabilities, permission requests, auth requirements, and
  collision warnings to the operator.
- Toggle plugin/capability enablement state as metadata.
- Bind requested secret names to Hecate `secret_ref` records without exposing
  the secret value.

Registry-only must not:

- Run plugin hooks, scripts, installers, or bundled binaries.
- Start plugin-declared MCP servers.
- Make connector network calls or OAuth requests.
- Mount tools into chats/tasks/profiles.
- Register executable slash-command handlers.
- Render arbitrary plugin-owned UI.
- Mutate projects, tasks, chats, memory, external refs, or evidence.

The minimum durable shape should keep raw manifests and normalized capability
records separate. The raw manifest preserves forward-compatible metadata; the
projection gives Hecate stable query/filter/conflict behavior:

```text
plugins
  id
  name
  description
  version
  source_kind          # builtin | local_path | imported | remote_registry
  source_ref
  manifest_schema_version
  manifest_digest
  manifest_json
  requested_permissions_json
  registry_state       # valid | invalid | unsupported
  enabled
  installed_at
  updated_at

plugin_capabilities
  plugin_id
  capability_id
  capability_kind     # connector | mcp_server | skill | slash_command | ...
  display_name
  requested_permissions_json
  enabled
  config_json

plugin_auth
  plugin_id
  capability_id
  requested_name       # e.g. github_token from the manifest
  auth_kind           # token | oauth | env | none
  status              # unknown | configured | expired | error
  secret_ref
```

Mirror the normal Hecate storage rule: memory, SQLite, and Postgres for any
durable plugin state.

The registry should enforce stable identity rules before writing records:

- `plugin.id` is globally unique.
- `capability_id` is unique inside one plugin.
- `(plugin_id, capability_id)` is the durable routing key for capabilities.
- Short command names are not unique globally; duplicate command names produce
  picker disambiguation metadata rather than install-order precedence.
- Manifest permission strings are parsed and stored, but each permission has to
  be classified as `enforced`, `advisory`, or `unsupported` before any
  executable capability ships.
- Secret requests store only requested names and binding status until the
  operator maps them to Hecate secret refs.

Initial APIs can be local-only:

```text
GET  /hecate/v1/plugins
GET  /hecate/v1/plugins/{id}
POST /hecate/v1/plugins/install-local
PATCH /hecate/v1/plugins/{id}
GET  /hecate/v1/plugins/{id}/health
```

API behavior for the registry slice:

- `POST /install-local` validates and stores a manifest projection. In the
  first implementation it records manifest JSON supplied by the operator/client
  plus an optional `source_ref`; it does not read arbitrary paths, execute
  package code, or fetch provider state.
- `PATCH /{id}` can enable/disable the plugin or individual capabilities as
  metadata. It does not grant secrets, network, tools, or UI execution by
  itself.
- `GET /{id}/health` reports registry health: manifest validity, unsupported
  permissions, unresolved secret bindings, disabled capabilities, and command
  collisions. It does not call external providers.

The first shipped registry slice uses `schema_version: "hecate.plugin.v0"`,
stores the raw manifest, projects capabilities from either a capability array
or grouped `connectors`, `mcp_servers`, `skills`, `slash_commands`,
`project_mappers`, `evidence_providers`, and `ui_surfaces`, and classifies
permission strings as review metadata (`advisory` or `unsupported`). No
permission string is treated as an executable grant in this slice.
`manifest_digest` is computed from Hecate's canonicalized manifest JSON rather
than caller/file bytes, so formatting-only differences produce the same digest.

The first UI can be a read-only/plugin-settings list: plugin name, version,
source, enabled state, capabilities, requested permissions, auth-binding status,
and warnings. Connector-specific network APIs and active plugin surfaces should
wait until the registry and permission model are in place.

## Security Model

- Plugins are disabled until installed and enabled by the operator.
- Installing a plugin records requested permissions; granting them is a
  separate step.
- Plugin MCP tools inherit Hecate's existing MCP server approval policy, not
  the plugin author's expectation.
- Connector writes require explicit UI confirmation or a Hecate approval gate.
- Plugin-provided descriptions, prompts, skills, and external data are labelled
  by origin and trust level in context packets.
- Plugin secrets use Hecate's existing secret storage/resolution model; secrets
  are not forwarded to External Agents or MCP servers unless explicitly
  configured for that capability.
- Remote-runtime mode must classify every plugin API route as remote-safe or
  local-only before it ships.
- Manifest permissions are requested grants, not proof of enforcement. For the
  registry-only slice they are descriptive metadata. Before any plugin-owned
  process, MCP server, or connector write path executes, each permission string
  has to be classified as enforced or advisory.
- Manifest secret names are binding requests, not secret values. Enabling a
  capability should bind each requested secret name to a Hecate-owned
  `secret_ref` before that secret can be delivered to a capability runtime.

## Implementation Sequence

1. **Design and docs**
   Land this vocabulary and update known limitation wording so "plugins" does
   not only mean browser/WASM marketplaces.
2. **Registry-only plugin records**
   Add memory/sqlite/postgres stores, API, and a read-only UI list. No network
   calls, no execution.
3. **MCP-backed plugin capability**
   Let a plugin declare MCP server configs that can be mounted into profiles or
   task/chat starts.
4. **GitHub plugin v0**
   Start with read/search/link external refs and evidence links.
5. **Linear plugin v0**
   Start with read/search/import-as-proposal.
6. **Compatibility importers**
   Import selected Claude/Codex-style skills/plugins into Hecate metadata after
   the native manifest and permission model are stable.

## Open Questions

- Should plugin packages live under `.hecate/plugins`, global app data, or both?
- Should project-local plugins be allowed, or only operator-installed global
  plugins referenced by projects?
- How should plugin versions and updates be trusted in local-first deployments?
- Should plugin-provided UI ever allow sandboxed web content, or should V1 stay
  typed/Hecate-rendered only?
- Should plugin MCP servers be long-lived cached processes, profile-scoped, or
  run-scoped only?
- How much of plugin auth should be shared with External Agent adapters, if any?
- Should `network:<host>` permissions become sandbox-enforced egress
  constraints for plugin-owned processes, stay advisory metadata for
  Hecate-owned connector calls, or have separate meanings for those two cases?
- How should Hecate surface and repair command/capability ID collisions across
  multiple installed plugins?
