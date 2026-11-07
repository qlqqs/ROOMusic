import { mkdir, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test } from "@playwright/test";
import { decodeFavoriteTracksPage, decodePlaylistItemsPage } from "../../src/features/personal/api/decoders";
import { body, catalog, fixtureRoot, login, origin, prepareLibrary, scan, syntheticWav } from "./support";

// 只在隔离脚本生成的目录中合成两首 WAV；不读取或修改用户音乐目录。
const directory = () => join(fixtureRoot, "personal");
const titles = ["Personal First", "Personal Second"] as const;
const albumTitle = "Personal Synthetic Album";
let groupId = "";
let trackIds: string[] = [];
let safeRoot = false;

test.beforeAll(async ({ request }) => {
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  safeRoot = true;
  await prepareLibrary(request);
  await mkdir(directory(), { recursive: true });
  for (const [index, title] of titles.entries()) {
    await writeFile(join(directory(), `${index + 1}.wav`), syntheticWav({ title, album: albumTitle, artist: "Personal Artist" }, 0x61 + index, 2));
  }
  await scan(request);
  const album = (await catalog(request)).find((item) => item.release.title === albumTitle);
  if (album === undefined) throw new Error("没有扫描到本轮合成专辑");
  groupId = album.groupId;
  trackIds = album.release.media.flatMap((medium) => medium.tracks).map((track) => track.id);
  expect(trackIds).toHaveLength(2);
});

test.afterAll(async ({ request }) => {
  if (!safeRoot) return;
  await rm(directory(), { recursive: true, force: true });
  await login(request);
  await scan(request);
});

test.beforeEach(async ({ page }) => {
  await login(page.request);
});

test("真实服务：收藏一首刷新仍在，取消时保留行、刷新后消失", async ({ page }) => {
  const id = trackIds[0];
  if (id === undefined) throw new Error("合成曲目不存在");
  await body(await page.request.delete(`/api/v1/me/favorites/tracks/${id}`, { headers: { Origin: origin } }));
  await page.goto(`/albums/${groupId}`);
  const heart = page.getByRole("button", { name: `收藏《${titles[0]}》`, exact: true });
  await expect(heart).toHaveAttribute("aria-pressed", "false");
  await heart.click();
  await expect(heart).toHaveAttribute("aria-pressed", "true");
  await page.goto("/me/liked");
  const table = page.getByRole("table", { name: "收藏的曲目" });
  await expect(table.getByText(titles[0], { exact: true })).toBeVisible();
  await page.reload();
  await expect(table.getByText(titles[0], { exact: true })).toBeVisible();
  const saved = decodeFavoriteTracksPage(await body(await page.request.get("/api/v1/me/favorites/tracks")));
  expect(saved.items.map((item) => item.track.track_id)).toContain(id);
  await table.getByRole("button", { name: `收藏《${titles[0]}》`, exact: true }).click();
  await expect(table.getByRole("button", { name: `收藏《${titles[0]}》`, exact: true })).toHaveAttribute("aria-pressed", "false");
  await expect.poll(async () => decodeFavoriteTracksPage(await body(await page.request.get("/api/v1/me/favorites/tracks"))).items.map((item) => item.track.track_id)).not.toContain(id);
  await expect(table.getByText(titles[0], { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText("还没有收藏的曲目。", { exact: false })).toBeVisible();
  await expect(table).toHaveCount(0);
});

test("真实服务：新建歌单、整张加入两首、菜单交换顺序，刷新仍保持", async ({ page }) => {
  const name = "真实服务双曲歌单";
  await page.goto("/me/playlists");
  await page.getByRole("button", { name: "新建歌单", exact: true }).click();
  const create = page.getByRole("dialog", { name: "新建歌单", exact: true });
  await create.getByRole("textbox", { name: "名字", exact: true }).fill(name);
  await create.getByRole("button", { name: "新建", exact: true }).click();
  await expect(page).toHaveURL(/\/me\/playlists\/[0-9a-f-]{36}$/);
  const playlistId = new URL(page.url()).pathname.split("/").at(-1);
  if (playlistId === undefined) throw new Error("没有新建歌单的 ID");
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  await page.goto(`/albums/${groupId}`);
  await page.getByRole("button", { name: "添加到歌单…", exact: true }).click();
  const add = page.getByRole("dialog", { name: "添加到歌单", exact: true });
  await add.getByRole("button", { name: new RegExp(`^${name}`) }).click();
  await expect(add).toBeHidden();
  // 全部新增时省略零计数；同时继续验证独立的排序与持久化。
  await expect.soft(page.getByText(`《${name}》：已加入 2 首。`, { exact: true })).toBeVisible();
  await expect(page.getByText("0 首已在歌单里", { exact: false })).toHaveCount(0);
  await page.goto(`/me/playlists/${playlistId}`);
  const itemsPath = `/api/v1/me/playlists/${playlistId}/items`;
  const before = decodePlaylistItemsPage(await body(await page.request.get(itemsPath)));
  expect(before.items.map((item) => item.track.track_id)).toEqual(trackIds);
  const firstTitle = before.items[0]?.track.title;
  if (firstTitle === undefined) throw new Error("歌单第一首不存在");
  const row = page.getByRole("listitem").filter({ has: page.getByRole("button", { name: `拖动《${firstTitle}》调整顺序`, exact: true }) });
  await row.getByRole("button", { name: "更多操作", exact: true }).click();
  await page.getByRole("menuitem", { name: "下移", exact: true }).click();
  await expect.poll(async () => decodePlaylistItemsPage(await body(await page.request.get(itemsPath))).items.map((item) => item.track.track_id)).toEqual([...trackIds].reverse());
  const expectedTitles = before.items.map((item) => item.track.title).reverse();
  const handles = page.getByRole("button", { name: /^拖动《/ });
  await expect(handles).toHaveCount(2);
  for (const [index, title] of expectedTitles.entries()) await expect(handles.nth(index)).toHaveAttribute("aria-label", `拖动《${title}》调整顺序`);
  await page.reload();
  await expect(handles).toHaveCount(2);
  for (const [index, title] of expectedTitles.entries()) await expect(handles.nth(index)).toHaveAttribute("aria-label", `拖动《${title}》调整顺序`);
  const refreshed = decodePlaylistItemsPage(await body(await page.request.get(itemsPath)));
  expect(refreshed.items.map((item) => item.track.track_id)).toEqual([...trackIds].reverse());
  await test.info().attach("歌单交换与刷新持久化证据", {
    contentType: "application/json",
    body: JSON.stringify({ playlistId, before: trackIds, afterReload: refreshed.items.map((item) => item.track.track_id) }, null, 2),
  });
});
