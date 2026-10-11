import { defineConfig } from "@playwright/test";

// The end-to-end run against the real local identity stack and the dev
// servers (make dev, make dev-worker, make dev-blogs); see README.md.
export default defineConfig({
  testDir: "./real",
  timeout: 120_000,
  expect: { timeout: 20_000 },
  workers: 1,
  reporter: [["list"]],
  use: { channel: "chrome", headless: true, trace: "retain-on-failure" },
});
