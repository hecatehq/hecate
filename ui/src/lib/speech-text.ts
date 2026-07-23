import {
  incompleteMarkdownDestinationStart,
  parseInlineNodes,
  parseMarkdownBlocks,
  type Block,
} from "./markdown";

const MAX_SPEECH_CHARACTERS = 12_000;
const MAX_SPEECH_SOURCE_CHARACTERS = 48_000;
const MAX_UTTERANCE_CHARACTERS = 500;
const TRUNCATION_NOTICE = "Response truncated for read aloud.";

export function markdownToSpeechChunks(content: string): string[] {
  const sourceWasTruncated = content.length > MAX_SPEECH_SOURCE_CHARACTERS;
  const boundedSource = sourceWasTruncated
    ? content.slice(0, safeSplitIndex(content, MAX_SPEECH_SOURCE_CHARACTERS))
    : content;
  const incompleteDestination = sourceWasTruncated
    ? incompleteMarkdownDestinationStart(boundedSource)
    : null;
  const source =
    incompleteDestination === null ? boundedSource : boundedSource.slice(0, incompleteDestination);
  const sections: string[] = [];
  let sectionCharacters = 0;
  let sectionsWereTruncated = false;

  sectionLoop: for (const block of parseMarkdownBlocks(source)) {
    for (const section of speechSectionsForBlock(block)) {
      const normalized = normalizeWhitespace(section);
      if (!normalized) continue;
      const separatorCharacters = sections.length > 0 ? 2 : 0;
      const remaining = MAX_SPEECH_CHARACTERS + 1 - sectionCharacters - separatorCharacters;
      if (remaining <= 0) {
        sectionsWereTruncated = true;
        break sectionLoop;
      }
      const boundedSection = normalized.slice(0, safeSplitIndex(normalized, remaining));
      if (boundedSection) {
        sections.push(boundedSection);
        sectionCharacters += separatorCharacters + boundedSection.length;
      }
      if (boundedSection.length < normalized.length) {
        sectionsWereTruncated = true;
        break sectionLoop;
      }
    }
  }

  return boundSpeechSections(sections, sourceWasTruncated || sectionsWereTruncated);
}

function* speechSectionsForBlock(block: Block): Generator<string> {
  switch (block.type) {
    case "code":
      yield "Code block omitted.";
      return;
    case "heading":
    case "p":
      yield inlineSpeechText(block.text);
      return;
    case "ul":
      for (const item of block.items ?? []) yield inlineSpeechText(item);
      return;
    case "ol":
      for (const [index, item] of (block.items ?? []).entries()) {
        yield `${index + 1}. ${inlineSpeechText(item)}`;
      }
      return;
    case "task":
      for (const task of block.tasks ?? []) {
        yield `${task.checked ? "Completed" : "Not completed"}: ${inlineSpeechText(task.text)}`;
      }
      return;
    case "table":
      yield* tableSpeechSections(block.table);
      return;
    case "hr":
      return;
  }
}

function inlineSpeechText(text: string): string {
  return inlineSpeechTextAtDepth(text, 0);
}

function inlineSpeechTextAtDepth(text: string, depth: number): string {
  if (depth >= 32) {
    return normalizeWhitespace(redactSpeechURIs(text));
  }

  const visibleText = parseInlineNodes(text)
    .map((node) => {
      if (node.t === "bold" || node.t === "italic") {
        return inlineSpeechTextAtDepth(node.v, depth + 1);
      }
      if (node.t === "link") {
        return node.v === node.href ? "link" : node.v;
      }
      return node.v;
    })
    .join("");

  // MarkdownContent renders ordinary JSX/HTML-looking source and entities as
  // literal text. Preserve that same visible text instead of interpreting it
  // as markup, while replacing URI-shaped values before they reach a voice.
  return normalizeWhitespace(redactSpeechURIs(visibleText));
}

function redactSpeechURIs(text: string): string {
  const output: string[] = [];
  let copiedUntil = 0;
  let position = 0;

  while (position < text.length) {
    const first = speechSchemeCharacterAt(text, position, true);
    if (!first || !isSpeechSchemeBoundary(text, position)) {
      position += first?.length ?? 1;
      continue;
    }

    let cursor = position + first.length;
    let colonEnd = speechColonEnd(text, cursor);
    while (colonEnd < 0) {
      const next = speechSchemeCharacterAt(text, cursor, false);
      if (!next) break;
      cursor += next.length;
      colonEnd = speechColonEnd(text, cursor);
    }
    if (colonEnd < 0) {
      position = Math.max(cursor, position + first.length);
      continue;
    }

    const end = speechURIEnd(text, colonEnd);
    if (end === colonEnd) {
      position = colonEnd;
      continue;
    }
    output.push(text.slice(copiedUntil, position), "link");
    copiedUntil = end;
    position = end;
  }

  if (output.length === 0) return text;
  output.push(text.slice(copiedUntil));
  return output.join("");
}

function speechSchemeCharacterAt(
  text: string,
  position: number,
  first: boolean,
): { character: string; length: number } | null {
  const literal = text[position] ?? "";
  if (isSpeechSchemeCharacter(literal, first)) return { character: literal, length: 1 };
  const entity = speechEntityAt(text, position);
  if (!entity || !isSpeechSchemeCharacter(entity.character, first)) return null;
  return entity;
}

function isSpeechSchemeCharacter(character: string, first: boolean): boolean {
  if (first) return /^[a-z]$/i.test(character);
  return /^[a-z\d+.-]$/i.test(character) || /[\t\n\r]/.test(character);
}

function isSpeechSchemeBoundary(text: string, position: number): boolean {
  if (position === 0) return true;
  return !/[a-z\d+.-]/i.test(text[position - 1]);
}

function speechColonEnd(text: string, position: number): number {
  if (text[position] === ":") return position + 1;
  const entity = speechEntityAt(text, position);
  return entity?.character === ":" ? position + entity.length : -1;
}

function speechEntityAt(
  text: string,
  position: number,
): { character: string; length: number } | null {
  if (text[position] !== "&") return null;
  if (text[position + 1] === "#") {
    let cursor = position + 2;
    let radix = 10;
    if (text[cursor]?.toLowerCase() === "x") {
      radix = 16;
      cursor += 1;
    }
    const digitsStart = cursor;
    const digitPattern = radix === 16 ? /[\da-f]/i : /\d/;
    while (cursor < text.length && digitPattern.test(text[cursor])) cursor += 1;
    if (cursor === digitsStart) return null;
    const value = Number.parseInt(text.slice(digitsStart, cursor), radix);
    if (!Number.isFinite(value) || value < 0 || value > 0x7f) return null;
    if (text[cursor] === ";") cursor += 1;
    return { character: String.fromCharCode(value), length: cursor - position };
  }

  for (const [name, character] of [
    ["newline", "\n"],
    ["colon", ":"],
    ["tab", "\t"],
  ] as const) {
    if (text.slice(position + 1, position + name.length + 1).toLowerCase() !== name) continue;
    if (text[position + name.length + 1] !== ";") continue;
    return { character, length: name.length + 2 };
  }
  return null;
}

function speechURIEnd(text: string, start: number): number {
  let parenthesisDepth = 0;
  let position = start;
  while (position < text.length) {
    const character = text[position];
    if ((/\s/.test(character) && !/[\t\n\r]/.test(character)) || /[<>{}"'`]/.test(character)) {
      break;
    }
    if (character === "(") parenthesisDepth += 1;
    else if (character === ")") {
      if (parenthesisDepth === 0) break;
      parenthesisDepth -= 1;
    }
    position += 1;
  }
  return position;
}

function* tableSpeechSections(
  table: { headers: string[]; rows: string[][] } | undefined,
): Generator<string> {
  if (!table) return;
  const headers = table.headers.map(inlineSpeechText);
  const visibleHeaders = headers.filter(Boolean);
  if (visibleHeaders.length > 0) yield `Table columns: ${visibleHeaders.join(", ")}.`;
  for (const [rowIndex, row] of table.rows.entries()) {
    const cells = table.headers
      .map((_, cellIndex) => {
        const value = inlineSpeechText(row[cellIndex] ?? "");
        if (!value) return "";
        const header = headers[cellIndex];
        return header ? `${header}: ${value}` : value;
      })
      .filter(Boolean);
    if (cells.length > 0) yield `Row ${rowIndex + 1}. ${cells.join("; ")}.`;
  }
}

function boundSpeechSections(sections: string[], forceTruncated = false): string[] {
  const normalized = sections.map(normalizeWhitespace).filter(Boolean);
  const combined = normalized.join("\n\n");
  const truncated = forceTruncated || combined.length > MAX_SPEECH_CHARACTERS;
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
