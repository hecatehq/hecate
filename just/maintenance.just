# Maintenance recipes: cheap recurring health checks and branch/worktree hygiene.

# Run the cheap recurring maintenance gate.
maintenance: docs-check go-format-check ui-format-check website-format-check vet test ui-lint website-lint ui-test

# Run the nightly maintenance report locally.
maintenance-nightly:
	bun scripts/nightly-maintenance.ts

# Check official Cursor Agent artifacts and update both Dockerfile pins.
cursor-agent-update: _go-cache
	{{go_env}} go run ./scripts/cursoragentupdate

# Report local branch and worktree cleanup candidates.
branches-report:
	git fetch origin --prune
	echo "Worktrees:"
	git worktree list
	echo
	echo "Local branches already merged into origin/master:"
	git branch --format='%(refname:short)' --merged origin/master | \
	  grep -Ev '^(master|main)$$' || true
	echo
	echo "Local branches with commits not in origin/master:"
	for branch in $(git for-each-ref refs/heads --format='%(refname:short)'); do \
	  case "$branch" in master|main) continue ;; esac; \
	  ahead=$(git rev-list --count origin/master.."$branch" 2>/dev/null || echo "?"); \
	  behind=$(git rev-list --count "$branch"..origin/master 2>/dev/null || echo "?"); \
	  if [ "$ahead" != "0" ]; then \
	    printf '%s ahead=%s behind=%s\n' "$branch" "$ahead" "$behind"; \
	  fi; \
	done
	echo
	echo "Open pull requests:"
	if command -v gh >/dev/null 2>&1; then \
	  gh pr list --repo hecatehq/hecate --state open --json number,headRefName,title,url \
	    --jq '.[] | "#\(.number) \(.headRefName) \(.title)\n\(.url)"'; \
	else \
	  echo "gh not installed; skipping PR lookup"; \
	fi
