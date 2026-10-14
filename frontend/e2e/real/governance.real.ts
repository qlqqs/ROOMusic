import { mkdir, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { decodeReleaseGroupDetail } from "../../src/features/catalog/api/decoders";
import { decodeCorrectionState, decodeOperationPage } from "../../src/features/governance/api/decoders";
import { admin, body, catalog, decodeID, fixtureRoot, login, origin, post, prepareLibrary, scan, syntheticWav } from "./support";

// 阶段 M3 的真实服务用例：隔离服务、隔离 PostgreSQL、真实 Chromium。素材是本用例自己生成的合成 WAV（带专辑标签），
// 放在 library/governance 下，用例结束后删除并重新扫描，不留下痕迹；不依赖别的用例文件的先后顺序。
// 覆盖单元与接口测试替身覆盖不到的整条链路：页面操作 → 真实后端写入 → 真实扫描 → 页面与接口读到的有效值。

const artist = "Governance Artist";
const alpha = { album: "Governance Alpha", title: "Alpha Track", fill: 0x11 };
const beta = { album: "Governance Beta", title: "Beta Track", fill: 0x22 };
const remastered = "Governance Alpha Remaster";
const corrected = "Governance Alpha (corrected)";

const directory = () => join(fixtureRoot, "governance");
const alphaFile = () => join(directory(), "alpha", "01.wav");
const betaFile = () => join(directory(), "beta", "01.wav");

type Located = { groupId: string; releaseId: string };
let alphaAlbum: Located;
let betaAlbum: Located;

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

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  // 文件写操作只允许脚本本轮生成的临时根，绝不接受真实音乐目录。
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  await prepareLibrary(request);
  for (const [file, album] of [[alphaFile(), alpha], [betaFile(), beta]] as const) {
    await mkdir(dirname(file), { recursive: true });
    await writeFile(file, syntheticWav({ title: album.title, album: album.album, artist }, album.fill));
  }
  await scan(request);
  alphaAlbum = await locate(request, alpha.album);
  betaAlbum = await locate(request, beta.album);
  expect(alphaAlbum.groupId).not.toBe(betaAlbum.groupId);
});

test.afterAll(async ({ request }) => {
  await removeSyntheticAlbums(request);
});

test.beforeEach(async ({ page }) => {
  await login(page.request);
});

const level1 = (page: Page, name: string) => page.getByRole("heading", { level: 1, name, exact: true });

async function titleField(request: APIRequestContext, releaseId: string) {
  const state = decodeCorrectionState(await body(await request.get(`/api/v1/releases/${releaseId}/corrections`)));
  const field = state.fields.find((item) => item.field === "title");
  if (field === undefined) throw new Error("修正状态里没有标题字段");
  return field;
}

test("页面上修改标题：重新扫描之后仍是修正值而扫描值随文件更新；在操作记录页撤销后回到现在的扫描值", async ({ page }) => {
  await page.goto(`/albums/${alphaAlbum.groupId}`);
  await expect(level1(page, alpha.album)).toBeVisible();
  await page.getByRole("button", { name: "编辑发行信息" }).click();
  const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
  await dialog.getByRole("group", { name: /^标题/ }).getByRole("radio", { name: "自定义" }).check();
  await dialog.getByRole("textbox", { name: "标题（自定义）" }).fill(corrected);
  await dialog.getByRole("button", { name: "保存修改" }).click();
  await expect(dialog).toBeHidden();
  await expect(level1(page, corrected)).toBeVisible();
  expect(await titleField(page.request, alphaAlbum.releaseId)).toMatchObject({ scan_value: alpha.album, correction: { mode: "set", value: corrected }, effective_value: corrected });

  // 文件的专辑标签变了（音频不变，所以仍是同一首曲目），重新扫描：扫描值更新，修正不动。
  await writeFile(alphaFile(), syntheticWav({ title: alpha.title, album: remastered, artist }, alpha.fill));
  await scan(page.request);
  const rescanned = (await catalog(page.request)).find((item) => item.release.id === alphaAlbum.releaseId);
  expect(rescanned, "重新扫描后仍是同一个发行").toBeDefined();
  expect(rescanned?.release.title).toBe(corrected);
  expect(rescanned?.release.corrected_fields).toContain("title");
  expect(await titleField(page.request, alphaAlbum.releaseId)).toMatchObject({ scan_value: remastered, correction: { mode: "set", value: corrected }, effective_value: corrected });
  await page.reload();
  await expect(level1(page, corrected)).toBeVisible();

  // 操作记录页：最新的一条是这次修改，写明前后的有效值，可以撤销（重新扫描不会让它失效）。
  await page.goto("/operations");
  const table = page.getByRole("table", { name: "操作记录" });
  const row = table.getByRole("row").filter({ hasText: corrected });
  // “撤销”按钮的可访问名称里带着操作类型，所以文字匹配用 exact。
  await expect(row.getByText("修改发行信息", { exact: true })).toBeVisible();
  await expect(row.getByText(`标题：${alpha.album} → ${corrected}`, { exact: true })).toBeVisible();
  await expect(row.getByText(admin.username, { exact: true })).toBeVisible();
  await row.getByRole("button", { name: /^撤销/ }).click();
  const confirm = page.getByRole("dialog", { name: "撤销这次修改？" });
  await expect(confirm.getByText(`标题：${corrected} → ${alpha.album}（恢复为扫描值）`)).toBeVisible();
  await confirm.getByRole("button", { name: "撤销" }).click();
  await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();

  // 撤销恢复的是“没有修正”这个状态，所以回到现在的扫描值（文件更新后的标签），而不是修改之前那一刻的旧值。
  expect(await titleField(page.request, alphaAlbum.releaseId)).toMatchObject({ scan_value: remastered, correction: null, effective_value: remastered });
  await page.goto(`/albums/${alphaAlbum.groupId}`);
  await expect(level1(page, remastered)).toBeVisible();
  const operations = decodeOperationPage(await body(await page.request.get(`/api/v1/operations?release_id=${alphaAlbum.releaseId}`)));
  expect(operations.items.map((item) => item.type)).toEqual(["revert", "release_correction"]);
  expect(operations.items.map((item) => item.revertible)).toEqual([false, false]);
});

test("把两张合成专辑归到一起：一个组两个版本，重新扫描后仍是一组；在“修改记录”里撤销后分开", async ({ page }) => {
  await page.goto(`/albums/${alphaAlbum.groupId}`);
  await expect(level1(page, remastered)).toBeVisible();
  await page.getByRole("button", { name: "归入其他专辑…" }).click();
  const dialog = page.getByRole("dialog", { name: "把这个版本归入其他专辑" });
  await dialog.getByRole("textbox", { name: "搜索专辑" }).fill(beta.album);
  await dialog.getByRole("textbox", { name: "搜索专辑" }).press("Enter");
  await dialog.getByRole("radio", { name: new RegExp(beta.album) }).check();
  await dialog.getByRole("button", { name: "归入这张专辑" }).click();
  await expect(dialog).toBeHidden();

  // 页面跟到目标专辑并选中被归入的版本；一个组两个版本，原来的组不再出现在目录里。
  await expect(page).toHaveURL(new RegExp(`/albums/${betaAlbum.groupId}`));
  await expect(page.getByRole("navigation", { name: "版本" })).toBeVisible();
  const members = async () => decodeReleaseGroupDetail(await body(await page.request.get(`/api/v1/release-groups/${betaAlbum.groupId}`)));
  expect((await members()).releases.map((release) => release.id).sort()).toEqual([alphaAlbum.releaseId, betaAlbum.releaseId].sort());
  expect((await catalog(page.request)).some((item) => item.groupId === alphaAlbum.groupId)).toBe(false);

  // 重新扫描不会改动人工归组。
  await scan(page.request);
  expect((await members()).release_count).toBe(2);
  expect((await catalog(page.request)).some((item) => item.groupId === alphaAlbum.groupId)).toBe(false);

  // 刷新后从“修改记录”撤销：只有最近一次（归组）可以撤销；撤销后页面回到原来的专辑，两张专辑各自成组。
  await page.reload();
  await expect(level1(page, remastered)).toBeVisible();
  await page.locator("summary", { hasText: "修改记录" }).click();
  const history = page.getByRole("table", { name: "修改记录" });
  await expect(history.getByText("调整归组", { exact: true })).toBeVisible();
  await expect(history.getByRole("button", { name: /^撤销/ })).toHaveCount(1);
  await history.getByRole("button", { name: /^撤销/ }).click();
  const confirm = page.getByRole("dialog", { name: "撤销这次归组调整？" });
  await expect(confirm.getByText(`从《${beta.album}》移回《${remastered}》`)).toBeVisible();
  await confirm.getByRole("button", { name: "撤销" }).click();
  await expect(page).toHaveURL(new RegExp(`/albums/${alphaAlbum.groupId}$`));
  await expect(page.getByText("已撤销这次归组调整，这个版本回到了原来的专辑。")).toBeVisible();
  await expect(page.getByRole("navigation", { name: "版本" })).toHaveCount(0);
  expect((await members()).release_count).toBe(1);
  expect((await catalog(page.request)).some((item) => item.groupId === alphaAlbum.groupId && item.release.id === alphaAlbum.releaseId)).toBe(true);
});

test("普通成员没有任何编辑入口与操作记录，接口也拒绝：401 之外的写与读都是 403", async ({ page, request }) => {
  const member = { username: "governance-listener", password: admin.password };
  await login(request);
  decodeID(await body(await post(request, "/api/v1/users", member)));
  await login(page.request, member);
  const headers = () => ({ Origin: origin, "Idempotency-Key": crypto.randomUUID() });

  expect((await page.request.get("/api/v1/operations")).status()).toBe(403);
  expect((await page.request.get(`/api/v1/releases/${alphaAlbum.releaseId}/corrections`)).status()).toBe(403);
  expect((await page.request.patch(`/api/v1/releases/${alphaAlbum.releaseId}/corrections`, { data: { expected_revision: 1, changes: { genre: { mode: "null" } } }, headers: headers() })).status()).toBe(403);
  expect((await page.request.post(`/api/v1/releases/${alphaAlbum.releaseId}/group`, { data: { expected_revision: 1, action: "detach" }, headers: headers() })).status()).toBe(403);
  expect((await page.request.post(`/api/v1/operations/${crypto.randomUUID()}/revert`, { headers: headers() })).status()).toBe(403);

  await page.goto("/operations");
  await expect(page.getByRole("heading", { level: 1, name: "需要管理员权限" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "操作记录" })).toHaveCount(0);
  await page.goto(`/albums/${alphaAlbum.groupId}`);
  await expect(level1(page, remastered)).toBeVisible();
  await expect(page.getByRole("button", { name: "编辑发行信息" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "归入其他专辑…" })).toHaveCount(0);
  await expect(page.locator("summary", { hasText: "修改记录" })).toHaveCount(0);
  // 成员照常浏览专辑：详情与只读的来源与证据都在。
  await expect(page.getByText("来源与证据", { exact: true })).toBeVisible();
});

test("清理：删除合成素材并重新扫描后，目录里不再有这两张专辑", async ({ request }) => {
  await removeSyntheticAlbums(request);
  const titles = (await catalog(request)).map((item) => item.release.title);
  expect(titles).not.toContain(alpha.album);
  expect(titles).not.toContain(remastered);
  expect(titles).not.toContain(beta.album);
});
