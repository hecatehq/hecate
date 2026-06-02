import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dirname, "..");

function fail(message: string): never {
  console.error(`agent-docs-check: ${message}`);
  process.exit(1);
}

function read(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

function gitLsFiles(): string[] {
  return execFileSync("git", ["ls-files"], { cwd: root, encoding: "utf8" })
    .split("\n")
    .filter(Boolean)
    .sort();
}

const tracked = gitLsFiles();
const forbidden = tracked.filter((path) => path.startsWith(".claude/") || path.startsWith(".cursor/"));
if (forbidden.length > 0) {
  fail(`tracked provider-specific adapter files are not allowed:\n${forbidden.join("\n")}`);
}

const copilotInstructionFiles = [
  ".github/instructions/agent-docs.instructions.md",
  ".github/instructions/backend.instructions.md",
  ".github/instructions/providers.instructions.md",
  ".github/instructions/tauri.instructions.md",
  ".github/instructions/ui.instructions.md",
];

const entrypoints = [
  "AGENTS.md",
  "ui/AGENTS.md",
  "internal/providers/AGENTS.md",
  ".github/copilot-instructions.md",
  ...copilotInstructionFiles,
  "docs-ai/README.md",
  "docs-ai/skills/README.md",
  "docs-ai/core/agent-guidance.md",
  "docs-ai/core/verification.md",
];

for (const file of entrypoints) {
  const content = read(file);
  if (!content.includes("docs-ai/")) {
    fail(`${file} must point to canonical docs-ai guidance`);
  }
}

const claude = read("CLAUDE.md").trim();
if (claude !== "@AGENTS.md") {
  fail("CLAUDE.md must be exactly @AGENTS.md");
}

const copilot = read(".github/copilot-instructions.md");
if (copilot.includes("@AGENTS.md")) {
  fail(".github/copilot-instructions.md must point to AGENTS.md in prose, not use @AGENTS.md import syntax");
}

for (const file of copilotInstructionFiles) {
  const content = read(file);
  const frontmatter = content.match(/^---\n([\s\S]*?)\n---\n/);
  if (!frontmatter || !/^applyTo:\s*.+$/m.test(frontmatter[1])) {
    fail(`${file} must declare applyTo frontmatter`);
  }
}

const agentGuidance = read("docs-ai/core/agent-guidance.md");
if (!agentGuidance.includes(".claude/") || !agentGuidance.includes(".cursor/") || !agentGuidance.includes("ignored local state")) {
  fail("docs-ai/core/agent-guidance.md must document that provider-specific directories are local-only");
}

const skillsDir = join(root, "docs-ai", "skills");
const skillDirs = readdirSync(skillsDir)
  .filter((name) => statSync(join(skillsDir, name)).isDirectory())
  .sort();
const skillRegistry = read("docs-ai/skills/README.md");
for (const name of skillDirs) {
  if (!skillRegistry.includes(`(${name}/SKILL.md)`)) {
    fail(`docs-ai/skills/README.md must include docs-ai/skills/${name}/SKILL.md`);
  }
}

console.log(
  `agent-docs-check: ${entrypoints.length} entrypoints, ${copilotInstructionFiles.length} Copilot path adapters, and ${skillDirs.length} skills OK; no tracked .claude/.cursor adapters`,
);
