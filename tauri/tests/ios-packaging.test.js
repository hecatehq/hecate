import { describe, expect, it } from "bun:test";

const appleProject = new URL("../src-tauri/gen/apple/", import.meta.url);
const projectSpec = await Bun.file(new URL("project.yml", appleProject)).text();
const pbxProject = await Bun.file(
  new URL("hecate-app.xcodeproj/project.pbxproj", appleProject),
).text();

describe("iOS packaging", () => {
  it("links the Rust archive without copying it into the app bundle", () => {
    expect(projectSpec).toMatch(
      /- path: Externals[\s\S]*?excludes:\s*\n\s*- "\*\*\/\*\.a"/,
    );
    expect(pbxProject).toContain("libapp.a in Frameworks");
    expect(pbxProject).not.toContain("libapp.a in Resources");
  });
});
