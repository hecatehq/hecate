import { describe, expect, it } from "vitest";

import { describeGatewayError, formatErrorCode } from "./error-diagnostics";

describe("describeGatewayError", () => {
  it("labels stable Hecate Chat error contracts", () => {
    expect(describeGatewayError("chat.agent_session_busy")?.title).toBe("Chat is still working");
    expect(describeGatewayError("chat.model_capability_required")?.title).toBe(
      "Tools unavailable for this model",
    );
    expect(describeGatewayError("chat.workspace_required")?.action).toContain("Choose a workspace");
    expect(describeGatewayError("chat.session_not_running")).toMatchObject({
      title: "No active turn",
      action: "Send a new message if you want to start another turn.",
    });
    expect(describeGatewayError("model_not_configured")?.title).toBe(
      "Selected model is unavailable",
    );
  });

  it("turns launch-time executable trust failures into Connections repair guidance", () => {
    expect(describeGatewayError("agent_adapter.executable_trust_required")).toMatchObject({
      title: "External agent app needs approval",
      action: expect.stringContaining("review the exact executable identity"),
    });
    expect(describeGatewayError("agent_adapter.executable_identity_changed")).toMatchObject({
      title: "External agent app changed",
      action: expect.stringContaining("review the new executable identity"),
    });
    expect(describeGatewayError("agent_adapter.executable_identity_unavailable")?.action).toContain(
      "refresh discovery",
    );
  });

  it("keeps HTTP status fallbacks for non-Hecate errors", () => {
    expect(describeGatewayError(undefined, 429)?.title).toBe("Gateway rate limit exceeded");
    expect(describeGatewayError(undefined, 502)?.title).toBe("Gateway or upstream failed");
  });
});

describe("formatErrorCode", () => {
  it("combines status and stable code", () => {
    expect(formatErrorCode("chat.agent_session_busy", 409)).toBe("409 · chat.agent_session_busy");
  });
});
