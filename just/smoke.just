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

# Exercise the built-in Go adapters with authenticated local provider CLIs.
# Successful prompts may consume operator quota, so this stays opt-in.
# Smoke test authenticated built-in adapter vendor CLIs.
test-acp-real-embedded adapters='codex,claude_code': _go-cache
	HECATE_ACP_REAL_CLI_SMOKE=1 HECATE_ACP_REAL_ADAPTERS='{{adapters}}' {{go_env}} go test -tags real_cli -count=1 -run TestRealACPCLIsSmoke -timeout 10m ./internal/agentadapters

# Exercise Cursor and Grok's direct ACP modes with authenticated local CLIs.
# Successful prompts may consume operator quota, so this stays opt-in.
# Smoke test authenticated direct ACP vendor CLIs.
test-acp-real-direct adapters='cursor_agent,grok_build': _go-cache
	HECATE_ACP_REAL_CLI_SMOKE=1 HECATE_ACP_REAL_ADAPTERS='{{adapters}}' {{go_env}} go test -tags real_cli -count=1 -run TestRealACPCLIsSmoke -timeout 10m ./internal/agentadapters
