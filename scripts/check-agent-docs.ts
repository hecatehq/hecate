import { lstatSync, readFileSync, readdirSync, readlinkSync } from "node:fs";
import { join, relative } from "node:path";

const root = join(import.meta.dirname, "..");

function fail(message: string): never {
  console.error(`agent-docs-check: ${message}`);
  process.exit(1);
}

function rel(path: string): string {
  return relative(root, path) || ".";
}

function read(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

const skillDir = join(root, "docs-ai", "skills");
const claudeSkillDir = join(root, ".claude", "skills");
const canonicalSkills = readdirSync(skillDir, { withFileTypes: true })
  .filter((entry) => entry.isDirectory())
  .map((entry) => entry.name)
  .sort();

const claudeSkills = readdirSync(claudeSkillDir).sort();
const expectedTarget = (name: string) => `../../docs-ai/skills/${name}`;

for (const name of canonicalSkills) {
  const path = join(claudeSkillDir, name);
  let stat;
  try {
    stat = lstatSync(path);
  } catch {
    fail(`missing Claude skill adapter ${rel(path)}`);
  }
  if (!stat.isSymbolicLink()) {
    fail(`${rel(path)} must be a symlink to ${expectedTarget(name)}`);
  }
  const target = readlinkSync(path);
  if (target !== expectedTarget(name)) {
    fail(`${rel(path)} points to ${target}, want ${expectedTarget(name)}`);
  }
}

for (const name of claudeSkills) {
  if (!canonicalSkills.includes(name)) {
    fail(`Claude skill adapter .claude/skills/${name} has no docs-ai/skills/${name}`);
  }
}

const adapterFiles = [
  "CLAUDE.md",
  ".cursor/rules/00-core.mdc",
  ".cursor/rules/10-planning.mdc",
  ".cursor/rules/20-testing.mdc",
  ".cursor/rules/30-review.mdc",
  ".claude/commands/race.md",
  ".claude/commands/test-affected.md",
];

for (const file of adapterFiles) {
  if (!read(file).includes("docs-ai/")) {
    fail(`${file} must point to canonical docs-ai guidance`);
  }
}

const claude = read("CLAUDE.md");
if (/\*\*Rule \d/.test(claude)) {
  fail("CLAUDE.md must not define standalone numbered project rules");
}

for (const file of adapterFiles.filter((file) => file.startsWith(".cursor/"))) {
  const content = read(file);
  if (content.includes("| Step |") || content.includes("## Seven-axis rubric")) {
    fail(`${file} appears to duplicate canonical checklists instead of linking to docs-ai`);
  }
}

console.log(`agent-docs-check: ${canonicalSkills.length} skills and ${adapterFiles.length} adapters OK`);
