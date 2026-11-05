#!/usr/bin/env bun

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

export const desktopTarget = 'cfg(not(any(target_os = "android", target_os = "ios")))';
const registry = "registry+https://github.com/rust-lang/crates.io-index";
const root = resolve(import.meta.dir, "../..");

type Dependency = string | { version: string; features?: string[]; "default-features"?: boolean };
type Manifest = {
  package: { name: string; "rust-version": string };
  dependencies: Record<string, Dependency>;
  "build-dependencies": Record<string, Dependency>;
  "dev-dependencies": Record<string, Dependency>;
  target: Record<string, { dependencies: Record<string, Dependency> }>;
};
type LockedPackage = {
  name: string;
  version: string;
  source?: string;
  checksum?: string;
  dependencies?: string[];
};
type PackageJson = {
  dependencies?: Record<string, string>;
  devDependencies?: Record<string, string>;
};
type BunLock = { packages: Record<string, [string, ...unknown[]]> };
export type UpdaterInputs = {
  manifest: Manifest;
  lock: { package: LockedPackage[] };
  ui: PackageJson;
  uiLock: BunLock;
  cli: PackageJson;
  cliLock: BunLock;
};
export type CargoMetadata = {
  packages: Array<{ id: string; name: string; version: string; rust_version: string | null }>;
  resolve: {
    root: string | null;
    nodes: Array<{
      id: string;
      features: string[];
      deps: Array<{ pkg: string; dep_kinds: Array<{ kind: string | null }> }>;
    }>;
  } | null;
};

function require(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

function atLeast(version: string, floor: string, label: string): void {
  require(Bun.semver.satisfies(
    version,
    `>=${floor}`,
  ), `${label} must be >=${floor}, got ${version}`);
}

function requirementFloor(
  dependency: Dependency | undefined,
  floor: string,
  label: string,
): string {
  const requirement = typeof dependency === "string" ? dependency : dependency?.version;
  // Deliberately accept only this repo's complete stable caret requirements.
  // Broader ranges need review instead of silently weakening the security floor.
  require(typeof requirement === "string" &&
    /^\^?\d+\.\d+\.\d+$/.test(requirement), `${label} needs a complete caret version`);
  atLeast(requirement.replace(/^\^/, ""), floor, label);
  return requirement;
}

export function readUpdaterInputs(): UpdaterInputs {
  const read = (path: string) => readFileSync(resolve(root, path), "utf8");
  return {
    manifest: Bun.TOML.parse(read("tauri/src-tauri/Cargo.toml")) as Manifest,
    lock: Bun.TOML.parse(read("tauri/src-tauri/Cargo.lock")) as UpdaterInputs["lock"],
    ui: JSON.parse(read("ui/package.json")),
    uiLock: Bun.JSONC.parse(read("ui/bun.lock")),
    cli: JSON.parse(read("tauri/package.json")),
    cliLock: Bun.JSONC.parse(read("tauri/bun.lock")),
  };
}

export function checkUpdaterDependencies(input: UpdaterInputs): void {
  const { manifest, lock } = input;
  atLeast(manifest.package["rust-version"], "1.90.0", "Rust floor");
  const desktop = manifest.target[desktopTarget]?.dependencies;
  const floors: Array<[Dependency | undefined, string, string]> = [
    [manifest.dependencies.tauri, "2.12.0", "tauri"],
    [desktop?.tauri, "2.12.0", "desktop tauri"],
    [manifest["build-dependencies"]["tauri-build"], "2.7.0", "tauri-build"],
    [desktop?.["tauri-plugin-updater"], "2.14.1", "tauri-plugin-updater"],
    [manifest["dev-dependencies"]["minisign-verify"], "0.3.0", "minisign-verify"],
  ];
  for (const [dependency, floor, label] of floors) requirementFloor(dependency, floor, label);

  const one = (name: string) => {
    const matches = lock.package.filter((pkg) => pkg.name === name);
    require(matches.length === 1, `expected one locked ${name}, found ${matches.length}`);
    return matches[0];
  };
  const edge = (from: LockedPackage, to: LockedPackage) => {
    const references = [
      to.name,
      `${to.name} ${to.version}`,
      `${to.name} ${to.version} (${to.source})`,
    ];
    require(from.dependencies?.some((ref) =>
      references.includes(ref),
    ), `missing locked edge ${from.name} -> ${to.name}`);
  };
  const app = one(manifest.package.name);
  const tauri = one("tauri");
  const updater = one("tauri-plugin-updater");
  const verifier = one("minisign-verify");
  const zip = one("zip");
  for (const [pkg, floor] of [
    [tauri, "2.12.0"],
    [updater, "2.14.1"],
    [verifier, "0.3.0"],
    [zip, "9.0.5"],
  ] as const) {
    atLeast(pkg.version, floor, pkg.name);
    require(pkg.source === registry &&
      /^[0-9a-f]{64}$/.test(pkg.checksum ?? ""), `${pkg.name} must have a crates.io checksum`);
  }
  edge(app, updater);
  edge(app, tauri);
  edge(app, verifier);
  edge(updater, zip);
  edge(updater, verifier);
  edge(updater, tauri);

  const jsVersion = (name: string, pkg: PackageJson, bunLock: BunLock, floor: string) => {
    const requirement = requirementFloor(
      pkg.dependencies?.[name] ?? pkg.devDependencies?.[name],
      floor,
      name,
    );
    const resolved = bunLock.packages[name]?.[0];
    require(resolved?.startsWith(`${name}@`), `missing locked ${name}`);
    const version = resolved.slice(name.length + 1);
    require(Bun.semver.satisfies(
      version,
      requirement,
    ), `${name} lock does not satisfy ${requirement}`);
    return version;
  };
  const jsUpdater = jsVersion("@tauri-apps/plugin-updater", input.ui, input.uiLock, "2.14.1");
  require(jsUpdater === updater.version, "Rust and JavaScript updater versions must match exactly");
  const api = jsVersion("@tauri-apps/api", input.ui, input.uiLock, "2.12.0");
  const cli = jsVersion("@tauri-apps/cli", input.cli, input.cliLock, "2.12.0");
  const minor = `${tauri.version.split(".").slice(0, 2).join(".")}.x`;
  require(Bun.semver.satisfies(api, minor) &&
    Bun.semver.satisfies(cli, minor), "Tauri Rust, API and CLI minor versions must match");
}

export function checkUpdaterMetadata(metadata: CargoMetadata, rustFloor: string): void {
  for (const pkg of metadata.packages) {
    if (!pkg.rust_version) continue;
    const version =
      pkg.rust_version.split(".").length === 2 ? `${pkg.rust_version}.0` : pkg.rust_version;
    require(Bun.semver.satisfies(
      version,
      `<=${rustFloor}`,
    ), `${pkg.name} ${pkg.version} requires Rust ${pkg.rust_version}, above declared ${rustFloor}`);
  }
  const nodes = new Map(metadata.resolve?.nodes.map((node) => [node.id, node]));
  const runtimeDependency = (id: string | null | undefined, name: string) => {
    const candidates = metadata.packages.filter((pkg) => pkg.name === name);
    const matches = candidates.filter((pkg) =>
      nodes
        .get(id ?? "")
        ?.deps.some(
          (dep) => dep.pkg === pkg.id && dep.dep_kinds.some((kind) => kind.kind === null),
        ),
    );
    require(matches.length === 1, `Windows runtime graph missing unambiguous ${name}`);
    return matches[0];
  };
  const updater = runtimeDependency(metadata.resolve?.root, "tauri-plugin-updater");
  const zip = runtimeDependency(updater.id, "zip");
  atLeast(updater.version, "2.14.1", "Windows updater");
  atLeast(zip.version, "9.0.5", "Windows zip");
  const features = nodes.get(updater.id)?.features ?? [];
  require(features.includes("zip") &&
    features.includes("rustls-tls"), "updater must retain archive and rustls support");
  require(!features.includes(
    "system-proxy",
  ), "updater system-proxy requires a separate policy review");
}

if (import.meta.main) {
  const input = readUpdaterInputs();
  checkUpdaterDependencies(input);
  const metadata = JSON.parse(
    execFileSync(
      "cargo",
      [
        "metadata",
        "--manifest-path",
        resolve(root, "tauri/src-tauri/Cargo.toml"),
        "--locked",
        "--format-version",
        "1",
        "--filter-platform",
        "x86_64-pc-windows-msvc",
      ],
      { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 },
    ),
  );
  checkUpdaterMetadata(metadata, input.manifest.package["rust-version"]);
  console.log(
    "updater-dependencies: patched Windows ZIP graph, paired packages and declared Rust floors verified (not a Windows runtime test)",
  );
}
