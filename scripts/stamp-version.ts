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
  process.env.TAURI_VERSION?.trim() || gitVersion() || cargoVersion();

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

const confPath = resolve(tauri, "src-tauri/tauri.conf.json");
const conf = JSON.parse(readFileSync(confPath, "utf8"));

if (conf.version === version) {
  console.log(`  tauri.conf.json already at ${version}`);
} else {
  conf.version = version;
  writeFileSync(confPath, JSON.stringify(conf, null, 2) + "\n");
  console.log(`  tauri.conf.json → ${version}`);
}

console.log("done.");
