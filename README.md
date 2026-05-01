# Hecate

[![Test](https://github.com/chicoxyzzy/hecate/actions/workflows/test.yml/badge.svg)](https://github.com/chicoxyzzy/hecate/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/chicoxyzzy/hecate)](https://goreportcard.com/report/github.com/chicoxyzzy/hecate)
[![Go version](https://img.shields.io/github/go-mod/go-version/chicoxyzzy/hecate)](go.mod)
[![License](https://img.shields.io/github/license/chicoxyzzy/hecate)](LICENSE)
[![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-enabled-f5a800?logo=opentelemetry&logoColor=white)](https://opentelemetry.io/)

Hecate is an open-source **AI gateway and agent-task runtime** for teams that want one control plane for model access, cost governance, routing, caching, observability, and controlled agent execution.

It sits between AI clients and model providers. Existing OpenAI-compatible and Anthropic-compatible clients can point at Hecate, while operators get a place to manage providers, costs, traces, cache behavior, and queued agent work. Multi-tenant management is opt-in — the default deployment is a single-user gateway with one admin bearer.

## Table Of Contents

- [Why Hecate](#why-hecate)
- [Modes](#modes)
- [Quick Start](#quick-start)
- [Architecture](#architecture)
- [Operator UI](#operator-ui)
- [What Works Today](#what-works-today)
- [Configuration](#configuration)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [License](#license)

## Why Hecate

AI workloads are moving from simple API calls to long-running agents, tool use, local/cloud routing, and budget-sensitive automation. Hecate is built for that messier runtime layer.

- **One gateway for many clients** — OpenAI Chat Completions and Anthropic Messages shapes.
- **Local and cloud providers together** — OpenAI, Anthropic, Ollama, LM Studio, LocalAI, llama.cpp-compatible servers, and other shipped presets.
- **Operator-controlled spend** — balances, pricebook management, rate limits, audit history, and (opt-in) per-tenant API keys with model/provider scoping.
- **Runtime visibility** — request ledger, route reports, failover details, cost, cache path, trace IDs, and OpenTelemetry export.
- **Agent-task runtime** — queued tasks, approvals, controlled shell/file/git execution, resumable runs, and MCP integration.
- **Single binary deploy** — Go gateway with the React operator UI embedded via `go:embed`. One process, one port, one volume; no separate frontend service to run.

## Modes

Hecate runs in one of two modes. The flag flips at startup; you can switch between runs without losing state.

| | **Single-user** (default) | **Multi-tenant** (opt-in) |
|---|---|---|
| Flag | `GATEWAY_MULTI_TENANT=false` | `GATEWAY_MULTI_TENANT=true` |
| Auth | One admin bearer; loopback handshake auto-fills it for same-host browsers. | Admin bearer **plus** per-tenant API keys, each scoped to allowed providers and models. |
| Operator UI | Chats, Providers, Tasks, Observability, Costs, Settings (Pricing / Policy / Retention). | Same plus the Tenants and Keys tabs in Settings. |
| Observability | Admin sees everything; tenants see nothing because there are no tenants. | Tenants see their own traces / requests / runtime stats via `/v1/*` mirrors of the `/admin/*` endpoints. |
| Use when | One operator on one host; local dev; a personal gateway behind a single key. | Multiple consumers, per-key audit, scoped credentials. |

The published Docker image ships single-user. Full breakdown in [`docs/tenants.md`](docs/tenants.md).

## Quick Start

Single-user path; for multi-tenant see [`docs/tenants.md`](docs/tenants.md). Two ways to get up:

| Path | Best for |
|---|---|
| **Desktop app** (below) | Personal use on your laptop. No terminal, no Docker. |
| **Docker** (below) | Servers, scripted deployments, anything you'd run as a service. |

### Desktop app

Download from the [latest release](https://github.com/chicoxyzzy/hecate/releases/latest):

| Platform | Bundle |
|---|---|
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg` |
| Linux x86_64 | `hecate-app_X.Y.Z_amd64.deb` or `.AppImage` |
| Windows x86_64 | `Hecate_X.Y.Z_x64_en-US.msi` |

Open the bundle, launch Hecate. The gateway runs as a sidecar inside the app and the UI loads automatically — no token paste, no port to remember. State lives in the platform data dir (`~/Library/Application Support/com.hecate.app/` on macOS, `%APPDATA%\com.hecate.app\` on Windows, `~/.local/share/com.hecate.app/` on Linux).

> **First-launch warnings.** Bundles are not yet code-signed. On macOS, double-clicking the first time fails with "Apple cannot check it for malicious software" — right-click `Hecate.app` → **Open**, confirm in the dialog. On Windows, click **More info** → **Run anyway** on the SmartScreen warning. Subsequent launches work normally. Full state, footguns, and roadmap: [docs/desktop-app.md](docs/desktop-app.md).

Skip to [step 2](#2-add-a-provider) once it's running.

### Docker

```bash
docker run --rm -p 8765:8765 -v hecate-data:/data \
  ghcr.io/chicoxyzzy/hecate:0.1.0-alpha.9
```

Open `http://127.0.0.1:8765`. On a localhost browser the console picks up the generated admin bearer through a same-origin loopback handshake — no token paste needed.

### 2. Add a provider

The Providers tab starts empty. Click **Add provider**, pick a preset (or **Custom** for any OpenAI-compatible endpoint), and paste an API key (cloud) or endpoint URL (local).

![Empty Providers tab on first boot — Add provider CTA](docs/screenshots/providers-empty.png)

![Add provider modal on the Cloud tab — preset catalog](docs/screenshots/providers-presets.png)

![Providers table populated with three providers — Health, Endpoint, Credentials, Models](docs/screenshots/providers.png)

Cloud presets need an API key; local presets just need the runtime listening on its default port. Full catalog, custom-endpoint walk-through, and credential rotation in [`docs/providers.md`](docs/providers.md).

### 3. Talk to it

![Chats workspace talking to a local Ollama llama3.1:8b model with sessions sidebar and inline runtime metadata](docs/screenshots/chat.png)

<details>
<summary>Remote browsers, reverse proxies, and cross-origin setups</summary>

The loopback handshake only fires for same-host browsers. Anywhere else (Tailscale, port-forward over SSH, reverse proxy with a different hostname) the UI shows a token paste prompt:

![Token paste prompt — remote / cross-origin browsers](docs/screenshots/onboard-wizard.png)

The bootstrap token is printed once to the container logs:

```text
============================================================
  Hecate first-run setup — admin bearer token generated.

    7f2a91b... (truncated)

  Saved to /data/hecate.bootstrap.json (mode 0600).
============================================================
```

It also lives in `hecate.bootstrap.json` on the `hecate-data` volume — recovery instructions in [`docs/deployment.md`](docs/deployment.md#recovering-a-lost-admin-token).
</details>

<details>
<summary>Other install paths (clone, Postgres profile, source build, env-as-code)</summary>

Cloning the repo lets you pick up optional compose profiles or rebuild from source:

```bash
docker compose up                    # uses the ghcr.io image; first run pulls
docker compose --profile postgres up # adds Postgres for durable state across all subsystems
```

Local development:

```bash
make dev
```

Pinned image tags, single-file binaries (linux/darwin × amd64/arm64), and checksums are in [`docs/deployment.md`](docs/deployment.md). Local development knobs in [`docs/development.md`](docs/development.md).

Provider keys can also be pre-seeded via `.env` for fleet automation — `PROVIDER_<NAME>_API_KEY`, `_BASE_URL`, `_DEFAULT_MODEL`, plus the `_PRECONFIGURED=1` gate. See [`docs/providers.md`](docs/providers.md#env-configured-providers). The `/admin/control-plane/providers` endpoints mirror every UI action for programmatic management.
</details>

## Architecture

One Go process, one port. Inside it: a chat/messages **gateway** that mediates client traffic to upstream providers, and a **task runtime** that queues agent work, drives approvals, and shells out through a sandbox boundary. The React operator UI is embedded into the same binary and served from the same port.

```mermaid
flowchart LR
    Clients["Clients<br/>Codex, Claude Code, SDKs"]
    Browser["Browser<br/>(operator UI)"]

    subgraph Hecate["Hecate (single binary, :8765)"]
        direction TB
        Gateway["Gateway<br/>/v1/chat/completions<br/>/v1/messages<br/>/v1/models"]
        Runtime["Task runtime<br/>/v1/tasks/*<br/>queue + workers + sandbox"]
        UI["Embedded UI<br/>(go:embed ui/dist)"]
    end

    Clients --> Gateway
    Clients --> Runtime
    Browser --> UI
    UI --> Gateway
    UI --> Runtime

    Gateway --> Providers["Cloud + local providers"]
    Gateway --> Cache["Exact + semantic cache"]
    Runtime --> Sandbox["sandboxd<br/>(out-of-process exec)"]
    Runtime --> MCP["External MCP servers"]
    Gateway --> OTel["OpenTelemetry"]
    Runtime --> OTel
```

For deeper internals, read [docs/architecture.md](docs/architecture.md), [docs/runtime-api.md](docs/runtime-api.md), and [docs/events.md](docs/events.md).

## Operator UI

The embedded UI is a runtime console for operators.

- **Chats** — send requests through Hecate, choose provider/model, inspect per-turn route/cost/cache metadata.
- **Providers** — manage provider credentials, defaults, model discovery, base URLs, and health.
- **Tasks** — create and manage agent runs, approvals, retries, resumes, and streamed output.
- **Observability** — inspect requests, route candidates, skip reasons, failover, costs, cache decisions, and trace events.
- **Costs** — balance, top-up / reset, usage table.
- **Settings** — pricebook, policy rules, retention, and (when `GATEWAY_MULTI_TENANT=true`) tenants + API keys.

<details>
<summary>Various UI screenshots</summary>

![Observability view — request ledger and route-report drilldown](docs/screenshots/observe.png)

![Tasks workspace — task list with run state and approval queue](docs/screenshots/tasks.png)

![Costs workspace — balance card and per-key usage table](docs/screenshots/costs.png)

![Settings → Pricebook — model catalog with priced / unpriced / deprecated filters](docs/screenshots/admin-pricebook.png)

</details>

## What Works Today

Hecate is public-alpha software. The core gateway path is usable; the agent runtime and sandbox are intentionally still evolving.

| Area | State | Notes |
|---|---|---|
| OpenAI-compatible gateway | Usable | Chat Completions, streaming, vision, model discovery |
| Anthropic-compatible gateway | Usable | Messages API shape, streaming translation, Claude Code support |
| Provider catalog | Usable | Built-in presets, encrypted credentials, health, routing readiness |
| Local providers | Usable | Ollama, LM Studio, LocalAI, llama.cpp-compatible servers |
| Auth | Usable | Admin bearer with same-origin loopback handshake; `GATEWAY_AUTH_DISABLED` for upstream-terminated auth |
| Tenants and API keys | Opt-in | `GATEWAY_MULTI_TENANT=true` exposes tenant + key management with provider/model scoping |
| Budgets and rate limits | Usable | Balances, warning thresholds, pricebook, `429` rate-limit headers |
| Caching | Usable | Exact cache; semantic cache is available but still early |
| OpenTelemetry | Usable | OTLP traces, metrics, logs, response headers, local trace view |
| Storage tiers | Usable | Memory, SQLite, Postgres, selected per subsystem |
| Operator UI | Usable | Main workflows are present; debugging ergonomics are still improving |
| Agent task runtime | Alpha | Queues, approvals, resumable runs, `agent_loop`, MCP integration |
| Execution isolation | Alpha | `sandboxd` boundary exists; stronger OS-level isolation is future work |

Read [docs/known-limitations.md](docs/known-limitations.md) before treating Hecate as production-stable.

## Configuration

The README intentionally stays light on configuration. The source of truth is:

- [`.env.example`](.env.example) — practical first-run environment knobs.
- [docs/deployment.md](docs/deployment.md) — Docker, storage tiers, rate limits, image pinning, reset/recovery.
- [docs/providers.md](docs/providers.md) — provider presets, local runtimes, credentials, health.
- [docs/telemetry.md](docs/telemetry.md) — OTLP traces, metrics, logs, collector recipes.
- [docs/agent-runtime.md](docs/agent-runtime.md) — task runtime, approvals, tools, workspace modes.
- [docs/mcp.md](docs/mcp.md) — MCP server and MCP tool integration.

## Documentation

Browse the full index at [`docs/README.md`](docs/README.md). Highlights:

- **Run it** — [Deployment](docs/deployment.md), [Desktop app](docs/desktop-app.md), [Providers](docs/providers.md), [Tenants and API keys](docs/tenants.md), [Known limitations](docs/known-limitations.md)
- **Use it** — [Runtime API](docs/runtime-api.md), [Agent runtime](docs/agent-runtime.md), [Events](docs/events.md), [MCP integration](docs/mcp.md)
- **Observe it** — [Telemetry](docs/telemetry.md)
- **Build it** — [Architecture](docs/architecture.md), [Development](docs/development.md), [Release](docs/release.md), [Chat sessions internals](docs/chat-sessions.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). If you work with an AI assistant, start with [AGENTS.md](AGENTS.md); the vendor-neutral agent instruction layer lives in [ai/](ai/README.md).

## License

MIT. See [LICENSE](LICENSE).

Third-party data and software notices live in [NOTICE.md](NOTICE.md), including LiteLLM pricing-data attribution.
