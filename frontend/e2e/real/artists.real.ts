import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { mkdir, mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";
import { decodeViewer } from "../../src/features/auth/api/decoders";
import { decodeArtistAlbumsPage, decodeArtistDetail, decodeArtistPage } from "../../src/features/artists/api/decoders";
import { decodeCatalogState } from "../../src/features/catalog/api/decoders";
import { decodeArtistMergedSources } from "../../src/features/governance/api/decoders";
import { decodeFavoriteArtistsPage } from "../../src/features/personal/api/decoders";
import { decodeArtistResults } from "../../src/features/search/api/decoders";
import { admin, body, decodeID, fixtureRoot, login, origin, post, prepareLibrary, scan, syntheticWav } from "./support";

// 每条用例独占随机目录；失败也只清理本次创建的目录，不删除公共素材、别的 spec 或用户音乐。
const ownedFolders = new Set<string>();
const test = base.extend<{ artistsFolder: string }>({
  artistsFolder: async ({ request }, provide) => {
    expect(isAbsolute(fixtureRoot)).toBe(true);
    expect(basename(fixtureRoot)).toBe("library");
    expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
    expect(await realpath(fixtureRoot)).toBe(resolve(fixtureRoot));
    await prepareLibrary(request);
    const folder = await mkdtemp(join(fixtureRoot, "artists-real-"));
    ownedFolders.add(folder);
    try {
      await provide(folder);
    } finally {
      await removeOwnedFolder(folder);
      await login(request);
      await scanReady(request);
    }
  },
});

test.setTimeout(90_000);

async function removeOwnedFolder(folder: string) {
  expect(ownedFolders.has(folder)).toBe(true);
  expect(dirname(folder)).toBe(fixtureRoot);
  expect(basename(folder)).toMatch(/^artists-real-[A-Za-z0-9]+$/);
  await rm(folder, { recursive: true, force: true });
}

test.afterAll(async ({ request }) => {
  if (ownedFolders.size === 0) return;
  // 用例失败或中断时仍兜底；后续 playback spec 的 13 首公共素材不受本 spec 影响。
  for (const folder of ownedFolders) await removeOwnedFolder(folder);
  await login(request);
  await scanReady(request);
});

type Artist = { id: string; name: string };
type AlbumFixture = { artist: string; album: string; fill: number };

async function scanReady(api: APIRequestContext) {
  await scan(api);
  // 扫描成功不等于后台已原子发布；搜索与艺人都就绪才继续断言。
  await expect.poll(async () => {
    const state = decodeCatalogState(await body(await api.get("/api/v1/catalog/status")));
    return { search: state.search_mode, artists: state.artists_state };
  }, { timeout: 30_000 }).toEqual({ search: "normalized", artists: "ready" });
}

const runFile = promisify(execFile);

async function seedAlbums(api: APIRequestContext, folder: string, albums: readonly AlbumFixture[]) {
  const { stdout } = await runFile(fileURLToPath(new URL("../../../scripts/ffmpeg-pinned.sh", import.meta.url)));
  const ffmpeg = stdout.trim();
  for (const [index, album] of albums.entries()) {
    const directory = join(folder, `album-${index + 1}`);
    await mkdir(directory);
    const wav = join(directory, "track.wav");
    const title = `${album.album}合成曲目`;
    await writeFile(wav, syntheticWav({ title, album: album.album, artist: album.artist }, album.fill));
    // 默认专辑栏要求显式 album_artist；WAV 的 IART 只有曲目艺人，不能冒充专辑艺人标签。
    await runFile(ffmpeg, ["-hide_banner", "-loglevel", "error", "-nostdin", "-i", wav,
      "-map_metadata", "-1", "-c:a", "flac", "-metadata", `TITLE=${title}`,
      "-metadata", `ALBUM=${album.album}`, "-metadata", `ARTIST=${album.artist}`,
      "-metadata", `ALBUMARTIST=${album.artist}`, join(directory, "track.flac")]);
    await rm(wav);
  }
  await scanReady(api);
}

async function artistDetail(api: APIRequestContext, id: string) {
  return decodeArtistDetail(await body(await api.get(`/api/v1/artists/${id}`)));
}

async function singleArtist(api: APIRequestContext, name: string): Promise<Artist> {
  const result = decodeArtistPage(await body(await api.get(`/api/v1/artists?q=${encodeURIComponent(name)}`)));
  expect(result.pagination.total).toBe(1);
  expect(result.items).toHaveLength(1);
  const artist = result.items[0];
  if (artist === undefined) throw new Error(`未找到合成艺人：${name}`);
  return artist;
}

async function separateArtists(api: APIRequestContext, names: readonly [string, string]) {
  const target = await singleArtist(api, names[0]);
  const source = await singleArtist(api, names[1]);
  expect(target.id).not.toBe(source.id);
  expect(target.name).toBe(names[0]);
  expect(source.name).toBe(names[1]);
  for (const artist of [target, source]) expect((await artistDetail(api, artist.id)).redirect_artist_id).toBeNull();
  return { target, source };
}

async function openArtistFromList(page: Page, query: string, artist: Artist) {
  await page.goto(`/artists?q=${encodeURIComponent(query)}`);
  const link = page.locator("#main").getByRole("link", { name: artist.name, exact: true });
  await expect(link).toHaveCount(1);
  await expect(link).toHaveAttribute("href", `/artists/${artist.id}`);
  await link.click();
  await expect(page).toHaveURL((url) => url.pathname === `/artists/${artist.id}`);
  await expect(page.getByRole("heading", { name: artist.name, exact: true, level: 1 })).toBeVisible();
}

async function mergeThroughUI(page: Page, target: Artist, source: Artist) {
  await page.goto(`/artists/${target.id}`);
  await page.getByRole("button", { name: "合并艺人…", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "合并艺人", exact: true });
  await dialog.getByRole("textbox", { name: "查找艺人", exact: true }).fill(source.name);
  await dialog.getByRole("button", { name: "查找", exact: true }).click();
  // 单独的搜索词只应找到这一位，避免依赖概况文字的拼接或同页别处的艺人链接。
  const choice = dialog.getByRole("radio");
  await expect(choice).toHaveCount(1);
  await expect(choice).toHaveAccessibleName(new RegExp(`^${source.name}`));
  await choice.check();
  await dialog.getByRole("button", { name: "合并", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(`已把“${source.name}”并入“${target.name}”。`, { exact: true })).toBeVisible();
  expect((await artistDetail(page.request, source.id)).redirect_artist_id).toBe(target.id);
  expect((await singleArtist(page.request, source.name)).id).toBe(target.id);
}

async function splitBackThroughUI(page: Page, target: Artist, source: Artist) {
  await page.goto(`/artists/${target.id}`);
  const merged = decodeArtistMergedSources(await body(await page.request.get(`/api/v1/artists/${target.id}/merged-sources`)));
  const prior = merged.items.find((item) => item.artist_id === source.id);
  expect(prior?.name).toBe(source.name);
  expect(prior?.name_key_ids.length).toBeGreaterThan(0);
  await page.getByRole("button", { name: "拆开…", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "拆开艺人", exact: true });
  await dialog.getByRole("button", { name: `拆回 ${source.name}`, exact: true }).click();
  await expect(dialog.getByRole("radio", { name: new RegExp(`^恢复为“${source.name}”`) })).toBeChecked();
  await expect(dialog.getByRole("checkbox", { name: source.name, exact: true })).toBeChecked();
  await dialog.getByRole("button", { name: "预览", exact: true }).click();
  await expect(dialog.getByRole("status")).toContainText(`分给“${source.name}”`);
  await dialog.getByRole("button", { name: "拆开", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(`已拆出“${source.name}”。`, { exact: true })).toBeVisible();
  const restored = await artistDetail(page.request, source.id);
  expect(restored.redirect_artist_id).toBeNull();
  expect(restored.artist.id).toBe(source.id);
  expect(restored.artist.name).toBe(source.name);
  await page.getByRole("link", { name: `打开“${source.name}”`, exact: true }).click();
  await expect(page).toHaveURL((url) => url.pathname === `/artists/${source.id}`);
  await expect(page.getByRole("heading", { name: source.name, exact: true, level: 1 })).toBeVisible();
}

async function expectFavorite(page: Page, artist: Artist, absent: Artist) {
  const saved = decodeFavoriteArtistsPage(await body(await page.request.get("/api/v1/me/favorites/artists")));
  expect(saved.pagination.total).toBe(1);
  expect(saved.items.map((item) => item.artist.id)).toEqual([artist.id]);
  expect(saved.items[0]?.artist.name).toBe(artist.name);
  // 不假设不同用户的缓存实时广播；重新打开收藏页，以真实服务落盘结果验证身份变化。
  await page.goto("/me/favorites?tab=artists");
  const section = page.getByRole("region", { name: "收藏的艺人", exact: true });
  const link = section.getByRole("link", { name: artist.name, exact: true });
  await expect(link).toHaveCount(1);
  await expect(link).toHaveAttribute("href", `/artists/${artist.id}`);
  await expect(section.getByRole("link", { name: absent.name, exact: true })).toHaveCount(0);
  await expect(section.getByRole("button", { name: `收藏“${artist.name}”`, exact: true })).toHaveAttribute("aria-pressed", "true");
  await page.reload();
  await expect(link).toBeVisible();
}

test("真实艺人：周杰倫与周杰伦扫描为同一身份，页面打开两张专辑", async ({ request, page, artistsFolder }) => {
  const albums = [
    { artist: "周杰倫", album: "艺人繁体合成专辑", fill: 211 },
    { artist: "周杰伦", album: "艺人简体合成专辑", fill: 212 },
  ];
  await seedAlbums(request, artistsFolder, albums);
  await login(page.request);
  const artist = await singleArtist(request, "周杰倫");
  expect((await singleArtist(request, "周杰伦")).id).toBe(artist.id);
  const detail = await artistDetail(request, artist.id);
  expect(detail.redirect_artist_id).toBeNull();
  expect(detail.names.map((name) => name.name)).toEqual(expect.arrayContaining(["周杰倫", "周杰伦"]));
  const releases = decodeArtistAlbumsPage(await body(await request.get(`/api/v1/artists/${artist.id}/albums`)));
  expect(releases.pagination.total).toBe(2);
  expect(releases.items.map((item) => item.group.representative.title).sort()).toEqual(albums.map((album) => album.album).sort());
  for (const album of albums) {
    await openArtistFromList(page, album.artist, artist);
    const section = page.getByRole("region", { name: "专辑", exact: true });
    const link = section.getByRole("link").filter({ has: page.getByText(album.album, { exact: true }) });
    const release = releases.items.find((item) => item.group.representative.title === album.album);
    if (release === undefined) throw new Error("艺人页缺少合成专辑");
    await expect(link).toHaveCount(1);
    await link.click();
    await expect(page).toHaveURL((url) => url.pathname === `/albums/${release.group.id}`);
    await expect(page.getByRole("heading", { name: album.album, exact: true, level: 1 })).toBeVisible();
  }
});

test("真实艺人：管理员合并邓丽君与 Teresa Teng，别名搜索后拆回原身份", async ({ request, page, artistsFolder }) => {
  await seedAlbums(request, artistsFolder, [
    // 主名由最常见署名决定，给 canonical 更多署名，避免同票时字典序改选英文名。
    { artist: "邓丽君", album: "艺人中文名合成专辑", fill: 213 },
    { artist: "邓丽君", album: "艺人中文名第二合成专辑", fill: 217 },
    { artist: "Teresa Teng", album: "艺人英文名合成专辑", fill: 214 },
  ]);
  await login(page.request);
  const { target, source } = await separateArtists(request, ["邓丽君", "Teresa Teng"]);
  for (const artist of [target, source]) await openArtistFromList(page, artist.name, artist);
  await mergeThroughUI(page, target, source);
  const search = page.getByRole("combobox", { name: "搜索", exact: true });
  await search.fill(source.name);
  await search.press("Enter");
  await expect(page).toHaveURL((url) => url.pathname === "/search" && url.searchParams.get("q") === source.name);
  const section = page.getByRole("region", { name: "艺人", exact: true });
  await expect(section.getByText(`别名：${source.name}`, { exact: true })).toBeVisible();
  const link = section.getByRole("link", { name: target.name, exact: true });
  await expect(link).toHaveAttribute("href", `/artists/${target.id}`);
  await expect(section.getByRole("link", { name: source.name, exact: true })).toHaveCount(0);
  const result = decodeArtistResults(await body(await request.get(`/api/v1/search?type=artists&q=${encodeURIComponent(source.name)}`)));
  expect(result.pagination.total).toBe(1);
  expect(result.items.map((item) => item.artist.id)).toEqual([target.id]);
  expect(result.items[0]?.match).toMatchObject({ field: "artist_alias", alias: source.name });
  await link.click();
  await expect(page).toHaveURL((url) => url.pathname === `/artists/${target.id}`);
  await splitBackThroughUI(page, target, source);
  const restored = await separateArtists(request, [target.name, source.name]);
  expect([restored.target.id, restored.source.id]).toEqual([target.id, source.id]);
  for (const artist of [target, source]) await openArtistFromList(page, artist.name, artist);
});

test("真实艺人：普通成员收藏 source，管理员合并后显示 canonical，拆回后恢复原收藏", async ({ request, page, browser, artistsFolder }) => {
  // 与上一条完全不同的名字，即使上一条停在合并状态，本条仍可独立运行。
  const suffix = crypto.randomUUID().slice(0, 8);
  const names: [string, string] = [`收藏主名 ${suffix}`, `Favorite Source ${suffix}`];
  await seedAlbums(request, artistsFolder, [
    { artist: names[0], album: `艺人收藏主名专辑 ${suffix}`, fill: 215 },
    { artist: names[0], album: `艺人收藏主名第二专辑 ${suffix}`, fill: 218 },
    { artist: names[1], album: `艺人收藏原名专辑 ${suffix}`, fill: 216 },
  ]);
  const { target, source } = await separateArtists(request, names);
  const member = { username: `artist-listener-${suffix}`, password: admin.password };
  const created = decodeID(await body(await post(request, "/api/v1/users", member)));
  await login(page.request);
  expect(decodeViewer(await body(await page.request.get("/api/v1/auth/me"))).role).toBe("admin");
  // page 自带独立的管理员 context；成员另建 context，Cookie 与 BroadcastChannel 均不共用。
  const memberContext = await browser.newContext({ baseURL: origin, locale: "zh-CN" });
  try {
    const memberPage = await memberContext.newPage();
    await login(memberPage.request, member);
    const viewer = decodeViewer(await body(await memberPage.request.get("/api/v1/auth/me")));
    expect(viewer).toMatchObject({ id: created.id, role: "user" });
    await memberPage.goto(`/artists/${source.id}`);
    await expect(memberPage.getByRole("button", { name: "合并艺人…", exact: true })).toHaveCount(0);
    const heart = memberPage.getByRole("button", { name: `收藏“${source.name}”`, exact: true });
    await expect(heart).toHaveAttribute("aria-pressed", "false");
    await heart.click();
    await expect(heart).toHaveAttribute("aria-pressed", "true");
    await expect.poll(async () => decodeFavoriteArtistsPage(await body(await memberPage.request.get("/api/v1/me/favorites/artists"))).items.map((item) => item.artist.id)).toEqual([source.id]);
    await expectFavorite(memberPage, source, target);
    await mergeThroughUI(page, target, source);
    await expectFavorite(memberPage, target, source);
    await splitBackThroughUI(page, target, source);
    await expectFavorite(memberPage, source, target);
    await memberPage.getByRole("region", { name: "收藏的艺人", exact: true }).getByRole("link", { name: source.name, exact: true }).click();
    await expect(memberPage).toHaveURL((url) => url.pathname === `/artists/${source.id}`);
    await expect(heart).toHaveAttribute("aria-pressed", "true");
  } finally {
    await memberContext.close();
  }
});
