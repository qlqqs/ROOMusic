import { mkdir } from "node:fs/promises";
import type { Locator, Page } from "@playwright/test";
import { sampleBody } from "../src/test/api-samples";
import { personalTrack, seedPersonalTracks } from "../src/test/personal-data";
import type { ApiMock } from "./support/api-mock";
import { expectNoHorizontalOverflow, expectNoSeriousViolations, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { tabTo } from "./support/keyboard";

const signalButton = (page: Page) => page.getByRole("button", { name: /^信号路径：/ });
const signalDialog = (page: Page) => page.getByRole("dialog", { name: /^信号路径：/ });

async function setup(page: Page, mock: ApiMock) {
  const track = personalTrack(711, { title: "信号路径合成曲目" });
  seedPersonalTracks(mock.personal, [track]);
  mock.zones.seedLocal(track);
  mock.zones.seedSignal({
    quality: "low", clipping: false, processing_speed: 85.3,
    steps: [
      { kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sample_rate: 96000, bit_depth: 24, channels: 2 } },
      { kind: "convert", quality: "high", lossy: false, compat: true, params: { from_rate: 96000, to_rate: 48000, by: "ffmpeg" } },
      { kind: "headroom", quality: "enhanced", lossy: false, compat: false, params: { gain_db: -3 } },
      { kind: "encode", quality: "low", lossy: true, compat: false, params: { codec: "opus", sample_rate: 48000, bitrate_kbps: 128 } },
    ],
  });
  const analysis = sampleBody("tracks/analysis-done");
  if (typeof analysis !== "object" || analysis === null) throw new Error("分析样本必须是对象");
  await page.route("**/api/v1/tracks/*/analysis", (route) => route.fulfill({ json: { ...analysis, track_id: track.track_id } }));
  await page.goto("/settings/playback");
  await expect(page.getByRole("region", { name: "播放器", exact: true }).getByText(track.title, { exact: true })).toBeVisible();
  await expect.poll(() => mock.calls.some((call) => call.path.endsWith("/output"))).toBe(true);
}

/** 弹层外壳铺满窗口，另外测量实际内容区，避免仅检查 document 时漏掉内部溢出。 */
async function expectFitsViewport(locator: Locator) {
  const bounds = await locator.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return { left: rect.left, right: rect.right, viewport: innerWidth, overflow: element.scrollWidth - element.clientWidth };
  });
  expect(bounds.left).toBeGreaterThanOrEqual(0);
  expect(bounds.right).toBeLessThanOrEqual(bounds.viewport);
  expect(bounds.overflow).toBeLessThanOrEqual(0);
}

async function screenshot(page: Page, name: string, width: number) {
  if (width !== 375 && width !== 1440) return;
  await mkdir("/tmp/pf-shots", { recursive: true });
  await page.screenshot({ path: `/tmp/pf-shots/${name}-${width}.png`, fullPage: true });
}

for (const width of widths) {
  test(`${width}px：信号路径与 Headroom 不溢出，axe 无严重问题`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    await setup(page, mock);

    const headroom = page.getByRole("switch", { name: "Headroom", exact: true });
    await headroom.click();
    await expect(headroom).toBeChecked();
    const gain = page.getByRole("slider", { name: "增益", exact: true });
    await expect(gain).toBeEnabled();
    await expect(gain).toHaveValue("-3");
    await page.getByRole("switch", { name: "削波指示", exact: true }).scrollIntoViewIfNeeded();
    await screenshot(page, "headroom", width);
    await expectNoHorizontalOverflow(page, `Headroom ${width}px`);
    await expectFitsViewport(headroom.locator(".."));
    await expectFitsViewport(gain.locator(".."));
    await expectFitsViewport(page.getByRole("switch", { name: "削波指示", exact: true }).locator(".."));
    await expectNoSeriousViolations(page, `Headroom ${width}px`);

    // 小于 900px 时播放栏隐藏格式区，信号路径入口保留在全屏播放器中。
    if (width < 900) await page.getByRole("button", { name: "打开全屏播放器", exact: true }).click();
    await signalButton(page).click();
    const dialog = signalDialog(page);
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("−21.8 LUFS", { exact: false })).toBeVisible();
    await expect(dialog.getByText("兼容转换", { exact: true })).toBeVisible();
    await expect(dialog.getByText("有损", { exact: true })).toBeVisible();
    await expect(dialog.getByRole("listitem").last()).toHaveText("测试浏览器");
    await screenshot(page, "signal-path", width);
    await expectNoHorizontalOverflow(page, `信号路径 ${width}px`);
    await expectFitsViewport(dialog.locator(":scope > div"));
    await expectNoSeriousViolations(page, `信号路径 ${width}px`);
  });
}

test("纯键盘打开信号路径，Escape 关闭并回到入口", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await setup(page, mock);
  await tabTo(page, (name) => name.startsWith("信号路径："), 100);
  await expect(signalButton(page)).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(signalDialog(page)).toBeVisible();
  await expect(signalDialog(page).getByRole("button", { name: "关闭", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(signalDialog(page)).not.toBeVisible();
  await expect(signalButton(page)).toBeFocused();
});
