import { readFileSync } from "node:fs";
import AxeBuilder from "@axe-core/playwright";
import type { Locator, Page } from "@playwright/test";
import { historyItem, historyPage, historyQueue } from "../src/test/history-data";
import type { HistoryItemDTO } from "../src/features/listening/api/decoders";
import { LONG_ARTIST, LONG_TITLE } from "./support/albums-data";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";

const plays = "/api/v1/me/plays";
const flac = readFileSync(new URL("../../backend/testdata/playback/flac-16-44100.flac", import.meta.url));
const table = (page: Page) => page.getByRole("table", { name: "播放历史" });
const row = (page: Page, title: string) => table(page).getByRole("row").filter({ has: page.getByText(title, { exact: true }) });

/** 后注册的页面路由只接管历史与音频；会话、外壳等继续使用已有受控后端。 */
async function mockHistory(page: Page, items = [historyItem(1), historyItem(2, { present: false })]) {
  const state = { items: [...items], deletes: [] as string[] };
  await page.route("**/api/v1/me/plays**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "DELETE") {
      state.deletes.push(url.pathname);
      const target = url.pathname.startsWith(`${plays}/tracks/`) ? url.pathname.split("/").at(-1) : undefined;
      const removed = state.items.filter((item) => target === undefined || item.track_id === target);
      state.items = state.items.filter((item) => !removed.includes(item));
      await route.fulfill({ json: { deleted: removed.reduce((sum, item) => sum + item.play_count, 0) } });
    } else if (url.pathname === `${plays}/tracks`) {
      await route.fulfill({ json: historyPage(state.items, Number(url.searchParams.get("page") ?? 1), Number(url.searchParams.get("page_size") ?? 50)) });
    } else if (url.pathname === `${plays}/queue`) {
      await route.fulfill({ json: historyQueue(state.items) });
    } else if (url.pathname === `${plays}/recent-releases`) {
      await route.fulfill({ json: { has_history: state.items.length > 0, items: state.items.filter((item) => item.present).slice(0, 8).map((item) => ({ group_id: item.group_id, release: item.release, last_track: { id: item.track_id, title: item.title }, last_played_at: item.last_played_at })) } });
    } else {
      await route.fallback();
    }
  });
  await page.route("**/api/v1/tracks/*/playback", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2);
    return route.fulfill({ json: { track_id: id, representation: { kind: "original", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: flac.length } } });
  });
  // 合成 FLAC 按 Range 提供原字节，不替换 Audio.play 或媒体事件。
  await page.route("**/api/v1/tracks/*/audio", (route) => {
    const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers()["range"] ?? "");
    const start = Number(range?.[1] ?? 0);
    const end = Math.min(Number(range?.[2] || flac.length - 1), flac.length - 1);
    if (start > end) return route.fulfill({ status: 416, headers: { "Content-Range": `bytes */${flac.length}` } });
    return route.fulfill({ status: range ? 206 : 200, contentType: "audio/flac", body: flac.subarray(start, end + 1), headers: {
      "Accept-Ranges": "bytes", "Content-Length": String(end - start + 1), ...(range ? { "Content-Range": `bytes ${start}-${end}/${flac.length}` } : {}),
    } });
  });
  return state;
}

async function axe(page: Page, label: string) {
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(violations.map((violation) => ({ id: violation.id, targets: violation.nodes.map((node) => node.target) })), label).toEqual([]);
}

async function keyboardActivate(page: Page, target: Locator) {
  for (let index = 0; index < 120; index += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) {
      await expectVisibleFocusRing(page, "历史页键盘操作");
      await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("Tab");
  }
  throw new Error("Tab 无法抵达历史页控件");
}

for (const width of widths) {
  test(`${width}px：历史长内容、缺失行、移除与清除对话框不溢出，axe 无违规`, async ({ page, backend }) => {
    await backend.start({ signedInAs: "listener" });
    const long = historyItem(1, { title: LONG_TITLE, artist: LONG_ARTIST });
    long.release = { ...long.release, title: LONG_TITLE };
    const missing = historyItem(2, { present: false });
    await mockHistory(page, [long, missing]);
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/me/history");
    await expect(table(page)).toBeVisible();
    await expect(row(page, long.title).getByRole("button", { name: `播放 ${long.title}`, exact: true })).toBeEnabled();
    await expect(row(page, missing.title).getByText("文件缺失", { exact: true })).toBeVisible();
    await expect(row(page, missing.title).getByRole("link")).toHaveCount(0);
    for (const item of [long, missing]) {
      const heart = row(page, item.title).getByRole("button", { name: `收藏《${item.title}》`, exact: true });
      await expect(heart).toBeEnabled();
      await expect(heart).toHaveAttribute("aria-pressed", "false");
    }
    const muted = await row(page, missing.title).getByText(missing.title, { exact: true }).evaluate((element) => ({ color: getComputedStyle(element).color, secondary: getComputedStyle(document.documentElement).getPropertyValue("--text-secondary").trim() }));
    const secondaryColor = await page.evaluate((value) => { const element = document.createElement("span"); element.style.color = value; document.body.append(element); const color = getComputedStyle(element).color; element.remove(); return color; }, muted.secondary);
    expect(muted.color).toBe(secondaryColor);
    await expectNoHorizontalOverflow(page, "历史页");
    await expectNoDocumentScroll(page, "历史页");
    await expectMinimumFontSize(page, "历史页");
    await axe(page, "历史页");

    const more = row(page, missing.title).getByRole("button", { name: "更多" });
    // 长表格先滚到操作区，等浏览器交付滚动事件后再点击，避免自动定位的旧 scroll 事件关闭刚打开的菜单。
    await more.evaluate((element) => element.scrollIntoView({ block: "center" }));
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
    await expect(more).toBeEnabled();
    await more.click();
    await expect(page.getByRole("menuitem", { name: "添加到歌单…" })).toBeEnabled();
    await expect(page.getByRole("menuitem", { name: "下一首播放" })).toBeDisabled();
    await expect(page.getByRole("menuitem", { name: "加入队列" })).toBeDisabled();
    await page.getByRole("menuitem", { name: "从历史中移除" }).click();
    let dialog = page.getByRole("dialog", { name: "从历史中移除这首曲目？" });
    await expect(dialog).toBeVisible();
    await expectNoHorizontalOverflow(page, "移除确认");
    await axe(page, "移除确认");
    await dialog.getByRole("button", { name: "取消" }).click();
    await page.getByRole("button", { name: "清除全部历史" }).click();
    dialog = page.getByRole("dialog", { name: "清除播放历史？" });
    await expect(dialog).toBeVisible();
    await expectNoHorizontalOverflow(page, "清除确认");
    await axe(page, "清除确认");
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
  });
}

test("纯键盘：侧栏进入我的音乐，行内播放，更多→移除→确认，清除全部→确认", async ({ page, backend }) => {
  await backend.start({ signedInAs: "listener" });
  const first = historyItem(1);
  const second = historyItem(2);
  const state = await mockHistory(page, [first, second]);
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "概览" })).toBeVisible();
  await keyboardActivate(page, page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "我的音乐" }));
  await expect(page).toHaveURL(/\/me\/favorites$/);
  await expect(page.getByRole("navigation", { name: "我的音乐", exact: true }).getByRole("link", { name: "收藏", exact: true })).toHaveAttribute("aria-current", "page");
  await keyboardActivate(page, page.getByRole("navigation", { name: "我的音乐", exact: true }).getByRole("link", { name: "历史", exact: true }));
  await expect(page).toHaveURL(/\/me\/history$/);
  await expect(table(page)).toBeVisible();
  const play = row(page, first.title).getByRole("button", { name: `播放 ${first.title}`, exact: true });
  await expect(play).toBeEnabled();
  await keyboardActivate(page, play);
  const bar = page.getByRole("region", { name: "播放器", exact: true });
  await expect(bar.getByText("正在播放", { exact: true })).toBeVisible();
  await expect(bar).toContainText(first.title);
  await keyboardActivate(page, row(page, first.title).getByRole("button", { name: "更多" }));
  await expect(page.getByRole("menuitem", { name: "添加到歌单…" })).toBeEnabled();
  await page.keyboard.press("End");
  await expect(page.getByRole("menuitem", { name: "从历史中移除" })).toBeFocused();
  await page.keyboard.press("Enter");
  let dialog = page.getByRole("dialog", { name: "从历史中移除这首曲目？" });
  await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "从历史中移除" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(row(page, first.title)).toHaveCount(0);
  expect(state.deletes).toEqual([`${plays}/tracks/${first.track_id}`]);
  await expect(page.getByRole("heading", { name: "播放历史", exact: true })).toBeFocused();
  await keyboardActivate(page, page.getByRole("button", { name: "清除全部历史" }));
  dialog = page.getByRole("dialog", { name: "清除播放历史？" });
  await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "清除播放历史" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByText("已清除你的播放历史。")).toBeVisible();
  await expect(table(page)).toHaveCount(0);
  expect(state.deletes).toEqual([`${plays}/tracks/${first.track_id}`, plays]);
});

test("概览全部历史链接直达历史页，/me 直接访问重定向，地址恢复分页", async ({ page, backend }) => {
  await backend.start({ signedInAs: "listener" });
  const items: HistoryItemDTO[] = Array.from({ length: 51 }, (_, index) => historyItem(index + 1));
  await mockHistory(page, items);
  await page.goto("/");
  const allHistory = page.getByRole("region", { name: "最近播放", exact: true }).getByRole("link", { name: "全部历史" });
  await expect(allHistory).toHaveAttribute("href", "/me/history");
  await allHistory.click();
  await expect(page).toHaveURL(/\/me\/history$/);
  await expect(table(page)).toBeVisible();
  await page.goto("/me");
  await expect(page).toHaveURL(/\/me\/favorites$/);
  const tabs = page.getByRole("navigation", { name: "我的音乐", exact: true });
  await expect(tabs.getByRole("link")).toHaveText(["收藏", "歌单", "历史"]);
  await expect(tabs.getByRole("link", { name: "收藏", exact: true })).toHaveAttribute("aria-current", "page");
  await page.goto("/me/history?page=2");
  await expect(row(page, "历史曲目 51")).toBeVisible();
  await expect(row(page, "历史曲目 1")).toHaveCount(0);
  await page.reload();
  await expect(row(page, "历史曲目 51")).toBeVisible();
  await page.goto("/me/history?page=9");
  await expect(page.getByText(/这一页没有记录。/)).toBeVisible();
  await expect(page).toHaveURL(/\?page=9$/);
  await page.getByRole("link", { name: "回到第一页" }).click();
  await expect(row(page, "历史曲目 1")).toBeVisible();
});
