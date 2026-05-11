# Desktop updater signing

> **Audience: maintainers.** This is the setup + rotation
> checklist for the Tauri updater keypair that signs auto-update
> manifests. End users downloading a built `.dmg` / `.deb` /
> `.AppImage` / `.msi` don't need to read this — the in-app
> updater verifies signatures transparently when it pulls
> `latest.json` from the GitHub Release.

The Tauri updater plugin verifies that every update payload is
signed by a private key that matches the public key embedded in
the shipping bundle. The signing happens during the release-
workflow build; the public key is committed to
`tauri/src-tauri/tauri.conf.json`. Two prerequisites for a
working pipeline:

1. **`bundle.createUpdaterArtifacts: "v1Compatible"`** in
   `tauri/src-tauri/tauri.conf.json`. Without this, the bundler
   produces no updater artifacts even when the signing key is
   present, so `sign_updaters` has nothing to sign and the build
   ships unsigned. The committed config already sets this.
2. **`TAURI_UPDATER_PRIVATE_KEY` + `TAURI_UPDATER_PRIVATE_KEY_PASSWORD`**
   in GitHub Secrets. Without the keypair, the updater stays
   inert and existing installs never see an update banner.

This doc walks through generating the keypair once, storing the
secrets, and flipping `active: true`. Subsequent releases then
auto-emit a signed `latest.json` manifest.

## Prerequisites

- A maintainer Mac (or any machine with `bun` available).
- Write access to `hecatehq/hecate` GitHub Secrets.

## One-time setup

### 1. Generate the keypair

Run on your local machine:

```sh
bunx @tauri-apps/cli signer generate -w ~/.tauri/hecate-updater.key
```

The CLI prompts for a password — pick a strong one and save it
somewhere you can retrieve later (1Password, etc.). The output:

- `~/.tauri/hecate-updater.key` — the private key, encrypted with
  the password you just chose.
- `~/.tauri/hecate-updater.key.pub` — the public key.

The private key never leaves your machine in plaintext; it'll
travel through GitHub Secrets in its encrypted form.

### 2. Add the GitHub Secrets

In the repo: **Settings → Secrets and variables → Actions → New
repository secret**. Add two:

| Secret | Value | Source |
|---|---|---|
| `TAURI_UPDATER_PRIVATE_KEY` | full contents of `~/.tauri/hecate-updater.key` | step 1 |
| `TAURI_UPDATER_PRIVATE_KEY_PASSWORD` | the password from step 1 | step 1 |

The release workflow's tauri-action step picks these up and uses
them to sign the platform bundles (`.app.tar.gz`, `.AppImage`,
`.msi` and their wrapped variants). Then two follow-on jobs in
`_tauri-shared.yml` ship the manifest:

1. **`publish-updater-manifest`** — stitches the per-platform
   `.sig` files into a single `latest.json` and uploads it to the
   GitHub Release. tauri-action itself can't build the manifest in
   matrix mode because each leg only sees its own signature; this
   job runs once after the matrix completes.
2. **`publish-updater-website`** — drops the same `latest.json`
   into `website/public/releases/alpha/latest.json` and commits to
   master. The website workflow's path filter (`website/**`) picks
   up the commit, rebuilds Astro, and deploys to GitHub Pages. The
   manifest is then served at
   `https://hecate.sh/releases/alpha/latest.json`, which is the URL
   the in-app updater is configured to read. A verification step
   blocks until the new manifest is live on `hecate.sh` (cap 10
   min; CI fails loud if it overruns).

Both secrets are scoped to `inputs.tagName != ''` in
`.github/workflows/_tauri-shared.yml`, so PR-validation runs of
`tauri-build.yml` never see them.

Why the dedicated `hecate.sh` channel instead of GitHub's
`/releases/latest/` redirect: every release stays a GitHub
pre-release until `v1.0.0` (see [release.md](release.md#pre-release-policy)),
and GitHub refuses to resolve `/releases/latest/` to a pre-release.
The dedicated channel sidesteps that constraint and gives us a
natural place to add `releases/beta/latest.json` and
`releases/stable/latest.json` later.

### 3. Commit the public key + flip `active`

Edit `tauri/src-tauri/tauri.conf.json`:

```json
"updater": {
  "active": true,
  "pubkey": "<paste contents of ~/.tauri/hecate-updater.key.pub>",
  "endpoints": [
    "https://hecate.sh/releases/alpha/latest.json"
  ]
}
```

Open a PR with that change. After it merges and the
next release tag is cut, the release workflow produces a signed
`latest.json` and existing installs (built with the same pubkey
in their bundle) detect and apply the update.

## Securely store the local key

The private key file is encrypted but the password is in your
chain too — together they unlock signing. Treat the pair like
the Apple `.p12` cert: keep both safe, ideally in a password
manager that supports file attachments. If you lose either, the
recovery is "generate a new pair, ship a new pubkey, accept that
operators on the old pubkey lose auto-update until they manually
reinstall."

## Verify the pipeline

After the next tagged release with both secrets configured and
`active: true`:

1. Watch the `tauri / build (...)` jobs. The bundle step output
   should mention signing and emit a `*.sig` file alongside each
   bundle.
2. After `goreleaser` and `tauri` jobs finish, the GitHub Release
   page should have `latest.json` listed as an asset alongside
   the platform bundles.
3. Watch the `publish-updater-website` job. Its final step polls
   `https://hecate.sh/releases/alpha/latest.json` and only exits
   green once the manifest at that URL matches the version being
   released. If the job fails on that step, the website
   redeploy didn't propagate within 10 minutes — see the
   troubleshooting section below.
4. Inspect the manifest at the canonical URL:
   ```bash
   curl -sL https://hecate.sh/releases/alpha/latest.json | jq .
   ```
   Should look roughly like:
   ```json
   {
     "version": "0.1.0-alpha.28",
     "pub_date": "...",
     "platforms": {
       "darwin-aarch64": { "signature": "...", "url": "..." },
       "linux-x86_64":   { "signature": "...", "url": "..." },
       "windows-x86_64": { "signature": "...", "url": "..." }
     }
   }
   ```
5. On a Mac with the previous version installed: launch the app,
   wait a few seconds, and the "Hecate X.Y.Z is available" banner
   should appear at the top of the workspace. Click **Install and
   Restart** — the new bundle downloads, replaces the running
   app, and relaunches. The banner does not reappear post-update.

## Rotating the keypair

If you suspect the private key is compromised, or it's been more
than a couple of years, rotate:

1. Generate a new pair (step 1).
2. Update both GitHub Secrets (step 2).
3. Commit the new pubkey to `tauri.conf.json` (step 3).
4. Cut a release.

Operators on the old pubkey **will not be able to auto-update to
the new pubkey's bundles** — Tauri's updater rejects payloads
signed by anything other than the embedded pubkey. Those
operators have to reinstall manually from the GitHub Release
page. There's no way around this; pubkey rotation is intentionally
disruptive so a leaked key can't be used to push malicious updates
to existing installs forever.

For low-stakes rotation (key not actually leaked, just hygiene),
don't bother — the updater plugin's signature scheme is built to
make long-lived keypairs safe.

## Troubleshooting

**"signature verification failed" in the updater log on a client
machine.** The manifest's pubkey doesn't match the bundled
pubkey. Either the operator's bundle was built before the pubkey
landed, or the keypair was rotated and the old client is on the
wrong side of the rotation. Reinstalling from the GitHub Release
page resolves it.

**`latest.json` is missing from the release.** Three failure
modes, in rough order of likelihood:

- `bundle.createUpdaterArtifacts` is missing or `false` in
  `tauri.conf.json` — the bundler produced no updater payloads,
  nothing got signed, and the `publish-updater-manifest` job
  failed on its `missing updater signature(s)` check. Restore
  the `"v1Compatible"` setting.
- `TAURI_UPDATER_PRIVATE_KEY` and/or `TAURI_UPDATER_PRIVATE_KEY_PASSWORD`
  are not set in repo secrets — the build proceeds unsigned and
  the same missing-signature check trips. Confirm both secrets.
- You're running PR validation (`tauri-build.yml`) instead of a
  release tag. PR validation intentionally skips signing and
  manifest publishing — that's working as designed.

**`publish-updater-website` failed at "Verify manifest is live at
hecate.sh".** The release published, the GitHub Release has its
`latest.json`, but the website didn't propagate the new content
within 10 minutes. Walk down:

- Check the website workflow run for the master commit
  `publish updater manifest for vX.Y.Z`. If it failed, fix the
  failure (Astro build error, Pages deploy permission issue,
  etc.) and re-run.
- If the website workflow succeeded but
  `https://hecate.sh/releases/alpha/latest.json` still serves
  stale content, the Fastly cache is stuck. Force a cache
  invalidation via **Settings → Pages → Visit site** in the GitHub
  UI (it triggers a CDN purge). Re-run `publish-updater-website`
  in the release workflow to re-poll.
- If both succeeded but the file content on `hecate.sh` doesn't
  match the release tag, the master commit wasn't created or was
  reverted. Inspect master's history; the commit should appear in
  the release tag's wake.

**Banner never appears even on a known-old install.** Possible
causes, in rough order:

- `active: false` in the bundle's `tauri.conf.json` — needs the
  one-line flip.
- The bundle was shipped without the pubkey embedded.
- The bundle was built with a stale `endpoints` URL (e.g. the
  old `/releases/latest/download/latest.json`) that no longer
  resolves now that the pre-release policy is in force. Bundles
  from alpha.21–27 fall in this category; the alpha.28 bridge
  release is the one-time migration to the `hecate.sh` channel.
- Network / fetch error against `hecate.sh` (the hook silently
  swallows errors; check the webview console in dev builds).
