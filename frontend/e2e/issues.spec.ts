import type { Page } from "@playwright/test";
import { albumGroup } from "../src/test/catalog-data";
import { markUncertain, scanIssues, seedIssues } from "../src/test/governance-data";
import { LONG_TITLE, LONG_TOKEN } from "./support/albums-data";
import type { ApiMock } from "./support/api-mock";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { tabTo } from "./support/keyboard";
import { heading, mainNav, uuidPattern } from "./support/locators";

// 元数据问题（M4 第二项）：管理员的问题页按版本列出问题，核对、取消核对、撤销与编辑发行信息。受控后端与 Vitest 共用
// src/test/governance-data.ts 的问题与核对行为（依据令牌、过期、各种 409），所以页面能走完整的流程；宽度、axe 与键盘路径在真实浏览器里检查。

const suites = () => albumGroup(1, { title: "Bach: Cello Suites", artist: "Pierre Fournier", year: 1961 });
const quartets = () => albumGroup(2, { title: "Beethoven: String Quartets", artist: "Alban Berg Quartett", year: 1989 });
const longAlbum = () => albumGroup(3, { title: LONG_TITLE, artist: "Long Artist", year: 2001 });
const live = () => albumGroup(4, { title: "Bach: Cello Suites (Live)", artist: "Pierre Fournier", year: 1961 });
const releaseOf = (group: ReturnType<typeof suites>) => group.representative.id;

function start(mock: ApiMock) {
  mock.groups = [suites(), quartets(), longAlbum(), live()];
  seedIssues(mock.governance, releaseOf(suites()), {
    fields: [
      { field: "media_type", value: "CD", reason: "inconsistent_candidates", candidates: ["CD", "Vinyl"] },
      { field: "title", value: "Bach: Cello Suites", reason: "inconsistent_candidates", candidates: ["Bach: Cello Suites", "Cello Suites BWV 1007-1012"] },
    ],
    grouping: { sourceRefs: ["Fournier/Suites/01.flac", "Fournier/Suites/02.flac"], siblings: [{ releaseId: releaseOf(live()), groupId: live().id, title: "Bach: Cello Suites (Live)" }] },
    missing: [{ id: "00000000-0000-4000-8000-0000000000e1", title: "Suite No. 1 – Sarabande", medium: 1, position: 4, source: "Fournier/Suites/04.flac", lastSeenAt: "2026-01-02T08:30:00Z" }],
  });
  seedIssues(mock.governance, releaseOf(quartets()), { fields: [{ field: "title", value: "Beethoven: String Quartets", reason: "field_missing", candidates: [] }] });
  markUncertain(mock.governance, releaseOf(quartets()), ["title"]);
}

/** 长内容：标题超过 1000 字，缺失曲目的路径是没有空格的长串，用来检查各种宽度下的换行。 */
function startLong(mock: ApiMock) {
  start(mock);
  seedIssues(mock.governance, releaseOf(longAlbum()), {
    fields: [{ field: "source_type", value: LONG_TOKEN.slice(0, 120), reason: "inconsistent_candidates", candidates: [LONG_TOKEN.slice(0, 120), "SACD"] }],
    grouping: { sourceRefs: [`${LONG_TOKEN}/01.flac`, `${LONG_TOKEN}/02.flac`] },
    missing: [{ id: "00000000-0000-4000-8000-0000000000e2", title: LONG_TITLE.slice(0, 200), medium: 12, position: 36, source: `${LONG_TOKEN}/36.flac`, lastSeenAt: "2026-01-02T08:30:00Z" }],
  });
}

const issuesPath = (release?: string) => (release === undefined ? "/issues" : `/issues?release=${release}`);
const reviewCalls = (mock: ApiMock, releaseId: string) => mock.callsTo("POST", `/api/v1/releases/${releaseId}/issue-review`);
const detail = (page: Page, title: string) => page.getByRole("article").filter({ has: page.getByRole("heading", { level: 2, name: title }) });
const section = (page: Page, name: string) => page.locator("section").filter({ has: page.getByRole("heading", { level: 3, name }) });
const issueList = (page: Page) => page.locator("section").filter({ has: page.getByRole("heading", { level: 2, name: "有问题的版本" }) });

test.describe("元数据问题：主流程", () => {
  test("从导航进入，选中版本；核对归组冲突后数字与列表一起变化，提示里的“撤销”恢复；取消核对与它的撤销也成立", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto("/");
    await mainNav(page).getByRole("link", { name: "元数据问题" }).click();
    await expect(heading(page, "元数据问题")).toBeVisible();
    await page.setViewportSize({ width: 1440, height: 900 });
    await issueList(page).getByRole("link", { name: /^Bach: Cello Suites Remastered/ }).click();
    await expect(page).toHaveURL(new RegExp(`release=${releaseOf(suites())}`));
    const shown = detail(page, "Bach: Cello Suites");
    await expect(shown.getByText(/需要关注：4/)).toBeVisible();

    await section(page, "同一目录的归组冲突").getByRole("button", { name: "标记为已核对：归组冲突" }).click();
    await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
    const [review] = reviewCalls(mock, releaseOf(suites()));
    expect(review?.body).toEqual({ expected_revision: 1, issue: "grouping", action: "review", basis: expect.stringMatching(/^[0-9a-f]{64}$/) });
    expect(review?.idempotencyKey).toMatch(uuidPattern);
    await expect(shown.getByText(/需要关注：3/)).toBeVisible();
    await expect(section(page, "同一目录的归组冲突").getByText("已核对", { exact: true })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "问题状态" }).getByRole("link", { name: "已核对，1 个版本" })).toBeVisible();

    await page.getByRole("button", { name: "撤销", exact: true }).click();
    await expect(page.getByText("已撤销这次核对，问题恢复到之前的状态。")).toBeVisible();
    await expect(shown.getByText(/需要关注：4/)).toBeVisible();
    await expect(section(page, "同一目录的归组冲突").getByRole("button", { name: "标记为已核对：归组冲突" })).toBeVisible();

    // 缺失曲目：核对、取消核对、撤销取消核对（回到已核对）。
    await section(page, "缺失的曲目（1 首）").getByRole("button", { name: "标记为已核对：缺失曲目" }).click();
    await expect(page.getByText(/已把“缺失曲目”标记为已核对/)).toBeVisible();
    await section(page, "缺失的曲目（1 首）").getByRole("button", { name: "取消核对：缺失曲目" }).click();
    await expect(page.getByText("已取消“缺失曲目”的核对。")).toBeVisible();
    await page.getByRole("button", { name: "撤销", exact: true }).click();
    await expect(page.getByText("已撤销这次核对，问题恢复到之前的状态。")).toBeVisible();
    await expect(section(page, "缺失的曲目（1 首）").getByRole("button", { name: "取消核对：缺失曲目" })).toBeVisible();
    expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(2);
  });

  test("页面打开之后扫描改了问题：409 issue_changed 说明“刚刚变了”并重新读取，按新的情况可以再核对", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(issuesPath(releaseOf(suites())));
    await expect(detail(page, "Bach: Cello Suites")).toBeVisible();
    scanIssues(mock.governance, releaseOf(suites()), { ...mock.governance.issues.get(releaseOf(suites()))?.seed, grouping: { sourceRefs: ["Fournier/Suites/09.flac"] } });
    await section(page, "同一目录的归组冲突").getByRole("button", { name: "标记为已核对：归组冲突" }).click();
    await expect(page.getByText("这个问题刚刚变了")).toBeVisible();
    await expect(section(page, "同一目录的归组冲突").getByText("Fournier/Suites/09.flac")).toBeVisible();
    await section(page, "同一目录的归组冲突").getByRole("button", { name: "标记为已核对：归组冲突" }).click();
    await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
  });

  test("拿不准的标题用“编辑发行信息”确认扫描值：问题消失，版本留在原处说明没有待处理的问题", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(issuesPath(releaseOf(quartets())));
    await section(page, "拿不准的字段").getByRole("button", { name: "编辑发行信息" }).click();
    const dialog = page.getByRole("dialog", { name: /编辑发行信息/ });
    await dialog.getByRole("button", { name: "确认扫描值" }).click();
    await dialog.getByRole("button", { name: "保存修改" }).click();
    await expect(page.getByText(/已保存修改：标题/)).toBeVisible();
    await expect(page.getByText("已经没有问题了。")).toBeVisible();
    await expect(page.getByRole("heading", { level: 2, name: "Beethoven: String Quartets" })).toBeVisible();
    await expect(issueList(page).getByRole("link", { name: /^Beethoven/ })).toHaveCount(0);
  });

  test("专辑详情的“查看问题”打开问题页并选中这个版本", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(`/albums/${suites().id}`);
    await page.getByRole("link", { name: "查看问题" }).click();
    await expect(page).toHaveURL(new RegExp(`/issues\\?release=${releaseOf(suites())}`));
    await expect(detail(page, "Bach: Cello Suites")).toBeVisible();
  });

  test("普通成员没有入口；直接访问显示“需要管理员权限”，不请求问题接口", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    start(mock);
    await page.goto("/");
    await expect(mainNav(page).getByRole("link", { name: "元数据问题" })).toHaveCount(0);
    await page.goto(issuesPath());
    await expect(heading(page, "需要管理员权限")).toBeVisible();
    expect(mock.callsTo("GET", "/api/v1/release-issues")).toHaveLength(0);
    await page.goto(`/albums/${suites().id}`);
    await expect(page.getByRole("heading", { level: 1, name: "Bach: Cello Suites" })).toBeVisible();
    await expect(page.getByRole("link", { name: "查看问题" })).toHaveCount(0);
  });
});

test.describe("元数据问题：布局与可访问性", () => {
  test("五种宽度（长内容）：宽屏两栏、窄屏先列表后详情，没有横向溢出，字号不小于 12px，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    startLong(mock);
    // 一个已核对、一个过期的状态也在页面上：已核对标签与“核对之后情况变了”的说明都要经过 axe。
    await page.goto(issuesPath(releaseOf(longAlbum())));
    await section(page, "同一目录的归组冲突").getByRole("button", { name: "标记为已核对：归组冲突" }).click();
    await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
    await section(page, "拿不准的字段").getByRole("button", { name: "标记为已核对：来源类型" }).click();
    await expect(page.getByText(/已把“来源类型”标记为已核对/)).toBeVisible();
    scanIssues(mock.governance, releaseOf(longAlbum()), {
      ...mock.governance.issues.get(releaseOf(longAlbum()))?.seed,
      fields: [{ field: "source_type", value: LONG_TOKEN.slice(0, 120), reason: "inconsistent_candidates", candidates: [LONG_TOKEN.slice(0, 120), "SACD", "DVD-Audio"] }],
    });
    for (const width of widths) {
      await page.setViewportSize({ width, height: 800 });
      await page.goto(issuesPath(releaseOf(longAlbum())));
      await page.evaluate(() => document.fonts.ready);
      await expect(page.getByText("核对之后各曲目的值变了")).toBeVisible();
      const columns = await page.locator("[data-layout]").getAttribute("data-layout");
      // 带侧栏时 1024px 视口里内容区不到 900px：顺序导航；1280 与 1440 是两栏。
      expect(columns, `布局 @${width}px`).toBe(width >= 1280 ? "columns" : "sequential");
      await expectNoHorizontalOverflow(page, `问题详情 @${width}px`);
      await expectMinimumFontSize(page, `问题详情 @${width}px`);
      await expectNoSeriousViolations(page, `问题详情 @${width}px`);
      if (width >= 768) await expectNoDocumentScroll(page, `问题详情 @${width}px`);
      if (columns === "sequential") {
        await expect(page.getByRole("heading", { level: 2, name: "有问题的版本" })).toHaveCount(0);
        await page.getByRole("link", { name: "返回问题列表" }).click();
        await expect(page.getByRole("heading", { level: 2, name: "有问题的版本" })).toBeVisible();
        await expectNoHorizontalOverflow(page, `问题列表 @${width}px`);
        await expectNoSeriousViolations(page, `问题列表 @${width}px`);
      } else {
        // 两栏：列表 380px，详情最宽 860px。
        const listBox = await issueList(page).boundingBox();
        expect(Math.round(listBox?.width ?? 0), `列表栏宽度 @${width}px`).toBe(380);
        const detailBox = await page.getByRole("article").boundingBox();
        expect(detailBox?.width ?? 0, `详情宽度 @${width}px`).toBeLessThanOrEqual(860);
      }
    }
  });

  test("只用键盘：切换筛选、选中版本、核对、撤销与取消核对，焦点环可见，提交后焦点不丢", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(issuesPath());
    await expect(issueList(page).getByRole("link", { name: /^Bach: Cello Suites Remastered/ })).toBeVisible();
    await tabTo(page, (name) => name === "归组，1 个版本", 40);
    await expectVisibleFocusRing(page, "归组筛选");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/category=grouping/);
    await tabTo(page, (name) => name.startsWith("Bach: Cello Suites Remastered"), 10);
    await expectVisibleFocusRing(page, "问题列表的版本");
    await page.keyboard.press("Enter");
    await expect(detail(page, "Bach: Cello Suites")).toBeVisible();
    await tabTo(page, (name) => name === "标记为已核对：归组冲突", 30);
    await expectVisibleFocusRing(page, "标记为已核对");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
    // 按钮换成“取消核对”：焦点没有掉回页面顶部，而是在这一节的标题上，下一次 Tab 就到“取消核对”。
    await expect(page.getByRole("heading", { level: 3, name: "同一目录的归组冲突" })).toBeFocused();
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => document.activeElement?.getAttribute("aria-label"))).toBe("取消核对：归组冲突");
    await tabTo(page, (name) => name === "撤销", 30);
    await page.keyboard.press("Enter");
    await expect(page.getByText("已撤销这次核对，问题恢复到之前的状态。")).toBeVisible();
    await tabTo(page, (name) => name === "标记为已核对：归组冲突", 30);
    await page.keyboard.press("Enter");
    await expect(page.getByText(/已把“归组冲突”标记为已核对/)).toBeVisible();
    await tabTo(page, (name) => name === "取消核对：归组冲突", 30);
    await expectVisibleFocusRing(page, "取消核对");
    await page.keyboard.press("Enter");
    await expect(page.getByText("已取消“归组冲突”的核对。")).toBeVisible();
    expect(reviewCalls(mock, releaseOf(suites())).map((call) => (call.body as { action: string }).action)).toEqual(["review", "review", "unreview"]);
  });
});
