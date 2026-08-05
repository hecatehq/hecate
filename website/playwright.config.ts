import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: "list",
  timeout: 20_000,
  expect: { timeout: 5_000 },
  use: {
    baseURL: "http://127.0.0.1:4321",
    colorScheme: "dark",
    locale: "en-US",
    screenshot: "only-on-failure",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "desktop",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "phone",
      use: {
        ...devices["Desktop Chrome"],
        hasTouch: true,
        isMobile: true,
        viewport: { width: 390, height: 844 },
      },
    },
    {
      name: "narrow-phone",
      use: {
        ...devices["Desktop Chrome"],
        hasTouch: true,
        isMobile: true,
        viewport: { width: 320, height: 800 },
      },
    },
    {
      name: "short-tablet",
      use: {
        ...devices["Desktop Chrome"],
        hasTouch: true,
        viewport: { width: 980, height: 600 },
      },
    },
    {
      name: "landscape-phone",
      use: {
        ...devices["Desktop Chrome"],
        hasTouch: true,
        isMobile: true,
        viewport: { width: 844, height: 390 },
      },
    },
  ],
  webServer: {
    command: "bun run preview -- --host 127.0.0.1 --port 4321",
    url: "http://127.0.0.1:4321",
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
});
