# Workspace Instructions, Skills, And Agent Presets

> **Status:** proposal; Agent Presets V1 core, workspace-guidance discovery,
> project skills registry core/UI, role skill ids, assignment skill resolution,
> preset-management UI, skill pickers, and Bootstrap V2 registry-based role
> suggestions, and native-assignment prompt inclusion for project memory plus
> portable `AGENTS.md` guidance are implemented.
> **Current source of truth:** [Context assembly and injection boundaries](context-assembly-and-injection-boundaries.md),
> [Agent memory](agent-memory.md), [Projects](../accepted/projects.md), and
> [Runtime API](../../runtime/runtime-api.md) for today's context-packet,
> memory, project, and task behavior.
> **Next action:** add compatibility warnings for host-specific guidance and
> decide whether arbitrary source-document body inclusion needs an explicit
> operator approval flow. Remote skill install and skill execution remain
> separate later slices.

Hecate needs a clean vocabulary for several things that are easy to blur:
workspace `AGENTS.md` files, other Markdown instruction files, reusable
`SKILL.md` packages, Agent Presets, memory, and workflow runbooks.
They are all "context" in the broad sense, but they have different authority,
lifecycle, safety, and UI needs.

This proposal defines the layering Hecate should use while finishing Agent
Presets V1 and before implementing planner/runbook features.

## External Alignment

Facts, accessed 2026-06-08 unless noted:

- Codex documents `AGENTS.md` as its project-instruction surface and includes
  feature-flagged hierarchy/precedence behavior for nested agent guidance.
  Source: <https://github.com/openai/codex/blob/main/docs/agents_md.md>.
- Claude Code skills use a skill folder with `SKILL.md`, optional supporting
  resources, and progressive disclosure. Claude Code also supports an
  `allowed-tools` frontmatter field that can grant tool access while the skill
  is active. Source: <https://code.claude.com/docs/en/skills> (accessed
  2026-07-04).
- Claude Code memory/instruction files include organization, user, and project
  `CLAUDE.md` locations, including project `./CLAUDE.md` and
  `./.claude/CLAUDE.md`. Source:
  <https://docs.claude.com/en/docs/claude-code/memory>.
- Microsoft Agent Skills search configured paths recursively for `SKILL.md`
  files, validate format/resources, and expose loading/resource/script tools to
  agents. Source:
  <https://learn.microsoft.com/en-us/agent-framework/agents/skills>.
- skills.sh documents a public skill ecosystem around downloadable `SKILL.md`
  packages and warns that registry quality/security cannot be guaranteed.
  Source: <https://skills.sh/docs>.
- OpenCode discovers skills from `.opencode/skills`, `.claude/skills`, and
  `.agents/skills` at project and global levels, and recognizes only
  `name`, `description`, `license`, `compatibility`, and `metadata`
  frontmatter fields. Source: <https://dev.opencode.ai/docs/skills>.
- GitHub Copilot repository custom instructions include
  `.github/copilot-instructions.md`, path-specific
  `.github/instructions/**/*.instructions.md`, and agent-instruction files such
  as `AGENTS.md`, `CLAUDE.md`, or `GEMINI.md`, with partial feature support.
  Source:
  <https://docs.github.com/en/copilot/concepts/prompting/response-customization>.
- VS Code/Copilot custom instructions explicitly recommends `AGENTS.md` when
  multiple AI agents share a workspace, supports nested `AGENTS.md`
  experimentally, and also detects `CLAUDE.md` locations. Source:
  <https://code.visualstudio.com/docs/agent-customization/custom-instructions>.
- Gemini CLI uses `GEMINI.md` as hierarchical memory/instructions and loads
  custom slash commands from user/project `.gemini/commands/*.toml` sources.
  Source:
  <https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/commands.md>.
- Gemini CLI Agent Skills documents workspace skill discovery from
  `.gemini/skills` and `.agents/skills`, with the `.agents/skills` alias taking
  precedence. Source: <https://geminicli.com/docs/cli/skills/> (accessed
  2026-07-04).
- Windsurf/Cascade separates Memories, Rules, Workflows, and Skills; it
  recommends durable reusable knowledge be stored as Rules or `AGENTS.md`, and
  currently prefers `.devin/rules/*.md` with legacy `.windsurf/rules/*.md`
  fallback. Source: <https://docs.windsurf.com/windsurf/cascade/memories>.
- Cursor rules live in `.cursor/rules` and act as project-scoped guidance, not
  reusable procedure packages. Source:
  <https://docs.cursor.com/context/rules-for-ai>.

Hecate should align with the `SKILL.md` package shape for portability, but not
inherit host-specific permission semantics. In Hecate, a skill can suggest or
require capabilities, but profile, policy, sandbox, and approvals decide what
is actually allowed.

The cross-ecosystem lesson is:

```text
AGENTS.md is the best portable workspace-instruction target.
SKILL.md is the best portable reusable-skill target.
Host-specific files should be compatibility sources with labels, not Hecate's
source of truth.
```

## Concept Stack

| Concept                   | Owns                                                                                                                                                          | Does not own                                                                              |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| Workspace instructions    | Repo/workspace-authored guidance such as `AGENTS.md`, nested `AGENTS.md`, `CLAUDE.md`, `.cursor/rules`, and similar.                                          | Durable operator memory, reusable procedures, model/provider/tool posture.                |
| Project context sources   | Operator-selected files/docs/source metadata attached to a Hecate project.                                                                                    | Prompt rendering or authority decisions by themselves.                                    |
| Skills                    | Reusable procedures or capability guidance packaged as `SKILL.md` plus optional references/templates/scripts.                                                 | Tool grants, sandbox bypasses, durable memory, project identity, or autonomous execution. |
| Agent Presets             | Saved Hecate runtime posture: model/provider hints, tool policy, memory/context activation, skills, approvals, adapter hints, and system/preset instructions. | Project identity, durable memory storage, or a concrete workflow instance.                |
| Templates                 | Authoring-time templates for creating/updating projects, presets, or runbooks.                                                                                | Runtime identity after they are applied.                                                  |
| Runbooks / workflow modes | Concrete workflow patterns such as `review`, `qa`, `ship`, or `investigate`, including inputs, evidence, approvals, and stop conditions.                      | General-purpose agent identity, memory, or separate execution runtime.                    |
| Memory                    | Operator-approved durable facts/preferences and generated candidates awaiting approval.                                                                       | Workspace documentation, skill instructions, or automatic transcript mining.              |

Short version:

```text
Workspace instructions tell an agent what this workspace expects.
Skills tell an agent how to perform a reusable procedure.
Agent Presets choose the Hecate runtime posture and which skills/context are active.
Runbooks define a concrete workflow execution.
Memory carries operator-approved durable knowledge.
```

## Workspace Instructions

Workspace instructions are local files owned by the workspace/repository. They
should be first-class in Hecate because external coding agents already rely on
them and operators expect `AGENTS.md` to matter.

Initial discovery targets:

```text
AGENTS.md
*/AGENTS.md
GEMINI.md
CLAUDE.md
.claude/CLAUDE.md
CLAUDE.local.md
.github/copilot-instructions.md
.github/instructions/*.instructions.md
.devin/rules/*.md
.windsurf/rules/*.md
.cursor/rules/*.mdc
.gemini/commands/*.toml
.opencode/AGENTS.md
```

V1 should prioritize root and nested `AGENTS.md`. Host-specific formats should
be discovered and labelled before Hecate tries to interpret their full rule or
command semantics.

Recommended source classification:

| Source pattern                                   | Hecate kind             | V1 behavior                                                                |
| ------------------------------------------------ | ----------------------- | -------------------------------------------------------------------------- |
| `AGENTS.md`, nested `AGENTS.md`                  | `workspace_instruction` | Highest-priority portable workspace guidance.                              |
| `CLAUDE.md`, `.claude/CLAUDE.md`, local variants | `host_instruction`      | Detect and label as Claude-compatible; include only when enabled.          |
| `GEMINI.md`                                      | `host_instruction`      | Detect and label as Gemini-compatible hierarchical memory/instructions.    |
| `.github/copilot-instructions.md`                | `host_instruction`      | Detect and label as Copilot repository-wide instructions.                  |
| `.github/instructions/*.instructions.md`         | `path_instruction`      | Parse only simple `applyTo`/path metadata in V1; otherwise metadata-only.  |
| `.cursor/rules/*`                                | `host_rule`             | Detect as Cursor rules; do not assume Hecate understands activation modes. |
| `.devin/rules/*`, `.windsurf/rules/*`            | `host_rule`             | Detect as Windsurf/Devin rules; preserve activation metadata if available. |
| `.gemini/commands/*.toml`                        | `host_command`          | Metadata only in V1; not injected as standing instructions by default.     |
| Host custom agents such as `.github/agents/*`    | `host_agent_definition` | Metadata only; Hecate profiles remain separate.                            |

Discovery rules:

- Bound discovery to configured workspace roots.
- Root `AGENTS.md` applies broadly.
- Nested `AGENTS.md` applies to matching path prefixes.
- For broad project work, include root instructions and any operator-selected
  nested instructions.
- For path-scoped work, include the root-to-leaf instruction chain for the
  relevant paths.
- Do not crawl huge trees blindly. Use bounded depth, ignore common vendor/build
  directories, and prefer explicit project context-source metadata once saved.

Context packet representation:

```json
{
  "kind": "workspace_instruction",
  "section": "instructions",
  "title": "AGENTS.md",
  "origin": "AGENTS.md",
  "trust_label": "workspace_guidance",
  "included": true,
  "metadata": {
    "format": "agents_md",
    "scope": "workspace",
    "applies_to": ["."]
  }
}
```

External-agent boundary:

- Hecate should detect and show relevant workspace instructions in Context
  Inspector.
- If an external adapter already loads `AGENTS.md`, Hecate should label the
  source as available or likely adapter-loaded instead of injecting duplicate
  full text.
- If an adapter supports launch preambles/config, Hecate may include a short
  operator-visible note pointing the agent to workspace instructions.
- Hecate should not read or rewrite an external agent's private memory/settings.

Policy rule: workspace instructions never override Hecate policy, sandbox,
approvals, or operator settings. If they conflict, Hecate policy wins and the
inspector should show the conflict where possible.

## Skills

Skills are reusable procedures/capabilities. They should align with the
emerging `SKILL.md` folder shape:

```text
<skill-id>/
  SKILL.md
  references/
  scripts/
  templates/
  examples/
  assets/
```

Hecate V1 supports persisted project-local skill metadata:

```text
.agents/skills/<skill-id>/SKILL.md       # cross-agent compatibility source
.cairnline/skills/<skill-id>/SKILL.md    # portable coordination source
.claude/skills/<skill-id>/SKILL.md       # Claude-compatible metadata source
.gemini/skills/<skill-id>/SKILL.md       # Gemini-compatible metadata source
.hecate/skills/<skill-id>/SKILL.md       # Hecate-native/project-local source
guidance-linked local roots              # explicit project guidance references
```

Better alignment target:

```text
.hecate/skills/<skill-id>/SKILL.md       # Hecate-native source of truth
.agents/skills/<skill-id>/SKILL.md       # cross-agent compatibility candidate
.cairnline/skills/<skill-id>/SKILL.md    # portable coordination candidate
.claude/skills/<skill-id>/SKILL.md       # Claude-compatible import candidate
.gemini/skills/<skill-id>/SKILL.md       # Gemini-compatible import candidate
.opencode/skills/<skill-id>/SKILL.md     # OpenCode-compatible import candidate
~/.agents/skills/<skill-id>/SKILL.md     # future user-global import candidate
~/.claude/skills/<skill-id>/SKILL.md     # future user-global import candidate
```

V1 scans `.agents/skills`, `.cairnline/skills`, `.claude/skills`,
`.gemini/skills`, `.hecate/skills`, and local skill roots explicitly linked
from enabled guidance context sources. Hecate stores metadata only: host-specific
skill bodies are not injected, executed, installed, or treated as policy
authority. Compatibility scanning for `.opencode/skills` or global skill
directories should remain explicit opt-in because those skills can carry
host-specific assumptions or permission fields.

Common frontmatter:

```yaml
id: diff-aware-review
name: Diff-aware Review
description: Review changed files for regressions, risks, and missing tests.
version: 0.1.0
tags:
  - review
  - qa
```

Optional Hecate metadata:

```yaml
hecate:
  surfaces:
    - hecate_task
    - external_agent
  invocation:
    default: profile_selected
    user_invocable: true
    model_invocable: false
  suggested_tools:
    - git.diff
    - file.read
  required_permissions:
    writes: false
    network: false
  max_context_tokens: 4000
```

Security boundary:

```text
skill suggested tools != granted tools
```

Skills may declare suggested tools or required capabilities. Hecate can warn
when a profile does not satisfy them. Skills must not grant tool access,
silently approve actions, bypass workspace validation, execute scripts, or write
memory. Profile/policy/approval layers own authority.

Implemented registry behavior:

- `GET /hecate/v1/projects/{id}/skills` lists persisted project skill metadata.
- `POST /hecate/v1/projects/{id}/skills/discover` refreshes metadata from
  active project roots and enabled guidance-linked local skill roots.
- `PATCH /hecate/v1/projects/{id}/skills/{skill_id}` updates operator-owned
  metadata: `enabled`, `title`, `description`, and `trust_label`.
- Discovery ignores nested worktree containers such as `.worktrees` and
  `.claude/worktrees`; worktrees should be explicit project roots when the
  operator wants them represented.
- Discovery parses bounded metadata only: frontmatter `name`/`title`,
  `description`, `hecate.suggested_tools`, and
  `hecate.required_permissions.{tools,writes,network}`, then H1/title fallback
  and directory id. Suggested-tool lists are normalized, de-duplicated, capped,
  and summarized in operator-facing text.
- Assignment launch planning compares resolved project skills with the resolved
  profile and surfaces suggested-tool / required-permission mismatches as
  operator warnings. It does not grant or revoke capabilities.
- Hecate does not return, store, inject, execute, install, or fetch skill
  bodies.
- Duplicate ids become `conflict` records with warnings. Missing rediscovered
  skills become `missing`.
- Operator edits are preserved across rediscovery.

V1 non-goals:

- Remote install from skills.sh or any registry.
- Automatic enabling of third-party skills.
- Script execution from skills.
- Marketplace/search UX.
- Skill-provided tool grants.
- Model-invoked skills that change runtime permissions.
- Treating host-specific command files as Hecate skills without conversion.

Hecate should still record compatibility metadata, including unknown
frontmatter fields, but unknown fields must not become behavior.

## Agent Presets

Agent Presets are the next core substrate. They turn loose profile strings
into saved runtime posture that projects, roles, chats, assignments, and future
external-agent launches can resolve consistently.

Minimal preset shape:

```go
type AgentPreset struct {
    ID          string
    Name        string
    Description string
    Instructions string
    Surface     string // hecate_task | hecate_chat | external_agent | any

    ProviderHint string
    ModelHint    string
    ExecutionProfile string

    ToolsEnabled bool
    WritesAllowed bool
    NetworkAllowed bool
    ApprovalPolicy string // inherit | require | block | allow

    ProjectMemoryPolicy string // inherit | include | visible_only | exclude
    ContextSourcePolicy string // inherit | include_enabled | visible_only | exclude
    SkillIDs []string

    ExternalAgentKind string
    ExternalAgentOptions map[string]string

    CreatedAt time.Time
    UpdatedAt time.Time
}
```

Resolution order:

```text
explicit launch override
→ assignment role default
→ project default
→ built-in fallback preset
```

Resolution output should be snapshotted into context packets:

- selected preset id
- effective preset fields
- active skill ids and resolution status
- active memory/context policies
- missing/disabled skill warnings
- permission conflicts such as read-only preset versus skill requesting writes

Presets can reference skills, but presets are not skills. A preset answers
"how should this agent run?" A skill answers "what reusable procedure is
available?" A runbook answers "what workflow is being executed?"

## Runbooks And Planner

Runbooks should build on presets and skills after they exist. A runbook can
require compatible presets/skills, evidence artifacts, approvals, and stop
conditions:

```text
Runbook "review"
  compatible presets: reviewer, security-reviewer
  required skills: diff-aware-review
  permissions: read-only
  required artifacts: final report
```

Planner UI should come later as a draft surface. It can propose work items,
roles, assignments, presets, skills, handoff expectations, and context bundles,
but the operator approves before anything is created or started.

## What Hecate Needs

Recommended sequence:

1. **Agent Presets V1 Core** — partially implemented.
   - Preset store with memory/SQLite parity.
   - CRUD/list API.
   - Preset-management UI for creating, editing, and deleting saved presets.
   - Resolution helper with explicit/role/project/fallback order.
   - `skill_ids` resolve against the project skills registry during project
     work starts.
   - Context packet fields for resolved preset metadata.
   - Implemented: preset memory/source policies drive project-assignment
     context packet active / visible-only / omitted state.
   - Implemented: native project assignments include bounded project memory and
     portable `AGENTS.md` bodies only when the resolved preset policy explicitly
     includes them.

2. **Workspace Instructions V1 Core** — partially implemented.
   - Discover `AGENTS.md` and nested `AGENTS.md` under project workspace roots.
   - Save/refresh them as project context-source metadata.
   - Label host-specific files as metadata-only context sources.
   - Project Assistant context exposes context-source metadata, and Bootstrap
     drafting can turn enabled guidance metadata into reviewable memory
     candidates with source refs.
   - Future: include relevant Hecate-owned instruction content in context packets
     after explicit injection policy is designed.
   - Future: label external-agent behavior as "available to adapter" or "not
     injected" when Hecate does not own the adapter prompt.

3. **Skills Registry V1 Core**
   - Implemented: project-scoped memory/SQLite store parity.
   - Implemented: project-local `.agents/skills`, `.cairnline/skills`,
     `.claude/skills`, `.gemini/skills`, and `.hecate/skills` discovery.
   - Implemented: local skill roots explicitly linked from enabled `AGENTS.md`
     or compatible guidance.
   - Implemented: safe metadata parsing, conflict/missing/invalid statuses, and
     operator override preservation.
   - Implemented: list/discover/patch API and Project Skills UI.
   - Implemented: Bootstrap V2 suggests roles from enabled available registry
     records without installing, interpreting, or executing skills.
   - Future: built-in/global skill registry.
   - Trust/source labels.
   - No script execution or remote install.

4. **Profile Skill Resolution**
   - Implemented: project roles have `skill_ids`.
   - Implemented: profile and role `skill_ids` resolve against project skills at
     assignment start.
   - Implemented: active/skipped skill metadata and warnings are snapshotted
     into context packets without bodies.
   - Implemented: project roles and the profile editor can select from the
     project skills registry while preserving manual/unresolved IDs.
   - Future: permission-compatibility warnings.

5. **UI**
   - Implemented: profile editor.
   - Implemented: Project Skills list/detail and role/profile skill pickers.
   - Implemented: workspace instruction source list.
   - Context Inspector sections for instructions, skills, profile, memory, and
     runbook/workflow context.

6. **Runbooks V0**
   - Start with one report-only `review` or `qa` workflow that uses profiles,
     skills, existing task runs, artifacts, approvals, and memory candidates.

## Context Inspector Sections

The UI should keep these separate:

| Section              | Examples                                                         |
| -------------------- | ---------------------------------------------------------------- |
| Profile              | selected/effective profile, model/provider/tool posture          |
| Instructions         | `AGENTS.md`, nested `AGENTS.md`, host-specific instruction files |
| Skills               | selected `SKILL.md` packages, included/skipped state             |
| Project docs/sources | operator-selected docs and context-source metadata               |
| Memory               | operator-approved project memory entries                         |
| Work context         | work item, assignment, handoff, review, artifact refs            |
| Runtime evidence     | task/run status, approvals, tool output, route reports           |
| Runbook/workflow     | workflow mode, runbook id/version, typed inputs, stop rules      |

This separation is the main product value: the operator can tell whether the
agent followed workspace instructions, used a skill, relied on durable memory,
or merely saw generated/runtime evidence.

## Open Questions

- Should `.hecate/skills` live under each workspace root, under project config,
  or both?
- Should Hecate support global user-local skills before project-local skills?
- What is the minimum built-in skill set: `code-review`, `diff-aware-qa`,
  `investigate`, `handoff`, `release-check`?
- Should additional host-specific roots such as `.opencode/skills` become
  metadata-only discovery sources, or require explicit import?
- How should conflicting nested `AGENTS.md` files be shown when a task touches
  multiple directories?
- Should skill bodies be included in context by default, or only skill summary
  plus on-demand references until a runbook/profile requests full instructions?
- What lockfile/hash model is needed before remote skill install is allowed?
