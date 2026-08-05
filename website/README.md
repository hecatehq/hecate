# Hecate Website

This directory contains the Astro homepage for [hecate.sh](https://hecate.sh).
It is separate from the embedded operator UI in `../ui`: `website/` builds the
public site, while `ui/` is bundled into the `hecate` runtime binary.

## Development

Run website commands from the repository root:

```bash
just website-install
just website-dev       # local Astro dev server
just website-check     # astro check + TypeScript
just website-build     # production build
just website-test-e2e  # desktop + phone browser and accessibility checks
just website-preview   # preview website/dist
```

The `just` recipes force package scripts through Bun (`bun --bun run ...`) so
Astro does not accidentally use a mismatched system Node runtime.

The browser suite runs against the production preview in Chromium. Install the
matching browser once before the first local run:

```bash
cd website
bunx playwright install chromium
```

It covers semantic navigation, metadata, release-link wording, responsive
overflow, image loading, the zero-client-JavaScript boundary, and automated
WCAG A/AA checks. Visual judgment, real Safari/Firefox rendering, screen-reader
behavior, and external-link availability remain manual checks.

## Deployment

Website-only pull requests run the dedicated `Website` GitHub Actions workflow.
The main Hecate `Test` workflow ignores `website/**`, so Go/Rust/UI jobs do not
run for website-only changes.

Pushes to `master` that touch `website/**` deploy `website/dist` to GitHub
Pages through `.github/workflows/website.yml`.

## Domain

GitHub Pages is configured for the custom domain `hecate.sh`; keep
`public/CNAME` set to that value.

DNS for the apex domain should use GitHub Pages' documented `A`/`AAAA` records.
The `www` subdomain should use the organization-level Pages target:

```text
www CNAME hecatehq.github.io
```
