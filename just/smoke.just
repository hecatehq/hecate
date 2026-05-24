# Cross-cutting smoke tests (Docker compose round-trip). Tauri's GUI smoke
# lives in tauri.just; ACP and other tagged Go smokes live alongside their
# subjects in go.just / e2e/.

# Spin up `docker compose` with the production image and verify /healthz,
# /v1/models auth, and the bootstrap volume round trip. Runs against a
# separate compose project name so it cannot collide with a developer's
# already-running `docker compose up`. Requires Docker.
# Docker smoke test for the production compose image.
test-docker-smoke: _go-cache
	{{go_env}} go test -tags 'e2e docker' -count=1 -timeout 5m ./e2e/...
