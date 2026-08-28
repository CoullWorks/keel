// Generate keel pixel-art assets with Nano Banana Pro (Gemini 3 Pro Image) via REST.
// Usage: node gen-pixel.mjs   (generates the batch defined below)
import fs from "node:fs/promises";
import path from "node:path";

const KEY = process.env.GOOGLE_AI_API_KEY;
if (!KEY) { console.error("no GOOGLE_AI_API_KEY"); process.exit(1); }

const MODELS = ["gemini-3-pro-image", "gemini-3-pro-image-preview", "gemini-2.5-flash-image"];
const OUT = path.resolve(new URL("../assets/pixel", import.meta.url).pathname);
await fs.mkdir(OUT, { recursive: true });

// Shared style so every asset is consistent: crisp retro pixel art, brand orange.
const STYLE =
  "retro 16-bit pixel art game sprite, crisp hard square pixels, no anti-aliasing, " +
  "bold clean outlines, limited color palette, brand color bright orange #ff6a2c as the hero color, " +
  "teal and cream accents, flat dark navy #10131a background, centered subject, " +
  "friendly and characterful, no text, no letters, no watermark, no signature";

// Icon style: keep the crisp pixel look + navy background, but allow each tool's
// recognisable brand colours so the studio reads at a glance.
const ISTYLE =
  "retro 16-bit pixel art app icon, crisp hard square pixels, no anti-aliasing, " +
  "bold clean 1px outline, limited palette, flat dark navy #10131a background, " +
  "single centered emblem, chunky and readable at small size, no watermark, no signature";

const BATCH = [
  ["icon-laravel", `app icon: the Laravel emblem, a geometric bright RED origami bird / stylized red 'V' facets, ${ISTYLE}`],
  ["icon-magento", `app icon: the Magento emblem, an ORANGE geometric optical-illusion hexagon box forming an 'M', orange #f26322, ${ISTYLE}`],
  ["icon-django", `app icon: the Django emblem, a dark green rounded tile with a stylized white 'dj', dark green #0C4B33, ${ISTYLE}`],
  ["icon-nextjs", `app icon: the Next.js emblem, a black circle with a clean white letter 'N', minimalist monochrome, ${ISTYLE}`],
  ["icon-docker", `app icon: a friendly light-blue whale carrying stacked shipping containers on its back (Docker), blue #2496ed, ${ISTYLE}`],
  ["icon-postgres", `app icon: a blue elephant head emblem (PostgreSQL), navy blue #336791, ${ISTYLE}`],
  ["icon-mysql", `app icon: a teal-blue dolphin emblem leaping (MySQL), teal #00758f with an orange accent, ${ISTYLE}`],
  ["icon-redis", `app icon: a stack of glossy RED cubes forming a tower (Redis), bright red #d82c20, ${ISTYLE}`],
];

async function tryModel(model, prompt) {
  const url = `https://generativelanguage.googleapis.com/v1beta/models/${model}:generateContent?key=${KEY}`;
  const body = { contents: [{ parts: [{ text: prompt }] }], generationConfig: { responseModalities: ["IMAGE"], imageConfig: { aspectRatio: "1:1" } } };
  const res = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const j = await res.json();
  if (!res.ok) return { err: `${res.status} ${JSON.stringify(j.error?.message || j).slice(0, 160)}` };
  const img = (j.candidates?.[0]?.content?.parts || []).find((p) => p.inlineData?.data);
  if (!img) return { err: `no image; finish=${j.candidates?.[0]?.finishReason}` };
  return { data: img.inlineData.data };
}

for (const [slug, prompt] of BATCH) {
  let r;
  for (const m of MODELS) { r = await tryModel(m, prompt); if (r.data) { console.log(`[${slug}] via ${m}`); break; } console.log(`[${slug}] ${m} → ${r.err}`); }
  if (!r?.data) { console.error(`[${slug}] FAILED`); continue; }
  const buf = Buffer.from(r.data, "base64");
  const ext = buf[0] === 0xff && buf[1] === 0xd8 ? "jpg" : "png";
  const file = path.join(OUT, `${slug}.${ext}`);
  await fs.writeFile(file, buf);
  console.log(`  wrote ${file} (${(buf.length / 1024).toFixed(0)} KB)`);
}
