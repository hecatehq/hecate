import { afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

import {
  MAX_RELEASE_NOTES_CHARACTERS,
  loadCuratedReleaseNotes,
  parseReleaseCommandArgs,
  validateCuratedReleaseNotes,
} from "./release-notes";
import { parseMarkdownBlocks } from "../ui/src/lib/markdown";

const temporaryDirectories: string[] = [];

function validNotes(version = "v1.2.3"): string {
  return [
    `# Hecate ${version}`,
    "",
    "A short operator-facing summary.",
    "",
    "## Highlights",
    "",
    "- Makes updates easier to review.",
    "- Keeps the complete technical notes available.",
    "",
    "## Breaking or risky changes",
    "",
    "- No migration is required.",
    "",
  ].join("\n");
}

function temporaryDirectory(prefix: string): string {
  const directory = mkdtempSync(join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { recursive: true, force: true });
  }
});

describe("parseReleaseCommandArgs", () => {
  test("accepts curated notes with release controls in any order", () => {
    expect(
      parseReleaseCommandArgs([
        "--preflight-only",
        "v1.2.3",
        "--notes",
        "docs/releases/v1.2.3.md",
        "--skip-snapshot",
        "--yes",
      ]),
    ).toEqual({
      version: "v1.2.3",
      notesPath: "docs/releases/v1.2.3.md",
      skipSnapshot: true,
      preflightOnly: true,
      assumeYes: true,
    });
  });

  test("requires an explicit notes path", () => {
    expect(() => parseReleaseCommandArgs(["v1.2.3"])).toThrow(
      "--notes is required so the published release and desktop updater never fall back to a commit dump.",
    );
    expect(() => parseReleaseCommandArgs(["v1.2.3", "--notes"])).toThrow(
      "--notes requires a Markdown file path.",
    );
  });

  test("rejects ambiguous or unknown arguments", () => {
    expect(() =>
      parseReleaseCommandArgs(["v1.2.3", "--notes", "one.md", "--notes", "two.md"]),
    ).toThrow("--notes may be supplied only once.");
    expect(() => parseReleaseCommandArgs(["v1.2.3", "extra", "--notes", "one.md"])).toThrow(
      "unexpected positional argument: extra",
    );
    expect(() => parseReleaseCommandArgs(["v1.2.3", "--notes", "one.md", "--force"])).toThrow(
      "unknown option: --force",
    );
  });
});

describe("validateCuratedReleaseNotes", () => {
  test("accepts a bounded release with concise highlights", () => {
    expect(() => validateCuratedReleaseNotes(validNotes(), "v1.2.3")).not.toThrow();
  });

  test("keeps the versioned release template invalid until it is filled in", () => {
    const template = readFileSync(resolve(import.meta.dir, "../docs/releases/template.md"), "utf8");
    expect(() =>
      validateCuratedReleaseNotes(template.replace("vX.Y.Z", "v1.2.3"), "v1.2.3"),
    ).toThrow("release notes must not contain HTML comments; remove all template guidance.");
  });

  test("requires the exact versioned title and one real Highlights section", () => {
    expect(() => validateCuratedReleaseNotes(validNotes("v1.2.4"), "v1.2.3")).toThrow(
      "release notes must begin with exactly: # Hecate v1.2.3",
    );
    expect(() =>
      validateCuratedReleaseNotes("# Hecate v1.2.3\n\n## Changelog\n\n- raw commit", "v1.2.3"),
    ).toThrow("release notes must contain exactly one ## Highlights section.");
    expect(() =>
      validateCuratedReleaseNotes(
        "# Hecate v1.2.3\n\n```md\n## Highlights\n- hidden fixture\n```",
        "v1.2.3",
      ),
    ).toThrow("release notes must contain exactly one ## Highlights section.");
    expect(() =>
      validateCuratedReleaseNotes(
        [
          "# Hecate v1.2.3",
          "",
          "`````md",
          "`````md",
          "## Highlights",
          "- still inside the real fence",
          "`````",
        ].join("\n"),
        "v1.2.3",
      ),
    ).toThrow("release notes must contain exactly one ## Highlights section.");
  });

  test("requires one to six highlight bullets", () => {
    expect(() =>
      validateCuratedReleaseNotes("# Hecate v1.2.3\n\n## Highlights\n\nNothing changed.", "v1.2.3"),
    ).toThrow("## Highlights must contain at least one bullet.");

    const bullets = Array.from({ length: 7 }, (_, index) => `- Change ${index + 1}`).join("\n");
    expect(() =>
      validateCuratedReleaseNotes(`# Hecate v1.2.3\n\n## Highlights\n\n${bullets}`, "v1.2.3"),
    ).toThrow("## Highlights must contain no more than six concise bullets.");

    expect(() =>
      validateCuratedReleaseNotes(
        "# Hecate v1.2.3\n\n## Highlights\n\n```md\n- not a real highlight\n```",
        "v1.2.3",
      ),
    ).toThrow("## Highlights must contain at least one bullet.");

    expect(() =>
      validateCuratedReleaseNotes(
        "# Hecate v1.2.3\n\n## Highlights\n\n<!--\n- hidden draft\n-->",
        "v1.2.3",
      ),
    ).toThrow("release notes must not contain HTML comments; remove all template guidance.");
  });

  test.each(["-", "*"])("accepts and renders the canonical %s highlight marker", (marker) => {
    const markdown = `# Hecate v1.2.3\n\n## Highlights\n\n${marker} Visible update.`;

    expect(() => validateCuratedReleaseNotes(markdown, "v1.2.3")).not.toThrow();
    expect(parseMarkdownBlocks(`${marker} Visible update.`)).toEqual([
      { type: "ul", text: "", items: ["Visible update."] },
    ]);
  });

  test.each(["+ Visible update.", "  - Visible update.", "-\tVisible update."])(
    "rejects a highlight form the updater would not render as a list: %s",
    (highlight) => {
      const markdown = `# Hecate v1.2.3\n\n## Highlights\n\n${highlight}`;

      expect(() => validateCuratedReleaseNotes(markdown, "v1.2.3")).toThrow(
        "## Highlights must contain at least one bullet.",
      );
    },
  );

  test("requires content for every included section", () => {
    expect(() =>
      validateCuratedReleaseNotes(
        "# Hecate v1.2.3\n\n## Highlights\n\n- Ready.\n\n## Security\n\n## Verification\n\n- Passed.",
        "v1.2.3",
      ),
    ).toThrow("## Security must contain release information or be removed.");
  });

  test("rejects notes that the updater cannot carry in full", () => {
    const oversized = `${validNotes()}\n${"x".repeat(MAX_RELEASE_NOTES_CHARACTERS)}`;
    expect(() => validateCuratedReleaseNotes(oversized, "v1.2.3")).toThrow(
      "release notes must be at most 12,000 characters so the updater receives them in full.",
    );
  });
});

describe("loadCuratedReleaseNotes", () => {
  test("loads a repository-local Markdown file without changing its bytes", () => {
    const root = temporaryDirectory("hecate-release-root-");
    const notesDirectory = join(root, "docs", "releases");
    mkdirSync(notesDirectory, { recursive: true });
    const markdown = validNotes();
    writeFileSync(join(notesDirectory, "v1.2.3.md"), markdown);

    expect(
      loadCuratedReleaseNotes({
        root,
        version: "v1.2.3",
        notesPath: "docs/releases/v1.2.3.md",
      }),
    ).toMatchObject({ relativePath: "docs/releases/v1.2.3.md", markdown });
  });

  test("rejects files outside the repository and invalid UTF-8", () => {
    const root = temporaryDirectory("hecate-release-root-");
    const external = temporaryDirectory("hecate-release-external-");
    const externalPath = join(external, "v1.2.3.md");
    writeFileSync(externalPath, validNotes());
    expect(() =>
      loadCuratedReleaseNotes({ root, version: "v1.2.3", notesPath: externalPath }),
    ).toThrow("release notes must be a file inside the Hecate repository.");

    const notesDirectory = join(root, "docs", "releases");
    mkdirSync(notesDirectory, { recursive: true });
    writeFileSync(join(notesDirectory, "v1.2.3.md"), Buffer.from([0xc3, 0x28]));
    expect(() =>
      loadCuratedReleaseNotes({
        root,
        version: "v1.2.3",
        notesPath: "docs/releases/v1.2.3.md",
      }),
    ).toThrow("release notes must be valid UTF-8 Markdown.");
  });
});
