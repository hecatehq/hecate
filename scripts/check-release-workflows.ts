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

const tauri = read(tauriPath);
const release = read(releasePath);
const delivery = read(deliveryPath);
const website = read(websitePath);
const test = read(testPath);
const tauriBuild = read(tauriBuildPath);

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
forbidText(releasePath, release, "actions: write");

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
requireText(websitePath, website, "Verify updater manifest is live");
requireText(websitePath, website, "release_manifest_version:");
requireText(websitePath, website, "release_manifest_sha256:");
requireText(websitePath, website, "WANT_SHA256:");
requireText(websitePath, website, 'sha256sum "${live_manifest}"');
requireText(websitePath, website, '[ "$live_sha256" = "$WANT_SHA256" ]');
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
  "release-workflow-check: read-only delivery proposal, scoped updater uploads, and post-merge website verification OK",
);
