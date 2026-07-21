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
    expect(
      markdownToSpeechChunks(
        "Open https://example.com/private/token, file:///Users/alice/private.txt, mailto:alice@example.com, data:text/plain,(secret), custom://host/(tenant)/private, or blob:https://example.com/private-id.",
      ),
    ).toEqual(["Open link link link link link or link"]);
  });

  it("preserves Markdown labels while suppressing balanced and parenthesized destinations", () => {
    expect(
      markdownToSpeechChunks(
        "Open [tenant guide](https://example.com/(private)/guide?token=secret) or https://example.com/a(b)c/private?token=secret.",
      ),
    ).toEqual(["Open tenant guide or link"]);
  });

  it("bounds repeated malformed Markdown links without rescanning the same line", () => {
    const chunks = markdownToSpeechChunks(
      `${"[missing](".repeat(12_000)}\n[Visible guide](https://example.com/private)`,
    );

    expect(chunks.at(-1)).toBe("Response truncated for read aloud.");
    expect(chunks.reduce((total, chunk) => total + chunk.length, 0)).toBeLessThanOrEqual(12_000);
  });

  it("hides a valid relative destination after a malformed bracket prefix", () => {
    const prefix = "[".repeat(100) + "a".repeat(1_000) + "]x ";
    const speech = markdownToSpeechChunks(`${prefix}[label](relative/SECRET_VALUE)`).join(" ");

    expect(speech).toContain("label");
    expect(speech).not.toContain("relative/SECRET_VALUE");
  });

  it("omits a destination cut by the source bound", () => {
    const privateDestination = "PRIVATE_DESTINATION_".repeat(3_000);
    const speech = markdownToSpeechChunks(
      `Prefix [public](relative/${privateDestination}) tail`,
    ).join(" ");

    expect(speech).toContain("Prefix");
    expect(speech).toContain("Response truncated for read aloud.");
    expect(speech).not.toContain("PRIVATE_DESTINATION");
    expect(speech).not.toContain("relative/");
  });

  it("omits a link whose destination begins exactly at the source bound", () => {
    const opening = "```\n";
    const suffix = "\n```\n[public](";
    const bounded = `${opening}${"x".repeat(48_000 - opening.length - suffix.length)}${suffix}`;
    const speech = markdownToSpeechChunks(`${bounded}PRIVATE_DESTINATION`).join(" ");

    expect(speech).toContain("Code block omitted.");
    expect(speech).toContain("Response truncated for read aloud.");
    expect(speech).not.toContain("[public](");
    expect(speech).not.toContain("PRIVATE_DESTINATION");
  });

  it("does not mistake old malformed links for a source-cut destination", () => {
    const speech = markdownToSpeechChunks(
      `Before [broken](not closed\n${"PUBLIC_AFTER ".repeat(5_000)}`,
    ).join(" ");

    expect(speech).toContain("PUBLIC_AFTER");
    expect(speech).toContain("Response truncated for read aloud.");
  });

  it("redacts destinations whose schemes are disguised with HTML entities", () => {
    expect(
      markdownToSpeechChunks(
        "file&colon;///Users/alice/private.txt data&#58;text/plain,secret mailto&#x3a;alice@example.com c&#117;stom&colon;//private-host/token file&#58///Users/bob/private.txt mailto&#58bob@example.com custom&#x3A//private-host/hex",
      ),
    ).toEqual(["link link link link link link link"]);
    expect(
      markdownToSpeechChunks(
        "<span>file&colon;///Users/alice/private.txt</span> <span>data&#58;text/plain,secret</span> <span>file&#58///Users/bob/private.txt</span> <span>custom&#x3A//private-host/hex</span>",
      ),
    ).toEqual(["<span>link</span> <span>link</span> <span>link</span> <span>link</span>"]);
    expect(
      markdownToSpeechChunks("&#102;&#105;&#108;&#101;&colon;///Users/alice/PRIVATE fully encoded"),
    ).toEqual(["link fully encoded"]);
  });

  it("does not treat semicolonless named-entity prefixes as URI syntax", () => {
    expect(markdownToSpeechChunks("Example a&colonial note and file&colonial path.")).toEqual([
      "Example a&colonial note and file&colonial path.",
    ]);
  });

  it("redacts URI candidates containing URL-ignored ASCII controls", () => {
    expect(
      markdownToSpeechChunks(
        "fi&Tab;le:///Users/alice/private.txt file&#10;:///Users/bob/private.txt file://&#13;/Users/carol/private.txt https&#10;://host.invalid/private?token=secret",
      ),
    ).toEqual(["link link link link"]);
  });

  it("speaks literal JSX, tag-like text, and entities exactly as the renderer shows them", () => {
    expect(
      markdownToSpeechChunks(
        'Use <Widget prop="value"> in JSX. Visible <script>example</script>. <!-- visible comment --> Escaped &lt;tag&gt;.',
      ),
    ).toEqual([
      'Use <Widget prop="value"> in JSX. Visible <script>example</script>. <!-- visible comment --> Escaped &lt;tag&gt;.',
    ]);
  });

  it("redacts URI-shaped values without interpreting visible tag-like text", () => {
    expect(
      markdownToSpeechChunks(
        '<Widget source="file:///Users/alice/private.txt">Visible <script>example</script> <a href="mailto:alice@example.com">Email support</a></Widget>',
      ),
    ).toEqual([
      '<Widget source="link">Visible <script>example</script> <a href="link">Email support</a></Widget>',
    ]);
    expect(
      markdownToSpeechChunks("<Widget source=custom://private-host/token>Visible label</Widget>"),
    ).toEqual(["<Widget source=link>Visible label</Widget>"]);
    expect(
      markdownToSpeechChunks("<Widget source={file:///private}>Visible label</Widget>"),
    ).toEqual(["<Widget source={link}>Visible label</Widget>"]);
  });

  it("balances URI parentheses and preserves unmatched closing punctuation", () => {
    expect(
      markdownToSpeechChunks("custom://host/(tenant)/private custom://host/private) visible"),
    ).toEqual(["link link) visible"]);
  });

  it("redacts URI-shaped Markdown labels and inline code", () => {
    expect(
      markdownToSpeechChunks(
        "Open [file:///Users/alice/private.txt](https://example.com/public) or `custom&colon;//private-host/token`.",
      ),
    ).toEqual(["Open link or link"]);
  });

  it("keeps malformed tag-like source audible because it remains visible", () => {
    expect(markdownToSpeechChunks('Before <span title="unterminated> after')).toEqual([
      'Before <span title="unterminated> after',
    ]);
    expect(markdownToSpeechChunks("Before <!-- unterminated comment")).toEqual([
      "Before <!-- unterminated comment",
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

  it("does not speak table cells beyond the columns the renderer displays", () => {
    const speech = markdownToSpeechChunks("| Public |\n| --- |\n| shown | SECRET_HIDDEN |")
      .join(" ")
      .replace(/\s+/g, " ");

    expect(speech).toBe("Table columns: Public. Row 1. Public: shown.");
    expect(speech).not.toContain("SECRET_HIDDEN");
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

  it.each([
    ["list", "- bounded list item with detail\n".repeat(150_000)],
    [
      "table",
      `| Route | State |\n| --- | --- |\n${"| Local provider | Ready |\n".repeat(150_000)}`,
    ],
  ])("bounds multi-megabyte %s input before section materialization", (_kind, content) => {
    const chunks = markdownToSpeechChunks(content);

    expect(chunks.at(-1)).toBe("Response truncated for read aloud.");
    expect(chunks.every((chunk) => chunk.length <= 500)).toBe(true);
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
