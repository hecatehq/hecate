# Astro website recipes: deps, dev server, build, preview, lint, format.

# Install website dependencies.
website-install:
	cd website && bun install

# Start the Astro website dev server.
website-dev: _website-deps
	cd website && bun --bun run dev

# Typecheck the Astro website.
website-check: _website-deps
	cd website && bun --bun run check

# Run website lint checks.
website-lint: _website-deps
	cd website && bun --bun run lint

# Format website source files with Oxfmt.
website-format: _website-deps
	cd website && bun --bun run format

# Check website source formatting with Oxfmt.
website-format-check: _website-deps
	cd website && bun --bun run format:check

# Build the Astro website.
website-build: _website-deps
	cd website && bun --bun run build

# Preview the built Astro website.
website-preview: _website-deps
	cd website && bun --bun run preview
