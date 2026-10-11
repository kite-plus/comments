import { defineConfig } from "@playwright/test";

// Browser tests against the harness (harness/main.go), in the installed
// Google Chrome; Playwright's own browsers are never downloaded.
export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  workers: 1,
  globalSetup: "./global-setup.ts",
  reporter: [["list"]],
  use: { channel: "chrome", headless: true, trace: "retain-on-failure" },
});
