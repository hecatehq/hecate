#!/usr/bin/env bun
// stamp-version.ts — sync Tauri version files to the Hecate release version.
//
// Version resolution order:
//   1. TAURI_VERSION env var (set by release.ts / CI)
//   2. Latest git tag reachable from HEAD (strips leading "v")
//   3. Existing Cargo.toml version (dev builds / untagged)
//
// Files updated:
//   tauri/src-tauri/Cargo.toml      source of truth
//   tauri/package.json              mirrors Cargo.toml
//   tauri/src-tauri/tauri.conf.json mirrors Cargo.toml
//     (Tauri 2.x removed "version": "package" — must be written explicitly)
//
// Usage:
//   bun scripts/stamp-version.ts                      # from repo root
//   TAURI_VERSION=0.1.0-alpha.9 bun scripts/stamp-version.ts

import { readFileSync, writeFileSync } from "fs";
import { resolve } from "path";
import { execSync } from "child_process";

const root = resolve(import.meta.dir, "..");
const tauri = resolve(root, "tauri");

// ── 1. Resolve the target version ────────────────────────────────────────────

function gitVersion(): string | null {
  try {
    const tag = execSync("git describe --tags --abbrev=0", {
      cwd: root,
      encoding: "utf8",
      stdio: ["pipe", "pipe", "pipe"],
    }).trim();
    return tag.replace(/^v/, "");
  } catch {
    return null;
  }
}

function cargoVersion(): string {
  const cargo = readFileSync(resolve(tauri, "src-tauri/Cargo.toml"), "utf8");
  const m = cargo.match(/^\[package\][^[]*version\s*=\s*"([^"]+)"/ms);
  if (!m) throw new Error("could not parse version from Cargo.toml");
  return m[1];
}

const version =
  process.env.TAURI_VERSION?.trim().replace(/^v/, "") || gitVersion() || cargoVersion();

console.log(`stamping version: ${version}`);

// ── 2. Cargo.toml — update [package] version only ────────────────────────────

const cargoPath = resolve(tauri, "src-tauri/Cargo.toml");
const cargo = readFileSync(cargoPath, "utf8");

// Replace the version line inside [package] only (before the next section).
// The regex matches `version = "..."` that appears before the first `[` after
// `[package]`, so dependency version pins are left untouched.
const updatedCargo = cargo.replace(
  /(\[package\][^[]*?version\s*=\s*)"[^"]+"/ms,
  `$1"${version}"`,
);

if (updatedCargo === cargo) {
  console.log(`  Cargo.toml  already at ${version}`);
} else {
  writeFileSync(cargoPath, updatedCargo);
  console.log(`  Cargo.toml  → ${version}`);
}

// ── 3. package.json ───────────────────────────────────────────────────────────

const pkgPath = resolve(tauri, "package.json");
const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));

if (pkg.version === version) {
  console.log(`  package.json already at ${version}`);
} else {
  pkg.version = version;
  writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
  console.log(`  package.json → ${version}`);
}

// ── 4. tauri.conf.json ────────────────────────────────────────────────────────
//
// Two version fields we maintain here:
//   - top-level `version` — the canonical semver. Used by every bundler
//     except Windows MSI.
//   - `bundle.windows.wix.version` — a Windows-style four-part numeric
//     version (Major.Minor.Build.Revision, each <= 65535). WiX/MSI rejects
//     pre-release identifiers like "-alpha.8". We derive it from the semver:
//       0.1.0-alpha.8 → 0.1.0.8     (alpha number rolls into Revision)
//       0.1.0         → 0.1.0.0     (no pre-release → revision 0)
//     Two semvers with the same M.m.p but different pre-release types
//     (e.g. 0.1.0-alpha.3 and 0.1.0-beta.3) would collide under this
//     scheme — fine for the alpha-only lifecycle today; revisit when
//     beta/rc tags start shipping.
//   - NSIS has no version override field in Tauri's schema — it uses the
//     top-level version directly. We don't ship NSIS bundles today (matrix
//     is msi-only on Windows); if NSIS is added, it'll hit the same MSI
//     constraint and require a different upstream fix.

function windowsVersion(semver: string): string {
  const m = semver.match(/^(\d+)\.(\d+)\.(\d+)(?:-(?:[A-Za-z]+)\.(\d+))?/);
  if (!m) {
    throw new Error(
      `unparseable semver for Windows MSI version: ${semver}. Expected M.m.p or M.m.p-<id>.N.`,
    );
  }
  const [, major, minor, patch, pre] = m;
  return `${major}.${minor}.${patch}.${pre ?? "0"}`;
}

const winVersion = windowsVersion(version);

const confPath = resolve(tauri, "src-tauri/tauri.conf.json");
const conf = JSON.parse(readFileSync(confPath, "utf8"));

let confChanged = false;

if (conf.version !== version) {
  conf.version = version;
  confChanged = true;
}

// Ensure the bundle.windows.wix.version override exists and is current.
conf.bundle ??= {};
conf.bundle.windows ??= {};
conf.bundle.windows.wix ??= {};

if (conf.bundle.windows.wix.version !== winVersion) {
  conf.bundle.windows.wix.version = winVersion;
  confChanged = true;
}

if (confChanged) {
  writeFileSync(confPath, JSON.stringify(conf, null, 2) + "\n");
  console.log(`  tauri.conf.json → ${version} (wix: ${winVersion})`);
} else {
  console.log(`  tauri.conf.json already at ${version} (wix: ${winVersion})`);
}

console.log("done.");
