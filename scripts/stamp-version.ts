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
//   tauri/src-tauri/Cargo.lock      mirrors the local hecate-app package
//   tauri/package.json              mirrors Cargo.toml
//   tauri/src-tauri/tauri.conf.json mirrors Cargo.toml
//     (Tauri 2.x removed "version": "package" — must be written explicitly)
//   tauri/src-tauri/tauri.{ios,android}.conf.json
//                                  use store-compatible monotonic build numbers
//   tauri/src-tauri/gen/apple/project.yml and Info.plist
//                                  use App Store-compatible version fields
//
// Usage:
//   bun scripts/stamp-version.ts                      # from repo root
//   TAURI_VERSION=0.1.0-alpha.9 bun scripts/stamp-version.ts

import { readFileSync, writeFileSync } from "fs";
import { resolve } from "path";
import { androidVersionCode, appleBuildNumber, windowsVersion } from "./mobile-version";
import { resolveTauriVersion, tauri } from "./resolve-tauri-version";

// ── 1. Resolve the target version ────────────────────────────────────────────

const version = resolveTauriVersion();

console.log(`stamping version: ${version}`);

// ── 2. Cargo.toml — update [package] version only ────────────────────────────

const cargoPath = resolve(tauri, "src-tauri/Cargo.toml");
const cargo = readFileSync(cargoPath, "utf8");

// Replace the version line inside [package] only (before the next section).
// The regex matches `version = "..."` that appears before the first `[` after
// `[package]`, so dependency version pins are left untouched.
const updatedCargo = cargo.replace(/(\[package\][^[]*?version\s*=\s*)"[^"]+"/ms, `$1"${version}"`);

if (updatedCargo === cargo) {
  console.log(`  Cargo.toml  already at ${version}`);
} else {
  writeFileSync(cargoPath, updatedCargo);
  console.log(`  Cargo.toml  → ${version}`);
}

// ── 3. Cargo.lock ─────────────────────────────────────────────────────────────

const cargoLockPath = resolve(tauri, "src-tauri/Cargo.lock");
const cargoLock = readFileSync(cargoLockPath, "utf8");
const updatedCargoLock = cargoLock.replace(
  /(\[\[package\]\]\nname = "hecate-app"\nversion = )"[^"]+"/,
  `$1"${version}"`,
);

if (updatedCargoLock === cargoLock) {
  console.log(`  Cargo.lock  already at ${version}`);
} else {
  writeFileSync(cargoLockPath, updatedCargoLock);
  console.log(`  Cargo.lock  → ${version}`);
}

// ── 4. package.json ───────────────────────────────────────────────────────────

const pkgPath = resolve(tauri, "package.json");
const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));

if (pkg.version === version) {
  console.log(`  package.json already at ${version}`);
} else {
  pkg.version = version;
  writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
  console.log(`  package.json → ${version}`);
}

// ── 5. tauri.conf.json ────────────────────────────────────────────────────────
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

const winVersion = windowsVersion(version);
const iosVersion = version.replace(/-.+$/, "");
const iosBuildNumber = appleBuildNumber(version);
const androidBuildNumber = androidVersionCode(version);

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

const iosConfPath = resolve(tauri, "src-tauri/tauri.ios.conf.json");
const iosConf = JSON.parse(readFileSync(iosConfPath, "utf8"));
iosConf.bundle ??= {};
iosConf.bundle.iOS ??= {};
if (iosConf.bundle.iOS.bundleVersion !== iosBuildNumber) {
  iosConf.bundle.iOS.bundleVersion = iosBuildNumber;
  writeFileSync(iosConfPath, JSON.stringify(iosConf, null, 2) + "\n");
  console.log(`  tauri.ios.conf.json → bundleVersion ${iosBuildNumber}`);
}

const androidConfPath = resolve(tauri, "src-tauri/tauri.android.conf.json");
const androidConf = JSON.parse(readFileSync(androidConfPath, "utf8"));
androidConf.bundle ??= {};
androidConf.bundle.android ??= {};
if (androidConf.bundle.android.versionCode !== androidBuildNumber) {
  androidConf.bundle.android.versionCode = androidBuildNumber;
  writeFileSync(androidConfPath, JSON.stringify(androidConf, null, 2) + "\n");
  console.log(`  tauri.android.conf.json → versionCode ${androidBuildNumber}`);
}

const appleProjectPath = resolve(tauri, "src-tauri/gen/apple/project.yml");
const appleProject = readFileSync(appleProjectPath, "utf8");
const updatedAppleProject = appleProject
  .replace(/(CFBundleShortVersionString:\s*)[^\n]+/, `$1${iosVersion}`)
  .replace(/(CFBundleVersion:\s*)"[^"]+"/, `$1"${iosBuildNumber}"`);
if (updatedAppleProject !== appleProject) {
  writeFileSync(appleProjectPath, updatedAppleProject);
  console.log(`  gen/apple/project.yml → ${iosVersion} (${iosBuildNumber})`);
}

const appleInfoPath = resolve(tauri, "src-tauri/gen/apple/hecate-app_iOS/Info.plist");
const appleInfo = readFileSync(appleInfoPath, "utf8");
const updatedAppleInfo = appleInfo
  .replace(
    /(<key>CFBundleShortVersionString<\/key>\s*<string>)[^<]+(<\/string>)/,
    `$1${iosVersion}$2`,
  )
  .replace(/(<key>CFBundleVersion<\/key>\s*<string>)[^<]+(<\/string>)/, `$1${iosBuildNumber}$2`);
if (updatedAppleInfo !== appleInfo) {
  writeFileSync(appleInfoPath, updatedAppleInfo);
  console.log(`  gen/apple/Info.plist → ${iosVersion} (${iosBuildNumber})`);
}

console.log("done.");
