#!/usr/bin/env bun

import { createHash } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { spawnSync } from "node:child_process";

type CargoDependency = {
  dep_kinds: Array<{ kind: "build" | "dev" | null }>;
  pkg: string;
};

type CargoNode = {
  deps: CargoDependency[];
  id: string;
};

type CargoPackage = {
  id: string;
  manifest_path: string;
  name: string;
  source: string | null;
  version: string;
};

type CargoMetadata = {
  packages: CargoPackage[];
  resolve: {
    nodes: CargoNode[];
    root: string | null;
  } | null;
};

const root = resolve(import.meta.dir, "../..");
const vendorRoot = join(root, "tauri/vendor/glib-0.18.5");
const manifest = join(root, "tauri/src-tauri/Cargo.toml");
const expectedManifest = realpathSync(join(vendorRoot, "Cargo.toml"));
const variantIterPath = "src/variant_iter.rs";
const vendorMetadata = new Set([".gitignore", "HECATE-PATCH.md"]);
const expectedUpstreamFileCount = 121;
const expectedOriginalVariantSha256 =
  "1fd02859333761c45321b32f28b24233446b97d0022a90d3a937ed162585b90e";
const expectedPatchedVariantSha256 =
  "a0f5ee8acb8faa089bcdfbc9a57372609fce7654026ccef7d9a224d05a654ccc";
const expectedOriginalTreeSha256 =
  "c52a2c6c879157ecc49c3e6aba04f4b01a1b7026823c54897711596deff71b81";
const expectedPatchedTreeSha256 =
  "1b51798bfba7da92001feb436b019110061f82c789c4cc6a7778fb9fd4cb9fb2";

function fail(message: string): never {
  console.error(`glib-backport-check: ${message}`);
  process.exit(1);
}

function sha256(bytes: Buffer): string {
  return createHash("sha256").update(bytes).digest("hex");
}

function run(command: string, args: string[]): string {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  if (result.status !== 0) {
    fail(result.error?.message ?? (result.stderr.trim() || `${command} ${args.join(" ")} failed`));
  }
  return result.stdout;
}

function replaceExactlyOnce(value: string, from: string, to: string): string {
  const first = value.indexOf(from);
  if (first < 0 || value.indexOf(from, first + from.length) >= 0) {
    fail(`expected exactly one ${JSON.stringify(from)} in ${variantIterPath}`);
  }
  return `${value.slice(0, first)}${to}${value.slice(first + from.length)}`;
}

function treeSha256(paths: string[], overrides = new Map<string, Buffer>()): string {
  const hash = createHash("sha256");
  for (const path of paths) {
    const bytes = overrides.get(path) ?? readFileSync(join(vendorRoot, path));
    hash.update(path);
    hash.update("\0");
    hash.update(String(bytes.length));
    hash.update("\0");
    hash.update(bytes);
    hash.update("\0");
  }
  return hash.digest("hex");
}

const trackedVendorPaths = run("git", ["ls-files", "-z", "--", relative(root, vendorRoot)])
  .split("\0")
  .filter(Boolean)
  .map((path) => relative(vendorRoot, join(root, path)))
  .filter((path) => !vendorMetadata.has(path))
  .sort();

if (trackedVendorPaths.length !== expectedUpstreamFileCount) {
  fail(
    `expected ${expectedUpstreamFileCount} tracked upstream files, found ${trackedVendorPaths.length}`,
  );
}

const patchedVariant = readFileSync(join(vendorRoot, variantIterPath));
if (sha256(patchedVariant) !== expectedPatchedVariantSha256) {
  fail(`${variantIterPath} does not match the immutable upstream fix`);
}

let originalVariant = replaceExactlyOnce(
  patchedVariant.toString("utf8"),
  "let mut p: *mut libc::c_char = std::ptr::null_mut();",
  "let p: *mut libc::c_char = std::ptr::null_mut();",
);
originalVariant = replaceExactlyOnce(originalVariant, "&mut p,", "&p,");
const originalVariantBytes = Buffer.from(originalVariant);
if (sha256(originalVariantBytes) !== expectedOriginalVariantSha256) {
  fail(`${variantIterPath} differs from the published crate beyond the two-token fix`);
}

const patchedTreeSha256 = treeSha256(trackedVendorPaths);
if (patchedTreeSha256 !== expectedPatchedTreeSha256) {
  fail(`patched vendor tree sha256 is ${patchedTreeSha256}, expected ${expectedPatchedTreeSha256}`);
}
const originalTreeSha256 = treeSha256(
  trackedVendorPaths,
  new Map([[variantIterPath, originalVariantBytes]]),
);
if (originalTreeSha256 !== expectedOriginalTreeSha256) {
  fail(
    `reconstructed published tree sha256 is ${originalTreeSha256}, expected ${expectedOriginalTreeSha256}`,
  );
}

const metadataText = run("cargo", [
  "metadata",
  "--manifest-path",
  manifest,
  "--locked",
  "--filter-platform",
  "x86_64-unknown-linux-gnu",
  "--format-version",
  "1",
]);

let metadata: CargoMetadata;
try {
  metadata = JSON.parse(metadataText) as CargoMetadata;
} catch (error) {
  fail(`cargo metadata returned invalid JSON: ${String(error)}`);
}

const legacyGlibPackages = metadata.packages.filter((pkg) => {
  if (pkg.name !== "glib") return false;
  const [major, minor] = pkg.version.split(".").map(Number);
  return major === 0 && minor < 20;
});
if (legacyGlibPackages.length !== 1 || legacyGlibPackages[0].version !== "0.18.5") {
  fail(
    `expected only vendored glib 0.18.5 below 0.20, found ${
      legacyGlibPackages.map((pkg) => pkg.version).join(", ") || "none"
    }`,
  );
}

const glib = legacyGlibPackages[0];
if (glib.source !== null) {
  fail(`glib 0.18.5 resolved from ${glib.source}, not the vendored path`);
}
if (realpathSync(glib.manifest_path) !== expectedManifest) {
  fail(`glib 0.18.5 resolved from ${glib.manifest_path}, expected ${expectedManifest}`);
}

const nodes = new Map(metadata.resolve?.nodes.map((node) => [node.id, node]));
const rootId = metadata.resolve?.root;
if (!rootId || !nodes.has(rootId)) {
  fail("cargo metadata did not identify the Hecate application root");
}
const runtimeIds = new Set([rootId]);
const pendingIds = [rootId];
while (pendingIds.length > 0) {
  const node = nodes.get(pendingIds.pop()!);
  for (const dependency of node?.deps ?? []) {
    if (!dependency.dep_kinds.some(({ kind }) => kind === null)) continue;
    if (runtimeIds.has(dependency.pkg)) continue;
    runtimeIds.add(dependency.pkg);
    pendingIds.push(dependency.pkg);
  }
}
if (!runtimeIds.has(glib.id)) {
  fail("vendored glib 0.18.5 is not in the locked Linux runtime dependency graph");
}

console.log(
  "glib-backport-check: provenance intact; locked Linux runtime graph uses vendored glib 0.18.5",
);
