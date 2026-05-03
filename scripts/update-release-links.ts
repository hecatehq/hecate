#!/usr/bin/env bun
// update-release-links.ts - regenerate README desktop bundle links from a
// published GitHub Release. The release workflow runs this after Tauri uploads
// native bundles, so the README points at files that actually exist.
//
// Usage:
//   bun scripts/update-release-links.ts v0.1.0-alpha.11

import { readFileSync, writeFileSync } from "fs";
import { resolve } from "path";

const root = resolve(import.meta.dir, "..");
const tag = process.argv.slice(2).find((arg) => !arg.startsWith("--")) ?? "";
const repo = process.env.GITHUB_REPOSITORY || "chicoxyzzy/hecate";
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

const rows: string[] = [
  "| Platform | Bundle |",
  "|---|---|",
];

if (macArm) {
  rows.push(`| macOS (Apple Silicon) | ${link(macArm)} |`);
}

if (linuxDeb || linuxAppImage) {
  const bundles = [linuxDeb, linuxAppImage].filter((asset): asset is ReleaseAsset => Boolean(asset));
  rows.push(`| Linux x86_64 | ${bundles.map(link).join(" or ")} |`);
}

if (windowsMsi) {
  rows.push(`| Windows x86_64 | ${link(windowsMsi)} |`);
}

if (rows.length === 2) {
  fail(`release ${tag} does not contain any desktop app bundles`);
}

const generated = [
  startMarker,
  ...rows,
  endMarker,
].join("\n");

const readmePath = resolve(root, "README.md");
const readme = readFileSync(readmePath, "utf8");
const start = readme.indexOf(startMarker);
const end = readme.indexOf(endMarker);

if (start === -1 || end === -1 || end < start) {
  fail(`README.md must contain ${startMarker} and ${endMarker}`);
}

const nextReadme = `${readme.slice(0, start)}${generated}${readme.slice(end + endMarker.length)}`;

if (nextReadme === readme) {
  console.log(`README desktop links already match ${tag}`);
} else {
  writeFileSync(readmePath, nextReadme);
  console.log(`README desktop links updated for ${tag}`);
}
