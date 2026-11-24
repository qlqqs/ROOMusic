// 由品牌标志生成主屏幕图标（PNG）。标志几何取自 src/app/brand/favicon.svg，颜色固定为浅色主题。
// 用法：node scripts/generate-app-icons.mjs（需要 Playwright 的 Chromium）。产物写入 public/icons/ 并提交。
import { chromium } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { fileURLToPath, URL } from "node:url";

const outDir = fileURLToPath(new URL("../public/icons/", import.meta.url));
const background = "#f3f2f2";

// scale 是标志在画布上的宽度占比：普通图标 0.72；可遮罩图标要落在中心 80% 的安全圆里，取 0.56。
const icons = [
  { file: "icon-192.png", size: 192, scale: 0.72 },
  { file: "icon-512.png", size: 512, scale: 0.72 },
  { file: "icon-maskable-512.png", size: 512, scale: 0.56 },
  { file: "apple-touch-icon.png", size: 180, scale: 0.68 },
];

function svg(size, scale) {
  const mark = size * scale;
  const offset = (size - mark) / 2;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
  <rect width="${size}" height="${size}" fill="${background}"/>
  <svg x="${offset}" y="${offset - mark * 0.03}" width="${mark}" height="${mark}" viewBox="0 0 32 32">
    <path d="M2 13.91L16 2.17L30 13.91M28 12.24V27.83A2 2 0 0 1 26 29.83H6A2 2 0 0 1 4 27.83V12.24" fill="none" stroke="#b68235" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
    <circle fill="#201f1d" cx="16" cy="17.83" r="7.25"/>
    <circle fill="#ffffff" cx="16" cy="17.83" r="2.25"/>
  </svg>
</svg>`;
}

await mkdir(outDir, { recursive: true });
const browser = await chromium.launch();
try {
  for (const icon of icons) {
    const page = await browser.newPage({ viewport: { width: icon.size, height: icon.size } });
    await page.setContent(`<html><body style="margin:0">${svg(icon.size, icon.scale)}</body></html>`);
    await page.screenshot({ path: outDir + icon.file, clip: { x: 0, y: 0, width: icon.size, height: icon.size } });
    await page.close();
  }
} finally {
  await browser.close();
}
