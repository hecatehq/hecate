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
  // Per-test timeout. The full local suite finishes in ~6 s with all 46
  // specs passing, so 10 s is plenty of head-room for CI's slower runner
  // while still surfacing genuinely stuck tests fast.
  timeout: 10_000,
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
