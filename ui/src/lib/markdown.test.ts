import { describe, expect, it } from "vitest";

import { parseInlineNodes, parseMarkdownBlocks } from "./markdown";

describe("parseMarkdownBlocks", () => {
  it("parses a plain paragraph", () => {
    const blocks = parseMarkdownBlocks("Hello world");
    expect(blocks).toEqual([{ type: "p", text: "Hello world" }]);
  });

  it("parses fenced code blocks with language tag", () => {
    const blocks = parseMarkdownBlocks("```ts\nconst x = 1;\n```");
    expect(blocks).toEqual([{ type: "code", text: "const x = 1;", lang: "ts" }]);
  });

  it("parses fenced code blocks with extra info string metadata", () => {
    const blocks = parseMarkdownBlocks("```sh hl_lines=1\ngit status\n```");
    expect(blocks).toEqual([{ type: "code", text: "git status", lang: "sh" }]);
  });

  it("parses indented fenced code blocks", () => {
    const blocks = parseMarkdownBlocks("  ```sh\ngit status\n  ```");
    expect(blocks).toEqual([{ type: "code", text: "git status", lang: "sh" }]);
  });

  it("parses fenced code block with no language tag", () => {
    const blocks = parseMarkdownBlocks("```\nraw code\n```");
    expect(blocks).toEqual([{ type: "code", text: "raw code", lang: "" }]);
  });

  it("parses headings h1–h3", () => {
    const input = "# One\n## Two\n### Three";
    const blocks = parseMarkdownBlocks(input);
    expect(blocks).toEqual([
      { type: "heading", text: "One", level: 1 },
      { type: "heading", text: "Two", level: 2 },
      { type: "heading", text: "Three", level: 3 },
    ]);
  });

  it("parses unordered list items", () => {
    const blocks = parseMarkdownBlocks("- apple\n- banana\n* cherry");
    expect(blocks).toEqual([{ type: "ul", text: "", items: ["apple", "banana", "cherry"] }]);
  });

  it("parses ordered list items", () => {
    const blocks = parseMarkdownBlocks("1. first\n2. second");
    expect(blocks).toEqual([{ type: "ol", text: "", items: ["first", "second"] }]);
  });

  it("parses task list items", () => {
    const blocks = parseMarkdownBlocks("- [x] done\n- [ ] todo\n* [X] shipped");
    expect(blocks).toEqual([
      {
        type: "task",
        text: "",
        tasks: [
          { checked: true, text: "done" },
          { checked: false, text: "todo" },
          { checked: true, text: "shipped" },
        ],
      },
    ]);
  });

  it("parses pipe tables", () => {
    const blocks = parseMarkdownBlocks(
      "| File | Status |\n| --- | --- |\n| README.md | updated |\n| docs.md | skipped |",
    );
    expect(blocks).toEqual([
      {
        type: "table",
        text: "",
        table: {
          headers: ["File", "Status"],
          rows: [
            ["README.md", "updated"],
            ["docs.md", "skipped"],
          ],
        },
      },
    ]);
  });

  it("does not treat a bare horizontal rule as a table separator", () => {
    const blocks = parseMarkdownBlocks("a | b\n---");
    expect(blocks).toEqual([
      { type: "p", text: "a | b" },
      { type: "hr", text: "" },
    ]);
  });

  it("parses horizontal rule", () => {
    expect(parseMarkdownBlocks("---")).toEqual([{ type: "hr", text: "" }]);
    expect(parseMarkdownBlocks("***")).toEqual([{ type: "hr", text: "" }]);
    expect(parseMarkdownBlocks("___")).toEqual([{ type: "hr", text: "" }]);
  });

  it("skips blank lines between blocks", () => {
    const blocks = parseMarkdownBlocks("# Title\n\nParagraph text.");
    expect(blocks).toEqual([
      { type: "heading", text: "Title", level: 1 },
      { type: "p", text: "Paragraph text." },
    ]);
  });

  it("accumulates multi-line paragraphs", () => {
    const blocks = parseMarkdownBlocks("line one\nline two");
    expect(blocks).toEqual([{ type: "p", text: "line one\nline two" }]);
  });

  it("stops paragraph accumulation at a code fence", () => {
    const blocks = parseMarkdownBlocks("intro\n```ts\ncode\n```");
    expect(blocks).toHaveLength(2);
    expect(blocks[0]).toEqual({ type: "p", text: "intro" });
    expect(blocks[1]).toEqual({ type: "code", text: "code", lang: "ts" });
  });

  it("stops paragraph accumulation at an indented code fence", () => {
    const blocks = parseMarkdownBlocks("intro\n  ```sh\ngit status\n  ```");
    expect(blocks).toEqual([
      { type: "p", text: "intro" },
      { type: "code", text: "git status", lang: "sh" },
    ]);
  });

  it("consumes an unmatched fence-looking line instead of looping forever", () => {
    const blocks = parseMarkdownBlocks("```bad`info");
    expect(blocks).toEqual([{ type: "p", text: "```bad`info" }]);
  });

  it("stops paragraph accumulation at a task list and table", () => {
    const blocks = parseMarkdownBlocks(
      "intro\n- [ ] todo\n\n| A | B |\n| --- | --- |\n| one | two |",
    );
    expect(blocks).toHaveLength(3);
    expect(blocks[0]).toEqual({ type: "p", text: "intro" });
    expect(blocks[1].type).toBe("task");
    expect(blocks[2].type).toBe("table");
  });
});

describe("parseInlineNodes", () => {
  it("returns a single text node for plain text", () => {
    expect(parseInlineNodes("hello")).toEqual([{ t: "text", v: "hello" }]);
  });

  it("parses **bold** spans", () => {
    expect(parseInlineNodes("say **hi** now")).toEqual([
      { t: "text", v: "say " },
      { t: "bold", v: "hi" },
      { t: "text", v: " now" },
    ]);
  });

  it("parses *italic* spans", () => {
    expect(parseInlineNodes("say *hi* now")).toEqual([
      { t: "text", v: "say " },
      { t: "italic", v: "hi" },
      { t: "text", v: " now" },
    ]);
  });

  it("parses `code` spans", () => {
    expect(parseInlineNodes("use `npm install`")).toEqual([
      { t: "text", v: "use " },
      { t: "code", v: "npm install" },
    ]);
  });

  it("parses markdown links", () => {
    expect(parseInlineNodes("open [Hecate](https://github.com/hecatehq/hecate)")).toEqual([
      { t: "text", v: "open " },
      { t: "link", v: "Hecate", href: "https://github.com/hecatehq/hecate" },
    ]);
  });

  it("parses bare http links", () => {
    expect(parseInlineNodes("see https://example.com/docs")).toEqual([
      { t: "text", v: "see " },
      { t: "link", v: "https://example.com/docs", href: "https://example.com/docs" },
    ]);
  });

  it("handles mixed inline markup", () => {
    const nodes = parseInlineNodes("**bold** and `code` and *em* and [docs](https://example.com)");
    expect(nodes).toEqual([
      { t: "bold", v: "bold" },
      { t: "text", v: " and " },
      { t: "code", v: "code" },
      { t: "text", v: " and " },
      { t: "italic", v: "em" },
      { t: "text", v: " and " },
      { t: "link", v: "docs", href: "https://example.com" },
    ]);
  });

  it("returns empty array for empty string", () => {
    expect(parseInlineNodes("")).toEqual([]);
  });
});
