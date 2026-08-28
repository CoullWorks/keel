// Headless screenshot helper for pixel verification. Usage:
//   node scripts/shot.mjs <url> <outfile>
import { chromium } from 'playwright'

const [, , url, out] = process.argv
const browser = await chromium.launch({
  args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
})
const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 })
await page.goto(url, { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {})
await page.waitForTimeout(700)
await page.screenshot({ path: out })
await browser.close()
console.log('wrote', out)
