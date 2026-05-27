#!/usr/bin/env bun
// release.ts — cut a Hecate release tag and push it to CI.
//
// Usage:
//   bun scripts/release.ts <version>                 # e.g. v0.1.0-alpha.9
//   bun scripts/release.ts v0.2.0 --skip-snapshot    # skip goreleaser dry-run
//   bun scripts/release.ts v0.2.0 --preflight-only   # validate local release deps
//
// The script runs pre-flight checks, fires a goreleaser snapshot dry-run so
// you can inspect the changelog before anything is published, stamps the Tauri
// app version, then tags and pushes on explicit confirmation. CI takes it from
// there (~5-10 min).
//
// Recovery if the CI run fails:
//   git push --delete origin <version>
//   git tag -d <version>
//   # fix root cause, re-run this script

import { execSync, execFileSync } from "child_process";
import { existsSync } from "fs";
import { resolve } from "path";

const root = resolve(import.meta.dir, "..");

// ── Helpers ───────────────────────────────────────────────────────────────────

function run(cmd: string, opts: { silent?: boolean } = {}): string {
  return execSync(cmd, {
    cwd: root,
    encoding: "utf8",
    stdio: opts.silent ? ["pipe", "pipe", "pipe"] : ["inherit", "pipe", "inherit"],
  }).trim();
}

function confirm(question: string): boolean {
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
  const maybeError = error as { stderr?: { toString(): string }; stdout?: { toString(): string }; message?: string };
  const stderr = maybeError.stderr?.toString().trim();
  if (stderr) return stderr;
  const stdout = maybeError.stdout?.toString().trim();
  if (stdout) return stdout;
  return maybeError.message ?? String(error);
}

function sep(label: string) {
  console.log(`\n── ${label} ${"─".repeat(Math.max(0, 72 - label.length - 4))}`);
}

// ── Args ──────────────────────────────────────────────────────────────────────

const args = process.argv.slice(2);
const version = args.find(a => !a.startsWith("--")) ?? "";
const skipSnapshot = args.includes("--skip-snapshot");
const preflightOnly = args.includes("--preflight-only");

if (!version) {
  console.error("usage: bun scripts/release.ts <version> [--skip-snapshot] [--preflight-only]");
  console.error("       version: vX.Y.Z  or  vX.Y.Z-pre.N  (e.g. v0.1.0-alpha.9)");
  process.exit(1);
}

const allowedFlags = new Set(["--skip-snapshot", "--preflight-only"]);
const unknownFlags = args.filter(a => a.startsWith("--") && !allowedFlags.has(a));
if (unknownFlags.length > 0) {
  fail(`unknown option${unknownFlags.length === 1 ? "" : "s"}: ${unknownFlags.join(", ")}`);
}

// ── Validate version format ───────────────────────────────────────────────────

if (!/^v\d+\.\d+\.\d+(-[a-zA-Z0-9._]+)?$/.test(version)) {
  fail(`version must be vX.Y.Z or vX.Y.Z-pre.N (got '${version}')`);
}

const semver = version.replace(/^v/, ""); // bare semver (no leading v)

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

// 2. Branch check — warn when releasing from non-master.
const branch = run("git rev-parse --abbrev-ref HEAD", { silent: true });
if (branch !== "master" && branch !== "main") {
  console.warn(`warning: releasing from branch '${branch}' (not master/main)`);
  if (!confirm("  Continue?")) abort("cancelled by user");
}
console.log(`  branch    : ${branch}`);
console.log(`  commit    : ${run("git rev-parse --short HEAD", { silent: true })}`);
const preStampHead = run("git rev-parse HEAD", { silent: true });

// 3. Tag must not already exist.
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

// 4. goreleaser must be on PATH.
try {
  const gr = run("goreleaser --version 2>&1", { silent: true }).split("\n")[0];
  console.log(`  goreleaser: ${gr}`);
} catch {
  fail(
    "goreleaser not found.\n" +
    "  Install: go install github.com/goreleaser/goreleaser/v2@latest",
  );
}

// 5. Bun must be available (needed for Tauri version stamp).
try {
  run("bun --version", { silent: true });
  console.log(`  bun       : ${run("bun --version", { silent: true })}`);
} catch {
  fail("bun not found — required for Tauri version stamping.");
}

// 6. Docker must be reachable when the local snapshot will build images.
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
  console.log("\nCheck the changelog before tagging:");
  console.log("  cat dist/CHANGELOG.md");
  console.log("\nIf this is the first tag the changelog includes all history — expected.");
}

// ── Confirm ───────────────────────────────────────────────────────────────────

sep("Confirm");
const remote = (() => {
  try { return run("git remote get-url origin", { silent: true }); }
  catch { return "(unknown)"; }
})();
console.log(`  tag    : ${version}`);
console.log(`  remote : ${remote}`);

if (!confirm("\nTag, stamp Tauri version, and push?")) abort("cancelled by user");

// ── Stamp Tauri version ───────────────────────────────────────────────────────

sep("Tauri version stamp");
const stampScript = resolve(root, "scripts/stamp-version.ts");
let stampCommitCreated = false;
if (existsSync(stampScript)) {
  // execFileSync (no shell) so that paths/args with spaces or special
  // characters can't be interpreted as shell metacharacters. CodeQL also
  // flags the template-string form as "shell command built from
  // environment values" — execFileSync makes that warning go away because
  // args are passed to the process verbatim.
  execFileSync("bun", [stampScript], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, TAURI_VERSION: semver },
  });

  // If stamping dirtied the tree, commit it before tagging.
  const stampDirty = run("git status --porcelain", { silent: true });
  if (stampDirty) {
    run("git add tauri/src-tauri/Cargo.toml tauri/src-tauri/Cargo.lock tauri/src-tauri/tauri.conf.json tauri/package.json");
    execFileSync("git", ["commit", "-m", `chore(tauri): stamp version ${semver}`], {
      cwd: root,
      stdio: "inherit",
    });
    stampCommitCreated = true;
    console.log("  committed Tauri version stamp");
  } else {
    console.log("  Tauri files already at correct version — no commit needed");
  }
} else {
  console.warn("  scripts/stamp-version.ts not found — skipping Tauri stamp");
}

// ── Tag and push ──────────────────────────────────────────────────────────────

sep("Tag and push");
execFileSync("git", ["tag", "-a", version, "-m", version], { cwd: root, stdio: "inherit" });
console.log(`Tagged ${version}`);

execFileSync("git", ["push", "origin", version], { cwd: root, stdio: "inherit" });

// Keep the local release branch on the pre-tag commit. The version stamp
// commit is intentionally reachable through the annotated tag only; leaving the
// branch there makes local master diverge as soon as CI publishes manifest/docs
// commits back to origin/master.
if (stampCommitCreated) {
  sep("Restore release branch");
  execFileSync("git", ["reset", "--hard", preStampHead], { cwd: root, stdio: "inherit" });
  console.log(`  ${branch} restored to ${preStampHead.slice(0, 12)}`);
  console.log(`  ${version} still points at the Tauri version stamp commit`);
}

// ── Done ──────────────────────────────────────────────────────────────────────

sep("Done");
console.log("CI is building the release. Track it at:");
console.log("  https://github.com/hecatehq/hecate/actions");
console.log("\nWhen CI finishes, sync the manifest/docs commits back locally:");
console.log("  git pull --ff-only origin master");
console.log(`\nWhen CI completes (~5-10 min), verify the published image:`);
console.log(`  docker pull ghcr.io/hecatehq/hecate:${semver}`);
console.log(`  docker run --rm -p 8765:8765 ghcr.io/hecatehq/hecate:${semver}`);
console.log(`\nTo recover if CI fails:`);
console.log(`  git push --delete origin ${version} && git tag -d ${version}`);
