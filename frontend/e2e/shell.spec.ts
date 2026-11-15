import { readFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import { albumGroup, mediaOf, releaseDetailOf } from "../src/test/catalog-data";
import { expectMinimumFontSize, expectNoHorizontalOverflow, expectNoSeriousViolations } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";
import { tabBar } from "./support/settings";
import { installZoneAudio } from "./support/zones";
import type { ApiMock } from "./support/api-mock";

// 窄屏外壳：底部标签栏、浮在它上方的迷你播放栏与主内容的底部留白。jsdom 没有排版，这些只能在真实浏览器里量。
const flac = readFileSync(new URL("../../backend/testdata/playback/flac-16-44100.flac", import.meta.url));
const player = (page: Page) => page.getByRole("region", { name: "播放器", exact: true });
const trackRows = (page: Page) => page.locator("[data-track-row]");

async function openAlbum(page: Page, mock: ApiMock) {
  const group = albumGroup(93, { title: "外壳专项专辑", artist: "合成艺术家", track_count: 18 });
  const release = releaseDetailOf(group.representative, {
    media: mediaOf(group.representative, (_medium, track) => ({
      title: `合成曲目 ${track}`, artist: "合成艺术家", source_kind: "flac_vorbis_comment", codec: "flac",
      duration_seconds: 3, bit_depth: 16, sample_rate: 44100, channels: 2, cue: null,
    })),
  });
  mock.groups = [group];
  mock.releases.set(release.id, release);
  await installZoneAudio(page, flac);
  await page.goto(`/albums/${group.id}`);
  await expect(page.getByRole("button", { name: "播放整张专辑", exact: true })).toBeEnabled();
}

async function scrollToBottom(page: Page) {
  await page.evaluate(() => {
    for (const element of [document.scrollingElement, document.getElementById("main")]) if (element !== null) element.scrollTop = element.scrollHeight;
  });
}

/**
 * 标签栏贴底、迷你栏整个在它上方；滚到底时页面最后一行（专辑页底部的“来源与证据”）整行在迷你栏上方。
 * 量真实位置，不只查 padding 样式。
 */
async function expectBottomStack(page: Page) {
  await scrollToBottom(page);
  const viewport = page.viewportSize();
  const lastRow = page.getByRole("main").locator("summary").filter({ hasText: "来源与证据" });
  const [bar, mini, last] = await Promise.all([tabBar(page).boundingBox(), player(page).boundingBox(), lastRow.boundingBox()]);
  if (viewport === null || bar === null || mini === null || last === null) throw new Error("标签栏、迷你栏与最后一行必须有可测量边界");
  expect(Math.abs(bar.y + bar.height - viewport.height), "标签栏贴在窗口底部").toBeLessThanOrEqual(1);
  expect(mini.y + mini.height, "迷你栏在标签栏上方").toBeLessThanOrEqual(bar.y + 1);
  expect(last.y + last.height, "最后一行在迷你栏上方").toBeLessThanOrEqual(mini.y + 1);
  expect(last.y, "最后一行在窗口里").toBeGreaterThanOrEqual(0);
}

test.describe("390px 窄屏外壳", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("底部标签栏可见、专辑页归在“专辑”下；播放后迷你栏浮在标签栏上方，页面最后一行不被遮住", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    await openAlbum(page, mock);
    const bar = tabBar(page);
    await expect(bar).toBeVisible();
    for (const name of ["概览", "专辑", "我的"]) await expect(bar.getByRole("link", { name, exact: true })).toBeVisible();
    await expect(bar.getByRole("link", { name: "专辑", exact: true })).toHaveAttribute("aria-current", "page");
    // 没有“菜单”按钮；顶栏右侧是搜索。
    await expect(page.getByRole("button", { name: "菜单", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "搜索全部音乐", exact: true })).toBeVisible();

    await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
    await expect(player(page)).toBeVisible();
    await expect(trackRows(page).last()).toContainText("合成曲目 18");
    await expect.poll(async () => {
      try {
        await expectBottomStack(page);
        return true;
      } catch {
        return false;
      }
    }, { message: "标签栏、迷你栏与最后一行依次排开" }).toBe(true);
    await expectBottomStack(page);
    await expectNoHorizontalOverflow(page, "标签栏与迷你栏");
    await expectMinimumFontSize(page, "标签栏与迷你栏");
    await expectNoSeriousViolations(page, "标签栏与迷你栏");
  });

  test("自检：去掉主内容的底部留白时，最后一行被遮住的检查失败", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    await openAlbum(page, mock);
    await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
    await expect(player(page)).toBeVisible();
    await expect.poll(async () => {
      try {
        await expectBottomStack(page);
        return true;
      } catch {
        return false;
      }
    }).toBe(true);
    const style = await page.addStyleTag({ content: "#main { padding-bottom: 0 !important; }" });
    try {
      await expect(expectBottomStack(page)).rejects.toThrow("最后一行在迷你栏上方");
    } finally {
      await style.evaluate((element) => element.parentNode?.removeChild(element));
    }
  });

  test("顶栏搜索按钮打开搜索对话框，提交后关闭并进入结果页", async ({ page, backend }) => {
    await backend.start({ signedInAs: "listener" });
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();
    await page.getByRole("button", { name: "搜索全部音乐", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "搜索全部音乐", exact: true });
    await expect(dialog).toBeVisible();
    const input = dialog.getByRole("combobox", { name: "搜索" });
    await expect(input).toBeFocused();
    await expect(input).toHaveAttribute("placeholder", "搜索全部音乐");
    // 触屏与窄屏不显示“/”快捷键提示。
    await expect(dialog.locator("kbd")).toBeHidden();
    const bounds = await dialog.boundingBox();
    if (bounds === null) throw new Error("搜索对话框必须有边界");
    expect(bounds.y, "搜索条贴在窗口顶部").toBeLessThanOrEqual(1);
    expect(bounds.width, "搜索条占满宽度").toBeGreaterThanOrEqual(389);
    await input.fill("合成");
    await input.press("Enter");
    await expect(page).toHaveURL(/\/search\?/);
    await expect(dialog).toBeHidden();
  });
});
