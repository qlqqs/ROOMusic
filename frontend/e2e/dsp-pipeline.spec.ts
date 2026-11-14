import { mkdir } from "node:fs/promises";
import type { Locator, Page } from "@playwright/test";
import { sampleBody } from "../src/test/api-samples";
import { personalTrack, seedPersonalTracks } from "../src/test/personal-data";
import type { ApiMock } from "./support/api-mock";
import { expectNoHorizontalOverflow, expectNoSeriousViolations, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { tabTo } from "./support/keyboard";
import { installZoneAudio } from "./support/zones";

// 入口是“质量色圆点 + 来源格式”的胶囊：可访问名是“格式，信号路径：质量”（含可见文字）。弹层的名称仍以“信号路径：”开头。
const signalButton = (page: Page) => page.getByRole("button", { name: /，信号路径：/ });
const fullscreen = (page: Page) => page.getByRole("dialog", { name: "信号路径合成曲目", exact: true });
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

    // 窄屏（小于 768px）迷你栏没有格式区，信号路径入口在全屏播放器里。
    let entry = page.getByRole("region", { name: "播放器", exact: true }).getByRole("button", { name: /，信号路径：/ });
    if (width < 768) {
      await page.getByRole("button", { name: "打开全屏播放器", exact: true }).click();
      entry = fullscreen(page).getByRole("button", { name: /，信号路径：/ });
    }
    await expect(entry).toHaveAccessibleName("FLAC 24/96，信号路径：有损");
    await expect(entry).toContainText("FLAC 24/96");
    await entry.click();
    const dialog = signalDialog(page);
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("−21.8 LUFS", { exact: false })).toBeVisible();
    await expect(dialog.getByText("兼容转换", { exact: true })).toBeVisible();
    await expect(dialog.getByText("有损", { exact: true })).toBeVisible();
    // 链从来源开始、按服务端步骤的顺序，最后是输出设备；拉低整体质量的那一步（编码为 Opus）加粗。
    const steps = dialog.getByRole("listitem");
    await expect(steps).toHaveCount(5);
    await expect(steps.first()).toContainText("来源");
    await expect(steps.nth(3)).toContainText("编码");
    await expect(steps.last()).toHaveText(/^输出\s*测试浏览器$/);
    await expect.poll(() => steps.nth(3).locator("span", { hasText: "Opus 128 kbps" }).last().evaluate((element) => Number(getComputedStyle(element).fontWeight))).toBeGreaterThanOrEqual(600);
    await expect.poll(() => steps.first().locator("span", { hasText: "FLAC" }).last().evaluate((element) => Number(getComputedStyle(element).fontWeight))).toBeLessThan(600);
    // 测量格：响度、范围、峰值来自分析结果，处理速度来自信号路径。
    await expect(dialog.getByText("处理速度", { exact: true })).toBeVisible();
    await expect(dialog.getByText("85.3×", { exact: true })).toBeVisible();
    await screenshot(page, "signal-path", width);
    await expectNoHorizontalOverflow(page, `信号路径 ${width}px`);
    await expectFitsViewport(dialog.locator(":scope > div"));
    await expectNoSeriousViolations(page, `信号路径 ${width}px`);
  });
}

test("纯键盘打开信号路径，Escape 关闭并回到入口", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await setup(page, mock);
  await tabTo(page, (name) => name.includes("，信号路径："), 100);
  await expect(signalButton(page)).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(signalDialog(page)).toBeVisible();
  await expect(signalDialog(page).getByRole("button", { name: "关闭", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(signalDialog(page)).not.toBeVisible();
  await expect(signalButton(page)).toBeFocused();
});

/** 入口圆点与它的柔光层（::after）当前的动画名；没有动画时是 none。 */
async function dotAnimations(entry: Locator) {
  return entry.locator("span").first().evaluate((dot) => ({ dot: getComputedStyle(dot).animationName, glow: getComputedStyle(dot, "::after").animationName }));
}

test("信号路径圆点：只在播放中呼吸，暂停时静止；削波时换成削波脉动；减少动态效果时都没有动画", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await installZoneAudio(page);
  await setup(page, mock);
  const bar = page.getByRole("region", { name: "播放器", exact: true });
  const entry = bar.getByRole("button", { name: /，信号路径：/ });
  // 暂停中：静止。
  await expect(entry).not.toHaveAttribute("data-state", "breathing");
  expect(await dotAnimations(entry)).toEqual({ dot: "none", glow: "none" });

  await bar.getByRole("button", { name: "播放", exact: true }).click();
  await expect(entry).toHaveAttribute("data-state", "breathing");
  await expect.poll(() => dotAnimations(entry)).toEqual({ dot: expect.stringMatching(/breathe/), glow: expect.stringMatching(/glow/) });

  // 削波：圆点换警示色，呼吸停下，柔光层快速脉动；名称里写出“削波”。
  mock.zones.seedSignal({ quality: "low", clipping: true, processing_speed: 85.3, steps: [{ kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sample_rate: 96000, bit_depth: 24, channels: 2 } }] });
  await expect(entry).toHaveAccessibleName("FLAC 24/96，信号路径：有损，削波");
  await expect.poll(() => dotAnimations(entry)).toEqual({ dot: "none", glow: expect.stringMatching(/clip/) });

  // 减少动态效果：只变色，不做任何动画。
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect.poll(() => dotAnimations(entry)).toEqual({ dot: "none", glow: "none" });
  await page.emulateMedia({ reducedMotion: "no-preference" });
  mock.zones.seedSignal({ quality: "low", clipping: false, processing_speed: 85.3, steps: [{ kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sample_rate: 96000, bit_depth: 24, channels: 2 } }] });
  await expect.poll(() => dotAnimations(entry)).toEqual({ dot: expect.stringMatching(/breathe/), glow: expect.stringMatching(/glow/) });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect.poll(() => dotAnimations(entry)).toEqual({ dot: "none", glow: "none" });
  await page.emulateMedia({ reducedMotion: "no-preference" });

  // 暂停后回到静止。
  await bar.getByRole("button", { name: "暂停", exact: true }).click();
  await expect(entry).not.toHaveAttribute("data-state", "breathing");
  await expect.poll(() => dotAnimations(entry)).toEqual({ dot: "none", glow: "none" });
});
