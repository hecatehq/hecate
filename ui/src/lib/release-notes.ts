import { markdownToPlainText, parseMarkdownBlocks } from "./markdown";

const LEGACY_ITEM_LIMIT = 3;
const LEGACY_PARAGRAPH_LIMIT = 320;
const FEATURED_INLINE_LIMIT = 240;

const FEATURED_SECTION_ORDER = ["highlights", "security", "breaking"] as const;
const FEATURED_ITEM_LIMIT: Record<FeaturedSectionKind, number> = {
  highlights: 6,
  security: 3,
  breaking: 3,
};

type FeaturedSectionKind = (typeof FEATURED_SECTION_ORDER)[number];

type MarkdownSection = {
  heading: string;
  markdown: string;
};

export type ReleaseNotesPresentation = {
  featuredMarkdown: string;
  fullMarkdown: string;
  hasAdditionalDetails: boolean;
};

/**
 * Select the release information an operator needs before updating while
 * preserving the complete publisher-authored Markdown for explicit review.
 */
export function buildReleaseNotesPresentation(content: string): ReleaseNotesPresentation | null {
  const fullMarkdown = content.trim();
  if (!fullMarkdown) return null;

  const selectedSections = new Map<FeaturedSectionKind, MarkdownSection>();
  for (const section of levelTwoSections(fullMarkdown)) {
    const kind = featuredSectionKind(section.heading);
    if (kind && !selectedSections.has(kind)) selectedSections.set(kind, section);
  }

  const featuredMarkdown = FEATURED_SECTION_ORDER.flatMap((kind) => {
    const section = selectedSections.get(kind);
    return section ? [conciseFeaturedSection(section, FEATURED_ITEM_LIMIT[kind])] : [];
  }).join("\n\n");
  const preview = featuredMarkdown || legacyReleaseNotesPreview(fullMarkdown);

  return {
    featuredMarkdown: preview,
    fullMarkdown,
    hasAdditionalDetails: comparableMarkdown(preview) !== comparableMarkdown(fullMarkdown),
  };
}

function levelTwoSections(content: string): MarkdownSection[] {
  const lines = content.replace(/\r\n?/g, "\n").split("\n");
  const sections: MarkdownSection[] = [];
  let activeHeading = "";
  let activeStart = -1;
  let fence: { character: "`" | "~"; length: number } | null = null;

  const finishSection = (end: number) => {
    if (activeStart < 0) return;
    const markdown = lines.slice(activeStart, end).join("\n").trim();
    if (markdown) sections.push({ heading: activeHeading, markdown });
  };

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const fenceMatch = /^\s*(`{3,}|~{3,})(.*)$/.exec(line);
    if (fence) {
      if (
        fenceMatch &&
        fenceMatch[1][0] === fence.character &&
        fenceMatch[1].length >= fence.length &&
        fenceMatch[2].trim() === ""
      ) {
        fence = null;
      }
      continue;
    }
    if (fenceMatch) {
      fence = {
        character: fenceMatch[1][0] as "`" | "~",
        length: fenceMatch[1].length,
      };
      continue;
    }

    const headingMatch = /^ {0,3}(#{1,2})(?!#)[\t ]+(.+)$/.exec(line);
    if (!headingMatch) continue;
    finishSection(index);
    activeStart = -1;
    activeHeading = "";
    if (headingMatch[1].length === 1) continue;
    activeHeading = headingMatch[2].replace(/[\t ]+#+[\t ]*$/, "").trim();
    activeStart = index;
  }

  finishSection(lines.length);
  return sections;
}

function featuredSectionKind(heading: string): FeaturedSectionKind | null {
  const normalized = heading
    .toLocaleLowerCase("en-US")
    .replace(/&/g, " and ")
    .replace(/[^a-z0-9]+/g, " ")
    .trim();

  if (normalized === "highlights") return "highlights";
  if (normalized === "security" || normalized === "security notes") return "security";
  if (
    normalized === "breaking" ||
    normalized === "breaking changes" ||
    normalized === "breaking and risky changes" ||
    normalized === "breaking or risky changes"
  ) {
    return "breaking";
  }
  return null;
}

function conciseFeaturedSection(section: MarkdownSection, itemLimit: number): string {
  const blocks = parseMarkdownBlocks(markdownOutsideFences(section.markdown));
  const paragraph = blocks.find((block) => block.type === "p");
  const items: string[] = [];

  for (const block of blocks) {
    if (block.type === "ul" || block.type === "ol") {
      for (const item of block.items ?? []) {
        if (item.trim()) items.push(boundedInlineMarkdown(item.trim()));
        if (items.length === itemLimit) break;
      }
    } else if (block.type === "task") {
      for (const task of block.tasks ?? []) {
        if (task.text.trim()) {
          items.push(`[${task.checked ? "x" : " "}] ${boundedInlineMarkdown(task.text.trim())}`);
        }
        if (items.length === itemLimit) break;
      }
    }
    if (items.length === itemLimit) break;
  }

  const content: string[] = [`## ${section.heading}`];
  if (paragraph) {
    const paragraphMarkdown = paragraph.text.trim();
    const paragraphCharacters = Array.from(paragraphMarkdown);
    content.push(
      "",
      paragraphCharacters.length <= FEATURED_INLINE_LIMIT
        ? paragraphMarkdown
        : truncateAtWord(markdownToPlainText(paragraphMarkdown), FEATURED_INLINE_LIMIT),
    );
  }
  if (items.length > 0) content.push("", ...items.map((item) => `- ${item}`));
  if (!paragraph && items.length === 0) {
    content.push("", "Review this section in the full release notes.");
  }
  return content.join("\n");
}

function boundedInlineMarkdown(value: string): string {
  if (Array.from(value).length <= FEATURED_INLINE_LIMIT) return value;
  return truncateAtWord(markdownToPlainText(value), FEATURED_INLINE_LIMIT);
}

function legacyReleaseNotesPreview(content: string): string {
  const blocks = parseMarkdownBlocks(markdownOutsideFences(content));
  const items: string[] = [];

  for (const block of blocks) {
    if (block.type === "ul" || block.type === "ol") {
      for (const item of block.items ?? []) {
        if (item.trim()) items.push(boundedInlineMarkdown(item.trim()));
        if (items.length === LEGACY_ITEM_LIMIT) break;
      }
    } else if (block.type === "task") {
      for (const task of block.tasks ?? []) {
        if (task.text.trim()) {
          items.push(`[${task.checked ? "x" : " "}] ${boundedInlineMarkdown(task.text.trim())}`);
        }
        if (items.length === LEGACY_ITEM_LIMIT) break;
      }
    }
    if (items.length === LEGACY_ITEM_LIMIT) break;
  }

  if (items.length > 0) {
    return ["## Changes at a glance", "", ...items.map((item) => `- ${item}`)].join("\n");
  }

  const paragraph = blocks.find((block) => block.type === "p");
  if (paragraph) {
    const plainText = markdownToPlainText(paragraph.text).replace(/\s+/g, " ").trim();
    if (plainText) {
      return `## Changes at a glance\n\n${truncateAtWord(plainText, LEGACY_PARAGRAPH_LIMIT)}`;
    }
  }

  return "## Release details\n\nOpen the full release notes to review this update.";
}

function truncateAtWord(value: string, limit: number): string {
  const characters = Array.from(value);
  if (characters.length <= limit) return value;
  const candidate = characters.slice(0, limit).join("");
  const wordBoundary = candidate.lastIndexOf(" ");
  const shortened =
    wordBoundary >= Math.floor(limit * 0.6) ? candidate.slice(0, wordBoundary) : candidate;
  return `${shortened.trimEnd()}…`;
}

function markdownOutsideFences(content: string): string {
  const lines = content.replace(/\r\n?/g, "\n").split("\n");
  const visibleLines: string[] = [];
  let fence: { character: "`" | "~"; length: number } | null = null;

  for (const line of lines) {
    const fenceMatch = /^\s*(`{3,}|~{3,})(.*)$/.exec(line);
    if (fence) {
      if (
        fenceMatch &&
        fenceMatch[1][0] === fence.character &&
        fenceMatch[1].length >= fence.length &&
        fenceMatch[2].trim() === ""
      ) {
        fence = null;
      }
      visibleLines.push("");
      continue;
    }
    if (fenceMatch) {
      fence = {
        character: fenceMatch[1][0] as "`" | "~",
        length: fenceMatch[1].length,
      };
      visibleLines.push("");
      continue;
    }
    visibleLines.push(line);
  }

  return visibleLines.join("\n");
}

function comparableMarkdown(value: string): string {
  return value.replace(/\r\n?/g, "\n").trim();
}
