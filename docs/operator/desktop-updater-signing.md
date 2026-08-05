# Desktop updater signing

> **Audience: maintainers.** This is the setup + rotation
> checklist for the Tauri updater keypair that signs auto-update
> payloads. End users downloading a built `.dmg` / `.deb` /
> `.AppImage` / `.msi` don't need to read this — the in-app
> updater reads `latest.json` from
> `https://github.com/hecatehq/hecate/releases/latest/download/latest.json` and verifies the
> referenced payload's signature before installation.

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
   inert and existing installs never see an update indicator.

This doc walks through generating the keypair once, storing the
secrets, and flipping `active: true`. Subsequent releases then
auto-emit a `latest.json` manifest containing signed payload references and
signatures on the GitHub Release.

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

| Secret                               | Value                                          | Source |
| ------------------------------------ | ---------------------------------------------- | ------ |
| `TAURI_UPDATER_PRIVATE_KEY`          | full contents of `~/.tauri/hecate-updater.key` | step 1 |
| `TAURI_UPDATER_PRIVATE_KEY_PASSWORD` | the password from step 1                       | step 1 |

The release workflow's tauri-action step picks these up and uses
them to sign the platform bundles (`.app.tar.gz`, `.AppImage`,
`.msi` and their wrapped variants). Packaging and protected-branch
delivery then proceed separately:

1. **`publish-updater-manifest`** — stitches the per-platform
   `.sig` files into a single `latest.json` and uploads it to the
   GitHub Release. tauri-action itself can't build the manifest in
   matrix mode because each leg only sees its own signature; this
   job runs once after the matrix completes.
2. **`release-delivery.yml`** — downloads that canonical Release
   asset, validates the version, platform signatures, referenced
   assets, and release-body digest, then uploads an allowlisted
   patch plus provenance containing refreshed release links. A maintainer
   applies it on current `master` and opens the human-reviewed PR, so normal
   checks run without an App/PAT secret or branch-rules bypass. For `v0.5.0`
   only, the proposal also updates the alpha.28+ website bridge.

This verifies signed updater artifact production and manifest publication. It
does not prove the Linux or Windows desktop updater path works on a real
machine yet; only the macOS desktop update flow is currently exercised.

Both secrets are scoped to `inputs.tagName != ''` in
`.github/workflows/_tauri-shared.yml`, so PR-validation runs of
`tauri-build.yml` never see them.

Stable Hecate releases use GitHub's native latest-release route. This removes
the duplicated website updater channel while retaining the Release asset as
the signed, canonical source of update metadata. The alpha.28+ website path is
retained only as a one-time bridge to `v0.5.0`.

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

Open a PR with that change. After it merges and the
next release tag is cut, the release workflow produces a
`latest.json` manifest that references signed platform payloads.
Existing installs built with the same pubkey detect and apply the
update after verifying the downloaded payload.

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
3. Download the release workflow's `release-delivery-<tag>` artifact,
   verify its provenance, apply the patch on current `master`, and
   open the delivery PR. Require latest-push approval plus green
   Required checks and Links runs before merge.
4. Inspect the manifest at the canonical URL:
   ```bash
   curl -sL https://github.com/hecatehq/hecate/releases/latest/download/latest.json | jq .
   ```
   Should look roughly like:
   ```json
   {
     "version": "0.5.0",
     "pub_date": "...",
     "notes": "Release notes from the published GitHub Release...",
     "platforms": {
       "darwin-aarch64": { "signature": "...", "url": "..." },
       "linux-x86_64": { "signature": "...", "url": "..." },
       "windows-x86_64": { "signature": "...", "url": "..." }
     }
   }
   ```
   `notes` is bounded plain-text release metadata. It is not part of the
   updater payload signature and must never be treated as a verification
   signal; the client verifies the downloaded package before installation.
5. On a Mac with the previous version installed: launch the app,
   wait a few seconds, and the **Updates** control in the status bar
   should show the available version. Open it to confirm the version,
   date, and notes, then click **Install and restart** — the new bundle
   downloads, replaces the running app, and relaunches. The update indicator
   does not reappear post-update.

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
- You're running PR validation (`test.yml`'s desktop bundle gate) or a manual
  `tauri-build.yml` rebuild instead of a release tag. Validation builds
  intentionally skip signing and manifest publishing — that's working as
  designed.

**The release-delivery proposal was not merged.** The release may still be valid:
confirm the GitHub Release has its platform bundles, signatures, and
three-platform `latest.json`. If those are complete, do not delete or retag.
From `master`, dispatch the recovery workflow:

```bash
gh workflow run release-delivery.yml \
  --repo hecatehq/hecate \
  --ref master \
  -f tag=vX.Y.Z
```

Download `release-delivery-vX.Y.Z` from the resulting run, verify its
`provenance.json`, apply `release-delivery.patch` on current `master`, and open
and merge the resulting PR normally.

**Update control never shows an update on a known-old install.** Possible
causes, in rough order:

- `active: false` in the bundle's `tauri.conf.json` — needs the
  one-line flip.
- The bundle was shipped without the pubkey embedded.
- The bundle was built with a retired alpha website endpoint. Alpha.28+ builds
  use the one-time `v0.5.0` bridge; reinstall from the stable Release page if
  that migration was skipped or the local version predates it.
- Network / fetch error against GitHub. Automatic checks log failures
  without interrupting the operator; an explicit check shows a safe retry
  message. Inspect the webview console or app log in dev builds.

**The update details dialog reaches `Downloading... 100%` / `Finishing installation...`
and never relaunches.** The updater payload was downloaded, but the app
did not complete the restart handoff. Confirm the UI watchdog in
`ui/src/lib/desktop-update.ts` is present, the UI calls
`@tauri-apps/plugin-process` `relaunch()`, `tauri_plugin_process::init()`
is registered in the Rust builder, and the default capability includes
`process:allow-restart`. The app log should include
`desktop updater install started`, `download finished`, and either
`install finished; relaunching` or `install did not resolve after
download; relaunching to finish`.
