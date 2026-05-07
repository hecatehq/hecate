# Hecate Website

This is the Astro homepage for [hecate.sh](https://hecate.sh). It lives in the
main Hecate repository so product copy, release links, and brand assets can move
with the runtime they describe.

## Development

```bash
just website-install
just website-dev
```

The local Astro dev server prints its URL on start. Production builds use:

```bash
just website-build
```

The website is intentionally independent from the embedded operator UI in
`ui/`: this directory builds the public marketing site, while `ui/` is bundled
into the `hecate` gateway binary.

## Deployment

Pushes to `master` that touch `website/**` deploy `website/dist` to GitHub
Pages through `.github/workflows/website.yml`. The custom domain is set by
`public/CNAME` and should remain `hecate.sh`.
