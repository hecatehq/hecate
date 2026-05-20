#!/usr/bin/env bun

// tauri-smoke.ts — opt-in native app lifecycle / ACP discovery smoke test.
//
// This intentionally launches the real packaged desktop app. It is not part
// of verify because GUI automation is host-specific and disruptive.
// Today the implementation is macOS-only, matching the platform we can
// validate locally; Linux/Windows need their own launch semantics.

import { existsSync } from "node:fs";
import { access, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { spawn, spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import readline from "node:readline";

const root = resolve(import.meta.dir, "..");
const appPath = join(
  root,
  "tauri/src-tauri/target/release/bundle/macos/Hecate.app",
);
const appDataDir = join(
  process.env.HOME ?? "",
  "Library/Application Support/sh.hecate.app",
);
const runtimeStatePath = join(appDataDir, "hecate.runtime.json");
const acpSidecarPath = join(
  appPath,
  `Contents/MacOS/hecate-acp-${macOSTriple()}`,
);

const mode = process.argv.includes("--acp") ? "acp" : "lifecycle";

type ListenTarget = {
  pid: string;
  port: number;
};

type RuntimeState = {
  base_url?: string;
  listen_addr?: string;
  pid?: number;
};

type RPCResponse<T = unknown> = {
  jsonrpc: "2.0";
  id: number | string;
  result?: T;
  error?: { code: number; message: string };
};

type InitializeResult = {
  availableModels?: Array<{ id: string }>;
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

function macOSTriple(): string {
  if (process.arch === "arm64") return "aarch64-apple-darwin";
  if (process.arch === "x64") return "x86_64-apple-darwin";
  fail(`unsupported macOS architecture for native smoke: ${process.arch}`);
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

async function waitForRuntimeState(expectedPort: number): Promise<RuntimeState> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      const raw = await readFile(runtimeStatePath, "utf8");
      const state = JSON.parse(raw) as RuntimeState;
      if (state.base_url?.endsWith(`:${expectedPort}`)) {
        return state;
      }
    } catch {
      // Keep polling until the sidecar has written runtime discovery state.
    }
    await sleep(250);
  }
  fail(`runtime state ${runtimeStatePath} did not point at port ${expectedPort}`);
}

async function waitForRuntimeStateRemoved(): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      await access(runtimeStatePath);
    } catch {
      return;
    }
    await sleep(250);
  }
  fail(`runtime state ${runtimeStatePath} still existed after app quit`);
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

async function runACPInitialize(): Promise<InitializeResult> {
  if (!existsSync(acpSidecarPath)) {
    fail(`missing bundled ACP sidecar at ${acpSidecarPath}; run just tauri-build-app first`);
  }

  const env = {
    ...process.env,
    HECATE_DATA_DIR: appDataDir,
  };
  delete env.HECATE_GATEWAY_URL;

  const acp = spawn(acpSidecarPath, {
    cwd: root,
    env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  acp.stderr.on("data", (chunk: Buffer) => process.stderr.write(chunk));

  try {
    return await requestACP<InitializeResult>(acp, "initialize", {
      protocolVersion: "0.1",
      clientCapabilities: { permissions: {} },
    });
  } finally {
    await stopChild(acp);
  }
}

function requestACP<T>(
  acp: ChildProcessWithoutNullStreams,
  method: string,
  params: unknown,
): Promise<T> {
  const rl = readline.createInterface({ input: acp.stdout });
  acp.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }) + "\n");

  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      rl.close();
      reject(new Error(`timeout waiting for ACP ${method}`));
    }, 10_000);

    rl.on("line", (line: string) => {
      const msg = JSON.parse(line) as RPCResponse<T>;
      if (msg.id !== 1) return;
      clearTimeout(timeout);
      rl.close();
      if (msg.error) reject(new Error(`ACP ${method}: ${msg.error.message}`));
      else resolve(msg.result as T);
    });
  });
}

async function stopChild(child: ChildProcessWithoutNullStreams): Promise<void> {
  child.stdin.destroy();
  child.stdout.destroy();
  child.stderr.destroy();
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
  child.kill("SIGTERM");
  await Promise.race([
    exited,
    sleep(2_000).then(() => {
      if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    }),
  ]);
}

async function main(): Promise<void> {
  if (process.platform !== "darwin") {
    console.log("tauri smoke skipped: currently implemented for macOS bundles only");
    return;
  }

  if (!existsSync(appPath)) {
    fail(`missing app bundle at ${appPath}; run just tauri-build first`);
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

  if (mode === "acp") {
    const state = await waitForRuntimeState(gateway.port);
    console.log(`runtime discovery state found at ${runtimeStatePath}: ${state.base_url}`);

    const init = await runACPInitialize();
    const modelCount = init.availableModels?.length ?? 0;
    console.log(`ACP initialize via native runtime discovery succeeded (${modelCount} models)`);
  }

  run("osascript", ["-e", 'tell application "Hecate" to quit']);
  await waitForPidExit(gateway.pid);
  await waitForRuntimeStateRemoved();
  console.log("app quit cleanly and hecate sidecar exited");
}

main().catch((error: unknown) => {
  fail(error instanceof Error ? error.message : String(error));
});
