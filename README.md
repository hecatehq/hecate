# Hecate

[![Latest release](https://img.shields.io/github/v/release/chicoxyzzy/hecate?include_prereleases)](https://github.com/chicoxyzzy/hecate/releases/latest)
[![Container](https://img.shields.io/badge/Container-ghcr.io-2496ED?logo=docker&logoColor=white)](https://github.com/chicoxyzzy/hecate/pkgs/container/hecate)
[![Test](https://github.com/chicoxyzzy/hecate/actions/workflows/test.yml/badge.svg)](https://github.com/chicoxyzzy/hecate/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/chicoxyzzy/hecate)](https://goreportcard.com/report/github.com/chicoxyzzy/hecate)
[![Go version](https://img.shields.io/github/go-mod/go-version/chicoxyzzy/hecate)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-enabled-f5a800?logo=opentelemetry&logoColor=white)](https://opentelemetry.io/)

**Open-source, self-hosted AI gateway and agent-task runtime** — a single control plane for the cloud and local models you use, with built-in policy, spend controls, and first-class OpenTelemetry.

> **Status: public alpha.** Core gateway is usable; agent runtime and sandbox are still evolving. Read [docs/known-limitations.md](docs/known-limitations.md) before depending on it.

## Table Of Contents

- [Why Hecate](#why-hecate)
- [Quick Start](#quick-start)
- [Architecture](#architecture)
- [Operator UI](#operator-ui)
- [What Works Today](#what-works-today)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [License](#license)

## Why Hecate

AI workloads are moving from simple API calls to long-running agents, tool use, local/cloud routing, and budget-sensitive automation. Hecate gives you that runtime layer as one self-contained binary you run yourself.

- **Cloud and local providers together** — OpenAI, Anthropic, Ollama, LM Studio, LocalAI, llama.cpp-compatible servers, and other shipped presets.
- **Operator-controlled spend** — balances, pricebook, rate limits, audit history.
- **Runtime visibility** — request ledger, route reports, failover details, cost, trace IDs, OpenTelemetry export.
- **Agent-task runtime** — queued tasks, approvals, controlled shell/file/git execution, resumable runs, MCP integration.
- **One artifact, many wrappers** — single Go binary with the React operator UI embedded via `//go:embed`. Ships as a Docker image, native desktop bundles (`.dmg` / `.deb` / `.AppImage` / `.msi`), and bare binary tarballs.

## Quick Start

| Path | Best for |
|---|---|
| [Desktop app](#desktop-app) | Personal use on your laptop. No terminal, no Docker. |
| [Docker](#docker) | Local container, scripted local deploys. |

### Desktop app

Download from the [latest release](https://github.com/chicoxyzzy/hecate/releases/latest):

| Platform | Bundle |
|---|---|
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg` |
| Linux x86_64 | `hecate-app_X.Y.Z_amd64.deb` or `.AppImage` |
| Windows x86_64 | `Hecate_X.Y.Z_x64_en-US.msi` |

Open the bundle and launch Hecate. The gateway runs as a sidecar inside the app and the UI loads automatically. State lives in the platform data dir (`~/Library/Application Support/com.hecate.app/` on macOS, `%APPDATA%\com.hecate.app\` on Windows, `~/.local/share/com.hecate.app/` on Linux).

> Bundles are not yet code-signed. On macOS, the first launch needs **right-click → Open** (Gatekeeper will block a plain double-click). On Windows, click **More info → Run anyway** on the SmartScreen warning. Subsequent launches work normally. Full footguns and roadmap in [docs/desktop-app.md](docs/desktop-app.md).

Skip to [Add a provider](#add-a-provider) once it's running.

### Docker

```bash
docker run --rm -p 127.0.0.1:8765:8765 -v hecate-data:/data \
  ghcr.io/chicoxyzzy/hecate:0.1.0-alpha.9
```

Open `http://127.0.0.1:8765`. The UI loads with no further setup.

> The container intentionally publishes only on `127.0.0.1`. Hecate is a single-operator local tool — it relies on the loopback boundary and enforces same-origin on every request. Don't expose it to the network without putting your own auth in front.

Pinned image tags, single-file binaries (linux/darwin × amd64/arm64), and checksums in [`docs/deployment.md`](docs/deployment.md). Local development knobs in [`docs/development.md`](docs/development.md). Provider keys can be pre-seeded via `.env` for fleet automation — `PROVIDER_<NAME>_API_KEY`, `_BASE_URL`, `_DEFAULT_MODEL`, plus the `_PRECONFIGURED=1` gate. See [`docs/providers.md`](docs/providers.md#env-configured-providers).

### Add a provider

The Providers tab starts empty. Click **Add provider**, pick a preset (or **Custom** for any OpenAI-compatible endpoint), and paste an API key (cloud) or endpoint URL (local).

![Empty Providers tab on first boot — Add provider CTA](docs/screenshots/providers-empty.png)

![Add provider modal on the Cloud tab — preset catalog](docs/screenshots/providers-presets.png)

![Providers table populated with three providers — Health, Endpoint, Credentials, Models](docs/screenshots/providers.png)

Cloud presets need an API key; local presets just need the runtime listening on its default port. Full catalog, custom-endpoint walk-through, and credential rotation in [`docs/providers.md`](docs/providers.md).

### Talk to it

![Chats workspace talking to a local Ollama llama3.1:8b model with sessions sidebar and inline runtime metadata](docs/screenshots/chat.png)

## Architecture

One Go process, one port, bound to loopback. Inside it: a chat/messages **gateway** that routes traffic to upstream providers, and a **task runtime** that queues agent work, drives approvals, and shells out through a sandbox boundary. The React operator UI is embedded into the same binary and served from the same port.

```mermaid
flowchart LR
    Browser["Browser<br/>(operator UI)"]
    APIClients["API clients<br/>(Claude Code, Codex, curl…)"]

    subgraph Hecate["Hecate (single binary, 127.0.0.1:8765)"]
        direction TB
        Gateway["Gateway<br/>/v1/chat/completions<br/>/v1/messages<br/>/v1/models"]
        Runtime["Task runtime<br/>/v1/tasks/*<br/>queue + workers + sandbox"]
        UI["Embedded UI<br/>(go:embed ui/dist)"]
    end

    Browser --> UI
    UI --> Gateway
    UI --> Runtime
    APIClients --> Gateway

    Gateway --> Providers["Cloud + local providers"]
    Runtime --> Sandbox["Sandboxed sh<br/>(per-call subprocess,<br/>output-capped, bwrap/sandbox-exec<br/>wrapped where available)"]
    Runtime --> MCP["External MCP servers"]
    Gateway --> OTel["OpenTelemetry"]
    Runtime --> OTel
```

For deeper internals, read [docs/architecture.md](docs/architecture.md), [docs/runtime-api.md](docs/runtime-api.md), and [docs/events.md](docs/events.md).

## Operator UI

The embedded UI is a runtime console for the operator.

- **Chats** — send requests through Hecate, choose provider/model, inspect per-turn route/cost metadata.
- **Providers** — manage provider credentials, defaults, model discovery, base URLs, and health.
- **Tasks** — create and manage agent runs, approvals, retries, resumes, and streamed output.
- **Observability** — inspect requests, route candidates, skip reasons, failover, costs, and trace events.
- **Costs** — balance, top-up / reset, usage table.
- **Settings** — pricebook and retention.

<details>
<summary>Various UI screenshots</summary>

![Observability view — request ledger and route-report drilldown](docs/screenshots/observe.png)

![Tasks workspace — task list with run state and approval queue](docs/screenshots/tasks.png)

![Costs workspace — balance card and usage table](docs/screenshots/costs.png)

![Settings → Pricing — model catalog with priced / unpriced / deprecated filters](docs/screenshots/settings-pricebook.png)

</details>

## What Works Today

Hecate is public-alpha software. The core gateway path is usable; the agent runtime and sandbox are intentionally still evolving.

| Area | State | Notes |
|---|---|---|
| OpenAI-compatible gateway | Usable | Chat Completions, streaming, vision, model discovery |
| Anthropic-compatible gateway | Usable | Messages API shape, streaming translation, Claude Code support |
| Provider catalog | Usable | Built-in presets, encrypted credentials, health, routing readiness |
| Local providers | Usable | Ollama, LM Studio, LocalAI, llama.cpp-compatible servers |
| Loopback-only binding | Usable | Listens on `127.0.0.1`; same-origin enforced on every request |
| Budgets and rate limits | Usable | Balances, warning thresholds, pricebook, `429` rate-limit headers |
| OpenTelemetry | Usable | OTLP traces, metrics, logs, response headers, local trace view |
| Storage tiers | Usable | Memory or SQLite, selected per subsystem |
| Operator UI | Usable | Main workflows are present; debugging ergonomics are still improving |
| Agent task runtime | Alpha | Queues, approvals, resumable runs, `agent_loop`, MCP integration; periodic reconciler auto-recovers stale runs |
| Execution isolation | Alpha | Per-call subprocess + env sanitisation + output cap + wall-clock timeout; `bwrap` (Linux) / `sandbox-exec` (macOS) wrapping where available. Not container-level — see [`docs/sandbox.md`](docs/sandbox.md) |

Read [docs/known-limitations.md](docs/known-limitations.md) before treating Hecate as production-stable.

## Documentation

Full index lives at [`docs/README.md`](docs/README.md), organized by reader role. The most-reached-for pages:

**Running Hecate**

- [Deployment](docs/deployment.md) — Docker, image pinning, binary install, storage tiers, rate limits.
- [Desktop app](docs/desktop-app.md) — native bundles, first-launch footguns, platform data dirs, roadmap.
- [Providers](docs/providers.md) — preset catalog, custom OpenAI-compatible endpoints, credentials, health, circuit breaking.
- [Known limitations](docs/known-limitations.md) — plain-language list of what's still alpha.

**Building against Hecate**

- [Runtime API](docs/runtime-api.md) — task lifecycle, approvals, SSE streaming.
- [Agent runtime](docs/agent-runtime.md) — `agent_loop` loop mechanics, tools, cost ceilings, retry-from-turn.
- [Events](docs/events.md) — every event type, payload shape, when each fires.
- [MCP integration](docs/mcp.md) — Hecate as MCP server + attaching external MCP servers as tools.

**Observability and internals**

- [Telemetry](docs/telemetry.md) — OTLP traces / metrics / logs, response headers, local trace view.
- [Architecture](docs/architecture.md) — gateway request flow, task-runtime queue / lease / sandbox boundary.
- [Development](docs/development.md) — building from source, the test ladder, screenshot tooling.
- [Release](docs/release.md) — cutting a tag, alpha gate, recovery if CI fails.

First-run environment knobs live in [`.env.example`](.env.example).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). If you work with an AI assistant, start with [AGENTS.md](AGENTS.md); the vendor-neutral agent instruction layer lives in [ai/](ai/README.md).

## License

MIT. See [LICENSE](LICENSE).

Third-party data notices live in [NOTICE.md](NOTICE.md), including LiteLLM pricing-data attribution.
