import { describe, expect, it } from "bun:test";

import { androidVersionCode, appleBuildNumber, windowsVersion } from "../../scripts/mobile-version";

describe("mobile store version encoding", () => {
  it("keeps prereleases ordered before the final release and next patch", () => {
    expect(appleBuildNumber("0.5.0-alpha.3")).toBe("6.0.3");
    expect(appleBuildNumber("0.5.0-alpha.4")).toBe("6.0.4");
    expect(appleBuildNumber("0.5.0")).toBe("6.0.99");
    expect(appleBuildNumber("0.5.1-alpha.1")).toBe("6.1.1");

    expect(androidVersionCode("0.5.0-alpha.3")).toBe(500_003);
    expect(androidVersionCode("0.5.0-alpha.4")).toBe(500_004);
    expect(androidVersionCode("0.5.0")).toBe(500_099);
    expect(androidVersionCode("0.5.1-alpha.1")).toBe(500_101);
  });

  it("keeps minor and major release lines distinct", () => {
    expect(appleBuildNumber("0.6.0-alpha.1")).toBe("7.0.1");
    expect(appleBuildNumber("1.0.0-alpha.1")).toBe("1001.0.1");
    expect(androidVersionCode("0.6.0-alpha.1")).toBe(600_001);
    expect(androidVersionCode("1.0.0-alpha.1")).toBe(100_000_001);
  });

  it("rejects colliding or store-invalid components", () => {
    expect(() => appleBuildNumber("0.1000.0-alpha.1")).toThrow();
    expect(() => appleBuildNumber("0.5.100-alpha.1")).toThrow();
    expect(() => appleBuildNumber("0.5.0-alpha.99")).toThrow();
    expect(() => androidVersionCode("0.1000.0-alpha.1")).toThrow();
    expect(() => androidVersionCode("0.5.1000-alpha.1")).toThrow();
    expect(() => androidVersionCode("0.5.0-alpha.99")).toThrow();
  });

  it("keeps the Windows version surface strict", () => {
    expect(windowsVersion("0.5.0-alpha.4")).toBe("0.5.0.4");
    expect(windowsVersion("0.5.0")).toBe("0.5.0.0");
    expect(() => windowsVersion("0.5.0-alpha.4+dirty")).toThrow();
  });
});
