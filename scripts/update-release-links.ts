#!/usr/bin/env bun
// update-release-links.ts - refresh release-pinned docs from a published
// GitHub Release. The release workflow runs this after Tauri uploads native
// bundles, so the README points at files that actually exist and copy-pasted
// install commands point at the tag that was just released.
//
// Usage:
//   bun scripts/update-release-links.ts v0.1.0-alpha.12

import { readFileSync, writeFileSync } from "fs";
import { resolve } from "path";

const root = resolve(import.meta.dir, "..");
const tag = process.argv.slice(2).find((arg) => !arg.startsWith("--")) ?? "";
const semver = tag.replace(/^v/, "");
const repo = process.env.GITHUB_REPOSITORY || "hecatehq/hecate";
const token = process.env.GITHUB_TOKEN || "";

const startMarker = "<!-- desktop-release-links:start -->";
const endMarker = "<!-- desktop-release-links:end -->";

type ReleaseAsset = {
  name: string;
  browser_download_url: string;
};

type ReleaseResponse = {
  assets?: ReleaseAsset[];
  message?: string;
};

function fail(message: string): never {
  console.error(`error: ${message}`);
  process.exit(1);
}

function usage(): never {
  console.error("usage: bun scripts/update-release-links.ts <tag>");
  process.exit(1);
}

if (!tag) usage();
if (!/^v\d+\.\d+\.\d+(-[a-zA-Z0-9._]+)?$/.test(tag)) {
  fail(`tag must look like vX.Y.Z or vX.Y.Z-pre.N (got '${tag}')`);
}

// Keep this narrower than the CLI tag validator: file names use underscores
// after the version (`hecate_0.1.0-alpha.12_linux_amd64.tar.gz`), so allowing
// `_` inside the replacement pattern would accidentally consume the OS segment.
const pinnedVersionPattern = "\\d+\\.\\d+\\.\\d+(?:-[a-zA-Z0-9.]+)?";

const releaseURL = `https://api.github.com/repos/${repo}/releases/tags/${tag}`;
const response = await fetch(releaseURL, {
  headers: {
    Accept: "application/vnd.github+json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  },
});

if (!response.ok) {
  const body = await response.text();
  fail(`failed to fetch ${repo} ${tag}: ${response.status} ${body}`);
}

const release = (await response.json()) as ReleaseResponse;
const assets = release.assets ?? [];

if (!assets.length) {
  fail(`release ${tag} has no assets yet`);
}

function assetURL(pattern: RegExp): ReleaseAsset | undefined {
  return assets
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name))
    .find((asset) => pattern.test(asset.name));
}

function link(asset: ReleaseAsset): string {
  return `[${asset.name}](${asset.browser_download_url})`;
}

const macArm = assetURL(/^Hecate_.+_aarch64\.dmg$/);
const linuxDeb = assetURL(/^Hecate_.+_amd64\.deb$/);
const linuxAppImage = assetURL(/^Hecate_.+_amd64\.AppImage$/);
const windowsMsi = assetURL(/^Hecate_.+_x64_en-US\.msi$/);
const linuxAMD64Tarball = assetURL(/^hecate_.+_linux_amd64\.tar\.gz$/);
const tarballs = [
  linuxAMD64Tarball,
  assetURL(/^hecate_.+_linux_arm64\.tar\.gz$/),
  assetURL(/^hecate_.+_darwin_amd64\.tar\.gz$/),
  assetURL(/^hecate_.+_darwin_arm64\.tar\.gz$/),
].filter((asset): asset is ReleaseAsset => Boolean(asset));

const rows: string[][] = [];

if (macArm) {
  rows.push(["macOS (Apple Silicon)", link(macArm)]);
}

if (linuxDeb || linuxAppImage) {
  const bundles = [linuxDeb, linuxAppImage].filter((asset): asset is ReleaseAsset => Boolean(asset));
  rows.push(["Linux x86_64", bundles.map(link).join(" or ")]);
}

if (windowsMsi) {
  rows.push(["Windows x86_64", link(windowsMsi)]);
}

if (rows.length === 0) {
  fail(`release ${tag} does not contain any desktop app bundles`);
}

const generated = [
  startMarker,
  "",
  ...markdownTable(["Platform", "Bundle"], rows),
  "",
  endMarker,
].join("\n");

const readmePath = resolve(root, "README.md");
const readme = readFileSync(readmePath, "utf8");
const start = readme.indexOf(startMarker);
const end = readme.indexOf(endMarker);

if (start === -1 || end === -1 || end < start) {
  fail(`README.md must contain ${startMarker} and ${endMarker}`);
}

let nextReadme = `${readme.slice(0, start)}${generated}${readme.slice(end + endMarker.length)}`;
nextReadme = updatePinnedReleaseReferences(nextReadme);

if (nextReadme === readme) {
  console.log(`README release references already match ${tag}`);
} else {
  writeFileSync(readmePath, nextReadme);
  console.log(`README release references updated for ${tag}`);
}

const deploymentPath = resolve(root, "docs/deployment.md");
updateFile(deploymentPath, updateDeploymentReferences);

const desktopPath = resolve(root, "docs/desktop-app.md");
updateFile(desktopPath, updatePinnedReleaseReferences);

function updateFile(path: string, update: (value: string) => string): void {
  const before = readFileSync(path, "utf8");
  const after = update(before);
  if (after === before) {
    console.log(`${relativeDocName(path)} already matches ${tag}`);
    return;
  }
  writeFileSync(path, after);
  console.log(`${relativeDocName(path)} updated for ${tag}`);
}

function relativeDocName(path: string): string {
  return path.replace(root + "/", "");
}

function markdownTable(headers: [string, string], rows: string[][]): string[] {
  const widths = headers.map((header, idx) =>
    Math.max(header.length, ...rows.map((row) => row[idx]?.length ?? 0)),
  );
  const formatRow = (cells: string[]): string =>
    `| ${cells.map((cell, idx) => cell.padEnd(widths[idx])).join(" | ")} |`;
  return [
    formatRow(headers),
    formatRow(widths.map((width) => "-".repeat(width))),
    ...rows.map(formatRow),
  ];
}

function updatePinnedReleaseReferences(value: string): string {
  return value
    .replace(
      new RegExp(`ghcr\\.io/hecatehq/hecate:${pinnedVersionPattern}`, "g"),
      `ghcr.io/hecatehq/hecate:${semver}`,
    )
    .replace(
      new RegExp(`/releases/download/v${pinnedVersionPattern}/`, "g"),
      `/releases/download/${tag}/`,
    )
    .replace(new RegExp(`hecate_${pinnedVersionPattern}_`, "g"), `hecate_${semver}_`)
    .replace(
      new RegExp(`Available tarballs for \`v${pinnedVersionPattern}\``, "g"),
      `Available tarballs for \`${tag}\``,
    )
    .replace(
      new RegExp(`Current state — \`v${pinnedVersionPattern}\``, "g"),
      `Current state — \`${tag}\``,
    );
}

function updateDeploymentReferences(value: string): string {
  let next = updatePinnedReleaseReferences(value);

  if (linuxAMD64Tarball) {
    next = next.replace(
      /curl -LO https:\/\/github\.com\/hecatehq\/hecate\/releases\/download\/v[^\s]+\/hecate_[^\s]+\.tar\.gz/,
      `curl -LO ${linuxAMD64Tarball.browser_download_url}`,
    );
    next = next.replace(
      /tar -xzf hecate_[^\s]+\.tar\.gz/,
      `tar -xzf ${linuxAMD64Tarball.name}`,
    );
  }

  if (tarballs.length) {
    const generatedTarballList = [
      `Available tarballs for \`${tag}\`:`,
      "",
      ...tarballs.map((asset) => `- \`${asset.name}\``),
    ].join("\n");

    next = next.replace(
      /Available tarballs for `v[^`]+`:\n\n(?:- `hecate_[^`]+\.tar\.gz`\n)+/,
      `${generatedTarballList}\n`,
    );
  }

  return next;
}
