#!/usr/bin/env bun
// Resolve the Tauri/native-app version used by release stamping and
// versioned sidecar builds.

import { readFileSync } from "fs";
import { resolve } from "path";
import { execSync } from "child_process";

export const root = resolve(import.meta.dir, "..");
export const tauri = resolve(root, "tauri");

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

export function resolveTauriVersion(): string {
  return process.env.TAURI_VERSION?.trim().replace(/^v/, "") || gitVersion() || cargoVersion();
}

if (import.meta.main) {
  console.log(resolveTauriVersion());
}
