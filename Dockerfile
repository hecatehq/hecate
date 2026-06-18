# Hecate runtime image. This is the default local/self-host Docker image and
# the base shape for hosted remote runtime deployments. The same image can run in
# either posture; Hecate's runtime mode env controls the security boundary.
#
# Build:   docker build -t hecate:dev .
# Run:     docker run --rm -p 8765:8765 hecate:dev
#
# The image embeds the React UI, the Hecate binary, git/ssh, and the supported
# External Agent CLIs/ACP adapters. Local mode can use mounted CLI login homes
# or API keys. Remote runtime mode ignores local login files and accepts only the
# remote-safe credential env families declared by the adapters.

ARG GO_VERSION=1.26.2
ARG BUN_VERSION=1.3.13
ARG NODE_IMAGE=node:24-trixie-slim
ARG HECATE_VERSION=dev

# ── 1. UI build ─────────────────────────────────────────────────────────────

FROM oven/bun:${BUN_VERSION}-alpine AS ui-builder
WORKDIR /app/ui

# Copy lockfile + manifest first so dependency installation caches
# independently of source changes.
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile

COPY ui/ ./
RUN bun run build

# ── 2. Go build ─────────────────────────────────────────────────────────────

FROM golang:${GO_VERSION}-alpine AS go-builder
ARG HECATE_VERSION=dev
WORKDIR /src

RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download

# The full source must come in before the embed line in embed.go is
# resolved. ui/dist is replaced by the artifacts the previous stage built.
COPY . .
COPY --from=ui-builder /app/ui/dist ./ui/dist

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X github.com/hecatehq/hecate/internal/version.Version=${HECATE_VERSION}" \
    -o /out/hecate \
    ./cmd/hecate

# ── 3. Runtime ──────────────────────────────────────────────────────────────

FROM ${NODE_IMAGE} AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
      bash \
      build-essential \
      ca-certificates \
      curl \
      git \
      jq \
      less \
      openssh-client \
      pkg-config \
      procps \
      python3 \
      python3-pip \
      python3-venv \
      ripgrep \
      tini \
      unzip \
      xz-utils \
    && rm -rf /var/lib/apt/lists/*

ARG OPENAI_CODEX_VERSION=0.139.0
ARG CODEX_ACP_VERSION=0.16.0
ARG CLAUDE_CODE_VERSION=2.1.177
ARG CLAUDE_AGENT_ACP_VERSION=0.44.0
ARG GROK_VERSION=0.2.51
ARG CURSOR_INSTALL_SHA256=dc91409e80e09bba95995c3ed3ad52fb01238f65f04505fb0c1d1fff978c6df9
ARG CURSOR_INSTALL_URL=https://cursor.com/install

RUN npm install -g \
      @openai/codex@${OPENAI_CODEX_VERSION} \
      @zed-industries/codex-acp@${CODEX_ACP_VERSION} \
      @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
      @agentclientprotocol/claude-agent-acp@${CLAUDE_AGENT_ACP_VERSION} \
      @xai-official/grok@${GROK_VERSION} \
    && npm cache clean --force

RUN mkdir -p /opt/cursor-agent \
    && curl -fsSL --retry 5 --retry-delay 2 --retry-all-errors "${CURSOR_INSTALL_URL}" -o /tmp/cursor-install.sh \
    && printf '%s  %s\n' "${CURSOR_INSTALL_SHA256}" /tmp/cursor-install.sh | sha256sum -c - \
    && for attempt in 1 2 3; do \
      HOME=/opt/cursor-agent PATH=/opt/cursor-agent/.local/bin:$PATH \
        bash /tmp/cursor-install.sh && break; \
      if [ "$attempt" = "3" ]; then exit 1; fi; \
      sleep 3; \
    done \
    && rm -f /tmp/cursor-install.sh \
    && ln -sf /opt/cursor-agent/.local/bin/cursor-agent /usr/local/bin/cursor-agent \
    && ln -sf /opt/cursor-agent/.local/bin/agent /usr/local/bin/agent

RUN groupadd --system --gid 65532 hecate \
    && useradd --system --uid 65532 --gid hecate --home-dir /home/hecate --create-home hecate \
    && mkdir -p /data /workspace \
    && chown -R hecate:hecate /data /workspace /home/hecate

COPY --from=go-builder /out/hecate /usr/local/bin/hecate

ENV HECATE_ADDRESS=0.0.0.0:8765 \
    HECATE_ALLOW_NON_LOOPBACK_BIND=1 \
    HECATE_PUBLIC_URL=http://127.0.0.1:8765 \
    HECATE_DATA_DIR=/data \
    HECATE_SQLITE_PATH=/data/hecate.db \
    HECATE_BACKEND=sqlite \
    HECATE_AGENT_ADAPTERS_DIR=/data/agent-adapters \
    NPM_CONFIG_CACHE=/data/npm-cache \
    TINI_SUBREAPER=1 \
    # Local inference: from inside a container 127.0.0.1 is the container's
    # own loopback, not the host. Override all local provider base URLs to
    # use host.docker.internal so model discovery reaches a server running on
    # the Docker host. This applies whether using `docker run` directly or
    # via docker compose. host.docker.internal is provided automatically by
    # Docker Desktop on macOS/Windows; on Linux add
    # --add-host host.docker.internal:host-gateway (docker-compose.yml does
    # this via extra_hosts). The inference server must also bind to 0.0.0.0,
    # not 127.0.0.1 — see docker-compose.yml header for per-server details.
    PROVIDER_OLLAMA_BASE_URL=http://host.docker.internal:11434/v1 \
    PROVIDER_LMSTUDIO_BASE_URL=http://host.docker.internal:1234/v1 \
    PROVIDER_LLAMACPP_BASE_URL=http://host.docker.internal:8080/v1 \
    PROVIDER_LOCALAI_BASE_URL=http://host.docker.internal:8080/v1

VOLUME ["/data", "/workspace"]
WORKDIR /workspace
EXPOSE 8765
USER hecate:hecate
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/hecate"]
CMD ["serve"]
