import { describe, expect, it } from "vitest";

import { terminalWebSocketURL } from "./terminal";

describe("terminalWebSocketURL", () => {
  it("builds a loopback websocket URL with workspace and size query parameters", () => {
    window.history.replaceState({}, "", "/chats");

    const url = new URL(terminalWebSocketURL("/Users/alice/dev/hecate", "ticket-1", 100, 30));

    expect(url.protocol).toBe(window.location.protocol === "https:" ? "wss:" : "ws:");
    expect(url.host).toBe(window.location.host);
    expect(url.pathname).toBe("/hecate/v1/terminal");
    expect(url.searchParams.get("workspace")).toBe("/Users/alice/dev/hecate");
    expect(url.searchParams.get("token")).toBe("ticket-1");
    expect(url.searchParams.get("cols")).toBe("100");
    expect(url.searchParams.get("rows")).toBe("30");
  });
});
