#!/usr/bin/env bun

// tauri-smoke.ts — opt-in native app lifecycle smoke test.
//
// This intentionally launches the real packaged desktop app. It is not part
// of verify-alpha because GUI automation is host-specific and disruptive.
// Today the implementation is macOS-only, matching the platform we can
// validate locally; Linux/Windows need their own launch semantics.

import { existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";

const root = resolve(import.meta.dir, "..");
const appPath = join(
  root,
  "tauri/src-tauri/target/release/bundle/macos/Hecate.app",
);

type ListenTarget = {
  pid: string;
  port: number;
};

function fail(message: string): never {
  console.error(`tauri smoke failed: ${message}`);
  process.exit(1);
}

function run(command: string, args: string[]): string {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    const stderr = result.stderr.trim();
    const stdout = result.stdout.trim();
    fail(
      `${command} ${args.join(" ")} exited ${result.status}: ${
        stderr || stdout || "no output"
      }`,
    );
  }
  return result.stdout;
}

function listenTargets(): ListenTarget[] {
  const result = spawnSync("lsof", ["-nP", "-iTCP", "-sTCP:LISTEN"], {
    cwd: root,
    encoding: "utf8",
  });

  if (result.status !== 0) {
    return [];
  }

  const targets: ListenTarget[] = [];
  for (const line of result.stdout.split("\n")) {
    const parts = line.trim().split(/\s+/);
    if (parts.length < 9 || parts[0] !== "hecate") {
      continue;
    }
    const endpoint = parts.at(-2) ?? "";
    const match = endpoint.match(/(?:127\.0\.0\.1|localhost):(\d+)$/);
    if (!match) {
      continue;
    }
    targets.push({ pid: parts[1], port: Number.parseInt(match[1], 10) });
  }
  return targets;
}

async function sleep(ms: number): Promise<void> {
  await new Promise((resolveSleep) => setTimeout(resolveSleep, ms));
}

async function waitForGateway(beforePids: Set<string>): Promise<ListenTarget> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const target = listenTargets().find((candidate) => !beforePids.has(candidate.pid));
    if (target) {
      const response = await fetch(`http://127.0.0.1:${target.port}/healthz`).catch(
        () => null,
      );
      if (response?.ok) {
        const body = await response.text();
        if (!body.includes('"status":"ok"')) {
          fail(`/healthz response did not contain status ok: ${body}`);
        }
        return target;
      }
    }
    await sleep(250);
  }
  fail("hecate sidecar did not become healthy within 30s");
}

async function waitForPidExit(pid: string): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const result = spawnSync("kill", ["-0", pid], { stdio: "ignore" });
    if (result.status !== 0) {
      return;
    }
    await sleep(250);
  }
  fail(`hecate sidecar pid ${pid} was still running after app quit`);
}

async function main(): Promise<void> {
  if (process.platform !== "darwin") {
    console.log("tauri smoke skipped: currently implemented for macOS bundles only");
    return;
  }

  if (!existsSync(appPath)) {
    fail(`missing app bundle at ${appPath}; run make tauri-build first`);
  }

  const before = new Set(listenTargets().map((target) => target.pid));
  console.log(`launching ${appPath}`);
  const open = spawn("open", ["-n", appPath], {
    cwd: root,
    stdio: "ignore",
    detached: true,
  });
  open.unref();

  const gateway = await waitForGateway(before);
  console.log(`gateway healthy on 127.0.0.1:${gateway.port} (pid ${gateway.pid})`);

  run("osascript", ["-e", 'tell application "Hecate" to quit']);
  await waitForPidExit(gateway.pid);
  console.log("app quit cleanly and hecate sidecar exited");
}

main().catch((error: unknown) => {
  fail(error instanceof Error ? error.message : String(error));
});
