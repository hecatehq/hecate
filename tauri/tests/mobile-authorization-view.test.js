import { describe, expect, it } from "bun:test";

import { authorizationView } from "../mobile/authorization-view.js";

describe("mobile browser authorization view", () => {
  it("uses one browser action and no code UI for a complete handoff", () => {
    expect(
      authorizationView({
        authorizing: true,
        approval_page_available: true,
      }),
    ).toEqual({
      showActions: true,
      openDisabled: false,
      openLabel: "Open sign-in again",
      openAriaLabel: "Open browser sign-in again",
      openDescribedBy: "statusDetail",
      statusDetail: "Approve in Safari. Hecate will return here automatically.",
    });
  });

  it("does not offer recovery until the native layer has a validated page", () => {
    expect(
      authorizationView({
        authorizing: true,
        approval_page_available: false,
      }),
    ).toMatchObject({
      showActions: false,
      openDisabled: true,
      statusDetail: "Preparing a secure browser confirmation…",
    });
  });
});
