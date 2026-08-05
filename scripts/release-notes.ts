import { readFileSync, realpathSync, statSync } from "node:fs";
import { isAbsolute, relative, resolve, sep as pathSeparator } from "node:path";

export const MAX_RELEASE_NOTES_CHARACTERS = 12_000;

export type ReleaseCommandOptions = {
  version: string;
  notesPath: string;
  skipSnapshot: boolean;
  preflightOnly: boolean;
  assumeYes: boolean;
};

export type CuratedReleaseNotes = {
  absolutePath: string;
  relativePath: string;
  bytes: Buffer;
  markdown: string;
};

export function parseReleaseCommandArgs(args: string[]): ReleaseCommandOptions {
  let version = "";
  let notesPath = "";
  let skipSnapshot = false;
  let preflightOnly = false;
  let assumeYes = false;

  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--notes") {
      if (notesPath) throw new Error("--notes may be supplied only once.");
      const value = args[index + 1];
      if (!value || value.startsWith("--")) {
        throw new Error("--notes requires a Markdown file path.");
      }
      notesPath = value;
      index += 1;
      continue;
    }
    if (argument === "--skip-snapshot") {
      skipSnapshot = true;
      continue;
    }
    if (argument === "--preflight-only") {
      preflightOnly = true;
      continue;
    }
    if (argument === "--yes") {
      assumeYes = true;
      continue;
    }
    if (argument.startsWith("--")) throw new Error(`unknown option: ${argument}`);
    if (version) throw new Error(`unexpected positional argument: ${argument}`);
    version = argument;
  }

  if (!version) throw new Error("a release version is required.");
  if (!notesPath) {
    throw new Error(
      "--notes is required so the published release and desktop updater never fall back to a commit dump.",
    );
  }

  return { version, notesPath, skipSnapshot, preflightOnly, assumeYes };
}

export function loadCuratedReleaseNotes({
  root,
  version,
  notesPath,
}: {
  root: string;
  version: string;
  notesPath: string;
}): CuratedReleaseNotes {
  let rootPath: string;
  let absolutePath: string;
  try {
    rootPath = realpathSync(root);
    absolutePath = realpathSync(resolve(rootPath, notesPath));
  } catch {
    throw new Error(`release notes file was not found: ${notesPath}`);
  }

  const relativePath = relative(rootPath, absolutePath);
  if (
    !relativePath ||
    relativePath === ".." ||
    relativePath.startsWith(`..${pathSeparator}`) ||
    isAbsolute(relativePath)
  ) {
    throw new Error("release notes must be a file inside the Hecate repository.");
  }
  if (!relativePath.toLowerCase().endsWith(".md")) {
    throw new Error("release notes must use a .md file.");
  }
  if (!statSync(absolutePath).isFile()) {
    throw new Error(`release notes path is not a regular file: ${notesPath}`);
  }

  let bytes: Buffer;
  let markdown: string;
  try {
    bytes = readFileSync(absolutePath);
    markdown = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    throw new Error("release notes must be valid UTF-8 Markdown.");
  }
  validateCuratedReleaseNotes(markdown, version);

  return {
    absolutePath,
    relativePath: relativePath.split(pathSeparator).join("/"),
    bytes,
    markdown,
  };
}

export function validateCuratedReleaseNotes(markdown: string, version: string): void {
  if (markdown.includes("\0")) throw new Error("release notes must not contain NUL bytes.");
  if (Array.from(markdown).length > MAX_RELEASE_NOTES_CHARACTERS) {
    throw new Error(
      `release notes must be at most ${MAX_RELEASE_NOTES_CHARACTERS.toLocaleString("en-US")} characters so the updater receives them in full.`,
    );
  }

  const lines = markdown.replace(/\r\n?/g, "\n").split("\n");
  const firstContentLine = lines.find((line) => line.trim().length > 0)?.trim();
  const expectedTitle = `# Hecate ${version}`;
  if (firstContentLine !== expectedTitle) {
    throw new Error(`release notes must begin with exactly: ${expectedTitle}`);
  }
  if (markdown.includes("<!--")) {
    throw new Error("release notes must not contain HTML comments; remove all template guidance.");
  }

  const headings = markdownHeadings(lines);
  const highlights = headings.filter(
    (heading) => heading.level === 2 && normalizeHeading(heading.title) === "highlights",
  );
  if (highlights.length !== 1) {
    throw new Error("release notes must contain exactly one ## Highlights section.");
  }

  const highlightsStart = highlights[0].line + 1;
  const highlightsEnd =
    headings.find(
      (heading) => heading.line > highlights[0].line && heading.level <= highlights[0].level,
    )?.line ?? lines.length;
  const highlightItems = markdownBulletLines(lines.slice(highlightsStart, highlightsEnd));
  if (highlightItems.length === 0) {
    throw new Error("## Highlights must contain at least one bullet.");
  }
  if (highlightItems.length > 6) {
    throw new Error("## Highlights must contain no more than six concise bullets.");
  }

  for (const heading of headings.filter((candidate) => candidate.level === 2)) {
    const sectionEnd =
      headings.find(
        (candidate) => candidate.line > heading.line && candidate.level <= heading.level,
      )?.line ?? lines.length;
    if (!markdownSectionHasContent(lines.slice(heading.line + 1, sectionEnd))) {
      throw new Error(`## ${heading.title} must contain release information or be removed.`);
    }
  }
}

function markdownSectionHasContent(lines: string[]): boolean {
  let fence: { character: "`" | "~"; length: number } | null = null;

  for (const line of lines) {
    const fenceMatch = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (fenceMatch) {
      const marker = fenceMatch[1];
      const character = marker[0] as "`" | "~";
      if (!fence) fence = { character, length: marker.length };
      else if (
        character === fence.character &&
        marker.length >= fence.length &&
        fenceMatch[2].trim() === ""
      ) {
        fence = null;
      }
      continue;
    }
    if (line.trim()) return true;
  }

  return false;
}

function markdownBulletLines(lines: string[]): string[] {
  const bulletLines: string[] = [];
  let fence: { character: "`" | "~"; length: number } | null = null;

  for (const line of lines) {
    const fenceMatch = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (fenceMatch) {
      const marker = fenceMatch[1];
      const character = marker[0] as "`" | "~";
      if (!fence) fence = { character, length: marker.length };
      else if (
        character === fence.character &&
        marker.length >= fence.length &&
        fenceMatch[2].trim() === ""
      ) {
        fence = null;
      }
      continue;
    }
    if (!fence && /^\s{0,3}[-*+]\s+\S/.test(line)) bulletLines.push(line);
  }

  return bulletLines;
}

type MarkdownHeading = {
  level: number;
  line: number;
  title: string;
};

function markdownHeadings(lines: string[]): MarkdownHeading[] {
  const headings: MarkdownHeading[] = [];
  let fence: { character: "`" | "~"; length: number } | null = null;

  lines.forEach((line, index) => {
    const fenceMatch = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (fenceMatch) {
      const marker = fenceMatch[1];
      const character = marker[0] as "`" | "~";
      if (!fence) fence = { character, length: marker.length };
      else if (
        character === fence.character &&
        marker.length >= fence.length &&
        fenceMatch[2].trim() === ""
      ) {
        fence = null;
      }
      return;
    }
    if (fence) return;

    const match = /^(#{1,6})[ \t]+(.+?)\s*$/.exec(line);
    if (!match) return;
    headings.push({
      level: match[1].length,
      line: index,
      title: match[2].replace(/[ \t]+#+[ \t]*$/, "").trim(),
    });
  });

  return headings;
}

function normalizeHeading(value: string): string {
  return value.trim().toLocaleLowerCase("en-US").replace(/\s+/g, " ");
}
