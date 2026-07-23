import { describe, expect, it } from "bun:test";
import { notificationView } from "../mobile/notification-state.js";

describe("mobile run notification state", () => {
  it("offers opt-in only after sign-in", () => {
    const status = {
      available: true,
      authorization: "not_determined",
      registration: "idle",
      requested_enabled: false,
      enabled: false,
      background_active: false,
      message: "Get run updates.",
    };
    expect(notificationView(status, false).hidden).toBe(true);
    expect(notificationView(status, true)).toMatchObject({
      hidden: false,
      showEnable: true,
      showSettings: false,
      showDisable: false,
      stateLabel: "Off",
    });
  });

  it("routes denied permission to iPhone Settings", () => {
    const view = notificationView(
      {
        available: true,
        authorization: "denied",
        registration: "idle",
        requested_enabled: true,
        enabled: false,
      },
      true,
    );
    expect(view).toMatchObject({
      state: "attention",
      stateLabel: "Blocked",
      showEnable: false,
      showSettings: true,
      showDisable: true,
    });
  });

  it("keeps background registration manageable while signed out", () => {
    const view = notificationView(
      {
        available: true,
        authorization: "authorized",
        registration: "registered",
        requested_enabled: true,
        enabled: true,
        background_active: true,
      },
      false,
    );
    expect(view).toMatchObject({
      hidden: false,
      stateLabel: "On",
      showEnable: false,
      showDisable: true,
    });
  });

  it("surfaces a queued Cloud cleanup without exposing native ids", () => {
    const status = {
      available: true,
      authorization: "authorized",
      registration: "pending_delete",
      requested_enabled: false,
      enabled: false,
      background_active: false,
      message: "Sign in to finish Cloud cleanup.",
    };
    const view = notificationView(status, false);
    expect(view.hidden).toBe(false);
    expect(view.stateLabel).toBe("Cleanup pending");
    expect(JSON.stringify(view)).not.toContain("pdev_");
    expect(JSON.stringify(view)).not.toContain("hpi_");
  });
});
