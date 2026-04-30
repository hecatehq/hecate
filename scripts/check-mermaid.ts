#!/usr/bin/env bun
// check-mermaid.ts — validate Mermaid diagrams embedded in markdown files.
//
// Run from the repo root:
//   bun scripts/check-mermaid.ts
//
// Catches the four classes of syntax that break GitHub's renderer:
//   1. Invalid erDiagram attribute key types  (only PK, FK, UK are valid)
//   2. HTML tags (<br/>) in sequenceDiagram / erDiagram contexts
//   3. Curly or square brackets in sequenceDiagram arrow labels
//   4. Semicolons in sequenceDiagram Note text (act as statement terminators)
//
// flowchart / graph nodes accept <br/> inside quoted labels — those are
// intentionally not flagged.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, extname, relative } from "node:path";

// ── types ─────────────────────────────────────────────────────────────────────

interface Failure {
  file: string;
  block: number;
  line: number;
  rule: string;
  text: string;
}

interface Block {
  /** 1-based line number of the first content line (after the opening fence) */
  start: number;
  text: string;
}

// ── extraction ────────────────────────────────────────────────────────────────

function extractMermaidBlocks(content: string): Block[] {
  const blocks: Block[] = [];
  const lines = content.split("\n");
  let inBlock = false;
  let blockStart = 0;
  let blockLines: string[] = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!inBlock && /^```mermaid\s*$/.test(line)) {
      inBlock = true;
      blockStart = i + 2; // 1-based, content starts on the next line
      blockLines = [];
    } else if (inBlock && /^```\s*$/.test(line)) {
      inBlock = false;
      blocks.push({ start: blockStart, text: blockLines.join("\n") });
    } else if (inBlock) {
      blockLines.push(line);
    }
  }
  return blocks;
}

function diagramKind(text: string): string {
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (t) return t.split(/\s+/)[0].toLowerCase();
  }
  return "";
}

// ── checks ────────────────────────────────────────────────────────────────────

const VALID_ER_KEY_TYPES = new Set(["PK", "FK", "UK"]);

function checkErDiagram(
  lines: string[],
  blockIdx: number,
  blockStart: number,
  file: string,
  failures: Failure[],
): void {
  let inEntityBody = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineNum = blockStart + i;
    const trimmed = line.trim();

    if (line.includes("{")) inEntityBody = true;
    if (line.includes("}")) inEntityBody = false;

    // HTML is not supported anywhere in erDiagram.
    if (/<br\s*\/?>|<[a-zA-Z]/.test(line)) {
      failures.push({
        file,
        block: blockIdx,
        line: lineNum,
        rule: "HTML tags are not supported in erDiagram",
        text: trimmed,
      });
    }

    // Attribute key types — only PK, FK, UK are valid.
    // Attribute lines are indented; format: "  type attrName [KeyType] [comment]"
    if (inEntityBody) {
      const m = line.match(/^\s+(\w+)\s+(\w+)\s+(\w+)/);
      if (m) {
        const keyType = m[3];
        if (!VALID_ER_KEY_TYPES.has(keyType)) {
          failures.push({
            file,
            block: blockIdx,
            line: lineNum,
            rule: `Invalid erDiagram key type "${keyType}" — valid values: PK, FK, UK`,
            text: trimmed,
          });
        }
      }
    }
  }
}

function checkSequenceDiagram(
  lines: string[],
  blockIdx: number,
  blockStart: number,
  file: string,
  failures: Failure[],
): void {
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineNum = blockStart + i;
    const trimmed = line.trim();

    // HTML tags are not rendered in sequenceDiagram.
    if (/<br\s*\/?>/.test(line)) {
      failures.push({
        file,
        block: blockIdx,
        line: lineNum,
        rule: "<br/> is not supported in sequenceDiagram (use a space or reword)",
        text: trimmed,
      });
    }

    // Semicolons inside Note text terminate the statement early.
    // Note lines: "Note over A,B: text" or "Note right of A: text"
    if (/^\s*Note\s/i.test(line)) {
      const colonIdx = line.indexOf(":");
      if (colonIdx !== -1 && line.slice(colonIdx + 1).includes(";")) {
        failures.push({
          file,
          block: blockIdx,
          line: lineNum,
          rule: "Semicolon in Note text terminates the statement — use 'and' or rephrase",
          text: trimmed,
        });
      }
    }

    // Curly or square brackets in arrow labels break the parser.
    // Arrow lines: "A->>B: label" or "A-->B: label"
    const colonIdx = trimmed.indexOf(":");
    if (colonIdx !== -1 && /[-=]/.test(trimmed.slice(0, colonIdx))) {
      const label = trimmed.slice(colonIdx + 1);
      if (/[{}\[\]]/.test(label)) {
        failures.push({
          file,
          block: blockIdx,
          line: lineNum,
          rule: "Curly/square brackets in sequenceDiagram arrow label break the parser",
          text: trimmed,
        });
      }
    }
  }
}

function checkBlock(
  block: Block,
  blockIdx: number,
  file: string,
  failures: Failure[],
): void {
  const lines = block.text.split("\n");
  const kind = diagramKind(block.text);

  if (kind === "erdiagram") {
    checkErDiagram(lines, blockIdx, block.start, file, failures);
  } else if (kind === "sequencediagram") {
    checkSequenceDiagram(lines, blockIdx, block.start, file, failures);
  }
  // flowchart / graph: <br/> inside quoted node labels is valid — skip.
}

// ── filesystem walk ───────────────────────────────────────────────────────────

const SKIP_DIRS = new Set(["node_modules", ".git", ".gomodcache", "dist"]);

function walkMd(dir: string, results: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walkMd(full, results);
    } else if (extname(entry) === ".md") {
      results.push(full);
    }
  }
  return results;
}

// ── main ──────────────────────────────────────────────────────────────────────

// scripts/ lives one level below the repo root.
const repoRoot = new URL("..", import.meta.url).pathname;
const files = walkMd(repoRoot);
const failures: Failure[] = [];

for (const file of files) {
  const content = readFileSync(file, "utf8");
  const blocks = extractMermaidBlocks(content);
  for (let i = 0; i < blocks.length; i++) {
    checkBlock(blocks[i], i + 1, relative(repoRoot, file), failures);
  }
}

if (failures.length === 0) {
  console.log(`✓ ${files.length} markdown files checked, no Mermaid issues found`);
  process.exit(0);
} else {
  for (const f of failures) {
    console.error(`${f.file}:${f.line} [block ${f.block}] ${f.rule}`);
    console.error(`  ${f.text}`);
  }
  console.error(`\n✗ ${failures.length} Mermaid issue(s) in ${files.length} files checked`);
  process.exit(1);
}
