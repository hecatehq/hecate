import { describe, expect, it } from "bun:test";

import { connectionView } from "../mobile/connection-view.js";

const now = Date.parse("2026-07-23T12:00:00.000Z");

describe("mobile Hecate instance rows", () => {
  it("presents a reachable desktop as one open action", () => {
    expect(
      connectionView(
        {
          id: "host_1",
          kind: "desktop_host",
          name: "Mac.home",
          reachable: true,
          remote_enabled: true,
          version: "0.5.0",
          last_seen_at: "2026-07-23T11:59:30.000Z",
        },
        now,
      ),
    ).toEqual({
      name: "Hecate on Mac.home",
      detail: "Desktop app · v0.5.0 · seen now",
      canOpen: true,
      canStart: false,
      canAct: true,
      action: "open",
      statusLabel: "Available",
      statusState: "online",
      ariaLabel: "Open Hecate on Mac.home",
    });
  });

  it("distinguishes an offline hosted runtime from disabled desktop access", () => {
    expect(
      connectionView(
        {
          kind: "hosted_runtime",
          name: "Hosted work",
          reachable: false,
          last_seen_at: "2026-07-23T10:00:00.000Z",
        },
        now,
      ),
    ).toMatchObject({
      detail: "Hosted · seen 2h ago",
      canOpen: false,
      canStart: false,
      canAct: false,
      action: "none",
      statusLabel: "Offline",
      statusState: "offline",
      ariaLabel: "Hosted work: Offline",
    });

    expect(
      connectionView(
        {
          kind: "desktop_host",
          name: "Office Mac",
          reachable: true,
          remote_enabled: false,
        },
        now,
      ),
    ).toMatchObject({
      name: "Hecate on Office Mac",
      canOpen: false,
      canStart: false,
      canAct: false,
      action: "none",
      statusLabel: "Remote access off",
      statusState: "attention",
      ariaLabel: "Hecate on Office Mac: Remote access off",
    });
  });

  it("never calls a desktop connection “This Mac” on an iPhone", () => {
    expect(
      connectionView({
        kind: "desktop_host",
        name: "This Mac",
        reachable: true,
        remote_enabled: true,
      }),
    ).toMatchObject({
      name: "Hecate on Mac",
      detail: "Desktop app",
      ariaLabel: "Open Hecate on Mac",
    });
  });

  it("keeps malformed optional metadata out of the row", () => {
    expect(
      connectionView(
        {
          kind: "unknown",
          name: "   ",
          reachable: true,
          last_seen_at: "not-a-date",
        },
        now,
      ),
    ).toMatchObject({
      name: "Unnamed Hecate",
      detail: "Hecate",
      canOpen: true,
      canStart: false,
      canAct: true,
      action: "open",
      ariaLabel: "Open Unnamed Hecate",
    });
  });

  it("offers Start only for a startable managed hosted runtime", () => {
    expect(
      connectionView(
        {
          id: "runtime_1",
          kind: "hosted_runtime",
          name: "Dogfood Runtime",
          status: "offline",
          reachable: false,
          can_start: true,
        },
        now,
      ),
    ).toMatchObject({
      canOpen: false,
      canStart: true,
      canAct: true,
      action: "start",
      statusLabel: "Start",
      statusState: "attention",
      ariaLabel: "Start Dogfood Runtime",
    });

    expect(
      connectionView(
        {
          id: "runtime_1",
          kind: "hosted_runtime",
          name: "Dogfood Runtime",
          status: "starting",
          reachable: false,
          can_start: true,
        },
        now,
      ),
    ).toMatchObject({
      canOpen: false,
      canStart: false,
      canAct: false,
      action: "none",
      statusLabel: "Starting",
      statusState: "attention",
      ariaLabel: "Dogfood Runtime: Starting",
    });

    expect(
      connectionView(
        {
          id: "runtime_1",
          kind: "hosted_runtime",
          name: "Dogfood Runtime",
          status: "offline",
          reachable: false,
          can_start: true,
        },
        now,
        true,
      ),
    ).toMatchObject({
      canOpen: false,
      canStart: false,
      canAct: false,
      action: "none",
      statusLabel: "Starting",
      statusState: "attention",
      ariaLabel: "Dogfood Runtime: Starting",
    });

    expect(
      connectionView(
        {
          id: "runtime_1",
          kind: "hosted_runtime",
          name: "Dogfood Runtime",
          status: "starting",
          reachable: true,
          can_start: true,
        },
        now,
      ),
    ).toMatchObject({
      canOpen: true,
      canStart: false,
      canAct: true,
      action: "open",
      statusLabel: "Available",
      statusState: "online",
      ariaLabel: "Open Dogfood Runtime",
    });
  });
});
