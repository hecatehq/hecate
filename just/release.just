# Release recipes: cut a tag, preflight-only validation, docker-stack reset
# (paired with release verification flows that exercise the production image).

# Validate release-only dependencies without running the full verification
# gate. Optional args pass through to scripts/release.ts.
# Check release-only dependencies.
release-preflight version *args:
	bun scripts/release.ts {{version}} {{args}} --preflight-only

# Check release-only dependencies, run verification, then cut a release tag.
# Optional args pass through to scripts/release.ts, for example:
# just release vX.Y.Z --skip-snapshot.
# Verify and cut a release tag.
release version *args:
	bun scripts/release.ts {{version}} {{args}} --preflight-only
	just verify
	bun scripts/release.ts {{version}} {{args}}

# Wipe the docker compose stack: stop + remove containers and the hecate-data
# named volume so the next `docker compose up` starts from scratch.
# Reset the docker compose stack and volume.
reset-docker:
	docker compose down -v --remove-orphans
	echo "Docker stack reset."
