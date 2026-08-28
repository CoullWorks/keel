import { defineConfig } from "@playwright/test";

/**
 * Recording is the point. A still screenshot proves a page rendered once; a
 * video shows what the stack actually did, which is the difference between
 * "it booted" and "it booted, hydrated, and did not flash an error first".
 *
 * Artifacts land beside the screenshots so a run leaves one directory to look
 * at.
 */
export default defineConfig({
  testDir: ".",
  outputDir: "../artifacts/sessions",
  timeout: 90_000,
  expect: { timeout: 30_000 },
  // A retry would record over the failure that is worth watching.
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: process.env.KEEL_URL,
    video: { mode: "on", size: { width: 1280, height: 800 } },
    screenshot: "on",
    trace: "retain-on-failure",
    viewport: { width: 1280, height: 800 },
    // A dev server on a fresh scaffold is slow on first compile.
    navigationTimeout: 60_000,
  },
});
