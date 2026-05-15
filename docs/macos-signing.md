# macOS code signing and notarization

> **Audience: maintainers.** This doc is the setup + rotation checklist
> for whoever holds the project's Apple Developer credentials and
> manages the GitHub Secrets that drive the signing pipeline. End users
> downloading a built `.dmg` don't need to read this — they just install
> it like any other Mac app.

A `.dmg` produced by a release-workflow run is signed with a Developer
ID Application certificate and notarized by Apple **when** the seven
`APPLE_*` / `KEYCHAIN_PASSWORD` repo secrets are configured.
"Release-workflow run" means any invocation of
`.github/workflows/release.yml` — tag push (the common case) or
manual `workflow_dispatch` (re-runs / hotfixes); both pass a
non-empty `tagName` to the reusable workflow, satisfying the env
gate. Such bundles launch on a clean Mac with no Gatekeeper warning
and drag-install to `/Applications` without `xattr`-fiddling.

Builds without the secrets — PR validation in `test.yml`, manual
`tauri-build.yml` rebuilds, fork PRs, or releases cut before the secrets landed
— produce **unsigned** `.dmg`s. Those are not a pipeline failure; they're the
documented fallback when the signing inputs aren't available. Operators
downloading an unsigned build need to right-click `Hecate.app` → **Open** to
bypass Gatekeeper on first launch.

The CI workflow (`.github/workflows/_tauri-shared.yml`) reads the
secrets via env. Setup steps below are what gets the secrets in
place; once they land, the next release-workflow run is signed.

## Prerequisites

- **Apple Developer Program** membership ($99/year). Sign up at
  [developer.apple.com](https://developer.apple.com) if you haven't already.
- A Mac to generate the certificate from. The certificate request needs to
  originate from a Mac's Keychain Access (Apple's flow doesn't support
  generating the CSR elsewhere). After export, the rest is platform-agnostic.

## One-time setup

### 1. Create a Developer ID Application certificate

This is the cert that signs the `.app` bundle inside the `.dmg`. Apple
treats Developer ID certs as long-lived (5 years) and project-agnostic —
one cert can sign multiple apps, so you don't need a new one per project.

1. Open **Keychain Access** on your Mac.
2. **Keychain Access menu → Certificate Assistant → Request a Certificate
   From a Certificate Authority**.
3. Fill in:
   - User Email Address: your Apple ID email
   - Common Name: anything descriptive (e.g. `Sergey Rubanov — Hecate`)
   - CA Email Address: leave empty
   - Request is: **Saved to disk** (Continue)
4. Save the `.certSigningRequest` file somewhere temporary.
5. Go to
   [developer.apple.com/account/resources/certificates/add](https://developer.apple.com/account/resources/certificates/add).
6. Pick **Developer ID Application** → Continue.
7. Upload the `.certSigningRequest` from step 4.
8. Download the resulting `.cer` file.
9. Double-click the `.cer` to import into your login keychain. Keychain
   Access shows it as `Developer ID Application: <Your Name> (TEAM_ID)` in
   the Certificates pane.

### 2. Export the certificate as `.p12` for CI

CI runners don't have your keychain. They need a portable, password-protected
copy of the cert + private key.

1. Generate a strong export password and copy it:
   ```sh
   openssl rand -base64 32 | pbcopy
   ```
2. In Keychain Access, find the `Developer ID Application` cert.
3. Right-click → **Export "Developer ID Application: …"**.
4. Save As: `hecate-signing.p12`. Format: **Personal Information Exchange (.p12)**.
5. When prompted for the export password, paste the password from step 1.
   (You'll also be asked for your macOS user password — that's your login
   password, different from the export password.)
6. Re-encode the `.p12` for GitHub:
   ```sh
   base64 -i ~/Downloads/hecate-signing.p12 | pbcopy
   ```

### 3. Generate an app-specific password for notarization

Notarization is a separate step from signing — Apple scans the signed bundle
for malware and gates first-launch on a successful scan. tauri-action
authenticates to Apple's notary service using your Apple ID + an
**app-specific password**.

1. Visit
   [appleid.apple.com](https://appleid.apple.com) → **Sign-In and Security**
   → **App-Specific Passwords**.
2. Click **+**. Label: `Hecate CI Notarization`.
3. Copy the generated password (Apple shows it once — save it now).

### 4. Find your Team ID

10-character alphanumeric, e.g. `XXXXXXXXXX`.

- [developer.apple.com/account](https://developer.apple.com/account) →
  Membership → **Team ID**.

## GitHub repo secrets

In the repo: **Settings → Secrets and variables → Actions → New repository
secret**. Add all seven:

| Secret | Value | Source |
|---|---|---|
| `APPLE_CERTIFICATE` | base64 of the `.p12` | step 2.6 above |
| `APPLE_CERTIFICATE_PASSWORD` | the `.p12` export password | step 2.1 above |
| `APPLE_SIGNING_IDENTITY` | full identity name | from Keychain, e.g. `Developer ID Application: Sergey Rubanov (XXXXXXXXXX)` |
| `APPLE_ID` | your Apple ID email | the email you sign into developer.apple.com with |
| `APPLE_PASSWORD` | app-specific password | step 3.3 above |
| `APPLE_TEAM_ID` | 10-char Team ID | step 4 above |
| `KEYCHAIN_PASSWORD` | any random string | `openssl rand -hex 32 | pbcopy` |

`KEYCHAIN_PASSWORD` is for a **temporary keychain** tauri-action creates on
the macOS runner just for the duration of the build, so the imported `.p12`
doesn't pollute any persistent keychain. The value never matters again;
just needs to be set.

## Securely delete the local `.p12`

Once the secret is in GitHub, the local `.p12` is one of the more sensitive
files on your machine — it contains both the cert and the private key, only
protected by your export password. Wipe it after upload:

```sh
rm -P ~/Downloads/hecate-signing.p12
```

(`-P` overwrites with random bytes before unlinking. On APFS this is best-effort
since the filesystem may have written to other physical blocks; for stronger
guarantees use full-disk encryption, which macOS enables by default.)

## Verify the pipeline

After the next tagged release with all seven secrets in place:

1. Watch the `tauri / build (macos-latest, ...)` job in the release workflow
   run. The Tauri bundle step output should show `signing` and
   `notarizing` phases (not `skipping signing — APPLE_CERTIFICATE not set`).
2. The job's wall-clock grows by ~5–15 minutes for the notarization wait.
   Apple's notary service is usually quick but occasionally has multi-minute
   backlogs.
3. Download the `.dmg` from the GitHub Release page.
4. On a Mac that **doesn't** have your Developer ID cert in its keychain
   (a clean VM, a colleague's machine, your secondary user account):
   ```sh
   spctl -a -vv --type execute /Volumes/Hecate/Hecate.app
   ```
   Should print `accepted` plus `source=Notarized Developer ID`. Anything
   else (`rejected: …no usable signature`, `source=Developer ID` without
   `Notarized`) means the bundle is signed but not notarized — fix the
   pipeline before publishing. Then drag the app to `/Applications`,
   double-click, confirm there's no "Apple cannot check this app for
   malicious software" Gatekeeper dialog.
5. Verify the notarization staple is attached to the bundle:
   ```sh
   xcrun stapler validate /Volumes/Hecate/Hecate.app
   ```
   Should print `The validate action worked!`. A staple lets the bundle
   pass Gatekeeper offline; without it, Gatekeeper falls back to a live
   check against Apple's notary service on first launch.
6. (Optional) Inspect the signature itself:
   ```sh
   codesign -dv --verbose=4 /Volumes/Hecate/Hecate.app
   ```
   Look for `Authority=Developer ID Application: <Your Name>` in the
   chain. (Note: `codesign -dv` reports the signature, not the
   notarization status — use `spctl` and `stapler validate` above for
   notarization verification.)

## Rotating credentials

The `.p12` export password and the app-specific password can both be
rotated without invalidating the underlying certificate or Apple ID.

**`.p12` password:** re-export the cert from Keychain Access with a new
password (steps 2.1–2.6 above), then update `APPLE_CERTIFICATE` and
`APPLE_CERTIFICATE_PASSWORD` in GitHub Secrets. The signing identity
doesn't change.

**App-specific password:** revoke the old one at
[appleid.apple.com](https://appleid.apple.com) → App-Specific Passwords,
generate a new one, update `APPLE_PASSWORD` in GitHub Secrets.

**Certificate itself:** valid for 5 years. When it expires, repeat steps
1–2. If it's compromised earlier, revoke at
[developer.apple.com](https://developer.apple.com) → Certificates →
select the cert → **Revoke**, then issue a new one. Revoked certs cannot
sign new builds; previously-signed-and-notarized builds keep working
(Apple's notary attestation outlives the cert).

## Troubleshooting

**"User interaction is not allowed"** during the codesign step: the temp
keychain tauri-action creates is locked. `KEYCHAIN_PASSWORD` is either
missing from the repo Secrets or not flowing through correctly. The env
in `.github/workflows/_tauri-shared.yml` wires it as a conditional:

```yaml
KEYCHAIN_PASSWORD: ${{ matrix.os == 'macos-latest' && inputs.tagName != '' && secrets.KEYCHAIN_PASSWORD || '' }}
```

If the literal value is empty in the workflow log (tauri-action's
keychain-setup step prints it as `***`), one of:
- The secret isn't set in the repo.
- The job didn't satisfy the gate (not the macOS leg, or
  `inputs.tagName` was empty — i.e., a PR-validation run, which is
  intentionally unsigned).
- The caller didn't pass `secrets: inherit` (only `release.yml` does;
  PR validation deliberately doesn't — see
  `.github/workflows/tauri-build.yml` job-level comment).

**"errSecInternalComponent"** during signing: usually a corrupt `.p12`
upload. Re-export, re-base64, re-paste into GitHub Secrets. On macOS,
use `base64 -i your-cert.p12` (or `base64 < your-cert.p12`) and make
sure you copy the full encoded output into GitHub Secrets without
introducing extra whitespace or truncation.

**Notarization succeeds but app still shows Gatekeeper warning:** check
that the `.dmg` itself is signed (not just the `.app` inside). tauri-action
v0 signs both by default, but if you've customized the signing flags this
can break. `codesign -dv` on the `.dmg` should print authority info.

**Notarization stuck for >30 minutes:** Apple's notary service is slow
today. Don't recut the tag — wait. The job will time out at the runner's
6-hour limit if Apple really doesn't respond, at which point delete the
tag and retry once Apple's status page is green.
