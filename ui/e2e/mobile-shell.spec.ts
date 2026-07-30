import { readFile } from "node:fs/promises";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { expect, test, type Page } from "@playwright/test";

const mobileRoot = fileURLToPath(new URL("../../tauri/mobile/", import.meta.url));
const mobileOrigin = "http://mobile.hecate.test";

type MobileCall = {
  name: string;
  args?: Record<string, unknown>;
};

async function serveMobileShell(page: Page) {
  await page.route(`${mobileOrigin}/**`, async (route) => {
    const requestURL = new URL(route.request().url());
    const relativePath =
      requestURL.pathname === "/" ? "index.html" : decodeURIComponent(requestURL.pathname.slice(1));
    if (!relativePath || relativePath.includes("/") || relativePath.includes("..")) {
      await route.fulfill({ status: 404, body: "Not found" });
      return;
    }

    try {
      const body = await readFile(join(mobileRoot, relativePath));
      const contentType = {
        ".css": "text/css",
        ".html": "text/html",
        ".js": "text/javascript",
      }[extname(relativePath)];
      await route.fulfill({
        status: 200,
        contentType: contentType ?? "application/octet-stream",
        body,
      });
    } catch {
      await route.fulfill({ status: 404, body: "Not found" });
    }
  });
}

async function installNativeBridge(page: Page) {
  await page.addInitScript(() => {
    const signedOutStatus = {
      phase: "signed_out",
      signed_in: false,
      authorizing: false,
      message: "Sign in to choose a Hecate instance.",
    };
    const authorizingStatus = {
      phase: "authorizing",
      signed_in: false,
      authorizing: true,
      approval_page_available: true,
      message: "Waiting for browser approval. Hecate will return here automatically.",
    };
    const signedInStatus = {
      phase: "signed_in",
      signed_in: true,
      authorizing: false,
      account_email: "operator@example.com",
      message: "Choose a Hecate instance.",
    };
    const state = {
      status: signedOutStatus,
      completeAuthorizationOnStatus: false,
      calls: [] as MobileCall[],
      connections: [
        {
          id: "desktop-1",
          kind: "desktop_host",
          name: "Studio Mac",
          version: "0.5.0-alpha.4",
          reachable: true,
          remote_enabled: true,
          last_seen_at: new Date().toISOString(),
        },
        {
          id: "hosted-1",
          kind: "hosted_runtime",
          name: "Dogfood Runtime",
          version: "0.5.0-alpha.4",
          reachable: false,
          can_start: true,
          status: "offline",
          last_seen_at: new Date().toISOString(),
        },
      ],
    };

    Object.defineProperty(window, "__mobileShellTest", {
      configurable: false,
      enumerable: false,
      value: state,
    });
    Object.defineProperty(window, "__TAURI__", {
      configurable: false,
      enumerable: false,
      value: {
        core: {
          invoke: async (name: string, args?: Record<string, unknown>) => {
            state.calls.push({ name, args });
            if (name === "mobile_status") {
              if (state.completeAuthorizationOnStatus) {
                state.completeAuthorizationOnStatus = false;
                state.status = signedInStatus;
              }
              return structuredClone(state.status);
            }
            if (name === "mobile_sign_in") {
              state.status = authorizingStatus;
              state.completeAuthorizationOnStatus = true;
              return structuredClone(state.status);
            }
            if (name === "mobile_reopen_authorization") {
              return structuredClone(authorizingStatus);
            }
            if (name === "mobile_connections") {
              return structuredClone(state.connections);
            }
            if (name === "mobile_open_connection") {
              return { message: "Secure Hecate session opened." };
            }
            if (name === "mobile_start_connection") {
              const connection = state.connections.find(
                (candidate) => candidate.id === args?.connectionId,
              );
              if (connection) {
                connection.can_start = false;
                connection.status = "starting";
              }
              return { message: "Dogfood Runtime is starting." };
            }
            if (name === "mobile_sign_out") {
              state.status = signedOutStatus;
              state.connections = [];
              return structuredClone(state.status);
            }
            if (name === "mobile_notification_status") {
              return { available: false };
            }
            throw new Error(`Unexpected mobile command: ${name}`);
          },
        },
      },
    });
  });
}

test("runs the packaged mobile companion journey", async ({ page }) => {
  await serveMobileShell(page);
  await installNativeBridge(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(mobileOrigin);

  await expect(page.getByRole("heading", { name: "Your Hecate, from anywhere" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in with Hecate" })).toBeEnabled();

  await page.getByRole("button", { name: "Sign in with Hecate" }).click();
  await expect(page.getByText("Waiting for browser approval", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open browser sign-in again" })).toBeEnabled();

  await expect(page.getByRole("heading", { name: "Choose Hecate" })).toBeVisible({
    timeout: 4_000,
  });
  await expect(page.getByText("operator@example.com")).toBeHidden();
  await expect(page.getByText("Hecate on Studio Mac")).toBeVisible();
  await expect(page.getByText("Dogfood Runtime")).toBeVisible();

  await page.getByRole("button", { name: "Start Dogfood Runtime" }).click();
  await expect(page.getByText("Dogfood Runtime is starting.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Dogfood Runtime: Starting" })).toBeDisabled();

  await page.getByRole("button", { name: "Open Hecate on Studio Mac" }).click();
  await expect(page.getByRole("button", { name: "Open Hecate on Studio Mac" })).toBeEnabled();

  await page.getByRole("button", { name: "Open settings" }).click();
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
  await expect(page.getByText("operator@example.com")).toBeVisible();
  await page.getByRole("button", { name: "Back to Hecate instances" }).click();
  await expect(page.getByRole("heading", { name: "Choose Hecate" })).toBeVisible();

  await page.getByRole("button", { name: "Open settings" }).click();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Your Hecate, from anywhere" })).toBeVisible();

  const calls = await page.evaluate(() => {
    const testWindow = window as typeof window & {
      __mobileShellTest: { calls: MobileCall[] };
    };
    return testWindow.__mobileShellTest.calls;
  });
  expect(calls).toContainEqual({
    name: "mobile_start_connection",
    args: { connectionId: "hosted-1" },
  });
  expect(calls).toContainEqual({
    name: "mobile_open_connection",
    args: { connectionId: "desktop-1" },
  });
  expect(calls.some((call) => call.name === "mobile_sign_out")).toBe(true);
});
