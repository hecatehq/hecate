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

    const fenceMatch = /^```(\w*)/.exec(line);
    if (fenceMatch) {
      const lang = fenceMatch[1];
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith("```")) {
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
      !/^```/.test(lines[i]) &&
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
  return cells.length > 0 && cells.every(cell => /^:?-{3,}:?$/.test(cell.trim()));
}

function splitTableRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map(cell => cell.trim());
}

export function parseInlineNodes(text: string): InlineNode[] {
  const nodes: InlineNode[] = [];
  const re = /(\[([^\]\n]+)\]\(([^)\s]+)\)|https?:\/\/[^\s<>)]+|\*\*(.+?)\*\*|\*(.+?)\*|`(.+?)`)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) nodes.push({ t: "text", v: text.slice(last, m.index) });
    if (m[0].startsWith("[")) nodes.push({ t: "link", v: m[2], href: m[3] });
    else if (m[0].startsWith("http://") || m[0].startsWith("https://")) nodes.push({ t: "link", v: m[0], href: m[0] });
    else if (m[0].startsWith("**")) nodes.push({ t: "bold", v: m[4] });
    else if (m[0].startsWith("`")) nodes.push({ t: "code", v: m[6] });
    else nodes.push({ t: "italic", v: m[5] });
    last = m.index + m[0].length;
  }
  if (last < text.length) nodes.push({ t: "text", v: text.slice(last) });
  return nodes;
}
