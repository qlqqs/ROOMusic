import { readFileSync } from "node:fs";
import AxeBuilder from "@axe-core/playwright";
import type { Locator, Page, TestInfo } from "@playwright/test";
import { albumGroup } from "../src/test/catalog-data";
import { personalTracksOf, PERSONAL_LISTENER_ID, seedFavoriteAlbums, seedFavoriteTracks, seedPersonalTracks, seedPlaylist } from "../src/test/personal-data";
import { LONG_ARTIST, LONG_TITLE } from "./support/albums-data";
import type { ApiMock } from "./support/api-mock";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";

const playlistName = "晚间室内乐";
const longName = "室内乐与交响诗".repeat(14).slice(0, 100);
const flac = readFileSync(new URL("../../backend/testdata/playback/flac-16-44100.flac", import.meta.url));

function seed(mock: ApiMock, long = false) {
  mock.groups = [
    albumGroup(1, { title: long ? LONG_TITLE : "第一张合成专辑", artist: long ? LONG_ARTIST : "室内乐团", artwork: true, track_count: 3 }),
    albumGroup(2, { title: "第二张合成专辑", artwork: true, track_count: 1 }),
  ];
  const tracks = personalTracksOf(mock).slice(0, 3).map((track, index) => ({
    ...track,
    title: index === 0 ? (long ? LONG_TITLE : "第一乐章") : index === 1 ? "第二乐章" : "缺失的第三乐章",
    artist: long && index === 0 ? LONG_ARTIST : "室内乐团",
    present: index !== 2,
  }));
  seedPersonalTracks(mock.personal, tracks);
  seedFavoriteTracks(mock.personal, PERSONAL_LISTENER_ID, tracks);
  seedFavoriteAlbums(mock.personal, PERSONAL_LISTENER_ID, [
    ...mock.groups,
    { group_id: "00000000-0000-4000-8000-a00000000009", available: false, title: "暂时不可用的旧专辑", artist: "旧乐团", release_count: 0, representative: null },
  ]);
  const playlist = seedPlaylist(mock.personal, PERSONAL_LISTENER_ID, { name: playlistName, trackIds: tracks.map((track) => track.track_id) });
  seedPlaylist(mock.personal, PERSONAL_LISTENER_ID, { name: longName, trackIds: tracks.slice(0, 2).map((track) => track.track_id) });
  return { tracks, playlist };
}

async function audio(page: Page) {
  await page.route("**/api/v1/tracks/*/playback", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2);
    return route.fulfill({ json: { track_id: id, representation: { kind: "original", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: flac.length } } });
  });
  await page.route("**/api/v1/tracks/*/audio", (route) => {
    const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers()["range"] ?? "");
    const start = Number(range?.[1] ?? 0);
    const end = Math.min(Number(range?.[2] || flac.length - 1), flac.length - 1);
    if (start > end) return route.fulfill({ status: 416, headers: { "Content-Range": `bytes */${flac.length}` } });
    return route.fulfill({ status: range ? 206 : 200, contentType: "audio/flac", body: flac.subarray(start, end + 1), headers: {
      "Accept-Ranges": "bytes", "Content-Length": String(end - start + 1), ...(range ? { "Content-Range": `bytes ${start}-${end}/${flac.length}` } : {}),
    } });
  });
}

async function axe(page: Page, label: string) {
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(violations.map((item) => ({ id: item.id, targets: item.nodes.map((node) => node.target) })), label).toEqual([]);
}

async function layout(page: Page, label: string) {
  await expectNoHorizontalOverflow(page, label);
  await expectNoDocumentScroll(page, label);
  await expectMinimumFontSize(page, label);
  await axe(page, label);
}

/** 按真实文字 Range 的位置数行，裁掉 line-clamp 隐藏的矩形，不用按钮最小高度猜行数。 */
async function visibleLines(locator: Locator) {
  return locator.evaluate((element) => {
    const box = element.getBoundingClientRect();
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const tops = new Set<number>();
    for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
      if (!node.textContent?.trim()) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      for (const rect of range.getClientRects()) if (rect.width > 0 && rect.top < box.bottom && rect.bottom > box.top) tops.add(Math.round(rect.top));
    }
    return tops.size;
  });
}

async function screenshot(page: Page, info: TestInfo, width: number, name: string) {
  if (width === 375 || width === 1440) await page.screenshot({ path: info.outputPath(`${width}-${name}.png`), fullPage: true });
}

const playlistRow = (page: Page, title: string) => page.getByRole("listitem").filter({ has: page.getByRole("button", { name: `拖动《${title}》调整顺序`, exact: true }) });
const trackTable = (page: Page) => page.getByRole("table", { name: "收藏的曲目" });

async function openMenu(page: Page, trigger: Locator) {
  await trigger.scrollIntoViewIfNeeded();
  // 等自动定位产生的滚动事件交付，避免其关闭刚打开的浮层。
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  await trigger.click();
  await expect(page.getByRole("menu")).toBeVisible();
}

for (const width of [375, 1440]) {
  test(`${width}px 心形重读失败：提示与重新读取不撑破曲目行`, async ({ page, backend }, info) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const { tracks } = seed(mock);
    const firstTrack = tracks[0];
    if (firstTrack === undefined) throw new Error("合成曲目不存在");
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/me/favorites?tab=tracks");
    const row = trackTable(page).getByRole("row").filter({ has: page.getByText("第一乐章", { exact: true }) });
    const heart = row.getByRole("button", { name: "收藏《第一乐章》", exact: true });
    await expect(heart).toHaveAttribute("aria-pressed", "true");
    await expect(heart).not.toHaveAttribute("aria-busy", "true");
    // 先完成初次读取，再令写入与对账读取都失败，稳定停留在可手动重读的状态。
    const unavailable = { status: 503, json: { error: { code: "database_unavailable", message: "个人音乐暂时不可用" }, request_id: "personal-layout" } };
    await page.route(`**/api/v1/me/favorites/tracks/${firstTrack.track_id}`, (route) => route.request().method() === "DELETE" ? route.fulfill(unavailable) : route.fallback());
    await page.route("**/api/v1/me/favorites/lookup", (route) => route.fulfill(unavailable));
    await heart.click();
    const notice = row.getByText("没能确认是否已收藏。", { exact: true });
    const retry = row.getByRole("button", { name: "重新读取《第一乐章》的收藏状态", exact: true });
    await expect(notice).toBeVisible();
    await expect(retry).toBeVisible();
    await expect(heart).toHaveAttribute("aria-busy", "true");
    await expect(heart).toHaveAttribute("aria-pressed", "true");
    await screenshot(page, info, width, "favorite-reread-failed");
    const rowBox = await row.boundingBox();
    if (rowBox === null) throw new Error("失败态曲目行不可见");
    for (const element of [notice, retry, heart, row.getByRole("button", { name: "更多操作", exact: true })]) {
      const box = await element.boundingBox();
      if (box === null) throw new Error("失败态内容不可见");
      expect.soft(box.x, "提示和按钮不能超出行左边界").toBeGreaterThanOrEqual(rowBox.x - 1);
      expect.soft(box.x + box.width, "提示和按钮不能超出行右边界").toBeLessThanOrEqual(rowBox.x + rowBox.width + 1);
      expect.soft(box.y, "提示和按钮不能超出行上边界").toBeGreaterThanOrEqual(rowBox.y - 1);
      expect.soft(box.y + box.height, "提示和按钮不能压到下一行").toBeLessThanOrEqual(rowBox.y + rowBox.height + 1);
    }
    const nextRow = trackTable(page).getByRole("row").filter({ has: page.getByText("第二乐章", { exact: true }) });
    const nextBox = await nextRow.boundingBox();
    expect.soft(nextBox?.y ?? 0, "失败态行与下一行不重叠").toBeGreaterThanOrEqual(rowBox.y + rowBox.height - 1);
    await layout(page, "心形重读失败");
  });
}

for (const width of widths) {
  test(`${width}px 收藏专辑：长卡片、不可用灰态、心形位置与 axe`, async ({ page, backend }, info) => {
    const mock = await backend.start({ signedInAs: "listener" });
    seed(mock, true);
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/me/favorites");
    const unavailable = page.getByRole("listitem").filter({ has: page.getByRole("button", { name: "收藏《暂时不可用的旧专辑》", exact: true }) });
    await expect(unavailable.getByText("暂时不可用", { exact: true })).toBeVisible();
    await expect(unavailable.getByRole("link")).toHaveCount(0);
    await expect(unavailable).toHaveAttribute("data-available", "false");
    const first = page.getByRole("listitem").filter({ has: page.getByRole("button", { name: `收藏《${LONG_TITLE}》`, exact: true }) });
    const title = first.getByText(LONG_TITLE, { exact: true });
    await expect(title).toBeVisible();
    expect(await visibleLines(title)).toBeLessThanOrEqual(3);
    const availableColor = await title.evaluate((element) => getComputedStyle(element).color);
    const unavailableColor = await unavailable.getByText("暂时不可用的旧专辑", { exact: true }).and(unavailable.locator(":not([aria-hidden='true'])")).evaluate((element) => getComputedStyle(element).color);
    expect(unavailableColor).not.toBe(availableColor);
    const linkBox = await first.getByRole("link").boundingBox();
    const heartBox = await first.getByRole("button").boundingBox();
    expect(linkBox).not.toBeNull();
    expect(heartBox?.y ?? 0).toBeGreaterThanOrEqual((linkBox?.y ?? 0) + (linkBox?.height ?? 0));
    await screenshot(page, info, width, "favorites-albums");
    await layout(page, "收藏专辑");
  });

  test(`${width}px 收藏曲目：千字标题、缺失行与 axe`, async ({ page, backend }, info) => {
    const mock = await backend.start({ signedInAs: "listener" });
    seed(mock, true);
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/me/favorites?tab=tracks");
    const table = trackTable(page);
    await expect(table).toBeVisible();
    const row = table.getByRole("row").filter({ has: page.getByText("缺失的第三乐章", { exact: true }) });
    await expect(row.getByText("文件缺失", { exact: true })).toBeVisible();
    await expect(row.getByRole("link")).toHaveCount(0);
    await expect(row.getByRole("button", { name: "播放 缺失的第三乐章", exact: true })).toBeDisabled();
    await expect(row.getByRole("button", { name: "收藏《缺失的第三乐章》", exact: true })).toBeEnabled();
    const play = row.getByRole("button", { name: "播放 缺失的第三乐章", exact: true });
    const heart = row.getByRole("button", { name: "收藏《缺失的第三乐章》", exact: true });
    const more = row.getByRole("button", { name: "更多操作", exact: true });
    const boxes = await Promise.all([play, heart, more].map((item) => item.boundingBox()));
    expect(boxes.every((box) => box !== null)).toBe(true);
    expect((boxes[1]?.x ?? 0) - (boxes[0]?.x ?? 0)).toBeGreaterThan(0);
    expect((boxes[2]?.x ?? 0) - (boxes[1]?.x ?? 0)).toBeGreaterThan(0);
    await expect(more).toHaveText("");
    expect(boxes[2]?.y).toBe(boxes[0]?.y);
    const missingColor = await row.getByText("缺失的第三乐章", { exact: true }).evaluate((element) => getComputedStyle(element).color);
    const presentColor = await table.getByRole("rowheader").getByText(LONG_TITLE, { exact: true }).evaluate((element) => getComputedStyle(element).color);
    expect(missingColor).not.toBe(presentColor);
    await screenshot(page, info, width, "favorites-tracks");
    await layout(page, "收藏曲目");
  });

  test(`${width}px 歌单列表：百字名字、卡片文字位置与 axe`, async ({ page, backend }, info) => {
    const mock = await backend.start({ signedInAs: "listener" });
    seed(mock);
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/me/playlists");
    const link = page.getByRole("link", { name: new RegExp(`^${longName}`) });
    await expect(link).toBeVisible();
    const name = link.getByText(longName, { exact: true });
    expect(await visibleLines(name)).toBeLessThanOrEqual(3);
    const nameBox = await name.boundingBox();
    const metaBox = await link.getByText(/2 首/).boundingBox();
    expect(metaBox?.y ?? 0).toBeGreaterThanOrEqual((nameBox?.y ?? 0) + (nameBox?.height ?? 0));
    expect(await visibleLines(page.getByRole("button", { name: "新建歌单", exact: true }))).toBe(1);
    await screenshot(page, info, width, "playlists");
    await layout(page, "歌单列表");
  });

  test(`${width}px 歌单详情与拖动中：操作位置、公告与 axe`, async ({ page, backend }, info) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const { playlist } = seed(mock);
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`/me/playlists/${playlist.state.id}`);
    const first = playlistRow(page, "第一乐章");
    await expect(first).toBeVisible();
    const handle = first.getByRole("button", { name: "拖动《第一乐章》调整顺序", exact: true });
    const title = first.getByText("第一乐章", { exact: true });
    const play = first.getByRole("button", { name: "播放 第一乐章", exact: true });
    const a = await handle.boundingBox();
    const b = await title.boundingBox();
    const c = await play.boundingBox();
    expect(b?.x ?? 0).toBeGreaterThan((a?.x ?? 0) + (a?.width ?? 0));
    // 各宽度下动作都在标题右侧、与标题同行，不再在窄屏另起一行。
    expect(c?.x ?? 0).toBeGreaterThan((b?.x ?? 0) + (b?.width ?? 0));
    expect(c?.y ?? 0).toBeLessThan((b?.y ?? 0) + (b?.height ?? 0));
    expect((c?.y ?? 0) + (c?.height ?? 0)).toBeGreaterThan(b?.y ?? 0);
    expect(await visibleLines(page.getByRole("button", { name: "删除歌单", exact: true }))).toBe(1);
    await screenshot(page, info, width, "playlist-detail");
    await layout(page, "歌单详情");
    const secondHandle = playlistRow(page, "第二乐章").getByRole("button", { name: "拖动《第二乐章》调整顺序", exact: true });
    const target = await secondHandle.boundingBox();
    if (a === null || target === null) throw new Error("拖动把手不可见");
    await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
    await page.mouse.down();
    await page.mouse.move(target.x + target.width / 2, target.y + target.height / 2, { steps: 8 });
    await expect(first).toHaveAttribute("data-dragging", "");
    await expect(page.getByText("《第一乐章》移到第 2 首。", { exact: true })).toHaveCount(1);
    await screenshot(page, info, width, "playlist-dragging");
    await layout(page, "歌单拖动中");
    await page.mouse.up();
    await expect.poll(() => mock.callsTo("POST", /\/items\/[^/]+\/move$/).length).toBe(1);
    await expect(page.getByRole("button", { name: /^拖动《/ }).first()).toHaveAttribute("aria-label", "拖动《第二乐章》调整顺序");
  });

  test(`${width}px 添加对话框与播放器心形：边界、文字行数与 axe`, async ({ page, backend }, info) => {
    const mock = await backend.start({ signedInAs: "listener" });
    seed(mock);
    await audio(page);
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/me/favorites?tab=tracks");
    const first = trackTable(page).getByRole("row").filter({ has: page.getByText("第一乐章", { exact: true }) });
    await first.getByRole("button", { name: "播放 第一乐章", exact: true }).click();
    const player = page.getByRole("region", { name: "播放器", exact: true });
    const heart = player.getByRole("button", { name: "收藏《第一乐章》", exact: true });
    await expect(heart).toBeEnabled();
    const playerBox = await player.boundingBox();
    const heartBox = await heart.boundingBox();
    expect(heartBox?.x ?? -1).toBeGreaterThanOrEqual(playerBox?.x ?? 0);
    expect((heartBox?.x ?? 0) + (heartBox?.width ?? 0)).toBeLessThanOrEqual((playerBox?.x ?? 0) + (playerBox?.width ?? 0));
    expect(heartBox?.y ?? -1).toBeGreaterThanOrEqual(playerBox?.y ?? 0);
    expect((heartBox?.y ?? 0) + (heartBox?.height ?? 0)).toBeLessThanOrEqual((playerBox?.y ?? 0) + (playerBox?.height ?? 0));
    expect(await visibleLines(player.getByRole("link", { name: "第一乐章", exact: true }))).toBeLessThanOrEqual(width < 768 ? 2 : 1);
    await screenshot(page, info, width, "player-heart");
    await layout(page, "播放器心形");
    await openMenu(page, first.getByRole("button", { name: "更多操作", exact: true }));
    await page.getByRole("menuitem", { name: "添加到歌单…", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "添加到歌单", exact: true });
    await expect(dialog).toBeVisible();
    const choice = dialog.getByRole("button", { name: new RegExp(`^${longName}`) });
    await expect(choice).toBeDisabled();
    await expect(choice).toContainText("已在歌单里");
    const bounds = await dialog.boundingBox();
    expect(bounds?.x ?? -1).toBeGreaterThanOrEqual(0);
    expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(width);
    expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
    const nameBox = await choice.getByText(longName, { exact: true }).boundingBox();
    const metaBox = await choice.getByText(/已在歌单里/).boundingBox();
    expect(metaBox?.y ?? 0).toBeGreaterThanOrEqual((nameBox?.y ?? 0) + (nameBox?.height ?? 0));
    expect(await visibleLines(dialog.getByRole("button", { name: "新建歌单", exact: true }))).toBe(1);
    await screenshot(page, info, width, "add-to-playlist");
    await layout(page, "添加到歌单");
  });
}

/** 只使用 Tab/Enter/方向键抵达和操作目标，不用 focus/click 绕过键盘路径。 */
async function keyboardTarget(page: Page, target: Locator, activate = true) {
  for (let i = 0; i < 160; i += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) {
      await expectVisibleFocusRing(page, "个人音乐键盘路径");
      if (activate) await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("Tab");
  }
  throw new Error("Tab 未抵达目标");
}

async function keyboardMenu(page: Page, trigger: Locator, label: string) {
  await keyboardTarget(page, trigger);
  const item = page.getByRole("menuitem", { name: label, exact: true });
  await expect(item).toBeEnabled();
  for (let i = 0; i < 10; i += 1) {
    if (await item.evaluate((element) => element === document.activeElement)) { await page.keyboard.press("Enter"); return; }
    await page.keyboard.press("ArrowDown");
  }
  throw new Error(`方向键未抵达菜单项 ${label}`);
}

test("纯键盘：收藏与取消、新建并加入、四种菜单移动、键盘拖动、移除撤销与播放", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  seed(mock);
  await audio(page);
  await page.goto("/me/favorites?tab=tracks");
  const first = trackTable(page).getByRole("row").filter({ has: page.getByText("第一乐章", { exact: true }) });
  const heart = first.getByRole("button", { name: "收藏《第一乐章》", exact: true });
  await keyboardTarget(page, heart);
  await expect(heart).toHaveAttribute("aria-pressed", "false");
  await expect(heart).not.toHaveAttribute("aria-busy", "true");
  await page.keyboard.press("Enter");
  await expect(heart).toHaveAttribute("aria-pressed", "true");
  await keyboardMenu(page, first.getByRole("button", { name: "更多操作", exact: true }), "添加到歌单…");
  let dialog = page.getByRole("dialog", { name: "添加到歌单", exact: true });
  await keyboardTarget(page, dialog.getByRole("button", { name: "新建歌单", exact: true }));
  await keyboardTarget(page, dialog.getByRole("textbox", { name: "名字", exact: true }), false);
  await page.keyboard.type("键盘歌单");
  await keyboardTarget(page, dialog.getByRole("button", { name: "新建并加入", exact: true }));
  await expect(dialog).toBeHidden();
  const second = trackTable(page).getByRole("row").filter({ has: page.getByText("第二乐章", { exact: true }) });
  await keyboardMenu(page, second.getByRole("button", { name: "更多操作", exact: true }), "添加到歌单…");
  dialog = page.getByRole("dialog", { name: "添加到歌单", exact: true });
  await keyboardTarget(page, dialog.getByRole("button", { name: /^键盘歌单/ }));
  await expect(dialog).toBeHidden();
  await keyboardTarget(page, page.getByRole("navigation", { name: "我的音乐", exact: true }).getByRole("link", { name: "歌单", exact: true }));
  await keyboardTarget(page, page.getByRole("link", { name: /^键盘歌单/ }));
  await expect(page.getByRole("heading", { name: "键盘歌单", exact: true })).toBeVisible();
  const firstRow = playlistRow(page, "第一乐章");
  for (const action of ["下移", "上移", "移到最后", "移到最前"]) {
    await keyboardMenu(page, firstRow.getByRole("button", { name: "更多操作", exact: true }), action);
    await expect(page.getByText(new RegExp(`《第一乐章》已移到第 ${action === "下移" || action === "移到最后" ? 2 : 1} 首`))).toBeVisible();
  }
  const handle = firstRow.getByRole("button", { name: "拖动《第一乐章》调整顺序", exact: true });
  await keyboardTarget(page, handle, false);
  await page.keyboard.press("Space");
  await expect(firstRow).toHaveAttribute("data-dragging", "");
  await page.keyboard.press("ArrowDown");
  await expect(page.getByText("《第一乐章》移到第 2 首。", { exact: true })).toHaveCount(1);
  await page.keyboard.press("Space");
  await expect(page.getByRole("button", { name: /^拖动《/ }).first()).toHaveAttribute("aria-label", "拖动《第二乐章》调整顺序");
  await keyboardMenu(page, firstRow.getByRole("button", { name: "更多操作", exact: true }), "从歌单移除");
  await expect(firstRow).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "键盘歌单", exact: true })).toBeFocused();
  await keyboardTarget(page, page.getByRole("button", { name: "撤销", exact: true }));
  await expect(firstRow).toBeVisible();
  await expect(page.getByText("已放回《第一乐章》，在第 2 首。", { exact: true })).toBeVisible();
  await keyboardTarget(page, firstRow.getByRole("button", { name: "播放 第一乐章", exact: true }));
  await expect(page.getByRole("region", { name: "播放器", exact: true }).getByRole("link", { name: "第一乐章", exact: true })).toBeVisible();
});

test("触屏把手拖动后顺序持久化，刷新不恢复旧顺序", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true, baseURL: "http://127.0.0.1:4173", locale: "zh-CN" });
  try {
    const { installApiMock } = await import("./support/api-mock");
    const mock = await installApiMock(context, { signedInAs: "listener" });
    const { playlist, tracks } = seed(mock);
    const page = await context.newPage();
    await page.goto(`/me/playlists/${playlist.state.id}`);
    const first = page.getByRole("button", { name: "拖动《第一乐章》调整顺序", exact: true });
    const second = page.getByRole("button", { name: "拖动《第二乐章》调整顺序", exact: true });
    await expect(first).toBeVisible();
    const a = await first.boundingBox();
    const b = await second.boundingBox();
    if (a === null || b === null) throw new Error("触屏拖动把手不可见");
    expect(await first.evaluate((element) => getComputedStyle(element).touchAction)).toBe("none");
    const cdp = await context.newCDPSession(page);
    const x = a.x + a.width / 2;
    const start = a.y + a.height / 2;
    const end = b.y + b.height / 2;
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y: start }] });
    for (let step = 1; step <= 8; step += 1) await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: start + (end - start) * step / 8 }] });
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await expect.poll(() => playlist.items.map((item) => item.track_id)).toEqual([tracks[1]?.track_id, tracks[0]?.track_id, tracks[2]?.track_id]);
    await page.reload();
    await expect(page.getByRole("button", { name: /^拖动《/ }).first()).toHaveAttribute("aria-label", "拖动《第二乐章》调整顺序");
  } finally {
    await context.close();
  }
});
