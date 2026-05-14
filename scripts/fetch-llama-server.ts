#!/usr/bin/env bun
// fetch-llama-server.ts — stage the bundled llama.cpp `llama-server`
// binary for the Tauri desktop app.
//
// Tauri's externalBin bundler expects binaries to live at
//   tauri/src-tauri/binaries/<base>-<target-triple>[.exe]
// where <target-triple> is the Rust target the .app is being built for.
// This script downloads the right llama.cpp release archive for the
// requested triple, extracts `llama-server` from it, and stages it at
// the expected path with the executable bit set.
//
// Reproducibility:
//   - The upstream release tag is pinned in LLAMA_CPP_RELEASE below.
//     Bumping it is a deliberate one-line change; CI verifies the
//     resulting bundle through the normal release flow.
//   - Downloads are sha256-verified when EXPECTED_SHA256 is set;
//     during v1 bring-up the sha is left empty so the script is
//     usable before the first verified build, with a warning.
//
// Platforms covered in v1:
//   - aarch64-apple-darwin  (macOS Apple Silicon, Metal-enabled)
// Linux / Windows are out of v1 per docs/rfcs/local-models-llamacpp.md.
// Adding a target = adding a row to TARGETS below.
//
// Usage:
//   bun scripts/fetch-llama-server.ts                  # auto-detect target
//   bun scripts/fetch-llama-server.ts --target aarch64-apple-darwin
//   bun scripts/fetch-llama-server.ts --force          # re-download even if cached

import { existsSync, mkdirSync, chmodSync, statSync, copyFileSync, rmSync } from "fs";
import { resolve, join } from "path";
import { mkdtemp, readFile } from "fs/promises";
import { tmpdir } from "os";
import { spawnSync } from "child_process";
import { createHash } from "crypto";

// Pinned upstream release. Bump deliberately; updating this string
// is the canonical way to roll the bundled binary forward.
const LLAMA_CPP_RELEASE = "b4404"; // 2025-01 era; see https://github.com/ggml-org/llama.cpp/releases

type TargetSpec = {
  triple: string;
  // The GitHub release asset's file name within the upstream release.
  // Upstream uses a per-platform naming scheme that's stable across
  // build numbers — only the b<N> prefix changes.
  asset: string;
  // Path inside the extracted archive at which `llama-server` lives.
  // macOS arm64 layout puts it under build/bin/.
  innerPath: string;
  // SHA256 of the asset. Empty during v1 bring-up — fill before
  // bumping LLAMA_CPP_RELEASE so the next operator sees the verified
  // path.
  sha256: string;
};

const TARGETS: TargetSpec[] = [
  {
    triple: "aarch64-apple-darwin",
    asset: `llama-${LLAMA_CPP_RELEASE}-bin-macos-arm64.zip`,
    innerPath: "build/bin/llama-server",
    sha256: "",
  },
];

const REPO_ROOT = resolve(import.meta.dir, "..");
const STAGE_DIR = join(REPO_ROOT, "tauri", "src-tauri", "binaries");

function detectTriple(): string {
  // Tauri's `tauri info` shells out to rustc to read the host triple;
  // doing the same here keeps us in sync. Fall back to the macOS arm64
  // assumption when rustc is unavailable — every other dev has it
  // through their tauri install anyway.
  const out = spawnSync("rustc", ["-vV"], { encoding: "utf8" });
  if (out.status === 0) {
    const line = (out.stdout ?? "").split("\n").find((l) => l.startsWith("host:"));
    if (line) return line.slice("host:".length).trim();
  }
  // Best-effort fallback. Operators on other platforms must pass --target explicitly.
  return "aarch64-apple-darwin";
}

function parseArgs() {
  const args = process.argv.slice(2);
  let triple = "";
  let force = false;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--target" && args[i + 1]) {
      triple = args[++i];
    } else if (a === "--force") {
      force = true;
    } else if (a === "--help" || a === "-h") {
      console.log("Usage: bun scripts/fetch-llama-server.ts [--target TRIPLE] [--force]");
      process.exit(0);
    } else {
      console.error(`unknown arg: ${a}`);
      process.exit(2);
    }
  }
  if (!triple) triple = detectTriple();
  return { triple, force };
}

async function downloadToFile(url: string, dest: string) {
  console.log(`downloading ${url}`);
  const res = await fetch(url);
  if (!res.ok || !res.body) {
    throw new Error(`download failed: ${res.status} ${res.statusText}`);
  }
  // Stream to disk to avoid loading the whole archive into RAM.
  const writer = Bun.file(dest).writer();
  for await (const chunk of res.body as unknown as AsyncIterable<Uint8Array>) {
    writer.write(chunk);
  }
  await writer.end();
}

async function sha256OfFile(path: string): Promise<string> {
  const buf = await readFile(path);
  return createHash("sha256").update(buf).digest("hex");
}

function unzip(archive: string, outDir: string) {
  // `unzip` is the platform-native tool on macOS and Linux. Windows
  // build users should run this script under WSL / a unix shell;
  // when we add Windows-native fetch it'll switch to powershell's
  // Expand-Archive.
  const out = spawnSync("unzip", ["-q", "-o", archive, "-d", outDir]);
  if (out.status !== 0) {
    throw new Error(`unzip failed (status ${out.status}): ${out.stderr?.toString()}`);
  }
}

async function fetchTarget(spec: TargetSpec, force: boolean) {
  if (!existsSync(STAGE_DIR)) mkdirSync(STAGE_DIR, { recursive: true });
  const stagedName = `llama-server-${spec.triple}`;
  const stagedPath = join(STAGE_DIR, stagedName);

  if (!force && existsSync(stagedPath)) {
    const info = statSync(stagedPath);
    if (info.size > 0) {
      console.log(`✓ ${stagedName} already staged (size=${info.size}). Use --force to re-download.`);
      return;
    }
  }

  const url = `https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_CPP_RELEASE}/${spec.asset}`;
  const work = await mkdtemp(join(tmpdir(), "hecate-llama-fetch-"));
  const archive = join(work, spec.asset);
  try {
    await downloadToFile(url, archive);

    if (spec.sha256) {
      const got = await sha256OfFile(archive);
      if (got !== spec.sha256) {
        throw new Error(
          `sha256 mismatch for ${spec.asset}\n  expected: ${spec.sha256}\n  got:      ${got}`,
        );
      }
      console.log(`✓ sha256 verified (${got.slice(0, 12)}…)`);
    } else {
      console.warn(`⚠ no pinned sha256 for ${spec.triple}; download is not verified.`);
    }

    unzip(archive, work);
    const inner = join(work, spec.innerPath);
    if (!existsSync(inner)) {
      throw new Error(`extracted archive does not contain ${spec.innerPath}`);
    }
    copyFileSync(inner, stagedPath);
    chmodSync(stagedPath, 0o755);
    console.log(`✓ staged ${stagedPath}`);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

async function main() {
  const { triple, force } = parseArgs();
  const spec = TARGETS.find((t) => t.triple === triple);
  if (!spec) {
    console.error(`target ${triple} is not supported in v1 (see TARGETS in this script).`);
    console.error(`v1 covers: ${TARGETS.map((t) => t.triple).join(", ")}`);
    process.exit(2);
  }
  await fetchTarget(spec, force);
}

main().catch((err) => {
  console.error(err.message ?? err);
  process.exit(1);
});
