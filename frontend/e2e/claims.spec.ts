import type { Locator, Page } from "@playwright/test";
import { albumGroup, groupDetailOf, releaseVersions } from "../src/test/catalog-data";
import { claimDetail, claimReleaseId, suggestionOf } from "../src/test/claim-data";
import { seedClaim } from "../src/test/governance-data";
import { LONG_TITLE, LONG_TOKEN } from "./support/albums-data";
import type { ApiMock } from "./support/api-mock";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { focusedName, tabTo } from "./support/keyboard";
import { heading, mainNav, uuidPattern } from "./support/locators";

// 待认领与认领（M4 第三项）：管理员把已不可见的旧版本上的人工修改迁给扫描新建的发行。受控后端与 Vitest 共用 src/test/governance-data.ts 的预览与认领行为
// （令牌、两个版本号、幂等重放、各种 409、撤销），所以页面能走完整的流程；宽度、axe 与键盘路径在真实浏览器里检查。

const suites = () => albumGroup(1, { title: "Bach: Cello Suites (new rip)", artist: "Pierre Fournier", year: 1961 });
const quartets = () => albumGroup(2, { title: "Beethoven: String Quartets", artist: "Alban Berg Quartett", year: 1989 });
const concertos = () => albumGroup(3, { title: "Delta Concertos", artist: "Delta Ensemble", year: 1985, release_count: 2 });
const longAlbum = () => albumGroup(4, { title: LONG_TITLE, artist: "Long Artist", year: 2001 });
const suitesId = () => suites().representative.id;
const oldId = claimReleaseId(1);
const reviewer = { id: "00000000-0000-4000-8000-000000000002", username: "curator" };

function start(mock: ApiMock) {
  mock.groups = [suites(), quartets(), concertos(), longAlbum()];
  mock.groupDetails.set(concertos().id, groupDetailOf(concertos(), releaseVersions(concertos(), 2)));
  // 旧版本 1：整张重新抓轨之后扫描新建了发行，推荐命中新版本（同样的文件）；有两个字段修正、在 Old Collection 里并且是它的默认版本、核对过介质类型。
  seedClaim(
    mock.governance,
    claimDetail(1, {
      title: "Bach: Cello Suites (my corrections)",
      artist: "Pierre Fournier",
      edition: "1961 original",
      directory: "Fournier/Suites",
      lastSeenAt: "2026-01-20T08:00:00Z",
      album: {
        id: "00000000-0000-4000-8000-0000000000f7",
        title: "Bach: Cello Suites Collection",
        siblings: [{ id: "00000000-0000-4000-8000-0000000000d1", title: "Bach: Cello Suites (live)", visible: true }],
      },
      defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f7", title: "Bach: Cello Suites Collection" }],
      reviews: [{ issue: "media_type", reviewed_at: "2026-01-05T09:30:00Z", reviewer }],
      suggestions: [suggestionOf(suites(), ["same_files", "same_directory"], 12), suggestionOf(quartets(), ["same_directory"], 0)],
    }),
    { media_type: "valid" },
  );
  seedClaim(mock.governance, claimDetail(2, { title: "Haydn: Piano Sonatas", artist: "Piano Duo", directory: "Haydn", lastSeenAt: "2026-01-10T08:00:00Z" }));
  seedClaim(mock.governance, claimDetail(3, { title: "Mozart: Serenades", artist: "Wind Ensemble", directory: "", lastSeenAt: "2025-12-01T08:00:00Z" }));
}

/** 长内容：标题超过 1000 字，目录与修正值是没有空格的长串，推荐也是长标题，用来检查各种宽度下的换行。 */
function startLong(mock: ApiMock) {
  start(mock);
  seedClaim(
    mock.governance,
    claimDetail(4, {
      title: LONG_TITLE,
      artist: "Long Artist",
      edition: LONG_TOKEN.slice(0, 80),
      directory: LONG_TOKEN,
      lastSeenAt: "2026-02-01T08:00:00Z",
      corrections: [
        { field: "title", correction: { mode: "set", value: LONG_TITLE.slice(0, 300) }, scan_value: LONG_TOKEN.slice(0, 200) },
        { field: "label", correction: { mode: "set", value: LONG_TOKEN.slice(0, 200) }, scan_value: null },
      ],
      album: {
        id: "00000000-0000-4000-8000-0000000000f8",
        title: LONG_TITLE.slice(0, 300),
        siblings: [
          { id: "00000000-0000-4000-8000-0000000000d2", title: LONG_TITLE.slice(0, 200), visible: true },
          { id: "00000000-0000-4000-8000-0000000000d3", title: LONG_TOKEN.slice(0, 200), visible: false },
        ],
      },
      defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f8", title: LONG_TITLE.slice(0, 300) }],
      reviews: [{ issue: "source_type", reviewed_at: "2026-01-05T09:30:00Z", reviewer }, { issue: "grouping", reviewed_at: "2026-01-05T09:30:00Z", reviewer }],
      suggestions: [suggestionOf(longAlbum(), ["same_files", "same_directory"], 999), suggestionOf(suites(), ["same_directory"], 0)],
    }),
    { source_type: "valid", grouping: "stale" },
  );
}

const claimsPath = (release?: string, target?: string) => {
  const query = new URLSearchParams();
  if (release !== undefined) query.set("release", release);
  if (target !== undefined) query.set("target", target);
  return query.size === 0 ? "/claims" : `/claims?${query}`;
};
const claimCalls = (mock: ApiMock, releaseId: string) => mock.callsTo("POST", `/api/v1/releases/${releaseId}/claim`);
const claimList = (page: Page) => page.locator("section").filter({ has: page.getByRole("heading", { level: 2, name: "留有修改的旧版本" }) });
const claimDetailOf = (page: Page, title: string) => page.getByRole("article").filter({ has: page.getByRole("heading", { level: 2, name: title }) });
const pickSuggestion = (page: Page, title: string) => page.getByRole("group", { name: "推荐的版本" }).getByRole("radio", { name: new RegExp(title) });
const claimButton = (page: Page, title: string) => page.getByRole("button", { name: `认领到《${title}》` });

/** 元素里第一个文本节点的前 length 个字符占了几行：日期被断成“2026-01- / 03”时是 2。 */
async function leadingTextLines(locator: Locator, length: number): Promise<number> {
  return locator.evaluate((element, count) => {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
      const text = node.textContent ?? "";
      if (text.trim() === "") continue;
      const range = document.createRange();
      range.setStart(node, 0);
      range.setEnd(node, Math.min(count, text.length));
      return new Set([...range.getClientRects()].filter((rect) => rect.width > 0).map((rect) => Math.round(rect.top))).size;
    }
    return 0;
  }, length);
}

/**
 * 元素里第一段可见文字占了几行（按每个字的矩形的顶端去重）。用来发现按钮或短句在字中间被断成两行：
 * 按钮的高度有最小值，两行字也塞得进去，所以量高度发现不了。视觉隐藏的说明在第一段文字之后，不会被数进来。
 */
async function firstTextLines(locator: Locator): Promise<number> {
  return locator.evaluate((element) => {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
      if ((node.textContent ?? "").trim() === "") continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      return new Set([...range.getClientRects()].filter((rect) => rect.width > 0).map((rect) => Math.round(rect.top))).size;
    }
    return 0;
  });
}

test.describe("待认领：主流程", () => {
  test("从导航进入，选中旧版本，选推荐、看预览、认领；提示里的“撤销”把旧版本还回待认领", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto("/");
    await mainNav(page).getByRole("link", { name: "待认领" }).click();
    await expect(heading(page, "待认领")).toBeVisible();
    await page.setViewportSize({ width: 1440, height: 900 });
    await claimList(page).getByRole("link", { name: /^Bach: Cello Suites \(my corrections\)/ }).click();
    await expect(page).toHaveURL(new RegExp(`release=${oldId}`));
    const shown = claimDetailOf(page, "Bach: Cello Suites (my corrections)");
    await expect(shown.getByText(/扫描时没找到它的音乐文件/)).toBeVisible();

    await pickSuggestion(page, "Bach: Cello Suites \\(new rip\\)").check();
    await expect(page).toHaveURL(new RegExp(`target=${suitesId()}`));
    const items = page.getByRole("list", { name: "认领会怎样处理旧版本上的修改" }).getByRole("listitem");
    await expect(items).toHaveCount(5);
    await expect(items.nth(0)).toContainText("迁移修正 · 标题");
    await expect(items.nth(2)).toContainText("迁移所在的专辑这个版本会归入旧版本原来的专辑《Bach: Cello Suites Collection》");
    await expect(items.nth(3)).toContainText("迁移默认版本这个版本接任《Bach: Cello Suites Collection》的默认版本");
    await expect(items.nth(4)).toContainText("这个版本有同样的问题，核对会直接生效");

    await claimButton(page, "Bach: Cello Suites (new rip)").click();
    // 提示写的是发起命令时的标题：认领之前新版本还叫 (new rip)。
    await expect(page.getByText(/已把《Bach: Cello Suites \(my corrections\)》上的修改认领到《Bach: Cello Suites \(new rip\)》/)).toBeVisible();
    const [call] = claimCalls(mock, suitesId());
    expect(call?.body).toEqual({ expected_revision: expect.any(Number), source_release_id: oldId, source_revision: 2, preview: expect.stringMatching(/^[0-9a-f]{64}$/) });
    expect(call?.idempotencyKey).toMatch(uuidPattern);
    await expect(claimList(page).getByRole("link", { name: /^Bach: Cello Suites \(my corrections\)/ })).toHaveCount(0);
    await expect(page.getByText("这个版本已经不需要认领了。")).toBeVisible();
    await expect(page.getByRole("link", { name: "打开这张专辑" })).toHaveAttribute("href", /\/albums\/00000000-0000-4000-8000-0000000000f7\?release=/);

    await page.getByRole("button", { name: "撤销", exact: true }).click();
    await expect(page.getByText("已撤销这次认领，修改还给了旧版本。")).toBeVisible();
    await expect(claimList(page).getByRole("link", { name: /^Bach: Cello Suites \(my corrections\)/ })).toBeVisible();
    expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  test("预览之后别人改了新版本：409 说明“两个版本刚被改动过”，预览重新读取，按新的预览可以再认领", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(claimsPath(oldId, suitesId()));
    const button = claimButton(page, "Bach: Cello Suites (new rip)");
    await expect(button).toBeVisible();
    const state = mock.governance.releases.get(suitesId());
    if (state === undefined) throw new Error("新版本还没有修正状态");
    state.revision += 1;
    const previews = mock.callsTo("GET", /claim-preview$/).length;
    await button.click();
    await expect(page.getByText("两个版本刚被改动过")).toBeVisible();
    await expect.poll(() => mock.callsTo("GET", /claim-preview$/).length).toBeGreaterThan(previews);
    await claimButton(page, "Bach: Cello Suites (new rip)").click();
    await expect(page.getByText(/已把《Bach: Cello Suites \(my corrections\)》上的修改认领到/)).toBeVisible();
  });

  test("搜索其他版本：单版本的专辑选中即选中了版本；多版本的专辑再选其中一个，别的目录里的版本也能认领", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(claimsPath(claimReleaseId(3)));
    const search = page.getByRole("search", { name: "搜索其他版本" });
    await search.getByRole("textbox", { name: /搜索其他版本/ }).fill("Delta");
    await search.getByRole("button", { name: "搜索" }).click();
    await page.getByRole("group", { name: "选择专辑" }).getByRole("radio", { name: /Delta Concertos/ }).check();
    const versions = page.getByRole("group", { name: "选择《Delta Concertos》的版本" });
    await expect(versions.getByRole("radio")).toHaveCount(2);
    await versions.getByRole("radio").nth(1).check();
    await expect(page.getByRole("heading", { level: 3, name: /^认领到《.+》会怎样$/ })).toBeVisible();
    await page.getByRole("button", { name: /^认领到《/ }).click();
    await expect(page.getByText(/已把《Mozart: Serenades》上的修改认领到/)).toBeVisible();
    expect(claimCalls(mock, concertos().representative.id).length + claimCalls(mock, releaseVersions(concertos(), 2)[1]?.id ?? "").length).toBe(1);
  });

  test("专辑详情的“可能接替了已不可见的《…》”点进去就是认领：旧版本选中、这个版本已预选", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(`/albums/${suites().id}`);
    const hint = page.getByRole("list", { name: "可能接替了已不可见的版本" });
    await expect(hint.getByText("可能接替了已不可见的《Bach: Cello Suites (my corrections)》")).toBeVisible();
    await hint.getByRole("link", { name: /查看并认领/ }).click();
    await expect(page).toHaveURL(new RegExp(`/claims\\?release=${oldId}&target=${suitesId()}`));
    await expect(claimButton(page, "Bach: Cello Suites (new rip)")).toBeVisible();
    await expect(pickSuggestion(page, "Bach: Cello Suites \\(new rip\\)")).toBeChecked();
  });

  test("操作记录里的认领与撤销：写明来源、迁移与保留；认领不能再撤销时写“之后又有修改”", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(claimsPath(oldId, suitesId()));
    await claimButton(page, "Bach: Cello Suites (new rip)").click();
    await expect(page.getByText(/已把《Bach: Cello Suites \(my corrections\)》上的修改认领到/)).toBeVisible();
    await page.goto("/operations");
    const row = page.getByRole("row").nth(1);
    await expect(row).toContainText("认领");
    await expect(row).toContainText("从《Bach: Cello Suites (my corrections)》（已不可见）认领：修正 标题、流派；专辑；默认版本；核对 介质类型");
    await row.getByRole("button", { name: /撤销/ }).click();
    await page.getByRole("dialog", { name: "撤销这次认领？" }).getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText("已撤销这次认领，修改还给了旧版本。")).toBeVisible();
    await expect(page.getByRole("row").nth(1)).toContainText("撤销认领");
    await expect(page.getByRole("row").nth(2)).toContainText("已撤销");
  });

  test("普通成员没有入口；直接访问显示“需要管理员权限”，不请求待认领的接口，专辑详情也没有提示", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    start(mock);
    await page.goto("/");
    await expect(mainNav(page).getByRole("link", { name: "待认领" })).toHaveCount(0);
    await page.goto(claimsPath());
    await expect(heading(page, "需要管理员权限")).toBeVisible();
    expect(mock.callsTo("GET", "/api/v1/release-claims")).toHaveLength(0);
    await page.goto(`/albums/${suites().id}`);
    await expect(page.getByRole("heading", { level: 1, name: "Bach: Cello Suites (new rip)" })).toBeVisible();
    await expect(page.getByRole("list", { name: "可能接替了已不可见的版本" })).toHaveCount(0);
    expect(mock.callsTo("GET", /claim-hints$/)).toHaveLength(0);
  });
});

test.describe("待认领：布局与可访问性", () => {
  test("五种宽度（长内容）：宽屏两栏、窄屏先列表后详情，没有横向溢出，字号不小于 12px，通过 axe；预览与认领结果也要通过", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    startLong(mock);
    const longId = claimReleaseId(4);
    for (const width of widths) {
      await page.setViewportSize({ width, height: 800 });
      await page.goto(claimsPath(longId, longAlbum().representative.id));
      await page.evaluate(() => document.fonts.ready);
      await expect(page.getByRole("heading", { level: 3, name: /^认领到《.+》会怎样$/ })).toBeVisible();
      const columns = await page.locator("[data-layout]").getAttribute("data-layout");
      // 带侧栏时 1024px 视口里内容区不到 900px：顺序导航；1280 与 1440 是两栏。
      expect(columns, `布局 @${width}px`).toBe(width >= 1280 ? "columns" : "sequential");
      await expectNoHorizontalOverflow(page, `待认领详情与预览 @${width}px`);
      await expectMinimumFontSize(page, `待认领详情与预览 @${width}px`);
      await expectNoSeriousViolations(page, `待认领详情与预览 @${width}px`);
      if (width >= 768) await expectNoDocumentScroll(page, `待认领详情与预览 @${width}px`);
      if (columns === "sequential") {
        await expect(page.getByRole("heading", { level: 2, name: "留有修改的旧版本" })).toHaveCount(0);
        await page.getByRole("link", { name: "返回待认领列表" }).click();
        await expect(page.getByRole("heading", { level: 2, name: "留有修改的旧版本" })).toBeVisible();
        await expectNoHorizontalOverflow(page, `待认领列表 @${width}px`);
        await expectNoSeriousViolations(page, `待认领列表 @${width}px`);
      } else {
        // 两栏：列表 380px，详情最宽 860px。
        const listBox = await claimList(page).boundingBox();
        expect(Math.round(listBox?.width ?? 0), `列表栏宽度 @${width}px`).toBe(380);
        const detailBox = await page.getByRole("article").boundingBox();
        expect(detailBox?.width ?? 0, `详情宽度 @${width}px`).toBeLessThanOrEqual(860);
      }
    }
  });

  test("认领成功与失败的提示通过 axe，窄屏也不溢出", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    startLong(mock);
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto(claimsPath(claimReleaseId(4), longAlbum().representative.id));
    // 预览出现之后，新版本才有修正状态（受控后端按需建立）；这时别人改了它：版本号过期。
    await expect(page.getByRole("button", { name: /^认领到《/ })).toBeVisible();
    const state = mock.governance.releases.get(longAlbum().representative.id);
    if (state === undefined) throw new Error("新版本还没有修正状态");
    state.revision += 1;
    await page.getByRole("button", { name: /^认领到《/ }).click();
    await expect(page.getByText("两个版本刚被改动过")).toBeVisible();
    await expectNoHorizontalOverflow(page, "认领被拒绝的提示 @375px");
    await expectNoSeriousViolations(page, "认领被拒绝的提示 @375px");
    await page.getByRole("button", { name: /^认领到《/ }).click();
    await expect(page.getByText(/已把《.+》上的修改认领到/)).toBeVisible();
    await expectNoHorizontalOverflow(page, "认领成功的提示 @375px");
    await expectMinimumFontSize(page, "认领成功的提示 @375px");
    await expectNoSeriousViolations(page, "认领成功的提示 @375px");
  });

  test("操作记录里认领的摘要很长时，操作列的“撤销”与不能撤销的原因不在字中间断开，时间列的日期也不断开", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(claimsPath(oldId, suitesId()));
    await claimButton(page, "Bach: Cello Suites (new rip)").click();
    await expect(page.getByText(/已把《Bach: Cello Suites \(my corrections\)》上的修改认领到/)).toBeVisible();
    // 改动列写着长长的摘要时，表格会把别的列挤到最窄：按钮若可以在字中间断开，就成了“撤 / 销”两行。
    for (const width of [1024, 1280, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto("/operations");
      const revert = page.getByRole("row").nth(1).getByRole("button", { name: /撤销/ });
      await expect(revert).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      expect(await firstTextLines(revert), `“撤销”按钮的文字行数 @${width}px`).toBe(1);
    }
    await page.getByRole("row").nth(1).getByRole("button", { name: /撤销/ }).click();
    await page.getByRole("dialog", { name: "撤销这次认领？" }).getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText("已撤销这次认领，修改还给了旧版本。")).toBeVisible();
    for (const width of [1024, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto("/operations");
      const reason = page.getByRole("cell", { name: "撤销不能再撤销" });
      await expect(reason).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      expect(await firstTextLines(reason), `“撤销不能再撤销”的行数 @${width}px`).toBe(1);
      expect(await firstTextLines(page.getByRole("cell", { name: "已撤销" })), `“已撤销”的行数 @${width}px`).toBe(1);
      // 操作列不再被挤窄之后，被挤的是别的列：时间的日期（2026-01-03）不能在中间断开，最多在日期与时间之间换行。
      for (const row of [1, 2]) {
        const time = page.getByRole("row").nth(row).getByRole("rowheader");
        expect(await leadingTextLines(time, "2026-01-03".length), `第 ${row} 行日期的行数 @${width}px`).toBe(1);
      }
    }
  });

  test("搜索其他版本：各种宽度下按钮都与输入框顶端对齐，不跑到输入框的上方", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    // 输入框上面有一行标签：按钮没有对齐输入框时，会停在标签那一行，比输入框高出约一行字。
    for (const width of [320, 375, 600, 767, 768, 900, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(claimsPath(oldId));
      const search = page.getByRole("search", { name: "搜索其他版本" });
      await expect(search).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const input = await search.getByRole("textbox").boundingBox();
      const button = await search.getByRole("button", { name: "搜索" }).boundingBox();
      if (input === null || button === null) throw new Error(`没有量到搜索行 @${width}px`);
      expect(Math.abs(button.y - input.y), `按钮与输入框顶端对齐 @${width}px（输入框 y=${Math.round(input.y)}，按钮 y=${Math.round(button.y)}）`).toBeLessThanOrEqual(3);
      await expectNoHorizontalOverflow(page, `搜索行 @${width}px`);
    }
  });

  test("专辑详情的“可能接替了……”提示：图标跟着文字的第一行，窄屏换行时不单独占一行", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    for (const width of [375, 600, 900, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`/albums/${suites().id}`);
      const hint = page.getByRole("list", { name: "可能接替了已不可见的版本" }).getByRole("listitem");
      await expect(hint).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const tops = await hint.evaluate((item) => ({ icon: item.querySelector("svg")?.getBoundingClientRect().top ?? -1, text: item.querySelector("span")?.getBoundingClientRect().top ?? -1 }));
      expect(Math.abs(tops.icon - tops.text), `图标与文字第一行的顶端相差 @${width}px`).toBeLessThan(8);
      await expect(hint.getByRole("link", { name: /查看并认领/ })).toBeVisible();
      await expectNoHorizontalOverflow(page, `专辑详情的提示 @${width}px`);
    }
  });

  test("只用键盘：选旧版本、选推荐（方向键）、认领、撤销，焦点环可见，认领之后焦点不丢", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(claimsPath());
    await expect(claimList(page).getByRole("link", { name: /^Bach: Cello Suites \(my corrections\)/ })).toBeVisible();
    await tabTo(page, (name) => name.startsWith("Bach: Cello Suites (my corrections)"), 20);
    await expectVisibleFocusRing(page, "待认领列表的版本");
    await page.keyboard.press("Enter");
    await expect(claimDetailOf(page, "Bach: Cello Suites (my corrections)")).toBeVisible();
    // 推荐是单选组：Tab 进入组（落在第一项），空格选中；方向键在组内移动并选中。
    await tabTo(page, (name) => name.startsWith("Bach: Cello Suites (new rip)"), 30);
    await expectVisibleFocusRing(page, "推荐的版本");
    await page.keyboard.press("Space");
    await expect(page).toHaveURL(new RegExp(`target=${suitesId()}`));
    await page.keyboard.press("ArrowDown");
    await expect(page).toHaveURL(new RegExp(`target=${quartets().representative.id}`));
    await page.keyboard.press("ArrowUp");
    await expect(page).toHaveURL(new RegExp(`target=${suitesId()}`));
    await tabTo(page, (name) => name === "认领到《Bach: Cello Suites (new rip)》", 30);
    await expectVisibleFocusRing(page, "认领按钮");
    await page.keyboard.press("Enter");
    // 提交按钮随预览一起被换掉：焦点落在结果提示上，不回到页面顶部。
    const notice = page.getByText(/已把《Bach: Cello Suites \(my corrections\)》上的修改认领到/).locator("xpath=ancestor::*[@tabindex='-1'][1]");
    await expect(notice).toBeFocused();
    await tabTo(page, (name) => name === "撤销", 10);
    await expectVisibleFocusRing(page, "撤销");
    await page.keyboard.press("Enter");
    await expect(page.getByText("已撤销这次认领，修改还给了旧版本。")).toBeVisible();
    expect(await focusedName(page)).not.toBe("BODY");
    expect(claimCalls(mock, suitesId())).toHaveLength(1);
    expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(1);
  });
});
