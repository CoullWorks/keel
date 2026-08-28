import { expect, test } from "@playwright/test";
import { mkdirSync, renameSync } from "node:fs";
import { dirname, join } from "node:path";

/**
 * One booted stack, driven by a real browser.
 *
 * KEEL_URL  where the stack is listening
 * KEEL_NAME what to call the artifacts
 * KEEL_EXPECT  text that must appear on the page
 *
 * This replaced `playwright screenshot`, which wrote a PNG and told you nothing
 * else. A stack trace screenshots perfectly well: the CLI could not see a page
 * error, an unhandled rejection or a 500, so a broken stack produced a
 * screenshot that looked like proof it worked.
 */
const name = process.env.KEEL_NAME ?? "app";
const wanted = process.env.KEEL_EXPECT ?? "app is running";
const artifacts = join(__dirname, "..", "artifacts");

test(`${name} serves a working page`, async ({ page }, testInfo) => {
  // Collected rather than asserted inline: the page should be allowed to finish
  // loading so the video shows the whole session, and the failure should report
  // every problem at once instead of the first.
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(`uncaught: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(`console: ${m.text()}`);
  });
  page.on("requestfailed", (r) => {
    // A cancelled navigation or an aborted HMR poll is normal noise.
    const why = r.failure()?.errorText ?? "";
    if (!/ERR_ABORTED|NS_BINDING_ABORTED/.test(why)) {
      errors.push(`request failed: ${r.url()} (${why})`);
    }
  });

  const response = await page.goto("/", { waitUntil: "domcontentloaded" });
  expect(response, "the server returned no response").not.toBeNull();
  expect(response!.status(), `GET / returned ${response!.status()}`).toBeLessThan(400);

  await expect(page.locator("body")).toContainText(wanted, { ignoreCase: true });

  // Let hydration run: a framework that throws does so after the first paint,
  // which is exactly the failure a still screenshot misses.
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(1000);

  const shot = join(artifacts, `${name}.png`);
  mkdirSync(dirname(shot), { recursive: true });
  await page.screenshot({ path: shot, fullPage: true });

  expect(errors, `${name} rendered but the page reported errors`).toEqual([]);

  // The video is only written when the context closes, so it is renamed after
  // the test in a fixture below rather than here.
  testInfo.annotations.push({ type: "stack", description: name });
});

// Playwright names videos after an internal hash. Renaming them to the stack
// makes the artifacts directory readable by a human afterwards.
test.afterEach(async ({}, testInfo) => {
  const video = testInfo.attachments.find((a) => a.name === "video");
  if (!video?.path) return;
  const target = join(artifacts, "sessions", `${name}.webm`);
  mkdirSync(dirname(target), { recursive: true });
  try {
    renameSync(video.path, target);
  } catch {
    // A rename across filesystems can fail; the video still exists where
    // Playwright put it, and losing the tidy name is not worth failing a run.
  }
});
