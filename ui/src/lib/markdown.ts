export type Block = {
  type: "code" | "heading" | "ul" | "ol" | "task" | "table" | "hr" | "p";
  text: string;
  lang?: string;
  level?: number;
  items?: string[];
  tasks?: Array<{ checked: boolean; text: string }>;
  table?: { headers: string[]; rows: string[][] };
};

export type InlineNode =
  | { t: "text"; v: string }
  | { t: "bold"; v: string }
  | { t: "italic"; v: string }
  | { t: "code"; v: string }
  | { t: "link"; v: string; href: string };

export function parseMarkdownBlocks(content: string): Block[] {
  const blocks: Block[] = [];
  const lines = content.split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    const fenceMatch = /^\s*```([^\s`]*)(?:\s+.*)?$/.exec(line);
    if (fenceMatch) {
      const lang = fenceMatch[1];
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      i++;
      blocks.push({ type: "code", text: codeLines.join("\n"), lang });
      continue;
    }

    const headingMatch = /^(#{1,3}) (.+)/.exec(line);
    if (headingMatch) {
      blocks.push({ type: "heading", text: headingMatch[2], level: headingMatch[1].length });
      i++;
      continue;
    }

    if (/^(-{3,}|\*{3,}|_{3,})$/.test(line.trim())) {
      blocks.push({ type: "hr", text: "" });
      i++;
      continue;
    }

    if (isTableStart(lines, i)) {
      const headers = splitTableRow(lines[i]);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && isTableRow(lines[i])) {
        rows.push(splitTableRow(lines[i]));
        i++;
      }
      blocks.push({ type: "table", text: "", table: { headers, rows } });
      continue;
    }

    if (/^[-*] \[[ xX]\] /.test(line)) {
      const tasks: Array<{ checked: boolean; text: string }> = [];
      while (i < lines.length && /^[-*] \[[ xX]\] /.test(lines[i])) {
        const match = /^[-*] \[([ xX])\] (.*)/.exec(lines[i]);
        if (match) tasks.push({ checked: match[1].toLowerCase() === "x", text: match[2] });
        i++;
      }
      blocks.push({ type: "task", text: "", tasks });
      continue;
    }

    if (/^[-*] /.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[-*] /.test(lines[i])) {
        items.push(lines[i].replace(/^[-*] /, ""));
        i++;
      }
      blocks.push({ type: "ul", text: "", items });
      continue;
    }

    if (/^\d+\. /.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\d+\. /.test(lines[i])) {
        items.push(lines[i].replace(/^\d+\. /, ""));
        i++;
      }
      blocks.push({ type: "ol", text: "", items });
      continue;
    }

    if (line.trim() === "") {
      i++;
      continue;
    }

    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^\s*```/.test(lines[i]) &&
      !/^#{1,3} /.test(lines[i]) &&
      !/^(-{3,}|\*{3,}|_{3,})$/.test(lines[i].trim()) &&
      !isTableStart(lines, i) &&
      !/^[-*] \[[ xX]\] /.test(lines[i]) &&
      !/^[-*] /.test(lines[i]) &&
      !/^\d+\. /.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    if (paraLines.length === 0) {
      paraLines.push(line);
      i++;
    }
    blocks.push({ type: "p", text: paraLines.join("\n") });
  }

  return blocks;
}

function isTableStart(lines: string[], index: number): boolean {
  return isTableRow(lines[index]) && isTableSeparator(lines[index + 1] ?? "");
}

function isTableRow(line: string): boolean {
  return line.includes("|") && line.trim().replace(/\|/g, "").trim() !== "";
}

function isTableSeparator(line: string): boolean {
  if (!line.includes("|")) return false;
  const cells = splitTableRow(line);
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell.trim()));
}

function splitTableRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((cell) => cell.trim());
}

export function parseInlineNodes(text: string): InlineNode[] {
  const nodes: InlineNode[] = [];
  const token = /(\[|https?:\/\/|\*\*|\*|`)/g;
  let cursor = 0;
  let markdownLinkIndex: MarkdownLinkIndex | null = null;

  while (cursor < text.length) {
    token.lastIndex = cursor;
    const match = token.exec(text);
    if (!match) {
      pushInlineText(nodes, text.slice(cursor));
      break;
    }
    if (match.index > cursor) pushInlineText(nodes, text.slice(cursor, match.index));

    const start = match.index;
    const value = match[0];
    if (value === "[") {
      markdownLinkIndex ??= buildMarkdownLinkIndex(text);
      const link = markdownLinkAt(text, start, markdownLinkIndex);
      if (link) {
        nodes.push({ t: "link", v: link.label, href: link.destination });
        cursor = link.end;
        continue;
      }
    } else if (value === "http://" || value === "https://") {
      const end = bareHTTPLinkEnd(text, start);
      const href = text.slice(start, end);
      nodes.push({ t: "link", v: href, href });
      cursor = end;
      continue;
    } else {
      const delimiter = value;
      const end = text.indexOf(delimiter, start + delimiter.length);
      if (
        end > start + delimiter.length &&
        !text.slice(start + delimiter.length, end).includes("\n")
      ) {
        const content = text.slice(start + delimiter.length, end);
        if (delimiter === "**") nodes.push({ t: "bold", v: content });
        else if (delimiter === "*") nodes.push({ t: "italic", v: content });
        else nodes.push({ t: "code", v: content });
        cursor = end + delimiter.length;
        continue;
      }
    }

    pushInlineText(nodes, value);
    cursor = start + value.length;
  }

  return nodes;
}

export function incompleteMarkdownDestinationStart(text: string): number | null {
  if (!text.includes("](")) return null;
  const index = buildMarkdownLinkIndex(text);
  for (let start = text.indexOf("["); start >= 0; start = text.indexOf("[", start + 1)) {
    const labelEnd = index.nextClosingBracket[start + 1] ?? -1;
    if (labelEnd <= start + 1 || text[labelEnd + 1] !== "(") continue;
    const destinationStart = labelEnd + 2;
    if (destinationStart > text.length) continue;
    if (index.matchingParenthesis[labelEnd + 1] >= 0) continue;
    if (destinationStart === text.length || index.nextWhitespace[destinationStart] < 0)
      return start;
  }
  return null;
}

type MarkdownLinkIndex = {
  matchingParenthesis: Int32Array;
  nextClosingBracket: Int32Array;
  nextWhitespace: Int32Array;
};

function markdownLinkAt(
  text: string,
  start: number,
  index: MarkdownLinkIndex,
): { label: string; destination: string; end: number } | null {
  const labelEnd = index.nextClosingBracket[start + 1] ?? -1;
  if (labelEnd <= start + 1 || text[labelEnd + 1] !== "(") return null;
  const destinationStart = labelEnd + 2;
  const destinationEnd = index.matchingParenthesis[labelEnd + 1] ?? -1;
  if (destinationEnd <= destinationStart) return null;
  const whitespace = index.nextWhitespace[destinationStart] ?? -1;
  if (whitespace >= 0 && whitespace < destinationEnd) return null;
  return {
    label: text.slice(start + 1, labelEnd),
    destination: text.slice(destinationStart, destinationEnd),
    end: destinationEnd + 1,
  };
}

function buildMarkdownLinkIndex(text: string): MarkdownLinkIndex {
  const matchingParenthesis = new Int32Array(text.length).fill(-1);
  const nextClosingBracket = new Int32Array(text.length + 1).fill(-1);
  const nextWhitespace = new Int32Array(text.length + 1).fill(-1);
  const parenthesisStack: number[] = [];
  let backslashRun = 0;

  for (let position = 0; position < text.length; position += 1) {
    const character = text[position];
    if (character === "\\") {
      backslashRun += 1;
      continue;
    }
    const escaped = backslashRun % 2 === 1;
    backslashRun = 0;
    if (escaped) continue;
    if (character === "(") parenthesisStack.push(position);
    else if (character === ")") {
      const opening = parenthesisStack.pop();
      if (opening !== undefined) matchingParenthesis[opening] = position;
    }
  }

  let closingBracket = -1;
  let whitespace = -1;
  for (let position = text.length - 1; position >= 0; position -= 1) {
    const character = text[position];
    if (character === "\n") closingBracket = -1;
    else if (character === "]") closingBracket = position;
    nextClosingBracket[position] = closingBracket;
    if (/\s/.test(character)) whitespace = position;
    nextWhitespace[position] = whitespace;
  }

  return { matchingParenthesis, nextClosingBracket, nextWhitespace };
}

function bareHTTPLinkEnd(text: string, start: number): number {
  let parenthesisDepth = 0;
  let index = start;
  while (index < text.length) {
    const character = text[index];
    if (/\s/.test(character) || character === "<" || character === ">" || /["'`]/.test(character)) {
      break;
    }
    if (character === "(") parenthesisDepth += 1;
    else if (character === ")") {
      if (parenthesisDepth === 0) break;
      parenthesisDepth -= 1;
    }
    index += 1;
  }
  return index;
}

function pushInlineText(nodes: InlineNode[], value: string): void {
  if (!value) return;
  const previous = nodes.at(-1);
  if (previous?.t === "text") {
    previous.v += value;
    return;
  }
  nodes.push({ t: "text", v: value });
}
