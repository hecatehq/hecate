#!/usr/bin/env bun
// nightly-maintenance.ts - run scheduled maintenance checks and write a report.
//
// Hard checks fail the workflow. Informational checks are captured in the
// report without changing the exit code.

import { appendFileSync, mkdirSync, writeFileSync } from "fs";
import { resolve } from "path";
import { spawnSync } from "child_process";

type CheckKind = "hard" | "informational";

type Check = {
  name: string;
  command: string[];
  kind: CheckKind;
  logFile: string;
};

type CheckResult = Check & {
  status: "pass" | "fail";
  exitCode: number | null;
};

const root = resolve(import.meta.dir, "..");
const outDir = resolve(root, ".maintenance");
mkdirSync(outDir, { recursive: true });

const checks: Check[] = [
  {
    name: "just maintenance",
    command: ["just", "maintenance"],
    kind: "hard",
    logFile: "maintenance.log",
  },
  {
    name: "just test-race",
    command: ["just", "test-race"],
    kind: "hard",
    logFile: "test-race.log",
  },
  {
    name: "just check-links-external",
    command: ["just", "check-links-external"],
    kind: "informational",
    logFile: "external-links.log",
  },
];

function runCheck(check: Check): CheckResult {
  const startedAt = new Date().toISOString();
  console.log(`running ${check.kind} check: ${check.name}`);
  const result = spawnSync(check.command[0], check.command.slice(1), {
    cwd: root,
    encoding: "utf8",
    env: process.env,
    maxBuffer: 64 * 1024 * 1024,
  });
  const finishedAt = new Date().toISOString();
  const exitCode = result.status;
  const status = exitCode === 0 && !result.error ? "pass" : "fail";
  const logPath = resolve(outDir, check.logFile);

  writeFileSync(
    logPath,
    [
      `$ ${check.command.join(" ")}`,
      `started_at=${startedAt}`,
      `finished_at=${finishedAt}`,
      `exit_code=${exitCode ?? "signal"}`,
      result.error ? `error=${result.error.message}` : "",
      "",
      "## stdout",
      result.stdout || "",
      "",
      "## stderr",
      result.stderr || "",
    ].join("\n"),
  );

  console.log(`${status}: ${check.name} -> ${check.logFile}`);
  return { ...check, status, exitCode };
}

function escapeCell(value: string): string {
  return value.replace(/\|/g, "\\|").replace(/\n/g, " ");
}

function reportFor(results: CheckResult[]): string {
  const hard = results.filter((r) => r.kind === "hard");
  const informational = results.filter((r) => r.kind === "informational");
  const failed = results.filter((r) => r.status === "fail");
  const commit = process.env.GITHUB_SHA ?? runGit(["rev-parse", "HEAD"]);
  const branch = process.env.GITHUB_REF_NAME ?? runGit(["branch", "--show-current"]);
  const runURL =
    process.env.GITHUB_SERVER_URL && process.env.GITHUB_REPOSITORY && process.env.GITHUB_RUN_ID
      ? `${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}`
      : "";

  const lines: string[] = [
    "# Nightly Maintenance Report",
    "",
    `- Date: ${new Date().toISOString()}`,
    `- Branch: ${branch || "unknown"}`,
    `- Commit: ${commit || "unknown"}`,
  ];
  if (runURL) {
    lines.push(`- Run: ${runURL}`);
  }

  lines.push("", "## Hard Checks", "", "| Check | Result | Log |", "| --- | --- | --- |");
  for (const result of hard) {
    lines.push(`| ${escapeCell(result.name)} | ${result.status} | ${escapeCell(result.logFile)} |`);
  }

  lines.push("", "## Informational Checks", "", "| Check | Result | Log |", "| --- | --- | --- |");
  for (const result of informational) {
    lines.push(`| ${escapeCell(result.name)} | ${result.status} | ${escapeCell(result.logFile)} |`);
  }

  lines.push("", "## Notes", "");
  if (failed.length === 0) {
    lines.push("- No failing checks.");
  } else {
    for (const result of failed) {
      lines.push(`- ${result.name} failed; see ${result.logFile}.`);
    }
  }

  lines.push("", "Artifacts include the Markdown report plus one log file per check.", "");

  return lines.join("\n");
}

function runGit(args: string[]): string {
  const result = spawnSync("git", args, { cwd: root, encoding: "utf8" });
  return result.status === 0 ? result.stdout.trim() : "";
}

const results = checks.map(runCheck);
const report = reportFor(results);
const reportPath = resolve(outDir, "maintenance-report.md");
writeFileSync(reportPath, report + "\n");

const summaryPath = process.env.GITHUB_STEP_SUMMARY;
if (summaryPath) {
  appendFileSync(summaryPath, report + "\n");
}

const missingSections = ["## Hard Checks", "## Informational Checks"].filter(
  (section) => !report.includes(section),
);
if (missingSections.length > 0) {
  console.error(`maintenance report missing sections: ${missingSections.join(", ")}`);
  process.exit(1);
}

const hardFailed = results.some((result) => result.kind === "hard" && result.status === "fail");
process.exit(hardFailed ? 1 : 0);
