# Cross-cutting smoke tests (Docker compose round-trip). Tauri's GUI smoke
# lives in tauri.just; ACP and other tagged Go smokes live alongside their
# subjects in go.just / e2e/.

# Spin up `docker compose` with the production image and verify /healthz,
# /v1/models auth, and the bootstrap volume round trip. Runs against a
# separate compose project name so it cannot collide with a developer's
# already-running `docker compose up`. Requires Docker.
# Docker smoke test for the production compose image.
test-docker-smoke: _go-cache
	{{go_env}} go test -tags 'e2e docker' -count=1 -timeout 10m ./e2e/...

# Download the pinned Codex/Claude Go ACP adapter release binaries and smoke
# them through Hecate's ACP boundary with fake vendor CLIs. Requires network.
# Smoke test packaged Go ACP adapter release binaries.
test-acp-release-smoke: _go-cache
	{{go_env}} go test -tags acp_release -count=1 -run TestACPAdapterReleaseBinaries -timeout 5m ./internal/agentadapters
