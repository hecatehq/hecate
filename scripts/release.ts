#!/usr/bin/env bun
// release.ts — cut a Hecate release tag and push it to CI.
//
// Usage:
//   bun scripts/release.ts <version> --notes <path>               # e.g. v0.5.1
//   bun scripts/release.ts v0.5.1 --notes docs/releases/v0.5.1.md --skip-snapshot
//   bun scripts/release.ts v0.5.1 --notes docs/releases/v0.5.1.md --preflight-only
//
// The script runs pre-flight checks, fires a goreleaser snapshot dry-run so
// you can inspect the artifacts before anything is published, validates the
// reviewed release notes, stamps the Tauri app version, then commits, tags,
// and pushes the branch + tag on explicit
// confirmation. CI takes it from there (~5-10 min).
//
// Recovery if the CI run fails:
//   # Stop/wait for the run, classify what published, and follow:
//   # docs/contributor/release.md#recovery

import { execSync, execFileSync } from "child_process";
import { mkdtempSync, rmSync } from "fs";
import { tmpdir } from "os";
import { join, resolve } from "path";

import {
  loadCuratedReleaseNotes,
  parseReleaseCommandArgs,
  validateCuratedReleaseNotes,
} from "./release-notes";
import type { CuratedReleaseNotes, ReleaseCommandOptions } from "./release-notes";

const root = resolve(import.meta.dir, "..");

// ── Helpers ───────────────────────────────────────────────────────────────────

function run(cmd: string, opts: { silent?: boolean } = {}): string {
  return execSync(cmd, {
    cwd: root,
    encoding: "utf8",
    stdio: opts.silent ? ["pipe", "pipe", "pipe"] : ["inherit", "pipe", "inherit"],
  }).trim();
}

function confirm(question: string, assumeYes: boolean): boolean {
  if (assumeYes) {
    console.log(`${question} [y/N] y (--yes)`);
    return true;
  }
  const answer = prompt(`${question} [y/N] `);
  return /^y/i.test(answer ?? "");
}

function abort(msg: string): never {
  console.error(`\nAborted: ${msg}`);
  process.exit(0);
}

function fail(msg: string): never {
  console.error(`\nerror: ${msg}`);
  process.exit(1);
}

function commandErrorOutput(error: unknown): string {
  const maybeError = error as {
    stderr?: { toString(): string };
    stdout?: { toString(): string };
    message?: string;
  };
  const stderr = maybeError.stderr?.toString().trim();
  if (stderr) return stderr;
  const stdout = maybeError.stdout?.toString().trim();
  if (stdout) return stdout;
  return maybeError.message ?? String(error);
}

type ReviewedReleaseNotes = {
  bytes: Buffer;
  objectId: string;
};

function readReviewedReleaseNotes(
  relativePath: string,
  version: string,
  revision = "HEAD",
): ReviewedReleaseNotes {
  let bytes: Buffer;
  let objectId: string;
  try {
    objectId = execFileSync("git", ["rev-parse", `${revision}:${relativePath}`], {
      cwd: root,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    bytes = execFileSync("git", ["cat-file", "blob", objectId], {
      cwd: root,
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (error) {
    fail(
      `release notes must be tracked in the reviewed release commit: ${relativePath}\n` +
        `  Git said: ${commandErrorOutput(error)}`,
    );
  }

  let markdown: string;
  try {
    markdown = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    fail(`reviewed release notes must be valid UTF-8 Markdown: ${relativePath}`);
  }
  try {
    validateCuratedReleaseNotes(markdown, version);
  } catch (error) {
    fail(`reviewed release notes are not publishable: ${(error as Error).message}`);
  }
  return { bytes, objectId };
}

function worktreeReleaseNotesObjectId(notes: CuratedReleaseNotes): string {
  try {
    return execFileSync(
      "git",
      ["hash-object", `--path=${notes.relativePath}`, "--", notes.absolutePath],
      {
        cwd: root,
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      },
    ).trim();
  } catch (error) {
    fail(
      `could not compare release notes with the reviewed commit: ${notes.relativePath}\n` +
        `  Git said: ${commandErrorOutput(error)}`,
    );
  }
}

function revisionContainsPath(revision: string, relativePath: string): boolean {
  try {
    execFileSync("git", ["cat-file", "-e", `${revision}:${relativePath}`], {
      cwd: root,
      stdio: ["ignore", "ignore", "ignore"],
    });
    return true;
  } catch {
    return false;
  }
}

function sep(label: string) {
  console.log(`\n── ${label} ${"─".repeat(Math.max(0, 72 - label.length - 4))}`);
}

// ── Args ──────────────────────────────────────────────────────────────────────

let commandOptions: ReleaseCommandOptions;
try {
  commandOptions = parseReleaseCommandArgs(process.argv.slice(2));
} catch (error) {
  console.error(
    "usage: bun scripts/release.ts <version> --notes <path> [--skip-snapshot] [--preflight-only] [--yes]",
  );
  console.error("       version: vX.Y.Z  (e.g. v0.5.0)");
  console.error(`\nerror: ${(error as Error).message}`);
  process.exit(1);
}
const { version, notesPath, skipSnapshot, preflightOnly, assumeYes } = commandOptions;

// ── Validate version format ───────────────────────────────────────────────────

if (!/^v\d+\.\d+\.\d+$/.test(version)) {
  fail(`version must be a stable vX.Y.Z tag (got '${version}')`);
}

const semver = version.replace(/^v/, ""); // bare semver (no leading v)
let releaseNotes: CuratedReleaseNotes;
try {
  releaseNotes = loadCuratedReleaseNotes({ root, version, notesPath });
} catch (error) {
  fail((error as Error).message);
}

// ── Pre-flight ────────────────────────────────────────────────────────────────

sep("Pre-flight");

// 1. Clean worktree — goreleaser refuses dirty state; catch it early.
const dirty = run("git status --porcelain", { silent: true });
if (dirty) {
  console.error("error: working tree is dirty. Commit or stash changes first.");
  run("git status --short");
  process.exit(1);
}
console.log("  worktree  : clean");

// 2. Releases must start from the default branch. Allowing a feature branch
// here is unsafe because the stamp commit and tag would be pushed together
// while master remained unchanged.
const branch = run("git rev-parse --abbrev-ref HEAD", { silent: true });
if (branch !== "master" && branch !== "main") {
  fail(
    `releases must be cut from master/main (current branch: '${branch}').\n` +
      "  Merge the candidate, then use a clean, current default-branch worktree.",
  );
}
console.log(`  branch    : ${branch}`);
const localCommit = run("git rev-parse HEAD", { silent: true });
console.log(`  commit    : ${localCommit.slice(0, 7)}`);

// Resolve notes from HEAD as well as the working tree. Git's assume-unchanged
// and skip-worktree bits can hide local edits from both status and ls-files.
// Comparing object IDs through Git's clean filter catches those edits without
// rejecting a clean checkout whose platform line-ending policy differs.
const reviewedReleaseNotes = readReviewedReleaseNotes(releaseNotes.relativePath, version);
if (worktreeReleaseNotesObjectId(releaseNotes) !== reviewedReleaseNotes.objectId) {
  fail(
    `release notes do not match the reviewed release commit: ${releaseNotes.relativePath}\n` +
      "  Restore the committed bytes or commit and review the intended notes before releasing.",
  );
}
console.log(`  notes     : ${releaseNotes.relativePath} (curated)`);

// 3. Refresh origin before checking the candidate. This makes the local tag
// uniqueness check authoritative for existing remote tags and prevents a
// stale or locally-ahead default branch from publishing an unreviewed commit.
try {
  execFileSync("git", ["fetch", "--tags", "origin"], {
    cwd: root,
    stdio: "inherit",
  });
} catch (error) {
  fail(`could not refresh origin before release.\n  Git said: ${commandErrorOutput(error)}`);
}
const upstream = `origin/${branch}`;
let upstreamCommit = "";
try {
  upstreamCommit = execFileSync("git", ["rev-parse", upstream], {
    cwd: root,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
} catch (error) {
  fail(`could not resolve ${upstream}.\n  Git said: ${commandErrorOutput(error)}`);
}
if (localCommit !== upstreamCommit) {
  fail(
    `local ${branch} does not exactly match ${upstream}.\n` +
      `  local:    ${localCommit}\n` +
      `  upstream: ${upstreamCommit}\n` +
      "  Pull or push the reviewed commit before cutting a release.",
  );
}
console.log(`  upstream  : ${upstream} (exact match)`);

// 4. Tag must not already exist locally or on the just-fetched origin.
try {
  // execFileSync to avoid the same shell-injection class CodeQL flags on
  // line 156 — version is regex-validated upstream, but defense in depth.
  execFileSync("git", ["rev-parse", version], {
    cwd: root,
    stdio: ["ignore", "ignore", "ignore"],
  });
  fail(
    `tag ${version} already exists locally.\n` +
      `  To delete: git tag -d ${version}  ` +
      `(and git push --delete origin ${version} if already pushed)`,
  );
} catch {
  // expected — tag does not exist yet
}
console.log(`  tag       : ${version} (new)`);

// 5. goreleaser must be on PATH.
try {
  const gr = run("goreleaser --version 2>&1", { silent: true }).split("\n")[0];
  console.log(`  goreleaser: ${gr}`);
} catch {
  fail(
    "goreleaser not found.\n" + "  Install: go install github.com/goreleaser/goreleaser/v2@latest",
  );
}

// 6. Bun must be available (needed for Tauri version stamp).
try {
  run("bun --version", { silent: true });
  console.log(`  bun       : ${run("bun --version", { silent: true })}`);
} catch {
  fail("bun not found — required for Tauri version stamping.");
}

// 7. Docker must be reachable when the local snapshot will build images.
// `just release` runs this preflight before `just verify`, so a stopped
// Docker Desktop fails in seconds instead of after the full release gate.
if (!skipSnapshot) {
  try {
    const dockerVersion = execFileSync("docker", ["info", "--format", "{{.ServerVersion}}"], {
      cwd: root,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    console.log(`  docker    : ${dockerVersion}`);
  } catch (error) {
    fail(
      "Docker is required for the Goreleaser snapshot dry-run, but the Docker daemon is not reachable.\n" +
        "  Start Docker Desktop and retry, or pass --skip-snapshot only after just verify has already passed.\n" +
        `  Docker said: ${commandErrorOutput(error)}`,
    );
  }
} else {
  console.log("  docker    : skipped (--skip-snapshot)");
}

if (preflightOnly) {
  console.log("\nRelease preflight passed.");
  process.exit(0);
}

// ── Goreleaser snapshot dry-run ───────────────────────────────────────────────

if (!skipSnapshot) {
  sep("Goreleaser snapshot");
  console.log("(builds binaries + Docker images locally without publishing)\n");
  execSync("goreleaser release --snapshot --clean", { cwd: root, stdio: "inherit" });
  console.log("\nSnapshot written to ./dist.");
  console.log("\nReview the curated notes that will be published:");
  console.log(`  cat ${releaseNotes.relativePath}`);
}

// ── Confirm ───────────────────────────────────────────────────────────────────

sep("Confirm");
const remote = (() => {
  try {
    return run("git remote get-url origin", { silent: true });
  } catch {
    return "(unknown)";
  }
})();
console.log(`  tag    : ${version}`);
console.log(`  remote : ${remote}`);
console.log(`  notes  : ${releaseNotes.relativePath}`);

if (!confirm("\nStamp Tauri version, tag, and push branch + tag?", assumeYes)) {
  abort("cancelled by user");
}

// ── Stamp Tauri version ───────────────────────────────────────────────────────

sep("Tauri version stamp");
const dirtyBeforeStamp = run("git status --porcelain", { silent: true });
if (dirtyBeforeStamp) {
  console.error("error: working tree changed after preflight. Commit or stash changes first.");
  run("git status --short");
  process.exit(1);
}
const headBeforeStamp = run("git rev-parse HEAD", { silent: true });
if (headBeforeStamp !== localCommit) {
  fail(
    "HEAD changed after release preflight; restart from the clean, reviewed default branch.\n" +
      `  reviewed: ${localCommit}\n` +
      `  current:  ${headBeforeStamp}`,
  );
}
const stampPaths = [
  "tauri/src-tauri/Cargo.toml",
  "tauri/src-tauri/Cargo.lock",
  "tauri/src-tauri/tauri.conf.json",
  "tauri/src-tauri/tauri.ios.conf.json",
  "tauri/src-tauri/tauri.android.conf.json",
  "tauri/src-tauri/gen/apple/project.yml",
  "tauri/src-tauri/gen/apple/hecate-app_iOS/Info.plist",
  "tauri/package.json",
];
let releaseCommit = localCommit;
const stampScriptPath = "scripts/stamp-version.ts";
if (!revisionContainsPath(localCommit, stampScriptPath)) {
  fail(`the reviewed release commit does not contain the required ${stampScriptPath} script.`);
}

// Status can hide assume-unchanged and skip-worktree edits. Refuse hidden
// stamp-surface changes explicitly, then derive the release commit in a
// detached checkout whose bytes come only from the reviewed commit.
for (const stampPath of stampPaths) {
  const reviewedObject = execFileSync("git", ["rev-parse", `${localCommit}:${stampPath}`], {
    cwd: root,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
  const worktreeObject = execFileSync(
    "git",
    ["hash-object", `--path=${stampPath}`, "--", stampPath],
    {
      cwd: root,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    },
  ).trim();
  if (worktreeObject !== reviewedObject) {
    fail(
      `version-stamp input differs from the reviewed release commit: ${stampPath}\n` +
        "  Restore or commit the hidden worktree edit before releasing.",
    );
  }
}

const temporaryWorktreeRoot = mkdtempSync(join(tmpdir(), "hecate-release-worktree-"));
const stampCheckout = join(temporaryWorktreeRoot, "checkout");
let checkoutAdded = false;
let checkoutRemoved = false;
let cleanupHint = "";
let stampFailure: unknown = null;
try {
  execFileSync("git", ["worktree", "add", "--detach", stampCheckout, localCommit], {
    cwd: root,
    stdio: "inherit",
  });
  checkoutAdded = true;
  const isolatedStampScript = resolve(stampCheckout, stampScriptPath);
  execFileSync("bun", [isolatedStampScript], {
    cwd: stampCheckout,
    stdio: "inherit",
    env: { ...process.env, TAURI_VERSION: semver },
  });

  const stampDirty = execFileSync("git", ["status", "--porcelain"], {
    cwd: stampCheckout,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
  if (stampDirty) {
    execFileSync("git", ["add", "--", ...stampPaths], {
      cwd: stampCheckout,
      stdio: "inherit",
    });
    const capturedStampTree = execFileSync("git", ["write-tree"], {
      cwd: stampCheckout,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    execFileSync("git", ["commit", "-m", `chore(tauri): stamp version ${semver}`], {
      cwd: stampCheckout,
      stdio: "inherit",
    });

    releaseCommit = execFileSync("git", ["rev-parse", "HEAD"], {
      cwd: stampCheckout,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    const stampParents = execFileSync("git", ["rev-list", "--parents", "-n", "1", releaseCommit], {
      cwd: stampCheckout,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    })
      .trim()
      .split(/\s+/);
    if (stampParents.length !== 2 || stampParents[1] !== localCommit) {
      throw new Error(
        "the version stamp must create exactly one commit directly on the reviewed release commit.",
      );
    }
    const releaseTree = execFileSync("git", ["rev-parse", `${releaseCommit}^{tree}`], {
      cwd: stampCheckout,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    if (releaseTree !== capturedStampTree) {
      throw new Error(
        "the version stamp commit differs from the exact isolated tree captured before commit hooks ran.",
      );
    }
    const changedStampPaths = execFileSync(
      "git",
      ["diff-tree", "--no-commit-id", "--name-only", "-r", "-z", releaseCommit],
      {
        cwd: stampCheckout,
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      },
    )
      .split("\0")
      .filter(Boolean);
    const unexpectedStampPaths = changedStampPaths.filter((path) => !stampPaths.includes(path));
    if (changedStampPaths.length === 0 || unexpectedStampPaths.length > 0) {
      throw new Error(
        "the version stamp commit changed files outside the release allowlist:\n" +
          `  ${unexpectedStampPaths.join("\n  ") || "(stamp commit had no changed files)"}`,
      );
    }
    const isolatedDirtyAfterCommit = execFileSync("git", ["status", "--porcelain"], {
      cwd: stampCheckout,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    if (isolatedDirtyAfterCommit) {
      throw new Error(
        "the version stamp changed files outside its captured commit:\n" +
          `  ${isolatedDirtyAfterCommit}`,
      );
    }
    console.log("  prepared isolated Tauri version stamp commit");
  } else {
    releaseCommit = localCommit;
    console.log("  Tauri files already at correct version — no commit needed");
  }
} catch (error) {
  stampFailure = error;
} finally {
  if (checkoutAdded) {
    try {
      execFileSync("git", ["worktree", "remove", "--force", stampCheckout], {
        cwd: root,
        stdio: ["ignore", "ignore", "pipe"],
      });
      checkoutRemoved = true;
    } catch (error) {
      const cleanupFailure = commandErrorOutput(error);
      const originalFailure = stampFailure ? `${commandErrorOutput(stampFailure)}\n  ` : "";
      stampFailure = new Error(
        `${originalFailure}temporary worktree cleanup also failed: ${cleanupFailure}`,
      );
      cleanupHint =
        `\n  Temporary checkout retained for recovery: ${stampCheckout}` +
        `\n  Remove it after inspection with: git worktree remove --force ${stampCheckout}`;
    }
  }
  if (!checkoutAdded || checkoutRemoved) {
    rmSync(temporaryWorktreeRoot, { recursive: true, force: true });
  }
}
if (stampFailure) {
  fail(
    `could not prepare the isolated version stamp.\n  Git said: ${commandErrorOutput(stampFailure)}${cleanupHint}`,
  );
}

const dirtyAfterStamp = run("git status --porcelain", { silent: true });
if (dirtyAfterStamp) {
  console.error("error: working tree contains changes outside the version stamp.");
  run("git status --short");
  process.exit(1);
}
const headAfterStamp = run("git rev-parse HEAD", { silent: true });
if (headAfterStamp !== localCommit) {
  fail("the main checkout HEAD changed while the isolated version stamp was prepared.");
}

// ── Tag and push ──────────────────────────────────────────────────────────────

sep("Tag and push");
let notesAtTag: CuratedReleaseNotes;
try {
  notesAtTag = loadCuratedReleaseNotes({ root, version, notesPath });
} catch (error) {
  fail((error as Error).message);
}
const notesInStampedCommit = readReviewedReleaseNotes(
  notesAtTag.relativePath,
  version,
  releaseCommit,
);
if (
  worktreeReleaseNotesObjectId(notesAtTag) !== notesInStampedCommit.objectId ||
  !notesInStampedCommit.bytes.equals(reviewedReleaseNotes.bytes)
) {
  fail(
    "release notes changed in the working tree or stamped commit after preflight; restart from a clean reviewed checkout.",
  );
}
execFileSync("git", ["tag", "-a", "--cleanup=verbatim", "-F", "-", version, releaseCommit], {
  cwd: root,
  input: notesInStampedCommit.bytes,
  stdio: ["pipe", "inherit", "inherit"],
});
console.log(`Tagged ${version}`);

execFileSync(
  "git",
  [
    "push",
    "--atomic",
    "origin",
    `${releaseCommit}:refs/heads/${branch}`,
    `refs/tags/${version}:refs/tags/${version}`,
  ],
  { cwd: root, stdio: "inherit" },
);
console.log(`Pushed ${branch} and ${version}`);

// ── Done ──────────────────────────────────────────────────────────────────────

sep("Done");
console.log("CI is building the release. Track it at:");
console.log("  https://github.com/hecatehq/hecate/actions");
console.log("\nWhen CI finishes, sync the manifest/docs commits back locally:");
console.log("  git pull --ff-only origin master");
console.log(`\nWhen CI completes (~5-10 min), verify the published image:`);
console.log(`  docker pull ghcr.io/hecatehq/hecate:${semver}`);
console.log(`  docker run --rm -p 127.0.0.1:8765:8765 ghcr.io/hecatehq/hecate:${semver}`);
console.log("\nIf CI fails, stop or wait for the run before changing published state.");
console.log("Classify the failure, then follow docs/contributor/release.md#recovery.");
console.log("Release/tag and GHCR cleanup are separate operations.");
console.log("Do not delete a complete release for a delivery-only failure.");
