#!/usr/bin/env bun
// release.ts — cut a Hecate release tag and push it to CI.
//
// Usage:
//   bun scripts/release.ts <version>                 # e.g. v0.1.0-alpha.9
//   bun scripts/release.ts v0.2.0 --skip-snapshot    # skip goreleaser dry-run
//   bun scripts/release.ts v0.2.0 --skip-snapshot --yes
//   bun scripts/release.ts v0.2.0 --preflight-only   # validate local release deps
//
// The script runs pre-flight checks, fires a goreleaser snapshot dry-run so
// you can inspect the changelog before anything is published, stamps the Tauri
// app version, then commits, tags, and pushes the branch + tag on explicit
// confirmation. CI takes it from there (~5-10 min).
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
  if (process.argv.includes("--yes")) {
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

function sep(label: string) {
  console.log(`\n── ${label} ${"─".repeat(Math.max(0, 72 - label.length - 4))}`);
}

// ── Args ──────────────────────────────────────────────────────────────────────

const args = process.argv.slice(2);
const version = args.find((a) => !a.startsWith("--")) ?? "";
const skipSnapshot = args.includes("--skip-snapshot");
const preflightOnly = args.includes("--preflight-only");

if (!version) {
  console.error(
    "usage: bun scripts/release.ts <version> [--skip-snapshot] [--preflight-only] [--yes]",
  );
  console.error("       version: vX.Y.Z  or  vX.Y.Z-pre.N  (e.g. v0.1.0-alpha.9)");
  process.exit(1);
}

const allowedFlags = new Set(["--skip-snapshot", "--preflight-only", "--yes"]);
const unknownFlags = args.filter((a) => a.startsWith("--") && !allowedFlags.has(a));
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
  console.log("\nCheck the changelog before tagging:");
  console.log("  cat dist/CHANGELOG.md");
  console.log("\nIf this is the first tag the changelog includes all history — expected.");
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

if (!confirm("\nStamp Tauri version, tag, and push branch + tag?")) abort("cancelled by user");

// ── Stamp Tauri version ───────────────────────────────────────────────────────

sep("Tauri version stamp");
const stampScript = resolve(root, "scripts/stamp-version.ts");
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

  // If stamping dirtied the tree, commit every desktop and mobile version
  // surface before tagging. Leaving the platform overlays untracked would
  // make the tag disagree with the store artifacts that CI builds from it.
  const stampDirty = run("git status --porcelain", { silent: true });
  if (stampDirty) {
    execFileSync(
      "git",
      [
        "add",
        "tauri/src-tauri/Cargo.toml",
        "tauri/src-tauri/Cargo.lock",
        "tauri/src-tauri/tauri.conf.json",
        "tauri/src-tauri/tauri.ios.conf.json",
        "tauri/src-tauri/tauri.android.conf.json",
        "tauri/src-tauri/gen/apple/project.yml",
        "tauri/src-tauri/gen/apple/hecate-app_iOS/Info.plist",
        "tauri/package.json",
      ],
      { cwd: root, stdio: "inherit" },
    );
    execFileSync("git", ["commit", "-m", `chore(tauri): stamp version ${semver}`], {
      cwd: root,
      stdio: "inherit",
    });
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

execFileSync("git", ["push", "origin", `HEAD:${branch}`, version], { cwd: root, stdio: "inherit" });
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
console.log(`\nTo recover if CI fails:`);
console.log(`  git push --delete origin ${version} && git tag -d ${version}`);
