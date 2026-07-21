import { parseInlineNodes, parseMarkdownBlocks } from "./markdown";

const MAX_SPEECH_CHARACTERS = 12_000;
const MAX_UTTERANCE_CHARACTERS = 500;
const TRUNCATION_NOTICE = "Response truncated for read aloud.";

export function markdownToSpeechChunks(content: string): string[] {
  const sections = parseMarkdownBlocks(content).flatMap((block) => {
    switch (block.type) {
      case "code":
        return ["Code block omitted."];
      case "heading":
      case "p":
        return [inlineSpeechText(block.text)];
      case "ul":
        return (block.items ?? []).map(inlineSpeechText);
      case "ol":
        return (block.items ?? []).map((item, index) => `${index + 1}. ${inlineSpeechText(item)}`);
      case "task":
        return (block.tasks ?? []).map(
          (task) =>
            `${task.checked ? "Completed" : "Not completed"}: ${inlineSpeechText(task.text)}`,
        );
      case "table":
        return tableSpeechSections(block.table);
      case "hr":
        return [];
    }
  });

  return boundSpeechSections(sections);
}

function inlineSpeechText(text: string): string {
  return normalizeWhitespace(
    parseInlineNodes(text)
      .map((node) => {
        if (node.t === "bold" || node.t === "italic") return inlineSpeechText(node.v);
        if (node.t === "link" && node.v === node.href) return "link";
        return node.v;
      })
      .join(""),
  );
}

function tableSpeechSections(table: { headers: string[]; rows: string[][] } | undefined): string[] {
  if (!table) return [];
  const headers = table.headers.map(inlineSpeechText).filter(Boolean);
  const sections = headers.length > 0 ? [`Table columns: ${headers.join(", ")}.`] : [];
  table.rows.forEach((row, rowIndex) => {
    const cells = row
      .map((cell, cellIndex) => {
        const value = inlineSpeechText(cell);
        if (!value) return "";
        const header = headers[cellIndex];
        return header ? `${header}: ${value}` : value;
      })
      .filter(Boolean);
    if (cells.length > 0) sections.push(`Row ${rowIndex + 1}. ${cells.join("; ")}.`);
  });
  return sections;
}

function boundSpeechSections(sections: string[]): string[] {
  const normalized = sections.map(normalizeWhitespace).filter(Boolean);
  const combined = normalized.join("\n\n");
  const truncated = combined.length > MAX_SPEECH_CHARACTERS;
  const availableCharacters = truncated
    ? MAX_SPEECH_CHARACTERS - TRUNCATION_NOTICE.length - 1
    : MAX_SPEECH_CHARACTERS;
  const bounded = combined.slice(0, safeSplitIndex(combined, availableCharacters)).trimEnd();
  const chunks = splitSpeechText(bounded, MAX_UTTERANCE_CHARACTERS);
  if (truncated) chunks.push(TRUNCATION_NOTICE);
  return chunks;
}

function splitSpeechText(text: string, maxCharacters: number): string[] {
  const chunks: string[] = [];
  let remaining = text.trim();

  while (remaining.length > maxCharacters) {
    const candidate = remaining.slice(0, maxCharacters + 1);
    const sentenceBreak = lastSentenceBreak(candidate);
    const whitespaceBreak = candidate.lastIndexOf(" ");
    const splitAt =
      sentenceBreak >= maxCharacters / 2
        ? sentenceBreak
        : whitespaceBreak > 0
          ? whitespaceBreak
          : maxCharacters;
    const safeSplitAt = safeSplitIndex(remaining, splitAt);
    chunks.push(remaining.slice(0, safeSplitAt).trim());
    remaining = remaining.slice(safeSplitAt).trimStart();
  }

  if (remaining) chunks.push(remaining);
  return chunks;
}

function safeSplitIndex(text: string, requestedIndex: number): number {
  const index = Math.max(0, Math.min(requestedIndex, text.length));
  if (
    index > 0 &&
    index < text.length &&
    isHighSurrogate(text.charCodeAt(index - 1)) &&
    isLowSurrogate(text.charCodeAt(index))
  ) {
    return index - 1;
  }
  return index;
}

function isHighSurrogate(codeUnit: number): boolean {
  return codeUnit >= 0xd800 && codeUnit <= 0xdbff;
}

function isLowSurrogate(codeUnit: number): boolean {
  return codeUnit >= 0xdc00 && codeUnit <= 0xdfff;
}

function lastSentenceBreak(text: string): number {
  for (let index = text.length - 1; index >= 0; index -= 1) {
    if (/[.!?]/.test(text[index]) && /\s/.test(text[index + 1] ?? "")) return index + 1;
  }
  return -1;
}

function normalizeWhitespace(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}
