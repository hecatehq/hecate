import { describe, expect, it } from "bun:test";
import { shouldApplyConnectionsResponse } from "../mobile/connection-request.js";

describe("mobile connection request generation", () => {
  it("accepts the current signed-in session response", () => {
    expect(shouldApplyConnectionsResponse(4, 4, { signed_in: true })).toBe(true);
  });

  it("rejects a response invalidated by sign-out or account switch", () => {
    expect(shouldApplyConnectionsResponse(4, 5, { signed_in: true })).toBe(false);
  });

  it("rejects a response after the current session signs out", () => {
    expect(shouldApplyConnectionsResponse(4, 4, { signed_in: false })).toBe(false);
    expect(shouldApplyConnectionsResponse(4, 4, null)).toBe(false);
  });
});
