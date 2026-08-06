import { describe, expect, test } from "bun:test";

import { updatePinnedReleaseReferences } from "./release-links";

describe("updatePinnedReleaseReferences", () => {
  test("replaces complete prerelease pins when publishing a stable release", () => {
    const before = [
      "  ghcr.io/hecatehq/hecate:0.5.0-alpha.5",
      "https://github.com/hecatehq/hecate/releases/download/v0.5.0-alpha.5/hecate_0.5.0-alpha.5_linux_amd64.tar.gz",
      "Available tarballs for `v0.5.0-alpha.5`:",
      "## Current state — `v0.5.0-alpha.5`",
    ].join("\n");

    expect(updatePinnedReleaseReferences(before, "v0.6.0")).toBe(
      [
        "  ghcr.io/hecatehq/hecate:0.6.0",
        "https://github.com/hecatehq/hecate/releases/download/v0.6.0/hecate_0.6.0_linux_amd64.tar.gz",
        "Available tarballs for `v0.6.0`:",
        "## Current state — `v0.6.0`",
      ].join("\n"),
    );
  });

  test("continues to replace stable pins without touching unrelated versions", () => {
    const before = [
      "image: ghcr.io/hecatehq/hecate:0.5.0",
      "Compatibility remains at 0.5.0-alpha.5.",
    ].join("\n");

    expect(updatePinnedReleaseReferences(before, "v0.6.0")).toBe(
      ["image: ghcr.io/hecatehq/hecate:0.6.0", "Compatibility remains at 0.5.0-alpha.5."].join(
        "\n",
      ),
    );
  });

  test("does not rewrite third-party release URLs or partial Docker tags", () => {
    const before = [
      "https://github.com/vendor/tool/releases/download/v1.2.3-alpha.1/tool.zip",
      "mirror.example/https://github.com/hecatehq/hecate/releases/download/v1.2.3/hecate_1.2.3_linux_amd64.tar.gz",
      "mirror.example/ghcr.io/hecatehq/hecate:1.2.3",
      "docker pull ghcr.io/hecatehq/hecate:1.2.3.4",
      "docker pull ghcr.io/hecatehq/hecate:1.2.3-alpha.1_extra",
      "https://example.test/download/hecate_1.2.3_linux_amd64.tar.gz",
      "Third-party Available tarballs for `v1.2.3`:",
      "Third-party Current state — `v1.2.3`",
    ].join("\n");

    expect(updatePinnedReleaseReferences(before, "v0.6.0")).toBe(before);
  });
});
