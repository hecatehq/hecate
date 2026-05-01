# syntax=docker/dockerfile:1.7
#
# Multi-stage build. Three layers:
#   1. ui-builder:  Bun compiles the React operator UI to ui/dist.
#   2. go-builder:  Go compiles cmd/gateway with //go:embed pulling in
#                   ui/dist from the previous stage. Result is one static
#                   binary with the UI embedded.
#   3. runtime:     distroless/static — ~2 MB base, no shell, runs as
#                   non-root. Suitable for production.
#
# Build:   docker build -t hecate:dev .
# Run:     docker run --rm -p 8765:8765 hecate:dev
#
# The runtime image needs no environment to start; it serves the API and
# UI on :8765 immediately. Provider configuration happens through the UI
# or by mounting a .env file into the container.

ARG GO_VERSION=1.26.2
ARG BUN_VERSION=1.3.13

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
WORKDIR /src

# Module download caches independently of source.
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download

# The full source must come in before the embed line in embed.go is
# resolved. ui/dist is replaced by the artifacts the previous stage built.
COPY . .
COPY --from=ui-builder /app/ui/dist ./ui/dist

# CGO_ENABLED=0 + -tags netgo + a static link give us a binary distroless
# can run unmodified. -ldflags trim symbols + path info to keep the image
# small and reproducible.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags='-s -w' \
    -o /out/gateway \
    ./cmd/gateway

# Pre-create an empty /data dir owned by distroless's nonroot uid (65532)
# so that, when compose mounts a named volume on top, the volume inherits
# nonroot ownership on first mount. Without this the binary boots as
# nonroot but can't write the bootstrap file because /data is root-owned.
# Distroless has no shell, so we have to set ownership in this builder
# stage and copy the prepared directory over with --chown below.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# ── 3. Runtime ──────────────────────────────────────────────────────────────

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# Copy the static binary. distroless has no package manager, no shell — the
# only file we add is the gateway binary itself.
COPY --from=go-builder /out/gateway /usr/local/bin/gateway

# /data holds the auto-generated bootstrap secrets (control-plane encryption
# key + admin bearer token) and any file-backed control-plane state. We
# copy in a pre-chowned empty dir from the builder so that when compose
# mounts a named volume here, the volume inherits nonroot ownership on
# first creation. Without this, the volume mounts root-owned and the
# nonroot binary can't persist its bootstrap file.
COPY --from=go-builder --chown=65532:65532 /out/data /data

ENV GATEWAY_ADDRESS=:8765 \
    GATEWAY_DATA_DIR=/data \
    GATEWAY_SQLITE_PATH=/data/hecate.db \
    # Default the durable subsystems to SQLite in the docker image so
    # `docker compose up` persists tenants / keys / pricebook / tasks /
    # chat sessions across restarts without extra config. The .db lives
    # on the /data volume and is wiped by `make reset-docker` along
    # with the rest of the stack. Operators can still override any of
    # these via .env / compose env (set to `memory` for ephemeral, or
    # `postgres` with the postgres profile). The semantic cache stays
    # on memory because it has no SQLite backend (modernc + sqlite-vec
    # mismatch — see internal/cache/semantic.go).
    GATEWAY_CONTROL_PLANE_BACKEND=sqlite \
    GATEWAY_RETENTION_HISTORY_BACKEND=sqlite \
    GATEWAY_CHAT_SESSIONS_BACKEND=sqlite \
    GATEWAY_TASKS_BACKEND=sqlite \
    GATEWAY_TASK_QUEUE_BACKEND=sqlite \
    GATEWAY_CACHE_BACKEND=sqlite \
    GATEWAY_BUDGET_BACKEND=sqlite \
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
VOLUME ["/data"]

EXPOSE 8765
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gateway"]
