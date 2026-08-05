import { describe, expect, it } from "vitest";

import { buildReleaseNotesPresentation } from "./release-notes";

describe("buildReleaseNotesPresentation", () => {
  it("prioritizes highlights, security, and breaking sections in that order", () => {
    const notes = [
      "# Hecate v0.5.1",
      "",
      "A concise release for desktop operators.",
      "",
      "## Breaking changes",
      "",
      "- Existing local state is migrated once.",
      "",
      "## Verification",
      "",
      "- Verified with the release smoke suite.",
      "",
      "## Highlights",
      "",
      "- Updates now show a useful summary.",
      "",
      "## Security",
      "",
      "- Download verification remains mandatory.",
    ].join("\n");

    expect(buildReleaseNotesPresentation(notes)).toEqual({
      featuredMarkdown: [
        "## Highlights",
        "",
        "- Updates now show a useful summary.",
        "",
        "## Security",
        "",
        "- Download verification remains mandatory.",
        "",
        "## Breaking changes",
        "",
        "- Existing local state is migrated once.",
      ].join("\n"),
      fullMarkdown: notes,
      hasAdditionalDetails: true,
    });
  });

  it("does not treat headings inside fenced code as release sections", () => {
    const notes = [
      "# Hecate v0.5.1",
      "",
      "```md",
      "## Highlights",
      "- This is an example, not a release highlight.",
      "```",
      "",
      "## Security notes",
      "",
      "~~~md",
      "- Ignore this fenced list too.",
      "~~~",
      "",
      "- Fixed a credential disclosure edge case.",
    ].join("\n");

    expect(buildReleaseNotesPresentation(notes)?.featuredMarkdown).toBe(
      "## Security notes\n\n- Fixed a credential disclosure edge case.",
    );
  });

  it("ends a featured section when another document title starts", () => {
    const notes = [
      "## Highlights",
      "",
      "- Visible release change.",
      "",
      "# Embedded legacy changelog",
      "",
      "- Must stay out of the featured preview.",
      "",
      "## Security",
      "",
      "- Visible security change.",
    ].join("\n");

    const featured = buildReleaseNotesPresentation(notes)?.featuredMarkdown ?? "";

    expect(featured).toContain("Visible release change.");
    expect(featured).toContain("Visible security change.");
    expect(featured).not.toContain("Embedded legacy changelog");
    expect(featured).not.toContain("Must stay out of the featured preview.");
  });

  it("bounds malformed legacy highlight sections without losing the full notes", () => {
    const longItem = `[Long linked change](https://example.com/details) ${"detail ".repeat(60)}`;
    const notes = [
      "## Highlights",
      "",
      `- ${longItem}`,
      "- Change two.",
      "- Change three.",
      "- Change four.",
      "- Change five.",
      "- Change six.",
      "- Change seven.",
    ].join("\n");

    const presentation = buildReleaseNotesPresentation(notes);

    expect(presentation?.featuredMarkdown).toContain("Long linked change detail");
    expect(presentation?.featuredMarkdown).not.toContain("https://example.com/details");
    expect(presentation?.featuredMarkdown).toContain("Change six.");
    expect(presentation?.featuredMarkdown).not.toContain("Change seven.");
    expect(Array.from(presentation?.featuredMarkdown ?? "").length).toBeLessThan(500);
    expect(presentation?.fullMarkdown).toContain(longItem);
    expect(presentation?.fullMarkdown).toContain("Change seven.");
    expect(presentation?.hasAdditionalDetails).toBe(true);
  });

  it("uses the first three list items as a concise legacy fallback", () => {
    const notes = [
      "# Changelog",
      "",
      "```md",
      "- Ignore this example.",
      "```",
      "",
      "## Changes",
      "",
      "- First shipped change.",
      "- Second shipped change.",
      "- Third shipped change.",
      "- Fourth shipped change.",
    ].join("\n");

    const presentation = buildReleaseNotesPresentation(notes);

    expect(presentation?.featuredMarkdown).toBe(
      [
        "## Changes at a glance",
        "",
        "- First shipped change.",
        "- Second shipped change.",
        "- Third shipped change.",
      ].join("\n"),
    );
    expect(presentation?.featuredMarkdown).not.toContain("Ignore this example");
    expect(presentation?.featuredMarkdown).not.toContain("Fourth shipped change");
    expect(presentation?.fullMarkdown).toBe(notes);
    expect(presentation?.hasAdditionalDetails).toBe(true);
  });

  it("falls back to a bounded plain-text paragraph when legacy notes have no list", () => {
    const longParagraph = `Read the [release guide](https://example.com/releases) before ${"continuing ".repeat(50)}`;
    const presentation = buildReleaseNotesPresentation(`# Changelog\n\n${longParagraph}`);

    expect(
      presentation?.featuredMarkdown.startsWith(
        "## Changes at a glance\n\nRead the release guide before continuing",
      ),
    ).toBe(true);
    expect(presentation?.featuredMarkdown).not.toContain("https://example.com");
    expect(Array.from(presentation?.featuredMarkdown ?? "").length).toBeLessThan(360);
    expect(presentation?.featuredMarkdown.endsWith("…")).toBe(true);
  });

  it("omits the disclosure when the concise notes are already the complete notes", () => {
    const notes = "## Highlights\n\n- One focused improvement.";

    expect(buildReleaseNotesPresentation(notes)).toEqual({
      featuredMarkdown: notes,
      fullMarkdown: notes,
      hasAdditionalDetails: false,
    });
  });

  it("returns null for empty notes", () => {
    expect(buildReleaseNotesPresentation(" \n\t ")).toBeNull();
  });
});
