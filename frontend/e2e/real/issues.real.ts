import { mkdir, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { decodeIssuePage, decodeOperationPage, decodeReleaseIssues } from "../../src/features/governance/api/decoders";
import { body, catalog, decodeID, fixtureRoot, login, origin, post, prepareLibrary, scan, syntheticWav } from "./support";

// M4 元数据页的真实服务用例：隔离服务、隔离 PostgreSQL、真实 Chromium。素材是本用例自己生成的合成 WAV，放在 library/issues/split 下：
// 同一个目录里三种专辑标签（A 三首、B 两首、C 两首，没有一种过半数，扫描按标签拆成三个版本，各有一个“同一目录的归组冲突”），
// 删掉 A 的一首再完整扫描，造出缺失曲目（A 剩两首、仍没有过半数；A 原有的三首里两首还在，仍是同一个发行）。
// 用例结束后删除并重新扫描，不留下痕迹。
// 覆盖单元与替身测试覆盖不到的整条链路：页面核对 → 真实后端写入 → 真实重扫 → 依据变了重新提醒 → 撤销。

const artist = "Issue Quartet";
const albumA = "Issue Split A";
const albumB = "Issue Split B";
const albumC = "Issue Split C";

const directory = () => join(fixtureRoot, "issues", "split");
const trackFile = (name: string) => join(directory(), name);
type Track = { file: string; title: string; album: string; fill: number };
const initial: readonly Track[] = [
  { file: "a1.wav", title: "A One", album: albumA, fill: 0x51 },
  { file: "a2.wav", title: "A Two", album: albumA, fill: 0x52 },
  { file: "a3.wav", title: "A Three", album: albumA, fill: 0x53 },
  { file: "b1.wav", title: "B One", album: albumB, fill: 0x61 },
  { file: "b2.wav", title: "B Two", album: albumB, fill: 0x62 },
  { file: "c1.wav", title: "C One", album: albumC, fill: 0x71 },
  { file: "c2.wav", title: "C Two", album: albumC, fill: 0x72 },
];
const write = (track: Track) => writeFile(trackFile(track.file), syntheticWav({ title: track.title, album: track.album, artist }, track.fill));

type Located = { groupId: string; releaseId: string };
let splitA: Located;
let splitB: Located;

async function locate(request: APIRequestContext, title: string): Promise<Located> {
  const found = (await catalog(request)).find((item) => item.release.title === title);
  if (found === undefined) throw new Error(`目录里没有合成专辑 ${title}`);
  return { groupId: found.groupId, releaseId: found.release.id };
}

async function removeSyntheticAlbums(request: APIRequestContext) {
  await rm(join(fixtureRoot, "issues"), { recursive: true, force: true });
  await login(request);
  await scan(request);
}

const issuesOf = async (request: APIRequestContext, releaseId: string) => decodeReleaseIssues(await body(await request.get(`/api/v1/releases/${releaseId}/issues`)));
const attentionOf = async (request: APIRequestContext, releaseId: string) => (await issuesOf(request, releaseId)).release.attention_count;
const operationTotal = async (request: APIRequestContext) => decodeOperationPage(await body(await request.get("/api/v1/operations?page_size=1"))).pagination.total;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  // 文件写操作只允许脚本本轮生成的临时根，绝不接受真实音乐目录。
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  await prepareLibrary(request);
  await mkdir(directory(), { recursive: true });
  for (const track of initial) await write(track);
  await scan(request);
  splitA = await locate(request, albumA);
  splitB = await locate(request, albumB);
  expect(splitA.releaseId).not.toBe(splitB.releaseId);
  // 删掉 A 的第二首、完整扫描：它成为缺失曲目（A 仍可见，仍是同一个发行）。
  await rm(trackFile("a2.wav"));
  await scan(request);
  expect((await locate(request, albumA)).releaseId).toBe(splitA.releaseId);
  const issues = await issuesOf(request, splitA.releaseId);
  expect(issues.grouping?.siblings).toHaveLength(2);
  expect(issues.grouping?.siblings.map((sibling) => sibling.title).sort()).toEqual([albumB, albumC]);
  expect(issues.missing_tracks?.count).toBe(1);
});

test.afterAll(async ({ request }) => {
  await removeSyntheticAlbums(request);
});

test.beforeEach(async ({ page }) => {
  await login(page.request);
});

const detail = (page: Page) => page.getByRole("article");
const section = (page: Page, name: RegExp) => page.locator("section").filter({ has: page.getByRole("heading", { level: 3, name }) });

test("问题页列出同一目录拆出的版本；在页面上核对归组冲突与缺失曲目，“需要关注”随之变少，专辑列表的“只看需要关注”不再列出它", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const before = await attentionOf(page.request, splitA.releaseId);
  expect(before).toBe(2);
  await page.goto("/issues");
  await expect(page.getByRole("link", { name: new RegExp(`^${albumA}`) })).toBeVisible();
  await expect(page.getByRole("link", { name: new RegExp(`^${albumB}`) })).toBeVisible();
  await page.getByRole("link", { name: new RegExp(`^${albumA}`) }).click();
  await expect(detail(page).getByRole("heading", { level: 2, name: albumA })).toBeVisible();
  // 归组冲突写明涉及的文件与同一目录的另一个版本；缺失曲目写明文件。
  const grouping = section(page, /^同一目录的归组冲突$/);
  await expect(grouping.getByText("issues/split/a1.wav")).toBeVisible();
  await expect(grouping.getByRole("link", { name: albumB })).toBeVisible();
  await expect(grouping.getByRole("link", { name: albumC })).toBeVisible();
  await expect(section(page, /^缺失的曲目/).getByText("issues/split/a2.wav")).toBeVisible();

  await grouping.getByRole("button", { name: "标记为已核对：归组冲突" }).click();
  await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
  await section(page, /^缺失的曲目/).getByRole("button", { name: "标记为已核对：缺失曲目" }).click();
  await expect(page.getByText(/已把“缺失曲目”标记为已核对/)).toBeVisible();
  await expect(detail(page).getByText("这个版本已经没有待处理的问题。")).toBeVisible();
  expect(await attentionOf(page.request, splitA.releaseId)).toBe(0);

  // 专辑列表的“只看需要关注”不再列出 A（B 与 C 的归组冲突还在）。
  await page.goto("/albums?attention=required&q=Issue");
  await expect(page.getByRole("link", { name: new RegExp(albumB) })).toBeVisible();
  await expect(page.getByRole("link", { name: new RegExp(albumA) })).toHaveCount(0);
});

test("原样重扫之后核对保持，扫描不改核对与操作记录；A 多了一首（涉及的文件变了）之后归组冲突重新提醒，详情说明“核对之后涉及的文件变了”", async ({ page }) => {
  const records = await operationTotal(page.request);
  await scan(page.request);
  expect(await attentionOf(page.request, splitA.releaseId)).toBe(0);
  expect(await operationTotal(page.request)).toBe(records);

  // A 多一首新的（A 三首、B 两首、C 两首）：仍然没有一种过半数，冲突还在，但 A 涉及的文件变了。
  await write({ file: "a4.wav", title: "A Four", album: albumA, fill: 0x54 });
  await scan(page.request);
  expect((await locate(page.request, albumA)).releaseId).toBe(splitA.releaseId);
  const issues = await issuesOf(page.request, splitA.releaseId);
  expect(issues.grouping).toMatchObject({ status: "open" });
  expect(issues.grouping?.review).not.toBeNull();
  expect(issues.missing_tracks).toMatchObject({ status: "reviewed" });
  expect(issues.release.attention_count).toBe(1);
  expect(await operationTotal(page.request)).toBe(records);

  await page.goto(`/issues?release=${splitA.releaseId}`);
  await expect(section(page, /^同一目录的归组冲突$/).getByText("核对之后涉及的文件变了")).toBeVisible();
  await section(page, /^同一目录的归组冲突$/).getByRole("button", { name: "按现在的情况重新核对：归组冲突" }).click();
  await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
  expect(await attentionOf(page.request, splitA.releaseId)).toBe(0);
});

test("取消核对之后重新计入；在专辑的“修改记录”里撤销这次取消，核对回来；更早的核对之后又有修改，不能撤销", async ({ page }) => {
  await page.goto(`/issues?release=${splitA.releaseId}`);
  await section(page, /^缺失的曲目/).getByRole("button", { name: "取消核对：缺失曲目" }).click();
  await expect(page.getByText("已取消“缺失曲目”的核对。")).toBeVisible();
  expect(await attentionOf(page.request, splitA.releaseId)).toBe(1);

  await page.goto(`/albums/${splitA.groupId}`);
  await page.locator("summary", { hasText: "修改记录" }).click();
  const history = page.getByRole("table", { name: "修改记录" });
  const latest = history.getByRole("row").filter({ hasText: "缺失曲目（1 首）：取消核对" });
  await latest.getByRole("button", { name: /^撤销/ }).click();
  const confirm = page.getByRole("dialog", { name: "撤销这次核对？" });
  await expect(confirm.getByText("缺失曲目（1 首）：标记为已核对", { exact: true })).toBeVisible();
  await confirm.getByRole("button", { name: "撤销" }).click();
  await expect(page.getByText("已撤销这次核对，问题恢复到之前的状态。")).toBeVisible();
  expect(await attentionOf(page.request, splitA.releaseId)).toBe(0);

  const records = decodeOperationPage(await body(await page.request.get(`/api/v1/operations?release_id=${splitA.releaseId}`)));
  expect(records.items.map((item) => [item.type, item.revertible])).toEqual([
    ["revert", false],
    ["release_issue_review", false],
    ["release_issue_review", false],
    ["release_issue_review", false],
    ["release_issue_review", false],
  ]);
});

test("问题列表接口：各类数目之和等于“需要关注”；普通成员没有入口，读写接口都是 403", async ({ page, request }) => {
  const list = decodeIssuePage(await body(await page.request.get("/api/v1/release-issues?page_size=100")));
  for (const item of list.items) expect(item.open.fields + item.open.grouping + item.open.missing_tracks).toBe(item.release.attention_count);
  const reviewed = decodeIssuePage(await body(await page.request.get("/api/v1/release-issues?status=reviewed&page_size=100")));
  expect(reviewed.items.map((item) => item.release.id)).toContain(splitA.releaseId);

  const member = { username: "issue-listener", password: "PlaybackFixturePassword42" };
  await login(request);
  decodeID(await body(await post(request, "/api/v1/users", member)));
  await login(page.request, member);
  await page.goto("/");
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "元数据" })).toHaveCount(0);
  await page.goto(`/albums/${splitB.groupId}`);
  await expect(page.getByRole("heading", { level: 1, name: albumB })).toBeVisible();
  await expect(page.getByRole("link", { name: "查看问题" })).toHaveCount(0);
  expect((await page.request.get("/api/v1/release-issues")).status()).toBe(403);
  expect((await page.request.get(`/api/v1/releases/${splitB.releaseId}/issues`)).status()).toBe(403);
  const write = await page.request.post(`/api/v1/releases/${splitB.releaseId}/issue-review`, {
    data: { expected_revision: 1, issue: "grouping", action: "unreview" },
    headers: { Origin: origin, "Idempotency-Key": crypto.randomUUID() },
  });
  expect(write.status()).toBe(403);
});

test("清理：删除合成素材并重新扫描后，目录里不再有这三个版本", async ({ request }) => {
  await removeSyntheticAlbums(request);
  const titles = (await catalog(request)).map((item) => item.release.title);
  for (const title of [albumA, albumB, albumC]) expect(titles).not.toContain(title);
});
