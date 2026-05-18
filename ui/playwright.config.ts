import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Two retries hides flake; one retry surfaces it on the second run.
  retries: process.env.CI ? 1 : 0,
  // Pin CI workers explicitly — Playwright's auto-detect picks half the
  // runner cores (2 on the default ubuntu-latest hosted runner). All specs
  // mock /hecate/v1 and provider-compatible /v1 routes, so they don't share
  // state and run cleanly in parallel.
  workers: process.env.CI ? 4 : undefined,
  reporter: "list",
  // Per-test timeout. The full suite now includes enough route-heavy chat
  // scenarios that browser page creation can exceed 10s on a busy release
  // run before any assertion code executes. Keep expectations tight below
  // so real UI stalls still fail quickly.
  timeout: 20_000,
  expect: { timeout: 5_000 },
  use: {
    baseURL: "http://localhost:5173",
    screenshot: "only-on-failure",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: "bun run dev",
    url: "http://localhost:5173",
    reuseExistingServer: true,
    timeout: 30_000,
  },
});
