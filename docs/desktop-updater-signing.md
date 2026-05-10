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
`tauri/src-tauri/tauri.conf.json`. Without a keypair, the updater
stays inert and existing installs never see an update banner.

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
them to sign the platform bundles + emit `latest.json`. Both
secrets are scoped to `inputs.tagName != ''` in
`.github/workflows/_tauri-shared.yml`, so PR-validation runs of
`tauri-build.yml` never see them.

### 3. Commit the public key + flip `active`

Edit `tauri/src-tauri/tauri.conf.json`:

```json
"updater": {
  "active": true,
  "pubkey": "<paste contents of ~/.tauri/hecate-updater.key.pub>",
  "endpoints": [
    "https://github.com/hecatehq/hecate/releases/latest/download/latest.json"
  ]
}
```

Open a one-line PR with that change. After it merges and the
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
3. Inspect the manifest — should look roughly like:
   ```json
   {
     "version": "0.1.0-alpha.24",
     "notes": "...",
     "pub_date": "...",
     "platforms": {
       "darwin-aarch64": { "signature": "...", "url": "..." },
       "linux-x86_64":   { "signature": "...", "url": "..." },
       "windows-x86_64": { "signature": "...", "url": "..." }
     }
   }
   ```
4. On a Mac with the previous version installed: launch the app,
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

**`latest.json` is missing from the release.** tauri-action
didn't get the signing secrets. Confirm both
`TAURI_UPDATER_PRIVATE_KEY` and `TAURI_UPDATER_PRIVATE_KEY_PASSWORD`
are set, and that you're cutting a tag (not running PR
validation, which intentionally skips signing — see
`tauri-build.yml`).

**Banner never appears even on a known-old install.** Possible
causes, in rough order:

- `active: false` in the bundle's `tauri.conf.json` — needs the
  one-line flip.
- The bundle was shipped without the pubkey embedded.
- Network / GitHub release fetch failed (the hook silently
  swallows errors; check the webview console in dev builds).
