import { mkdir, rm, utimes, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { decodeReleaseDetail, decodeReleaseGroupDetail } from "../../src/features/catalog/api/decoders";
import { decodeClaimDetail, decodeClaimPage, decodeOperationPage, decodeReleaseIssues } from "../../src/features/governance/api/decoders";
import { body, catalog, fixtureRoot, login, origin, post, prepareLibrary, scan, syntheticWav } from "./support";

// M4 认领的真实服务用例：隔离服务、隔离 PostgreSQL、真实 Chromium。素材是本用例自己生成的合成 WAV，放在 library/claims 下：
// set 目录里三种专辑标签（One 三首、X 两首、Y 两首，没有一种过半数，扫描按标签拆成三个版本，各有一个“同一目录的归组冲突”），
// two 目录是另一张专辑 Two。先用接口在 One 上做好四类人工状态（改标题、归入 Two 的专辑、设为那张专辑的默认版本、核对归组冲突），
// 再把 One 的三首换成另一份音频（同路径、同标签，改修改时间）并重新扫描：扫描证明不了是同一个版本，新建发行 One'，旧的 One 不可见。
// 页面上在“待认领”里按推荐认领，检查专辑页与接口读到的结果、原样重扫后四类状态保持、撤销与再认领。用例结束后删除素材并重新扫描，不留下痕迹。
// 覆盖单元与替身测试覆盖不到的整条链路：真实扫描 → 待认领与推荐 → 页面认领 → 真实后端迁移 → 真实重扫 → 撤销。

const artist = "Claim Quartet";
const one = { album: "Claim One", corrected: "Claim One (corrected)" };
const splitX = "Claim X";
const splitY = "Claim Y";
const two = "Claim Two";

const directory = () => join(fixtureRoot, "claims");
const setFile = (name: string) => join(directory(), "set", name);
const twoFile = (name: string) => join(directory(), "two", name);
type Track = { path: (name: string) => string; file: string; title: string; album: string; fill: number };
const oneTracks: readonly Track[] = [1, 2, 3].map((number) => ({ path: setFile, file: `one${number}.wav`, title: `One ${number}`, album: one.album, fill: 0x10 + number }));
const initial: readonly Track[] = [
  ...oneTracks,
  { path: setFile, file: "x1.wav", title: "X One", album: splitX, fill: 0x31 },
  { path: setFile, file: "x2.wav", title: "X Two", album: splitX, fill: 0x32 },
  { path: setFile, file: "y1.wav", title: "Y One", album: splitY, fill: 0x41 },
  { path: setFile, file: "y2.wav", title: "Y Two", album: splitY, fill: 0x42 },
  { path: twoFile, file: "t1.wav", title: "Two One", album: two, fill: 0x51 },
  { path: twoFile, file: "t2.wav", title: "Two Two", album: two, fill: 0x52 },
];
const write = (track: Track, fill = track.fill) => writeFile(track.path(track.file), syntheticWav({ title: track.title, album: track.album, artist }, fill));

type Located = { groupId: string; releaseId: string };
let oldOne: Located;
let albumTwo: Located;
let newOneId: string;

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
const releaseOf = async (request: APIRequestContext, releaseId: string) => decodeReleaseDetail(await body(await request.get(`/api/v1/releases/${releaseId}`)));
const groupOf = async (request: APIRequestContext, groupId: string) => decodeReleaseGroupDetail(await body(await request.get(`/api/v1/release-groups/${groupId}`)));
const issuesOf = async (request: APIRequestContext, releaseId: string) => decodeReleaseIssues(await body(await request.get(`/api/v1/releases/${releaseId}/issues`)));
const claimsOf = async (request: APIRequestContext) => decodeClaimPage(await body(await request.get("/api/v1/release-claims?page_size=100")));
const operationTotal = async (request: APIRequestContext) => decodeOperationPage(await body(await request.get("/api/v1/operations?page_size=1"))).pagination.total;
/** 版本现在所在的组（发行详情不含组，从目录里找）。 */
const groupIdOf = async (request: APIRequestContext, releaseId: string) => (await catalog(request)).find((item) => item.release.id === releaseId)?.groupId;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  // 文件写操作只允许脚本本轮生成的临时根，绝不接受真实音乐目录。
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  await prepareLibrary(request);
  await mkdir(join(directory(), "set"), { recursive: true });
  await mkdir(join(directory(), "two"), { recursive: true });
  for (const track of initial) await write(track);
  await scan(request);
  oldOne = await locate(request, one.album);
  albumTwo = await locate(request, two);
  expect(oldOne.releaseId).not.toBe(albumTwo.releaseId);

  // 旧版本 One 上的四类人工状态，都用真实接口做：改标题、归入 Two 的专辑、设为那张专辑的默认版本、核对归组冲突。
  const revision = async () => (await releaseOf(request, oldOne.releaseId)).revision;
  await body(await request.patch(`/api/v1/releases/${oldOne.releaseId}/corrections`, { data: { expected_revision: await revision(), changes: { title: { mode: "set", value: one.corrected } } }, headers: writeHeaders() }));
  await body(await post(request, `/api/v1/releases/${oldOne.releaseId}/group`, { expected_revision: await revision(), action: "join", group_id: albumTwo.groupId }));
  await body(await post(request, `/api/v1/release-groups/${albumTwo.groupId}/default`, { expected_revision: (await groupOf(request, albumTwo.groupId)).revision, release_id: oldOne.releaseId }));
  const basis = (await issuesOf(request, oldOne.releaseId)).grouping?.basis;
  expect(basis).toMatch(/^[0-9a-f]{64}$/);
  await body(await post(request, `/api/v1/releases/${oldOne.releaseId}/issue-review`, { expected_revision: await revision(), issue: "grouping", action: "review", basis }));
  expect((await issuesOf(request, oldOne.releaseId)).release.attention_count).toBe(0);

  // 把 One 的三首换成另一份音频（同路径、同标签），改修改时间：扫描证明不了是同一个版本，新建发行。
  const later = new Date(Date.now() + 60_000);
  for (const track of oneTracks) {
    await write(track, track.fill + 0x80);
    await utimes(track.path(track.file), later, later);
  }
  await scan(request);
  newOneId = (await locate(request, one.album)).releaseId;
  expect(newOneId).not.toBe(oldOne.releaseId);
  expect((await claimsOf(request)).items.map((item) => item.release.id)).toEqual([oldOne.releaseId]);
});

test.afterAll(async ({ request }) => {
  await removeSyntheticAlbums(request);
});

test.beforeEach(async ({ page }) => {
  await login(page.request);
});

const level2 = (page: Page, name: string) => page.getByRole("heading", { level: 2, name, exact: true });
const claimButton = (page: Page) => page.getByRole("button", { name: `认领到《${one.album}》` });
const claimedNotice = (page: Page) => page.getByText(new RegExp(`已把《${one.corrected.replace(/[()]/g, "\\$&")}》上的修改认领到《${one.album}》`));

test("旧版本出现在“待认领”：写明原来的专辑、位置和留着的四类修改；推荐命中新版本（同样的文件三个、同一目录）；新版本的专辑页提示它", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  // 专辑页的提示：管理员打开新版本，看到“可能接替了已不可见的《…》”。
  const newGroup = await groupIdOf(page.request, newOneId);
  await page.goto(`/albums/${newGroup}`);
  const hints = page.getByRole("list", { name: "可能接替了已不可见的版本" });
  await expect(hints.getByText(`可能接替了已不可见的《${one.corrected}》`)).toBeVisible();

  await page.goto("/claims");
  const row = page.getByRole("link", { name: new RegExp(`^${one.corrected.replace(/[()]/g, "\\$&")}`) });
  await expect(row).toBeVisible();
  await expect(row).toContainText(`原来在《${two}》的专辑里`);
  await expect(row).toContainText("claims/set");
  await expect(row).toContainText("修正 1 项 · 专辑 · 默认版本 · 核对 1 项");
  await row.click();
  await expect(level2(page, one.corrected)).toBeVisible();

  // 留着的修改明细。
  await expect(page.getByRole("table", { name: "旧版本上的字段修正" })).toContainText(`改成“${one.corrected}”`);
  await expect(page.getByRole("table", { name: "旧版本上的字段修正" })).toContainText(one.album);
  await expect(page.getByText(`它是《${two}》这张专辑的默认版本。`)).toBeVisible();
  await expect(page.getByRole("table", { name: "旧版本上的问题核对" })).toContainText("归组冲突");

  // 推荐：新版本排第一，两个理由都命中；同目录的 X、Y 只是同一目录。
  const options = page.getByRole("group", { name: "推荐的版本" }).getByRole("radio");
  await expect(options.first()).toBeVisible();
  const first = options.first().locator("xpath=ancestor::label");
  await expect(first).toContainText(one.album);
  await expect(first).toContainText("同样的文件（3 个）、同一目录");
  const api = decodeClaimDetail(await body(await page.request.get(`/api/v1/release-claims/${oldOne.releaseId}`)));
  expect(api.suggestions[0]).toMatchObject({ release: { id: newOneId }, reasons: ["same_files", "same_directory"], same_files: 3 });
  expect(api.suggestions.slice(1).every((suggestion) => suggestion.reasons.join() === "same_directory" && suggestion.same_files === 0)).toBe(true);
});

test("在页面上认领：预览四类都迁移、核对立即生效；认领之后专辑页、默认版本与“需要关注”都在新版本上，旧版本不再待认领", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  expect((await issuesOf(page.request, newOneId)).release.attention_count).toBe(1);
  await page.goto(`/claims?release=${oldOne.releaseId}`);
  await page.getByRole("group", { name: "推荐的版本" }).getByRole("radio").first().check();
  await expect(page).toHaveURL(new RegExp(`target=${newOneId}`));
  const items = page.getByRole("list", { name: "认领会怎样处理旧版本上的修改" }).getByRole("listitem");
  await expect(items).toHaveCount(4);
  await expect(items.nth(0)).toContainText("迁移修正 · 标题");
  await expect(items.nth(1)).toContainText(`迁移所在的专辑这个版本会归入旧版本原来的专辑《${two}》`);
  await expect(items.nth(2)).toContainText(`迁移默认版本这个版本接任《${two}》的默认版本`);
  await expect(items.nth(3)).toContainText("迁移核对 · 归组冲突");
  await expect(items.nth(3)).toContainText("这个版本有同样的问题，核对会直接生效");

  const operationsBefore = await operationTotal(page.request);
  await claimButton(page).click();
  await expect(claimedNotice(page)).toBeVisible();
  expect(await operationTotal(page.request)).toBe(operationsBefore + 1);
  // 旧版本不再待认领；新版本的专辑页显示迁来的标题，在 Two 的专辑里并且是默认版本。
  expect((await claimsOf(page.request)).items).toEqual([]);
  const migrated = await releaseOf(page.request, newOneId);
  expect(migrated.title).toBe(one.corrected);
  expect(migrated.corrected_fields).toContain("title");
  const album = await groupOf(page.request, albumTwo.groupId);
  expect(album.default_release_id).toBe(newOneId);
  expect(album.releases.map((release) => release.id)).toContain(newOneId);
  expect(album.releases.map((release) => release.id)).not.toContain(oldOne.releaseId);
  // 归组冲突的核对依据相同，仍然有效：新版本的“需要关注”从 1 变成 0。
  expect((await issuesOf(page.request, newOneId)).release.attention_count).toBe(0);

  // “打开这张专辑”到 Two 的专辑，新版本是默认版本。
  await page.getByRole("link", { name: "打开这张专辑" }).click();
  await expect(page).toHaveURL(new RegExp(`/albums/${albumTwo.groupId}`));
  await expect(page.getByRole("heading", { level: 1, name: one.corrected })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "版本" }).getByText("默认版本")).toBeVisible();
});

test("原样重扫之后四类状态都保持，扫描不改操作记录，新版本仍是同一个发行", async ({ page }) => {
  const records = await operationTotal(page.request);
  await scan(page.request);
  expect((await locate(page.request, one.corrected)).releaseId).toBe(newOneId);
  expect((await releaseOf(page.request, newOneId)).title).toBe(one.corrected);
  expect((await groupOf(page.request, albumTwo.groupId)).default_release_id).toBe(newOneId);
  expect((await issuesOf(page.request, newOneId)).release.attention_count).toBe(0);
  expect((await claimsOf(page.request)).items).toEqual([]);
  expect(await operationTotal(page.request)).toBe(records);
});

test("撤销认领：新版本回到认领之前，旧版本又在待认领里；可以再认领一次", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/settings/operations");
  const row = page.getByRole("row").filter({ hasText: "认领" }).first();
  await expect(row).toContainText(`从《${one.corrected}》（已不可见）认领：修正 标题；专辑；默认版本；核对 归组冲突`);
  await row.getByRole("button", { name: /^撤销/ }).click();
  await page.getByRole("dialog", { name: "撤销这次认领？" }).getByRole("button", { name: "撤销" }).click();
  await expect(page.getByText("已撤销这次认领，修改还给了旧版本。")).toBeVisible();

  // 新版本：扫描值的标题、没有核对、不在 Two 的专辑里；旧版本拿回了它的四类状态。
  expect((await releaseOf(page.request, newOneId)).title).toBe(one.album);
  expect((await issuesOf(page.request, newOneId)).release.attention_count).toBe(1);
  const album = await groupOf(page.request, albumTwo.groupId);
  expect(album.releases.map((release) => release.id)).not.toContain(newOneId);
  expect(album.default_release_id).toBeNull();
  const claims = await claimsOf(page.request);
  expect(claims.items.map((item) => item.release.id)).toEqual([oldOne.releaseId]);
  expect(claims.items[0]?.holds).toEqual({ corrections: ["title"], album: true, default: true, reviews: ["grouping"] });

  // 撤销之后又能认领，回到同一个结果。
  await page.goto(`/claims?release=${oldOne.releaseId}&target=${newOneId}`);
  await claimButton(page).click();
  await expect(claimedNotice(page)).toBeVisible();
  expect((await releaseOf(page.request, newOneId)).title).toBe(one.corrected);
  expect((await claimsOf(page.request)).items).toEqual([]);
});

test("普通成员没有入口，待认领的读写接口都是 403", async ({ page, request }) => {
  const member = { username: "claim-listener", password: "PlaybackFixturePassword42" };
  await login(request);
  await body(await post(request, "/api/v1/users", member));
  await login(page.request, member);
  await page.goto("/");
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "待认领" })).toHaveCount(0);
  await page.goto("/claims");
  await expect(page.getByRole("heading", { level: 1, name: "需要管理员权限" })).toBeVisible();
  expect((await page.request.get("/api/v1/release-claims")).status()).toBe(403);
  expect((await page.request.get(`/api/v1/release-claims/${oldOne.releaseId}`)).status()).toBe(403);
  expect((await page.request.get(`/api/v1/releases/${newOneId}/claim-hints`)).status()).toBe(403);
  const write = await page.request.post(`/api/v1/releases/${newOneId}/claim`, {
    data: { expected_revision: 1, source_release_id: oldOne.releaseId, source_revision: 1, preview: "0".repeat(64) },
    headers: { Origin: origin, "Idempotency-Key": crypto.randomUUID() },
  });
  expect(write.status()).toBe(403);
});

test("清理：删除合成素材并重新扫描后，目录里不再有这些版本；带着人工修改的新版本因此成为待认领，一直留在列表里（没有接替者）", async ({ request }) => {
  await removeSyntheticAlbums(request);
  const titles = (await catalog(request)).map((item) => item.release.title);
  for (const title of [one.album, one.corrected, splitX, splitY, two]) expect(titles).not.toContain(title);
  // 新版本现在带着改过的标题、Two 的专辑与默认版本，文件都没了：它自己成了待认领，没有新版本可以接替它，所以留在列表里（本项不做“放弃”）。
  const claims = await claimsOf(request);
  expect(claims.items.map((item) => item.release.id)).toContain(newOneId);
  expect(claims.items.find((item) => item.release.id === newOneId)?.holds).toMatchObject({ corrections: ["title"], default: true });
});
