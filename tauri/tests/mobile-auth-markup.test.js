import { describe, expect, it } from "bun:test";
import { readFileSync } from "node:fs";

const html = readFileSync(new URL("../mobile/index.html", import.meta.url), "utf8");

describe("mobile authorization markup", () => {
  it("offers only a validated approval-page recovery action", () => {
    expect(html).toContain('id="authorizationActions" hidden');
    expect(html).toContain('id="openApprovalButton"');
    expect(html).toContain('aria-describedby="statusDetail"');
    expect(html).not.toContain("verificationCode");
    expect(html).not.toContain("copyCodeButton");
    expect(html).not.toContain("copyCodeFeedback");
    expect(html).not.toContain("Copy code");
  });
});
