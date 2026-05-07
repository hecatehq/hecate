import { describe, expect, it } from "vitest";

import { describeGatewayError, formatErrorCode } from "./error-diagnostics";

describe("describeGatewayError", () => {
  it("labels stable Hecate Chat error contracts", () => {
    expect(describeGatewayError("agent_chat.agent_session_busy")?.title).toBe("Chat is still working");
    expect(describeGatewayError("agent_chat.model_capability_required")?.title).toBe("Tools unavailable for this model");
    expect(describeGatewayError("agent_chat.workspace_required")?.action).toContain("Choose a workspace");
    expect(describeGatewayError("model_not_configured")?.title).toBe("Selected model is unavailable");
  });

  it("keeps HTTP status fallbacks for non-Hecate errors", () => {
    expect(describeGatewayError(undefined, 429)?.title).toBe("Gateway rate limit exceeded");
    expect(describeGatewayError(undefined, 502)?.title).toBe("Gateway or upstream failed");
  });
});

describe("formatErrorCode", () => {
  it("combines status and stable code", () => {
    expect(formatErrorCode("agent_chat.agent_session_busy", 409)).toBe("409 · agent_chat.agent_session_busy");
  });
});
