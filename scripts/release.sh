#!/usr/bin/env bash
# release.sh — cut a Hecate release tag and push it to CI.
#
# Usage:
#   scripts/release.sh <version>        # e.g. v0.1.0-alpha.7
#   scripts/release.sh v0.2.0 --skip-snapshot   # skip the goreleaser dry-run
#
# The script runs pre-flight checks, fires a goreleaser snapshot dry-run so
# you can inspect the changelog before anything is published, then tags and
# pushes on explicit confirmation.  CI takes it from there (~5-10 min).
#
# Recovery if the CI run fails:
#   git push --delete origin <version>
#   git tag -d <version>
#   # fix root cause, re-run this script

set -euo pipefail

# ── Args ──────────────────────────────────────────────────────────────────────

VERSION=${1:-}
SKIP_SNAPSHOT=false
for arg in "$@"; do
  [[ "$arg" == "--skip-snapshot" ]] && SKIP_SNAPSHOT=true
done

if [[ -z "$VERSION" ]]; then
  echo "usage: scripts/release.sh <version> [--skip-snapshot]"
  echo "       version: vX.Y.Z  or  vX.Y.Z-pre.N  (e.g. v0.1.0-alpha.7)"
  exit 1
fi

# ── Validate version format ───────────────────────────────────────────────────

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9._]+)?$ ]]; then
  echo "error: version must be vX.Y.Z or vX.Y.Z-pre.N (got '$VERSION')"
  exit 1
fi

# ── Pre-flight ────────────────────────────────────────────────────────────────

echo "── Pre-flight ───────────────────────────────────────────────────────────"

# 1. Clean worktree — goreleaser refuses dirty state; catch it early.
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "error: working tree is dirty. Commit or stash changes first."
  git status --short
  exit 1
fi
echo "  worktree  : clean"

# 2. Branch check — warn when releasing from non-master.
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$BRANCH" != "master" && "$BRANCH" != "main" ]]; then
  echo "warning: releasing from branch '$BRANCH' (not master/main)"
  read -r -p "  Continue? [y/N] " confirm
  [[ "$confirm" =~ ^[Yy] ]] || { echo "Aborted."; exit 0; }
fi
echo "  branch    : $BRANCH"
echo "  commit    : $(git rev-parse --short HEAD)"

# 3. Tag must not already exist.
if git rev-parse "$VERSION" &>/dev/null 2>&1; then
  echo "error: tag $VERSION already exists locally."
  echo "  To delete: git tag -d $VERSION  (and git push --delete origin $VERSION if already pushed)"
  exit 1
fi
echo "  tag       : $VERSION (new)"

# 4. goreleaser must be on PATH.
if ! command -v goreleaser &>/dev/null; then
  echo "error: goreleaser not found."
  echo "  Install: go install github.com/goreleaser/goreleaser/v2@latest"
  exit 1
fi
echo "  goreleaser: $(goreleaser --version 2>&1 | head -1)"

echo ""

# ── Goreleaser snapshot dry-run ───────────────────────────────────────────────

if [[ "$SKIP_SNAPSHOT" == false ]]; then
  echo "── Goreleaser snapshot ──────────────────────────────────────────────────"
  echo "(builds binaries + Docker images locally without publishing)"
  echo ""
  goreleaser release --snapshot --clean
  echo ""
  echo "Snapshot written to ./dist."
  echo ""
  echo "Check the changelog before tagging:"
  echo "  cat dist/CHANGELOG.md"
  echo ""
  echo "If this is the first tag in the repo the changelog includes all history —"
  echo "that's expected. Tune .goreleaser.yaml changelog.filters if needed."
  echo ""
fi

# ── Confirm ───────────────────────────────────────────────────────────────────

echo "Ready to tag and push:"
echo "  tag    : $VERSION"
echo "  remote : $(git remote get-url origin 2>/dev/null || echo '(unknown)')"
echo ""
read -r -p "Tag and push? [y/N] " confirm
[[ "$confirm" =~ ^[Yy] ]] || { echo "Aborted."; exit 0; }

# ── Tag and push ──────────────────────────────────────────────────────────────

git tag -a "$VERSION" -m "$VERSION"
echo "Tagged $VERSION"

git push origin "$VERSION"

# Strip leading 'v' — goreleaser uses bare semver as the Docker tag.
DOCKER_TAG="${VERSION#v}"

echo ""
echo "── Done ─────────────────────────────────────────────────────────────────"
echo "CI is building the release. Track it at:"
echo "  https://github.com/chicoxyzzy/hecate/actions"
echo ""
echo "When CI completes (~5-10 min), verify the published image:"
echo "  docker pull ghcr.io/chicoxyzzy/hecate:${DOCKER_TAG}"
echo "  docker inspect ghcr.io/chicoxyzzy/hecate:${DOCKER_TAG} \\"
echo "    --format '{{range .Config.Env}}{{println .}}{{end}}' | grep PROVIDER"
echo ""
echo "To recover if CI fails:"
echo "  git push --delete origin $VERSION && git tag -d $VERSION"
