import { describe, expect, it } from "bun:test";

import {
  checkUpdaterDependencies,
  checkUpdaterMetadata,
  desktopTarget,
  readUpdaterInputs,
  type CargoMetadata,
  type UpdaterInputs,
} from "../scripts/check-updater-dependencies";

const current = readUpdaterInputs();

describe("updater dependency security policy", () => {
  it("checks the real TOML/JSON/JSONC manifests and lockfiles", () => {
    expect(() => checkUpdaterDependencies(current)).not.toThrow();
  });

  const reject = (mutate: (input: UpdaterInputs) => void, message: string) => {
    const input = structuredClone(current);
    mutate(input);
    expect(() => checkUpdaterDependencies(input)).toThrow(message);
  };

  for (const version of ["4.6.1", "9.0.0", "9.0.4", "9.0.5-rc.1"]) {
    it(`rejects vulnerable or prerelease ZIP ${version}`, () => {
      reject((input) => {
        input.lock.package.find((pkg) => pkg.name === "zip")!.version = version;
      }, "zip must be >=9.0.5");
    });
  }

  it("accepts the first fully patched ZIP release", () => {
    const input = structuredClone(current);
    input.lock.package.find((pkg) => pkg.name === "zip")!.version = "9.0.5";
    expect(() => checkUpdaterDependencies(input)).not.toThrow();
  });

  it("rejects a second vulnerable ZIP hidden beside a fixed version", () => {
    reject((input) => {
      input.lock.package.push({ name: "zip", version: "4.6.1" });
    }, "expected one locked zip");
  });

  it("requires the updater to actually depend on the patched ZIP", () => {
    reject((input) => {
      const updater = input.lock.package.find((pkg) => pkg.name === "tauri-plugin-updater")!;
      updater.dependencies = updater.dependencies!.filter((name) => name !== "zip");
    }, "missing locked edge tauri-plugin-updater -> zip");
  });

  it("rejects non-registry replacements", () => {
    reject((input) => {
      delete input.lock.package.find((pkg) => pkg.name === "zip")!.source;
    }, "crates.io checksum");
  });

  it("rejects a lower declared Rust floor", () => {
    reject((input) => {
      input.manifest.package["rust-version"] = "1.89.0";
    }, "Rust floor");
  });

  it("rejects lowered or incomplete Cargo requirements", () => {
    reject((input) => {
      input.manifest.target[desktopTarget].dependencies["tauri-plugin-updater"] = "2.14.0";
    }, "tauri-plugin-updater must be");
    reject((input) => {
      input.manifest.dependencies.tauri = "2";
    }, "complete caret version");
    reject((input) => {
      input.manifest["build-dependencies"]["tauri-build"] = "2.6.2";
    }, "tauri-build must be");
    reject((input) => {
      input.manifest["dev-dependencies"]["minisign-verify"] = "0.2.5";
    }, "minisign-verify must be");
  });

  it("requires paired JavaScript/Rust versions", () => {
    reject((input) => {
      input.uiLock.packages["@tauri-apps/plugin-updater"][0] = "@tauri-apps/plugin-updater@2.14.2";
    }, "must match exactly");
    reject((input) => {
      input.uiLock.packages["@tauri-apps/api"][0] = "@tauri-apps/api@2.13.0";
    }, "minor versions must match");
    reject((input) => {
      input.cli.devDependencies!["@tauri-apps/cli"] = "^2.11.2";
    }, "must be >=2.12.0");
  });
});

describe("resolved Windows graph and Rust requirements", () => {
  const metadata: CargoMetadata = {
    packages: [
      { id: "app", name: "hecate-app", version: "0.8.0", rust_version: "1.90.0" },
      { id: "updater", name: "tauri-plugin-updater", version: "2.14.1", rust_version: "1.90" },
      { id: "zip", name: "zip", version: "9.3.0", rust_version: "1.88" },
    ],
    resolve: {
      root: "app",
      nodes: [
        { id: "app", features: [], deps: [{ pkg: "updater", dep_kinds: [{ kind: null }] }] },
        {
          id: "updater",
          features: ["zip", "rustls-tls"],
          deps: [{ pkg: "zip", dep_kinds: [{ kind: null }] }],
        },
        { id: "zip", features: [], deps: [] },
      ],
    },
  };

  it("accepts the normal Windows runtime edge and two-component MSRV", () => {
    expect(() => checkUpdaterMetadata(metadata, "1.90.0")).not.toThrow();
  });

  it("rejects a transitive dependency above the declared floor", () => {
    const input = structuredClone(metadata);
    input.packages[2].rust_version = "1.91";
    expect(() => checkUpdaterMetadata(input, "1.90.0")).toThrow("above declared 1.90.0");
  });

  it("does not confuse a dev-only ZIP edge with Windows runtime support", () => {
    const input = structuredClone(metadata);
    input.resolve!.nodes[1].deps[0].dep_kinds[0].kind = "dev";
    expect(() => checkUpdaterMetadata(input, "1.90.0")).toThrow("Windows runtime graph");
  });

  it("rejects missing archive support or newly enabled system proxies", () => {
    for (const features of [["rustls-tls"], ["zip", "rustls-tls", "system-proxy"]]) {
      const input = structuredClone(metadata);
      input.resolve!.nodes[1].features = features;
      expect(() => checkUpdaterMetadata(input, "1.90.0")).toThrow();
    }
  });
});
