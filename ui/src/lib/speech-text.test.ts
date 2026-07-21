import { describe, expect, it } from "vitest";

import { markdownToSpeechChunks } from "./speech-text";

describe("markdownToSpeechChunks", () => {
  it("turns visible Markdown structure into readable text", () => {
    const chunks = markdownToSpeechChunks(`
# Result

Use **Hecate** with *care* and \`go test\`.

- Review [the guide](https://example.com/private-path)
- Keep the change focused

1. Build
2. Test

- [x] Types
- [ ] Browser smoke
`);

    expect(chunks.join(" ").replace(/\s+/g, " ")).toBe(
      "Result Use Hecate with care and go test. Review the guide Keep the change focused 1. Build 2. Test Completed: Types Not completed: Browser smoke",
    );
    expect(chunks.join(" ")).not.toContain("example.com");
  });

  it("omits fenced source while preserving an audible marker", () => {
    expect(
      markdownToSpeechChunks("Before.\n\n```ts\nconst secret = token;\n```\n\nAfter."),
    ).toEqual(["Before.\n\nCode block omitted.\n\nAfter."]);
  });

  it("does not speak bare link destinations", () => {
    expect(markdownToSpeechChunks("Open https://example.com/private/token now.")).toEqual([
      "Open link now.",
    ]);
  });

  it("flattens tables using their visible headers", () => {
    expect(
      markdownToSpeechChunks(
        "| Route | State |\n| --- | --- |\n| Local | Ready |\n| Cloud | Off |",
      ),
    ).toEqual([
      "Table columns: Route, State.\n\nRow 1. Route: Local; State: Ready.\n\nRow 2. Route: Cloud; State: Off.",
    ]);
  });

  it("skips separators and empty content", () => {
    expect(markdownToSpeechChunks("\n---\n\n")).toEqual([]);
  });

  it("bounds long responses into short utterances and discloses truncation", () => {
    const chunks = markdownToSpeechChunks(`Summary. ${"word ".repeat(3_000)}`);

    expect(chunks.length).toBeGreaterThan(1);
    expect(chunks.every((chunk) => chunk.length <= 500)).toBe(true);
    expect(chunks.at(-1)).toContain("Response truncated for read aloud.");
    expect(chunks.join(" ").length).toBeLessThanOrEqual(12_000);
  });

  it("keeps emoji code points intact at chunk and truncation boundaries", () => {
    const chunkBoundary = markdownToSpeechChunks(`${"a".repeat(499)}😀${"b".repeat(10)}`);
    expect(chunkBoundary[0]).toBe("a".repeat(499));
    expect(chunkBoundary[1]).toBe(`😀${"b".repeat(10)}`);

    const notice = "Response truncated for read aloud.";
    const speechLimitBeforeNotice = 12_000 - notice.length - 1;
    const truncationBoundary = markdownToSpeechChunks(
      `${"a".repeat(speechLimitBeforeNotice - 1)}😀${"b".repeat(100)}`,
    );
    expect(truncationBoundary.at(-1)).toBe(notice);
    expect(truncationBoundary.slice(0, -1).join("")).not.toMatch(
      /[\uD800-\uDBFF]$|^[\uDC00-\uDFFF]/,
    );
  });
});
