# Hecate runtime image. This is the default local/self-host Docker image and
# the base shape for hosted remote runtime deployments. The same image can run in
# either posture; Hecate's runtime mode env controls the security boundary.
#
# Build:   docker build -t hecate:dev .
# Run:     docker run --rm -p 8765:8765 hecate:dev
#
# The image embeds the React UI, the Hecate binary, git/ssh, and the supported
# External Agent CLIs and built-in ACP adapter libraries. Local mode can use
# mounted CLI login homes
# or API keys. Remote runtime mode ignores local login files and accepts only the
# remote-safe credential env families declared by the adapters.

ARG GO_VERSION=1.26.5
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

# -- 2. Go build ------------------------------------------------------------

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

# -- 3. Runtime -------------------------------------------------------------

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
ARG CLAUDE_CODE_VERSION=2.1.177
ARG GROK_VERSION=0.2.51
ARG CURSOR_AGENT_VERSION=2026.07.20-8cc9c0b
ARG CURSOR_AGENT_LINUX_X64_SHA256=6e9f17247ffeb5f8f7e2246b4bcd6bb26cb2d5a9f9a4b0012c9a80d868ed25b4
ARG CURSOR_AGENT_LINUX_ARM64_SHA256=2986152b283c70a666b015035b2e99a96d13afd2660a587b8639417cfdd147fb

RUN npm install -g \
      @openai/codex@${OPENAI_CODEX_VERSION} \
      @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
      @xai-official/grok@${GROK_VERSION} \
    && npm cache clean --force

RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) cursor_arch=x64; cursor_sha256="${CURSOR_AGENT_LINUX_X64_SHA256}" ;; \
      arm64) cursor_arch=arm64; cursor_sha256="${CURSOR_AGENT_LINUX_ARM64_SHA256}" ;; \
      *) echo "unsupported Cursor Agent architecture: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    cursor_archive=/tmp/cursor-agent.tar.gz; \
    cursor_dir="/opt/cursor-agent/.local/share/cursor-agent/versions/${CURSOR_AGENT_VERSION}"; \
    cursor_url="https://downloads.cursor.com/lab/${CURSOR_AGENT_VERSION}/linux/${cursor_arch}/agent-cli-package.tar.gz"; \
    curl -fsSL --retry 5 --retry-delay 2 --retry-all-errors "${cursor_url}" -o "${cursor_archive}"; \
    printf '%s  %s\n' "${cursor_sha256}" "${cursor_archive}" | sha256sum -c -; \
    mkdir -p "${cursor_dir}" /opt/cursor-agent/.local/bin; \
    tar --no-same-owner --no-same-permissions --strip-components=1 -xzf "${cursor_archive}" -C "${cursor_dir}"; \
    rm -f "${cursor_archive}"; \
    test -x "${cursor_dir}/cursor-agent"; \
    test -x "${cursor_dir}/node"; \
    ln -sf "${cursor_dir}/cursor-agent" /opt/cursor-agent/.local/bin/agent; \
    ln -sf "${cursor_dir}/cursor-agent" /opt/cursor-agent/.local/bin/cursor-agent; \
    ln -sf /opt/cursor-agent/.local/bin/cursor-agent /usr/local/bin/cursor-agent; \
    ln -sf /opt/cursor-agent/.local/bin/agent /usr/local/bin/agent

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
