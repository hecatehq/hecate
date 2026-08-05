#!/usr/bin/env bun

import { readFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dirname, "..");

function read(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

function fail(message: string): never {
  console.error(`release-workflow-check: ${message}`);
  process.exit(1);
}

function requireText(path: string, content: string, expected: string): void {
  if (!content.includes(expected)) {
    fail(`${path} must contain ${JSON.stringify(expected)}`);
  }
}

function forbidText(path: string, content: string, forbidden: string): void {
  if (content.includes(forbidden)) {
    fail(`${path} must not contain ${JSON.stringify(forbidden)}`);
  }
}

function forbidPattern(path: string, content: string, forbidden: RegExp, label: string): void {
  if (forbidden.test(content)) {
    fail(`${path} must not contain ${label}`);
  }
}

const tauriPath = ".github/workflows/_tauri-shared.yml";
const releasePath = ".github/workflows/release.yml";
const deliveryPath = ".github/workflows/release-delivery.yml";
const websitePath = ".github/workflows/website.yml";
const testPath = ".github/workflows/test.yml";
const tauriBuildPath = ".github/workflows/tauri-build.yml";
const releaseNotesHelperPath = "scripts/prepare-release-notes.ts";
const releaseJustPath = "just/release.just";
const goreleaserPath = ".goreleaser.yaml";
const tauriConfigPath = "tauri/src-tauri/tauri.conf.json";
const releaseScriptPath = "scripts/release.ts";
const releaseLinksScriptPath = "scripts/update-release-links.ts";

const tauri = read(tauriPath);
const release = read(releasePath);
const delivery = read(deliveryPath);
const website = read(websitePath);
const test = read(testPath);
const tauriBuild = read(tauriBuildPath);
const releaseNotesHelper = read(releaseNotesHelperPath);
const releaseJust = read(releaseJustPath);
const goreleaser = read(goreleaserPath);
const tauriConfig = read(tauriConfigPath);
const releaseScript = read(releaseScriptPath);
const releaseLinksScript = read(releaseLinksScriptPath);

requireText(goreleaserPath, goreleaser, "prerelease: false");
requireText(
  tauriConfigPath,
  tauriConfig,
  "https://github.com/hecatehq/hecate/releases/latest/download/latest.json",
);
forbidText(tauriConfigPath, tauriConfig, "https://hecate.sh/releases/alpha/latest.json");
requireText(releaseScriptPath, releaseScript, "version must be a stable vX.Y.Z tag");
requireText(releaseLinksScriptPath, releaseLinksScript, "tag must be a stable vX.Y.Z tag");

forbidText(tauriPath, tauri, "publish-updater-website:");
forbidText(tauriPath, tauri, "actions: write");
forbidText(tauriPath, tauri, "-name '*.tar.gz'");
forbidText(tauriPath, tauri, "-name '*.zip'");
requireText(tauriPath, tauri, "-name '*.app.tar.gz'");
requireText(tauriPath, tauri, "-name '*.AppImage.tar.gz'");
requireText(tauriPath, tauri, "-name '*.msi.zip'");
requireText(tauriPath, tauri, "-name '*.sig'");
requireText(tauriPath, tauri, `release_notes=$(jq -r '.body // ""' <<<"$release_metadata")`);
requireText(tauriPath, tauri, 'release_notes="${release_notes:0:12000}"');

forbidText(releasePath, release, "git push origin master");
requireText(releasePath, release, "uses: ./.github/workflows/release-delivery.yml");
requireText(releasePath, release, "expected_release_body_sha256:");
requireText(releasePath, release, "Release workflow requires a stable vX.Y.Z tag ref");
forbidText(releasePath, release, "actions: write");
requireText(
  releasePath,
  release,
  'release_args=$(bun scripts/prepare-release-notes.ts "$TAG" "$notes_path")',
);
forbidText(releasePath, release, "tag_notes=$(git for-each-ref");
requireText(releaseNotesHelperPath, releaseNotesHelper, "%(contents:size)");
requireText(releaseNotesHelperPath, releaseNotesHelper, "%(contents:signature)");
requireText(releaseNotesHelperPath, releaseNotesHelper, "writeFileSync(notesPath, annotation)");
requireText(testPath, test, "bun test scripts/prepare-release-notes.test.ts");
requireText(releaseJustPath, releaseJust, "bun test scripts/prepare-release-notes.test.ts");

const deliveryCallerStart = release.indexOf("  prepare-release-delivery:");
if (deliveryCallerStart < 0) {
  fail(`${releasePath} must define prepare-release-delivery`);
}
const deliveryCaller = release.slice(deliveryCallerStart);
requireText(releasePath, deliveryCaller, "contents: read");
forbidText(releasePath, deliveryCaller, "secrets: inherit");

requireText(deliveryPath, delivery, "permissions:\n  contents: read");
requireText(deliveryPath, delivery, "persist-credentials: false");
requireText(
  deliveryPath,
  delivery,
  "if: startsWith(github.ref, 'refs/tags/v') || github.ref == 'refs/heads/master'",
);
requireText(deliveryPath, delivery, "actions/upload-artifact@");
requireText(deliveryPath, delivery, "release-delivery.patch");
requireText(deliveryPath, delivery, "provenance.json");
requireText(deliveryPath, delivery, "allowed_paths:");
requireText(deliveryPath, delivery, "Release delivery requires a stable vX.Y.Z tag");
requireText(deliveryPath, delivery, "legacy_manifest=website/public/releases/alpha/latest.json");
requireText(deliveryPath, delivery, '[ "$TAG" = "v0.5.0" ]');
requireText(deliveryPath, delivery, "Legacy alpha updater manifest differs from the stable release asset.");
requireText(deliveryPath, delivery, "bridge_updated=${bridge_updated}");
requireText(deliveryPath, delivery, "The release workflow deliberately cannot push");
requireText(
  deliveryPath,
  delivery,
  `release_notes=$(jq -r '.body // ""' "\${RUNNER_TEMP}/release.json")`,
);
requireText(deliveryPath, delivery, 'expected_release_notes="${release_notes:0:12000}"');
requireText(deliveryPath, delivery, '--arg expected_notes "${expected_release_notes}"');
requireText(deliveryPath, delivery, "$manifest.notes == $expected_notes");
forbidText(deliveryPath, delivery, "contents: write");
forbidText(deliveryPath, delivery, "actions/create-github-app-token@");
forbidText(deliveryPath, delivery, "RELEASE_DELIVERY_APP_");
forbidText(deliveryPath, delivery, "github.event_name == 'workflow_call'");
forbidText(deliveryPath, delivery, "$manifest.notes == (($release[0].body //");
forbidPattern(
  deliveryPath,
  delivery,
  /\bgit(?:\s+-C\s+\S+)?\s+push(?:\s|$)/m,
  "a git push command",
);
forbidPattern(
  deliveryPath,
  delivery,
  /\bgh\s+pr\s+(?:create|edit|merge|review)(?:\s|$)/m,
  "a pull-request mutation command",
);

requireText(websitePath, website, "github.ref == 'refs/heads/master'");
forbidText(websitePath, website, "Verify updater manifest is live");
forbidText(websitePath, website, "release_manifest_sha256:");
requireText(websitePath, website, "Verify committed alpha migration bridge");
requireText(websitePath, website, "Verify alpha bridge is live");
requireText(websitePath, website, 'git diff --name-only "${EVENT_BEFORE}" "${GITHUB_SHA}"');
requireText(
  websitePath,
  website,
  "https://github.com/hecatehq/hecate/releases/download/v0.5.0/latest.json",
);
forbidText(testPath, test, "actions: write");
forbidText(tauriBuildPath, tauriBuild, "actions: write");

const trailingNewlineProbe = Bun.spawnSync([
  "bash",
  "-c",
  [
    "set -euo pipefail",
    `release_notes=$(printf 'alpha.4 notes\\n\\n')`,
    'release_notes="${release_notes:0:12000}"',
    `printf '%s' "$release_notes"`,
  ].join("\n"),
]);
if (trailingNewlineProbe.exitCode !== 0) {
  fail(
    `release-note trailing-newline probe failed: ${new TextDecoder().decode(
      trailingNewlineProbe.stderr,
    )}`,
  );
}
const normalizedSyntheticNotes = new TextDecoder().decode(trailingNewlineProbe.stdout);
if (normalizedSyntheticNotes !== "alpha.4 notes") {
  fail(
    `release-note normalization must strip trailing newlines before truncation; got ${JSON.stringify(
      normalizedSyntheticNotes,
    )}`,
  );
}

console.log(
  "release-workflow-check: stable tags, GitHub updater manifests, read-only delivery proposals, and scoped updater uploads OK",
);
