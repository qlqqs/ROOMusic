import type { Page } from "@playwright/test";
import { sampleBody } from "../src/test/api-samples";
import { albumGroups } from "../src/test/catalog-data";
import type { DiagnosticsPageDTO } from "../src/features/library/api/decoders";
import { LONG_TITLE, longContentGroup, multiVersionGroup, seedLongContent, seedVersions, singleVersionGroup } from "./support/albums-data";
import type { ApiMock, MockOptions } from "./support/api-mock";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

// 每个页面在五种宽度下都不能出现横向滚动（页面与主内容区都检查）。
// 页面按 URL 到达，状态由受控后端预置；扫描详情用一个带多类诊断的失败扫描，让表格与汇总都有内容。

type Case = {
  name: string;
  options: MockOptions;
  path: (mock: ApiMock) => string;
  title: string;
  prepare?: (mock: ApiMock) => void;
  /** 到达页面之后、逐个宽度检查之前的操作，例如展开证据。 */
  after?: (page: Page) => Promise<void>;
};

/** 长内容的列表与详情：A7（超过 1000 字的标题、超长艺人名、无空格长串、中日英混排、十二张介质）与 A8（字体回退）都用它们。 */
const longList: Case = {
  name: "专辑列表（长内容）",
  options: { signedInAs: "listener" },
  prepare: (mock) => {
    const long = longContentGroup();
    seedLongContent(mock, long);
    mock.groups = [long, ...albumGroups(11, () => ({ artwork: true }))];
  },
  path: () => "/albums",
  title: "专辑",
};

const longDetail: Case = {
  name: "专辑详情（长内容）",
  options: { signedInAs: "listener" },
  prepare: (mock) => {
    const long = longContentGroup();
    seedLongContent(mock, long);
    mock.groups = [long];
  },
  path: () => `/albums/${longContentGroup().id}`,
  title: LONG_TITLE,
};

/** 展开管理员的完整证据：证据表格与归组依据在页面的最下面，也是文档被撑高问题最容易出现的地方。 */
const expandEvidence = async (page: Page) => {
  await page.locator("summary").click();
  await page.getByRole("button", { name: "查看完整证据" }).click();
  await expect(page.getByRole("table", { name: "字段证据" })).toBeVisible();
};

const detailCases: Case[] = [
  {
    name: "专辑详情（单版本）",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      mock.groups = [singleVersionGroup()];
    },
    path: () => `/albums/${singleVersionGroup().id}`,
    title: "Sample Symphonies",
  },
  {
    name: "专辑详情（多版本，多介质，管理员展开完整证据）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      const group = multiVersionGroup();
      mock.groups = [group];
      seedVersions(mock, group);
    },
    path: () => `/albums/${multiVersionGroup().id}`,
    title: "Symphony No. 9 in D minor, Op. 125 “Choral”",
    after: expandEvidence,
  },
  {
    name: "专辑详情（所选版本已不可见）",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      const group = multiVersionGroup();
      mock.groups = [group];
      mock.hiddenReleases.add(seedVersions(mock, group)[1]?.id ?? "");
    },
    path: (mock) => `/albums/${multiVersionGroup().id}?release=${[...mock.hiddenReleases][0] ?? ""}`,
    title: "Symphony No. 9 in D minor, Op. 125 “Choral”",
  },
  { name: "专辑不存在", options: { signedInAs: "listener" }, path: () => "/albums/00000000-0000-4000-8000-0000000000aa", title: "专辑不存在" },
  longList,
  { ...longDetail, name: "专辑详情（长内容，管理员展开完整证据）", options: { signedInAs: "curator" }, after: expandEvidence },
  longDetail,
];

/** 概览的“最近播放”：八张卡片，第一张是超长标题与超长艺人名（卡片内换行，不撑宽网格）。 */
const recentPlaysCases: Case[] = [
  {
    name: "概览（最近播放八张，含长内容）",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      const long = longContentGroup();
      mock.groups = [long, ...albumGroups(11, (index) => ({ artwork: index % 2 === 0 }))];
      mock.seedRecentPlays(8);
    },
    path: () => "/",
    title: "概览",
  },
  {
    name: "概览（最近播放，清除确认对话框打开）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      mock.seedRecentPlays(3);
    },
    path: () => "/",
    title: "概览",
    after: async (page) => {
      await page.getByRole("button", { name: "清除播放历史" }).click();
      await expect(page.getByRole("dialog", { name: "清除播放历史？" })).toBeVisible();
    },
  },
  {
    name: "概览（最近播放，播放过的曲目都不在资料库里）",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      mock.hasPlayHistory = true;
    },
    path: () => "/",
    title: "概览",
  },
  {
    name: "概览（最近播放读取失败）",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      mock.forbid.add("GET /api/v1/me/plays/recent-releases");
    },
    path: () => "/",
    title: "概览",
  },
];

const cases: Case[] = [
  { name: "登录", options: {}, path: () => "/login", title: "登录" },
  { name: "初始化", options: { setupRequired: true }, path: () => "/setup", title: "创建管理员" },
  { name: "概览（管理员）", options: { signedInAs: "curator" }, path: () => "/", title: "概览" },
  { name: "概览（成员）", options: { signedInAs: "listener" }, path: () => "/", title: "概览" },
  ...recentPlaysCases,
  { name: "扫描目录", options: { signedInAs: "curator" }, path: () => "/library", title: "扫描目录" },
  { name: "专辑列表", options: { signedInAs: "curator" }, path: () => "/albums", title: "专辑" },
  {
    name: "专辑列表（多页，需要关注与多版本）",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      mock.groups = albumGroups(100, (index) => ({ artwork: index % 2 === 0, attention_count: index % 7 === 0 ? 1 : 0, release_count: index % 5 === 0 ? 3 : 1 }));
    },
    path: () => "/albums?page=2",
    title: "专辑",
  },
  {
    name: "专辑列表（空库）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      mock.groups = [];
    },
    path: () => "/albums",
    title: "专辑",
  },
  {
    name: "扫描目录（扫描记录有数据，多页）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      mock.seedHistory(25);
    },
    path: () => "/library",
    title: "扫描目录",
  },
  {
    name: "扫描详情（失败，多类诊断）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      const diagnostics = sampleBody("scans/diagnostics-multiple-kinds") as DiagnosticsPageDTO;
      const scan = mock.seedScan({ status: "failed", finished_at: "2026-01-02T00:01:00Z", diagnostics: diagnostics.summary });
      mock.diagnostics.set(scan.id, diagnostics);
    },
    path: (mock) => `/library/scans/${[...mock.scans.keys()][0] ?? ""}`,
    title: "扫描详情",
  },
  ...detailCases,
  { name: "成员", options: { signedInAs: "curator" }, path: () => "/members", title: "成员" },
  { name: "页面不存在", options: { signedInAs: "curator" }, path: () => "/nowhere", title: "页面不存在" },
  { name: "需要管理员权限", options: { signedInAs: "listener" }, path: () => "/library", title: "需要管理员权限" },
  { name: "服务不可用", options: { failSetupStatusTimes: 99 }, path: () => "/", title: "暂时无法使用" },
];

test.describe("五种宽度下没有横向溢出", () => {
  for (const item of cases) {
    test(item.name, async ({ page, backend }) => {
      const mock = await backend.start(item.options);
      item.prepare?.(mock);
      await page.goto(item.path(mock));
      await expect(heading(page, item.title)).toBeVisible();
      await item.after?.(page);
      for (const width of widths) {
        await page.setViewportSize({ width, height: 900 });
        await expect(heading(page, item.title)).toBeVisible();
        await page.evaluate(() => document.fonts.ready);
        await expectNoHorizontalOverflow(page, `${item.name} @${width}px`);
        await expectNoDocumentScroll(page, `${item.name} @${width}px`);
      }
    });
  }
});

// A8：字体全部加载失败（被拦截）时，页面回退到系统衬线字体，长内容的列表与详情仍然没有横向溢出、没有遮挡、文字不小于 12px。
test.describe("字体被拦截时的回退", () => {
  for (const item of [longList, { ...longDetail, options: { signedInAs: "curator" } satisfies MockOptions, after: expandEvidence }]) {
    test(item.name, async ({ page, backend }) => {
      let blocked = 0;
      await page.route("**/*.woff2", async (route) => {
        blocked += 1;
        await route.abort();
      });
      const mock = await backend.start(item.options);
      item.prepare?.(mock);
      await page.goto(item.path(mock));
      await expect(heading(page, item.title)).toBeVisible();
      await item.after?.(page);
      // 拦截确实生效：页面请求过字体，而且没有一个字体加载成功。
      await page.evaluate(() => document.fonts.ready);
      expect(blocked, "页面应该请求过字体文件").toBeGreaterThan(0);
      expect(await page.evaluate(() => Array.from(document.fonts).filter((font) => font.status === "loaded").length), "字体不应加载成功").toBe(0);
      for (const width of widths) {
        await page.setViewportSize({ width, height: 900 });
        await expect(heading(page, item.title)).toBeVisible();
        await expectNoHorizontalOverflow(page, `${item.name}（无字体）@${width}px`);
        await expectNoDocumentScroll(page, `${item.name}（无字体）@${width}px`);
        await expectMinimumFontSize(page, `${item.name}（无字体）@${width}px`);
      }
    });
  }
});

// content-visibility 把屏幕外的介质分段跳过：窗口先宽后窄时，被跳过的分段不能把页面撑宽（它们保留着上一次渲染的宽度）。
test.describe("窗口先宽后窄", () => {
  test("长内容详情：滚到底（上面的分段被跳过）再缩到窄屏，没有横向溢出", async ({ page, backend }) => {
    const mock = await backend.start(longDetail.options);
    longDetail.prepare?.(mock);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(longDetail.path(mock));
    await expect(heading(page, longDetail.title)).toBeVisible();
    await expect(page.getByRole("region", { name: /^第 12 张/ })).toBeAttached();
    for (const width of [1024, 768, 375]) {
      await page.locator("#main").evaluate((main) => main.scrollTo(0, main.scrollHeight));
      await page.setViewportSize({ width, height: 900 });
      await expectNoHorizontalOverflow(page, `先宽后窄 @${width}px`);
    }
  });
});
