import { afterEach, describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { prepareReleaseNotes } from "./prepare-release-notes";

const releaseArgs = "release --clean";
const repos: string[] = [];
const sshSignature = Buffer.from(
  [
    "-----BEGIN SSH SIGNATURE-----",
    "U1NIU0lHAAAAAQAAACMAAAALc3NoLWVkMjU1MTkAAAAgZmFrZS1maXh0dXJl",
    "-----END SSH SIGNATURE-----",
    "",
  ].join("\n"),
);
const pgpSignature = Buffer.from(
  [
    "-----BEGIN PGP SIGNATURE-----",
    "",
    "ZmFrZS1maXh0dXJl",
    "=AAAA",
    "-----END PGP SIGNATURE-----",
    "",
  ].join("\n"),
);

function git(cwd: string, args: string[], input?: Buffer): Buffer {
  const result = spawnSync("git", args, {
    cwd,
    encoding: null,
    input,
  });
  if (result.error || result.status !== 0) {
    throw new Error(
      `git ${args.join(" ")} failed: ${Buffer.from(result.stderr ?? []).toString("utf8")}`,
    );
  }
  return Buffer.from(result.stdout ?? []);
}

function createRepo(): string {
  const cwd = mkdtempSync(join(tmpdir(), "hecate-release-notes-"));
  repos.push(cwd);
  git(cwd, ["init", "--quiet"]);
  git(cwd, ["config", "user.name", "Hecate Test"]);
  git(cwd, ["config", "user.email", "test@hecate.invalid"]);
  git(cwd, ["config", "commit.gpgSign", "false"]);
  git(cwd, ["config", "tag.gpgSign", "false"]);
  writeFileSync(join(cwd, "fixture.txt"), "fixture\n");
  git(cwd, ["add", "fixture.txt"]);
  git(cwd, ["commit", "--quiet", "--no-gpg-sign", "-m", "fixture"]);
  return cwd;
}

function createLightweightTag(cwd: string, tag: string): void {
  git(cwd, ["update-ref", `refs/tags/${tag}`, "HEAD"]);
}

function createBlobTag(cwd: string, tag: string): void {
  const oid = git(cwd, ["hash-object", "-w", "--stdin"], Buffer.from("blob\n"))
    .toString("ascii")
    .trim();
  git(cwd, ["update-ref", `refs/tags/${tag}`, oid]);
}

function createAnnotatedTag(
  cwd: string,
  tag: string,
  annotation: Buffer,
  signature = Buffer.alloc(0),
  committedNotes: Buffer | null = annotation,
): void {
  if (committedNotes !== null) {
    const releaseDirectory = join(cwd, "docs", "releases");
    mkdirSync(releaseDirectory, { recursive: true });
    writeFileSync(join(releaseDirectory, `${tag}.md`), committedNotes);
    git(cwd, ["add", `docs/releases/${tag}.md`]);
    git(cwd, ["commit", "--quiet", "--no-gpg-sign", "-m", `release notes for ${tag}`]);
  }
  const commit = git(cwd, ["rev-parse", "HEAD"]).toString("ascii").trim();
  const header = Buffer.from(
    [
      `object ${commit}`,
      "type commit",
      `tag ${tag}`,
      "tagger Hecate Test <test@hecate.invalid> 1700000000 +0000",
      "",
      "",
    ].join("\n"),
  );
  const object = Buffer.concat([header, annotation, signature]);
  const oid = git(cwd, ["hash-object", "-t", "tag", "-w", "--stdin"], object)
    .toString("ascii")
    .trim();
  git(cwd, ["update-ref", `refs/tags/${tag}`, oid]);
}

function notesPath(cwd: string): string {
  return join(cwd, "release-notes.md");
}

function curatedNotes(tag: string, lineEnding = "\n"): Buffer {
  return Buffer.from(
    [
      `# Hecate ${tag}`,
      "",
      "A short operator-facing summary.",
      "",
      "## Highlights",
      "",
      "- Makes updates easier to review.",
      "- Keeps the complete notes available.",
      "",
      "## Security",
      "",
      "- No security action is required.",
      "",
    ].join(lineEnding),
  );
}

afterEach(() => {
  for (const repo of repos.splice(0)) {
    rmSync(repo, { recursive: true, force: true });
  }
});

describe("prepareReleaseNotes", () => {
  test("rejects non-stable release tag names before resolving Git objects", () => {
    const cwd = createRepo();

    expect(() =>
      prepareReleaseNotes({ cwd, tag: "v1.2.3-rc.1", notesPath: notesPath(cwd) }),
    ).toThrow("Release tag v1.2.3-rc.1 must use stable vX.Y.Z format.");
  });

  test("rejects lightweight tags without curated notes", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    createLightweightTag(cwd, tag);

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: output })).toThrow(
      `Release tag ${tag} must be annotated with curated Markdown notes.`,
    );
    expect(existsSync(output)).toBe(false);
  });

  test("rejects unsigned version-only annotations", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    createAnnotatedTag(cwd, tag, Buffer.from(`${tag}\r\n\r\n`));

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: output })).toThrow(
      `Release tag ${tag} contains only its version, not curated release notes.`,
    );
    expect(existsSync(output)).toBe(false);
  });

  test("writes unsigned Markdown byte-for-byte", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    const markdown = curatedNotes(tag);
    createAnnotatedTag(cwd, tag, markdown);

    expect(prepareReleaseNotes({ cwd, tag, notesPath: output })).toBe(
      `${releaseArgs} --release-notes=${output}`,
    );
    expect(readFileSync(output).equals(markdown)).toBe(true);
  });

  test.each([
    ["SSH", sshSignature],
    ["PGP", pgpSignature],
  ])("strips an exact %s signature suffix from Markdown", (_kind, signature) => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    const markdown = curatedNotes(tag, "\r\n");
    createAnnotatedTag(cwd, tag, markdown, signature);

    expect(prepareReleaseNotes({ cwd, tag, notesPath: output })).toBe(
      `${releaseArgs} --release-notes=${output}`,
    );
    const written = readFileSync(output);
    expect(written.equals(markdown)).toBe(true);
    expect(written.includes(Buffer.from("BEGIN"))).toBe(false);
  });

  test("rejects a tag whose commit omits the canonical release notes", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    createAnnotatedTag(cwd, tag, curatedNotes(tag), Buffer.alloc(0), null);

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: output })).toThrow(
      `Tagged commit for ${tag} must contain canonical release notes at docs/releases/${tag}.md.`,
    );
    expect(existsSync(output)).toBe(false);
  });

  test("rejects a tag annotation that differs from the committed release notes", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    const annotation = curatedNotes(tag);
    const committedNotes = Buffer.concat([annotation, Buffer.from("\nDifferent audit record.\n")]);
    createAnnotatedTag(cwd, tag, annotation, Buffer.alloc(0), committedNotes);

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: output })).toThrow(
      `Release tag ${tag} annotation must match docs/releases/${tag}.md byte-for-byte.`,
    );
    expect(existsSync(output)).toBe(false);
  });

  test("rejects a signed version-only annotation", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    createAnnotatedTag(cwd, tag, Buffer.from(`${tag}\n`), sshSignature);

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: output })).toThrow(
      `Release tag ${tag} contains only its version, not curated release notes.`,
    );
    expect(existsSync(output)).toBe(false);
  });

  test.each([
    ["tag with an empty annotation", Buffer.alloc(0), Buffer.alloc(0)],
    ["tag with only a signature", Buffer.from("\n"), pgpSignature],
  ])("rejects a %s", (_kind, annotation, signature) => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    createAnnotatedTag(cwd, tag, annotation, signature);

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: notesPath(cwd) })).toThrow(
      `Release tag ${tag} has an empty annotation.`,
    );
  });

  test("rejects a release ref with an unsupported object type", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    createBlobTag(cwd, tag);

    expect(() => prepareReleaseNotes({ cwd, tag, notesPath: notesPath(cwd) })).toThrow(
      `Release ref ${tag} points to unsupported object type blob.`,
    );
  });

  test("exposes the same behavior through the workflow CLI", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    createAnnotatedTag(cwd, tag, curatedNotes(tag), pgpSignature);

    const result = spawnSync(
      process.execPath,
      [join(import.meta.dir, "prepare-release-notes.ts"), tag, output],
      {
        cwd,
        encoding: "utf8",
      },
    );

    expect(result.status).toBe(0);
    expect(result.stdout).toBe(`${releaseArgs} --release-notes=${output}\n`);
    expect(result.stderr).toBe("");
    expect(readFileSync(output).equals(curatedNotes(tag))).toBe(true);
  });

  test("fails the workflow CLI for an uncurated tag", () => {
    const cwd = createRepo();
    const tag = "v1.2.3";
    const output = notesPath(cwd);
    createAnnotatedTag(cwd, tag, Buffer.from(`${tag}\n`));

    const result = spawnSync(
      process.execPath,
      [join(import.meta.dir, "prepare-release-notes.ts"), tag, output],
      {
        cwd,
        encoding: "utf8",
      },
    );

    expect(result.status).toBe(1);
    expect(result.stdout).toBe("");
    expect(result.stderr).toContain("contains only its version, not curated release notes");
    expect(existsSync(output)).toBe(false);
  });
});
