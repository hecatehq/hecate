#!/usr/bin/env bun

import { spawnSync } from "node:child_process";
import { writeFileSync } from "node:fs";

const generatedChangelogArgs = "release --clean";
const maxGitOutputBytes = 2 * 1024 * 1024;
const refFormat = "%(refname)%00%(contents:size)%00%(contents:signature)%00%(contents)";

interface PrepareReleaseNotesOptions {
  cwd: string;
  tag: string;
  notesPath: string;
}

interface TagContents {
  contents: Buffer;
  signature: Buffer;
}

function runGit(cwd: string, args: string[]): Buffer {
  const result = spawnSync("git", args, {
    cwd,
    encoding: null,
    maxBuffer: maxGitOutputBytes,
  });
  if (result.error) {
    throw new Error(`git ${args[0]} failed: ${result.error.message}`);
  }
  if (result.status !== 0) {
    const stderr = Buffer.from(result.stderr ?? [])
      .toString("utf8")
      .trim();
    throw new Error(`git ${args[0]} failed${stderr ? `: ${stderr}` : ""}`);
  }
  return Buffer.from(result.stdout ?? []);
}

function readTagContents(cwd: string, tagRef: string): TagContents {
  const output = runGit(cwd, ["for-each-ref", `--format=${refFormat}`, "--count=1", tagRef]);
  if (output.length === 0) {
    throw new Error(`Release ref ${tagRef} was not found.`);
  }

  const refEnd = output.indexOf(0);
  const sizeEnd = refEnd < 0 ? -1 : output.indexOf(0, refEnd + 1);
  const signatureEnd = sizeEnd < 0 ? -1 : output.indexOf(0, sizeEnd + 1);
  if (refEnd < 0 || sizeEnd < 0 || signatureEnd < 0) {
    throw new Error(`Git returned a malformed release-tag record for ${tagRef}.`);
  }

  const actualRef = output.subarray(0, refEnd);
  if (!actualRef.equals(Buffer.from(tagRef, "utf8"))) {
    throw new Error(`Git returned an unexpected release ref for ${tagRef}.`);
  }

  const sizeText = output.subarray(refEnd + 1, sizeEnd).toString("ascii");
  if (!/^(0|[1-9]\d*)$/.test(sizeText)) {
    throw new Error(`Git returned an invalid annotation size for ${tagRef}.`);
  }
  const contentsSize = Number(sizeText);
  if (!Number.isSafeInteger(contentsSize) || contentsSize > maxGitOutputBytes) {
    throw new Error(`Release tag ${tagRef} has an unsupported annotation size.`);
  }

  const contentsStart = signatureEnd + 1;
  const contentsEnd = contentsStart + contentsSize;
  if (contentsEnd !== output.length - 1 || output[contentsEnd] !== 0x0a) {
    throw new Error(`Git returned a truncated or ambiguous annotation for ${tagRef}.`);
  }

  return {
    signature: output.subarray(sizeEnd + 1, signatureEnd),
    contents: output.subarray(contentsStart, contentsEnd),
  };
}

function stripSignature(tagRef: string, contents: Buffer, signature: Buffer): Buffer {
  if (signature.length === 0) {
    return contents;
  }
  if (
    signature.length > contents.length ||
    !contents.subarray(contents.length - signature.length).equals(signature)
  ) {
    throw new Error(`Git did not report the signature as an exact suffix of ${tagRef}.`);
  }
  return contents.subarray(0, contents.length - signature.length);
}

function isWhitespaceOnly(contents: Buffer): boolean {
  return contents.every((byte) => byte === 0x20 || (byte >= 0x09 && byte <= 0x0d));
}

function withoutTrailingLineEndings(contents: Buffer): Buffer {
  let end = contents.length;
  while (end > 0 && contents[end - 1] === 0x0a) {
    end -= 1;
    if (end > 0 && contents[end - 1] === 0x0d) {
      end -= 1;
    }
  }
  return contents.subarray(0, end);
}

function isNonEmptySingleLine(value: string): boolean {
  return (
    value.length > 0 && !value.includes("\0") && !value.includes("\r") && !value.includes("\n")
  );
}

export function prepareReleaseNotes({ cwd, tag, notesPath }: PrepareReleaseNotesOptions): string {
  if (!isNonEmptySingleLine(tag)) {
    throw new Error("Release tag must be a non-empty single-line ref name.");
  }
  if (!isNonEmptySingleLine(notesPath)) {
    throw new Error("Release notes path must be a non-empty single-line path.");
  }

  const tagRef = `refs/tags/${tag}`;
  const objectType = runGit(cwd, ["cat-file", "-t", tagRef]).toString("ascii").trim();
  if (objectType === "commit") {
    return generatedChangelogArgs;
  }
  if (objectType !== "tag") {
    throw new Error(`Release ref ${tag} points to unsupported object type ${objectType}.`);
  }

  const { contents, signature } = readTagContents(cwd, tagRef);
  const annotation = stripSignature(tagRef, contents, signature);
  if (isWhitespaceOnly(annotation)) {
    throw new Error(`Release tag ${tag} has an empty annotation.`);
  }
  if (withoutTrailingLineEndings(annotation).equals(Buffer.from(tag, "utf8"))) {
    return generatedChangelogArgs;
  }

  writeFileSync(notesPath, annotation);
  return `${generatedChangelogArgs} --release-notes=${notesPath}`;
}

if (import.meta.main) {
  const [tag, notesPath, ...extraArgs] = process.argv.slice(2);
  if (!tag || !notesPath || extraArgs.length > 0) {
    console.error("usage: bun scripts/prepare-release-notes.ts <tag> <notes-path>");
    process.exit(2);
  }

  try {
    process.stdout.write(`${prepareReleaseNotes({ cwd: process.cwd(), tag, notesPath })}\n`);
  } catch (error) {
    console.error(`prepare-release-notes: ${(error as Error).message}`);
    process.exit(1);
  }
}
