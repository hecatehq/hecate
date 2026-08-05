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
const releaseNotesInputPath = "scripts/release-notes.ts";
const releaseNotesInputTestPath = "scripts/release-notes.test.ts";
const releaseJustPath = "just/release.just";
const releaseGuidePath = "docs/contributor/release.md";
const goreleaserPath = ".goreleaser.yaml";
const tauriConfigPath = "tauri/src-tauri/tauri.conf.json";
const releaseScriptPath = "scripts/release.ts";
const releaseLinksScriptPath = "scripts/update-release-links.ts";
const cloudConnectionPath = "tauri/src-tauri/src/desktop/cloud_connection.rs";
const updaterVerifierPath = "tauri/src-tauri/examples/verify_updater_signatures.rs";

const tauri = read(tauriPath);
const release = read(releasePath);
const delivery = read(deliveryPath);
const website = read(websitePath);
const test = read(testPath);
const tauriBuild = read(tauriBuildPath);
const releaseNotesHelper = read(releaseNotesHelperPath);
const releaseNotesInput = read(releaseNotesInputPath);
const releaseNotesInputTest = read(releaseNotesInputTestPath);
const releaseJust = read(releaseJustPath);
const releaseGuide = read(releaseGuidePath);
const goreleaser = read(goreleaserPath);
const tauriConfig = read(tauriConfigPath);
const releaseScript = read(releaseScriptPath);
const releaseLinksScript = read(releaseLinksScriptPath);
const cloudConnection = read(cloudConnectionPath);
const updaterVerifier = read(updaterVerifierPath);

requireText(goreleaserPath, goreleaser, "prerelease: false");
requireText(
  tauriConfigPath,
  tauriConfig,
  "https://github.com/hecatehq/hecate/releases/latest/download/latest.json",
);
forbidText(tauriConfigPath, tauriConfig, "https://hecate.sh/releases/alpha/latest.json");
requireText(releaseScriptPath, releaseScript, "version must be a stable vX.Y.Z tag");
requireText(releaseScriptPath, releaseScript, "--notes <path>");
requireText(releaseScriptPath, releaseScript, '"--cleanup=verbatim"');
requireText(releaseScriptPath, releaseScript, '"-F", "-", version, releaseCommit');
requireText(releaseScriptPath, releaseScript, "input: notesInStampedCommit.bytes");
requireText(releaseScriptPath, releaseScript, '"hash-object", `--path=${notes.relativePath}`');
forbidText(releaseScriptPath, releaseScript, '"commit", "--only"');
requireText(releaseScriptPath, releaseScript, "GIT_INDEX_FILE: temporaryIndex");
requireText(releaseScriptPath, releaseScript, '["read-tree", localCommit]');
requireText(
  releaseScriptPath,
  releaseScript,
  'capturedStampTree = execFileSync("git", ["write-tree"]',
);
requireText(releaseScriptPath, releaseScript, "releaseTree !== capturedStampTree");
requireText(releaseScriptPath, releaseScript, "realIndexTree !== reviewedTree");
requireText(releaseScriptPath, releaseScript, '["read-tree", releaseCommit]');
requireText(releaseScriptPath, releaseScript, 'revision = "HEAD"');
requireText(releaseScriptPath, releaseScript, "`${revision}:${relativePath}`");
requireText(releaseScriptPath, releaseScript, "headBeforeStamp !== localCommit");
requireText(releaseScriptPath, releaseScript, "let releaseCommit = localCommit");
requireText(
  releaseScriptPath,
  releaseScript,
  "stampParents.length !== 2 || stampParents[1] !== localCommit",
);
requireText(
  releaseScriptPath,
  releaseScript,
  "changedStampPaths.filter((path) => !stampPaths.includes(path))",
);
requireText(releaseScriptPath, releaseScript, "unstampedHead !== localCommit");
requireText(releaseScriptPath, releaseScript, "headAfterStamp !== releaseCommit");
requireText(
  releaseScriptPath,
  releaseScript,
  "notesAtTag.relativePath,\n  version,\n  releaseCommit",
);
requireText(releaseScriptPath, releaseScript, '"--atomic"');
requireText(releaseScriptPath, releaseScript, "`${releaseCommit}:refs/heads/${branch}`");
requireText(releaseScriptPath, releaseScript, "`refs/tags/${version}:refs/tags/${version}`");
forbidText(releaseScriptPath, releaseScript, "`HEAD:${branch}`");
requireText(releaseScriptPath, releaseScript, "docs/contributor/release.md#recovery");
forbidText(
  releaseScriptPath,
  releaseScript,
  "git push --delete origin ${version} && git tag -d ${version}",
);
forbidText(releaseScriptPath, releaseScript, '["tag", "-a", version, "-m", version]');
const releaseCommitCapture = releaseScript.indexOf("let releaseCommit = localCommit");
const explicitTag = releaseScript.indexOf('["tag", "-a", "--cleanup=verbatim"');
const atomicPush = releaseScript.indexOf('"--atomic"', explicitTag);
if (
  releaseCommitCapture < 0 ||
  explicitTag < 0 ||
  atomicPush < 0 ||
  !(releaseCommitCapture < explicitTag && explicitTag < atomicPush)
) {
  fail(`${releaseScriptPath} must pin the release commit before tagging and atomic publication`);
}
requireText(releaseLinksScriptPath, releaseLinksScript, "tag must be a stable vX.Y.Z tag");

requireText(releaseGuidePath, releaseGuide, 'gh release delete "$failed"');
requireText(releaseGuidePath, releaseGuide, "--cleanup-tag --yes");
requireText(releaseGuidePath, releaseGuide, "--paginate --slurp");
requireText(releaseGuidePath, releaseGuide, "`read:packages`");
requireText(releaseGuidePath, releaseGuide, "`write:packages`");
requireText(releaseGuidePath, releaseGuide, "`delete:packages`");
requireText(releaseGuidePath, releaseGuide, "docker buildx imagetools create");
requireText(releaseGuidePath, releaseGuide, 'test "$latest_digest" = "$last_good_digest"');
forbidText(
  releaseGuidePath,
  releaseGuide,
  "Tag deletion on GitHub also clears the dangling Release entry",
);
forbidText(releaseGuidePath, releaseGuide, "last_good=0.2.0-alpha.4");

forbidText(tauriPath, tauri, "publish-updater-website:");
forbidText(tauriPath, tauri, "actions: write");
forbidText(tauriPath, tauri, "-name '*.tar.gz'");
forbidText(tauriPath, tauri, "-name '*.zip'");
requireText(tauriPath, tauri, "-name '*.app.tar.gz'");
requireText(tauriPath, tauri, "-name '*.AppImage.tar.gz'");
requireText(tauriPath, tauri, "-name '*.msi.zip'");
requireText(tauriPath, tauri, "-name '*.sig'");
requireText(tauriPath, tauri, "Require macOS release-signing credentials");
requireText(tauriPath, tauri, "missing required macOS release credential");
requireText(tauriPath, tauri, "Verify signed macOS release identity");
requireText(tauriPath, tauri, "codesign --verify --deep --strict --verbose=4");
requireText(tauriPath, tauri, "TeamIdentifier=${EXPECTED_TEAM_ID}");
requireText(tauriPath, tauri, "EXPECTED_TEAM_ID: HHRFM4BVMT");
forbidText(tauriPath, tauri, "EXPECTED_TEAM_ID: ${{ secrets.APPLE_TEAM_ID }}");
requireText(
  cloudConnectionPath,
  cloudConnection,
  'certificate leaf[subject.OU] = \\"HHRFM4BVMT\\"',
);
requireText(tauriPath, tauri, "spctl --assess --type execute --verbose=4");
requireText(tauriPath, tauri, 'set -- "${bundle}"/macos/*.app.tar.gz');
requireText(tauriPath, tauri, 'set -- "${bundle}"/dmg/*.dmg');
requireText(tauriPath, tauri, 'codesign --verify --strict --verbose=4 "${dmg}"');
requireText(tauriPath, tauri, 'xcrun notarytool submit "${dmg}"');
requireText(tauriPath, tauri, 'xcrun stapler staple "${dmg}"');
requireText(tauriPath, tauri, 'xcrun stapler validate "${dmg}"');
requireText(
  tauriPath,
  tauri,
  "spctl --assess --type open --context context:primary-signature --verbose=4",
);
requireText(tauriPath, tauri, "Verify updater payload signatures");
requireText(tauriPath, tauri, "--example verify_updater_signatures");
requireText(updaterVerifierPath, updaterVerifier, "plugins/updater/pubkey");
requireText(updaterVerifierPath, updaterVerifier, ".verify(&payload, &signature, true)");
requireText(tauriPath, tauri, "-name '*.dmg'");
requireText(tauriPath, tauri, "-name '*.deb'");
requireText(tauriPath, tauri, "-name '*.AppImage'");
requireText(tauriPath, tauri, "-name '*.msi'");
requireText(tauriPath, tauri, `release_notes=$(jq -r '.body // ""' <<<"$release_metadata")`);
requireText(tauriPath, tauri, 'release_notes="${release_notes:0:12000}"');

const signedBuildStep = tauri.indexOf("      - name: Build Tauri bundles (signed release)");
const signedVerificationStep = tauri.indexOf("      - name: Verify signed macOS release identity");
const updaterVerificationStep = tauri.indexOf("      - name: Verify updater payload signatures");
const verifiedPublishStep = tauri.indexOf("      - name: Publish verified release assets");
if (
  signedBuildStep < 0 ||
  signedVerificationStep < 0 ||
  updaterVerificationStep < 0 ||
  verifiedPublishStep < 0 ||
  !(
    signedBuildStep < signedVerificationStep &&
    signedVerificationStep < updaterVerificationStep &&
    updaterVerificationStep < verifiedPublishStep
  )
) {
  fail(
    `${tauriPath} must verify signed macOS artifacts and updater signatures after building and before publication`,
  );
}
const signedBuild = tauri.slice(signedBuildStep, signedVerificationStep);
const signedBuildInputs = signedBuild.slice(signedBuild.indexOf("        with:"));
forbidText(tauriPath, signedBuildInputs, "releaseId:");
forbidText(tauriPath, signedBuildInputs, "tagName:");
const workflowArtifactStep = tauri.indexOf(
  "      - name: Upload bundles as workflow artifacts",
  verifiedPublishStep,
);
if (workflowArtifactStep < 0) {
  fail(`${tauriPath} must retain the workflow-artifact upload boundary`);
}
const verifiedPublish = tauri.slice(verifiedPublishStep, workflowArtifactStep);
requireText(tauriPath, verifiedPublish, "gh release upload");

forbidText(releasePath, release, "git push origin master");
requireText(releasePath, release, "uses: ./.github/workflows/release-delivery.yml");
requireText(releasePath, release, "expected_release_body_sha256:");
requireText(releasePath, release, "Release workflow requires a stable vX.Y.Z tag ref");
forbidText(releasePath, release, "actions: write");

const validateReleaseStart = release.indexOf("  validate-release-ref:");
const mobileStart = release.indexOf("  mobile:", validateReleaseStart);
if (validateReleaseStart < 0 || mobileStart < 0) {
  fail(`${releasePath} must validate release tags before mobile and publication jobs`);
}
const validateRelease = release.slice(validateReleaseStart, mobileStart);
requireText(releasePath, validateRelease, "contents: read");
requireText(releasePath, validateRelease, "persist-credentials: false");
requireText(
  releasePath,
  validateRelease,
  'git fetch --no-tags --force origin "refs/heads/master:refs/remotes/origin/master"',
);
requireText(releasePath, validateRelease, '"refs/tags/${REF_NAME}^{commit}"');
requireText(
  releasePath,
  validateRelease,
  'git merge-base --is-ancestor "$tag_commit" refs/remotes/origin/master',
);
const validateCheckout = validateRelease.indexOf("Checkout release history");
const validateFetch = validateRelease.indexOf("git fetch --no-tags --force origin");
const validateContainment = validateRelease.indexOf("git merge-base --is-ancestor");
if (
  validateCheckout < 0 ||
  validateFetch < 0 ||
  validateContainment < 0 ||
  !(validateCheckout < validateFetch && validateFetch < validateContainment)
) {
  fail(`${releasePath} must fetch master before checking release-tag containment`);
}
requireText(
  releasePath,
  release,
  'release_args=$(bun scripts/prepare-release-notes.ts "$TAG" "$notes_path")',
);
forbidText(releasePath, release, "tag_notes=$(git for-each-ref");
requireText(releaseNotesHelperPath, releaseNotesHelper, "%(contents:size)");
requireText(releaseNotesHelperPath, releaseNotesHelper, "%(contents:signature)");
requireText(releaseNotesHelperPath, releaseNotesHelper, "writeFileSync(notesPath, annotation)");
requireText(
  releaseNotesHelperPath,
  releaseNotesHelper,
  "validateCuratedReleaseNotes(markdown, tag)",
);
requireText(releaseNotesInputPath, releaseNotesInput, "MAX_RELEASE_NOTES_CHARACTERS = 12_000");
requireText(releaseNotesInputPath, releaseNotesInput, "exactly one ## Highlights section");
requireText(
  releaseNotesInputTestPath,
  releaseNotesInputTest,
  "requires one to six highlight bullets",
);
const releaseNotesTestCommand =
  "bun test scripts/release-notes.test.ts scripts/prepare-release-notes.test.ts";
requireText(testPath, test, releaseNotesTestCommand);
requireText(releaseJustPath, releaseJust, releaseNotesTestCommand);

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
requireText(
  deliveryPath,
  delivery,
  "Legacy alpha updater manifest differs from the stable release asset.",
);
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

const proposalStepStart = delivery.indexOf("      - name: Build review proposal");
const proposalStepEnd = delivery.indexOf("      - name: Upload review proposal", proposalStepStart);
if (proposalStepStart < 0 || proposalStepEnd < 0) {
  fail(`${deliveryPath} must define a bounded Build review proposal step`);
}
const proposalStep = delivery.slice(proposalStepStart, proposalStepEnd);
const proposalManifest = 'manifest="${RUNNER_TEMP}/release-manifest/latest.json"';
const proposalManifestIndex = proposalStep.indexOf(proposalManifest);
const proposalCopyIndex = proposalStep.indexOf('cp "${manifest}"');
if (
  proposalManifestIndex < 0 ||
  proposalCopyIndex < 0 ||
  proposalManifestIndex > proposalCopyIndex
) {
  fail(`${deliveryPath} must bind the canonical manifest before copying it into the proposal`);
}
requireText(
  `${deliveryPath} Build review proposal step`,
  proposalStep,
  "Canonical release manifest is unavailable for the delivery proposal.",
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
