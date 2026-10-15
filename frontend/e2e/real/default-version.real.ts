import { mkdir, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { decodeReleaseDetail, decodeReleaseGroupDetail } from "../../src/features/catalog/api/decoders";
import { decodeOperationPage } from "../../src/features/governance/api/decoders";
import { body, catalog, decodeID, fixtureRoot, login, origin, post, prepareLibrary, scan, syntheticWav } from "./support";

// M4 默认版本与版本对比的真实服务用例：隔离服务、隔离 PostgreSQL、真实 Chromium。素材是本用例自己生成的两张合成 WAV：
// 标准版（16 位 / 44.1 kHz）与高解析版（24 位 / 96 kHz）；WAV 的 INFO 不带年份，年份用 M3 的修正接口设定（标准版 2001、高解析版 2010），
// 所以自动规则下年份最早的标准版是代表版本。放在 library/defaults 下，用例结束后删除并重新扫描，不留下痕迹。
// 覆盖单元与替身测试覆盖不到的整条链路：页面操作 → 真实后端写入 → 真实扫描 → 页面与接口读到的默认版本与对比。

const artist = "Default Artist";
const standard = { album: "Default Standard", title: "Standard Track", fill: 0x31, year: 2001 };
const hires = { album: "Default Hi-Res", title: "Hi-Res Track", fill: 0x42, year: 2010 };
const standardRemastered = "Default Standard Remaster";

const directory = () => join(fixtureRoot, "defaults");
const standardFile = () => join(directory(), "standard", "01.wav");
const hiresFile = () => join(directory(), "hires", "01.wav");

type Located = { groupId: string; releaseId: string };
let standardAlbum: Located;
let hiresAlbum: Located;
/** 两张专辑归到一起之后的组：标准版原来的组。 */
let groupId: string;

async function locate(request: APIRequestContext, title: string): Promise<Located> {
  const found = (await catalog(request)).find((item) => item.release.title === title);
  if (found === undefined) throw new Error(`目录里没有合成专辑 ${title}`);
  return { groupId: found.groupId, releaseId: found.release.id };
}

async function removeSyntheticAlbums(request: APIRequestContext) {
  await rm(directory(), { recursive: true, force: true });
  await login(request);
  await scan(request);
}

const writeHeaders = () => ({ Origin: origin, "Idempotency-Key": crypto.randomUUID() });
const releaseRevision = async (request: APIRequestContext, releaseId: string) => decodeReleaseDetail(await body(await request.get(`/api/v1/releases/${releaseId}`))).revision;

/** 用 M3 的修正接口设定年份（WAV 的标签里没有年份）。 */
async function correctYear(request: APIRequestContext, releaseId: string, year: number) {
  await body(await request.patch(`/api/v1/releases/${releaseId}/corrections`, { data: { expected_revision: await releaseRevision(request, releaseId), changes: { year: { mode: "set", value: year } } }, headers: writeHeaders() }));
}

const group = async (request: APIRequestContext) => decodeReleaseGroupDetail(await body(await request.get(`/api/v1/release-groups/${groupId}`)));
const defaultPath = () => `/api/v1/release-groups/${groupId}/default`;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  // 文件写操作只允许脚本本轮生成的临时根，绝不接受真实音乐目录。
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  await prepareLibrary(request);
  await mkdir(dirname(standardFile()), { recursive: true });
  await mkdir(dirname(hiresFile()), { recursive: true });
  await writeFile(standardFile(), syntheticWav({ title: standard.title, album: standard.album, artist }, standard.fill));
  await writeFile(hiresFile(), syntheticWav({ title: hires.title, album: hires.album, artist }, hires.fill, 0.2, { sampleRate: 96_000, bitDepth: 24 }));
  await scan(request);
  standardAlbum = await locate(request, standard.album);
  hiresAlbum = await locate(request, hires.album);
  expect(standardAlbum.groupId).not.toBe(hiresAlbum.groupId);

  // 设定年份，再把两张专辑归到一起：一个组两个版本，自动规则下年份最早的标准版是代表版本。
  await correctYear(request, standardAlbum.releaseId, standard.year);
  await correctYear(request, hiresAlbum.releaseId, hires.year);
  await body(await post(request, `/api/v1/releases/${hiresAlbum.releaseId}/group`, { expected_revision: await releaseRevision(request, hiresAlbum.releaseId), action: "join", group_id: standardAlbum.groupId }));
  groupId = standardAlbum.groupId;
  const joined = await group(request);
  expect(joined.releases.map((release) => release.id)).toEqual([standardAlbum.releaseId, hiresAlbum.releaseId]);
  expect(joined.default_release_id).toBeNull();
});

test.afterAll(async ({ request }) => {
  await removeSyntheticAlbums(request);
});

test.beforeEach(async ({ page }) => {
  await login(page.request);
});

const level1 = (page: Page, name: string) => page.getByRole("heading", { level: 1, name, exact: true });
const versionNav = (page: Page) => page.getByRole("navigation", { name: "版本" });

test("页面上设为默认版本：打开专辑与专辑列表都先显示它，版本导航把它排第一并标“默认版本”，页面停在刚才的版本", async ({ page }) => {
  await page.goto(`/albums/${groupId}`);
  // 自动规则：年份最早的标准版。
  await expect(level1(page, standard.album)).toBeVisible();
  await expect(versionNav(page).getByText("默认版本")).toHaveCount(0);
  await versionNav(page).getByRole("link", { name: /^版本 2 / }).click();
  await expect(level1(page, hires.album)).toBeVisible();
  await page.getByRole("button", { name: "设为默认版本" }).click();
  await expect(page.getByText("已把这个版本设为默认版本。专辑列表和打开这张专辑时会先显示它。")).toBeVisible();

  // 页面停在刚才的版本；它成为代表版本，排在版本导航第一位并带标签。
  await expect(level1(page, hires.album)).toBeVisible();
  await expect(versionNav(page).getByRole("link", { name: /^版本 1 默认版本/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "改回自动选择" })).toBeVisible();
  const detail = await group(page.request);
  expect(detail).toMatchObject({ revision: 2, default_release_id: hiresAlbum.releaseId });
  expect(detail.releases.map((release) => release.id)).toEqual([hiresAlbum.releaseId, standardAlbum.releaseId]);
  expect(detail.representative.id).toBe(hiresAlbum.releaseId);

  // 专辑列表（按标题搜索这个组）显示它，重新打开专辑先显示它。
  await page.goto("/albums?q=Default");
  await expect(page.getByRole("link", { name: new RegExp(hires.album) })).toBeVisible();
  await expect(page.getByRole("link", { name: new RegExp(standard.album) })).toHaveCount(0);
  await page.goto(`/albums/${groupId}`);
  await expect(level1(page, hires.album)).toBeVisible();
});

test("重新扫描（标准版的专辑标签变了）之后默认版本保持，操作记录不变", async ({ page }) => {
  const before = decodeOperationPage(await body(await page.request.get(`/api/v1/operations?group_id=${groupId}`)));
  await writeFile(standardFile(), syntheticWav({ title: standard.title, album: standardRemastered, artist }, standard.fill));
  await scan(page.request);

  const detail = await group(page.request);
  expect(detail).toMatchObject({ revision: 2, default_release_id: hiresAlbum.releaseId });
  expect(detail.releases.map((release) => release.id)).toEqual([hiresAlbum.releaseId, standardAlbum.releaseId]);
  // 重新扫描改了标准版的扫描值，但没有碰默认版本、专辑的版本号与操作记录。
  expect(detail.releases.find((release) => release.id === standardAlbum.releaseId)?.title).toBe(standardRemastered);
  const after = decodeOperationPage(await body(await page.request.get(`/api/v1/operations?group_id=${groupId}`)));
  expect(after.pagination.total).toBe(before.pagination.total);
  await page.goto(`/albums/${groupId}`);
  await expect(level1(page, hires.album)).toBeVisible();
  await expect(versionNav(page).getByRole("link", { name: /^版本 1 默认版本/ })).toBeVisible();
});

test("版本对比：两种音频格式与年份的差异被标出，人工修正有标记，不出现质量结论", async ({ page }) => {
  await page.goto(`/albums/${groupId}`);
  await expect(level1(page, hires.album)).toBeVisible();
  await page.getByRole("button", { name: "对比版本" }).click();
  const dialog = page.getByRole("dialog", { name: "对比版本" });
  const table = dialog.getByRole("table", { name: "各个版本的对比" });
  await expect(table).toBeVisible();

  const headers = await table.getByRole("columnheader").allTextContents();
  expect(headers[1]).toContain("默认版本");
  // 音频格式：版本 1 是高解析版，版本 2 与它不同，写具体参数。
  const formats = table.getByRole("row", { name: /^音频格式/ }).getByRole("cell");
  await expect(formats.nth(0)).toContainText("24 bit / 96 kHz");
  await expect(formats.nth(1)).toContainText("16 bit / 44.1 kHz");
  await expect(formats.nth(1)).toContainText("与版本 1 不同");
  await expect(formats.nth(0)).not.toContainText("与版本 1 不同");
  // 年份是用修正接口设定的，两个版本都带“人工修正”标记；与基准不同的那个有读屏文字。
  const year = table.getByRole("row", { name: /^年份/ }).getByRole("cell");
  await expect(year.nth(0)).toContainText("2010");
  await expect(year.nth(1)).toContainText("2001");
  await expect(year.nth(1)).toContainText("人工修正");
  await expect(year.nth(1)).toContainText("与版本 1 不同");
  // 两个版本的标题不同，所以有标题行。没有任何质量结论。
  await expect(table.getByRole("rowheader", { name: "标题" })).toBeVisible();
  expect(await dialog.textContent()).not.toMatch(/最高|更好|最佳|高解析|音质/);

  // 从列头切换到版本 2：关闭对话框、页面换成那个版本。
  await dialog.getByRole("link", { name: /^查看这个版本\s*（版本 2）$/ }).click();
  await expect(dialog).toBeHidden();
  await expect(level1(page, standardRemastered)).toBeVisible();
});

test("在“修改记录”里撤销：专辑回到自动规则，页面仍停在当前版本，专辑的版本号继续递增", async ({ page }) => {
  await page.goto(`/albums/${groupId}?release=${standardAlbum.releaseId}`);
  await expect(level1(page, standardRemastered)).toBeVisible();
  await page.locator("summary", { hasText: "修改记录" }).click();
  const history = page.getByRole("table", { name: "修改记录" });
  // 专辑级的默认版本记录与这个版本自己的修正记录一起列出；默认版本的设定是专辑最近一次调整，可以撤销。
  const row = history.getByRole("row").filter({ hasText: "设定默认版本" });
  await expect(row.getByText(`默认版本：自动选择 → 《${hires.album}》`)).toBeVisible();
  await row.getByRole("button", { name: /^撤销/ }).click();
  const confirm = page.getByRole("dialog", { name: "撤销这次默认版本调整？" });
  // 确认对话框写出会改回什么：默认版本从高解析版（快照里的标题与年份）改回自动选择。
  await expect(confirm.getByText(`默认版本：《${hires.album}》（${hires.year}） → 自动选择`, { exact: true })).toBeVisible();
  await confirm.getByRole("button", { name: "撤销" }).click();
  await expect(page.getByText("已撤销默认版本的调整。")).toBeVisible();

  // 回到自动规则：年份最早的标准版又是代表版本；页面仍停在这个版本（它刚好就是标准版）。
  await expect(level1(page, standardRemastered)).toBeVisible();
  const detail = await group(page.request);
  expect(detail).toMatchObject({ revision: 3, default_release_id: null });
  expect(detail.releases.map((release) => release.id)).toEqual([standardAlbum.releaseId, hiresAlbum.releaseId]);
  const records = decodeOperationPage(await body(await page.request.get(`/api/v1/operations?group_id=${groupId}`)));
  expect(records.items.map((item) => item.type)).toEqual(["revert", "release_group_default"]);
  expect(records.items.map((item) => item.revertible)).toEqual([false, false]);
});

test("普通成员：看得到标签并能打开对比，没有设定入口，写接口返回 403", async ({ page, request }) => {
  // 先由管理员再设定一次（专辑的版本号是 3）。
  await login(request);
  await body(await post(request, defaultPath(), { expected_revision: 3, release_id: hiresAlbum.releaseId }));
  const member = { username: "default-listener", password: "PlaybackFixturePassword42" };
  decodeID(await body(await post(request, "/api/v1/users", member)));
  await login(page.request, member);

  await page.goto(`/albums/${groupId}`);
  await expect(level1(page, hires.album)).toBeVisible();
  await expect(versionNav(page).getByRole("link", { name: /^版本 1 默认版本/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "设为默认版本" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "改回自动选择" })).toHaveCount(0);
  await expect(page.locator("summary", { hasText: "修改记录" })).toHaveCount(0);
  await page.getByRole("button", { name: "对比版本" }).click();
  await expect(page.getByRole("dialog", { name: "对比版本" }).getByRole("table", { name: "各个版本的对比" })).toBeVisible();

  expect((await page.request.post(defaultPath(), { data: { expected_revision: 4, release_id: null }, headers: writeHeaders() })).status()).toBe(403);
  expect((await page.request.get(`/api/v1/release-groups/${groupId}/comparison`)).status()).toBe(200);
  // 被拒绝的写入没有任何副作用。
  await login(request);
  expect(await group(request)).toMatchObject({ revision: 4, default_release_id: hiresAlbum.releaseId });
});

test("清理：删除合成素材并重新扫描后，目录里不再有这两张专辑", async ({ request }) => {
  await removeSyntheticAlbums(request);
  const titles = (await catalog(request)).map((item) => item.release.title);
  for (const title of [standard.album, standardRemastered, hires.album]) expect(titles).not.toContain(title);
});
