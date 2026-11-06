import { openSettingsScene, settingsScenes } from "./support/settings";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { accountLayoutScenes, expectLogoutInViewport, openAccountScene } from "./support/account-password";
import type { Page } from "@playwright/test";
import { sampleBody } from "../src/test/api-samples";
import { albumGroups } from "../src/test/catalog-data";
import { respondToCorrect } from "../src/test/governance-data";
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
  await page.locator("summary", { hasText: "来源与证据" }).click();
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

/** 在受控后端上预置操作记录：count 条厂牌修正（预期版本依次递增），最后一条的值是超长的无空格串，检查表格与对话框里的换行。 */
function seedOperations(mock: ApiMock, releaseId: string, count: number): void {
  for (let index = 0; index < count; index += 1) {
    const value = index === count - 1 ? `${"超长的厂牌名称".repeat(8)}${"B".repeat(60)}` : `Label ${index + 1}`;
    const key = `00000000-0000-4000-8000-${String(0xd000 + index).padStart(12, "0")}`;
    respondToCorrect(mock.governance, releaseId, key, { expected_revision: index + 1, changes: { label: { mode: "set", value } } });
  }
}

/** 操作记录页（管理员）与专辑详情里展开的“修改记录”：表格在窄屏堆叠成逐条卡片，最后一条带超长的改动内容。 */
const operationCases: Case[] = [
  {
    name: "操作记录（多页，含超长改动内容）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      mock.groups = [singleVersionGroup()];
      seedOperations(mock, singleVersionGroup().representative.id, 25);
    },
    path: () => "/settings/operations",
    title: "操作记录",
  },
  { name: "操作记录（没有记录）", options: { signedInAs: "curator" }, path: () => "/settings/operations", title: "操作记录" },
  {
    name: "专辑详情（管理员展开修改记录，含超长改动内容）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      mock.groups = [singleVersionGroup()];
      seedOperations(mock, singleVersionGroup().representative.id, 7);
    },
    path: () => `/albums/${singleVersionGroup().id}`,
    title: "Sample Symphonies",
    after: async (page) => {
      await page.locator("summary", { hasText: "修改记录" }).click();
      await expect(page.getByRole("table", { name: "修改记录" })).toBeVisible();
    },
  },
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
  { name: "扫描目录", options: { signedInAs: "curator" }, path: () => "/settings/library", title: "扫描目录" },
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
    path: () => "/settings/library",
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
    path: (mock) => `/settings/library/scans/${[...mock.scans.keys()][0] ?? ""}`,
    title: "扫描详情",
  },
  ...detailCases,
  ...operationCases,
  { name: "成员", options: { signedInAs: "curator" }, path: () => "/settings/members", title: "成员" },
  { name: "页面不存在", options: { signedInAs: "curator" }, path: () => "/nowhere", title: "页面不存在" },
  { name: "需要管理员权限", options: { signedInAs: "listener" }, path: () => "/settings/library", title: "需要管理员权限" },
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

test.describe("账户密码：五宽布局", () => {
  for (const scene of accountLayoutScenes) {
    for (const width of widths) {
      test(`${scene} @${width}px`, async ({ page, backend }, testInfo) => {
        await page.setViewportSize({ width, height: 900 });
        const mock = await backend.start({ signedInAs: "curator" });
        await openAccountScene(page, mock, scene);
        await page.evaluate(() => document.fonts.ready);
        const label = `${scene} @${width}px`;
        await expectNoHorizontalOverflow(page, label);
        await expectNoDocumentScroll(page, label);
        await expectMinimumFontSize(page, label);
        // 页面度量看不到 top-layer 对话框内部的溢出，两层模态各自检查。
        for (const dialog of await page.locator("dialog[open]").all()) {
          expect(await dialog.evaluate((node) => node.scrollWidth - node.clientWidth), `${label}：对话框内部无横向滚动`).toBeLessThanOrEqual(0);
          const bounds = await dialog.boundingBox();
          expect(bounds, `${label}：对话框有可见边界`).not.toBeNull();
          expect(bounds?.x ?? -1).toBeGreaterThanOrEqual(0);
          expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(width);
        }
        if (width === 1440 && (scene === "账号栏" || scene === "成功关闭后账号栏")) await expectLogoutInViewport(page);
        if (width === 375 || width === 1440) {
          // 截图放在独立目录；auth/axe 使用其他 output，不能清掉供主会话复核的图片。
          const directory = resolve(import.meta.dirname, "../test-results/account-screenshots");
          await mkdir(directory, { recursive: true });
          const path = resolve(directory, `${scene}-${width}.png`);
          await page.screenshot({ path, fullPage: true });
          await testInfo.attach(`${scene}-${width}`, { path, contentType: "image/png" });
        }
      });
    }
  }
});

/** 账号值是正文信息，不应恢复成统计数字；限定 main 并核对两项内容，避免误选侧栏或空列表。 */
async function expectAccountValueFontSize(page: Page): Promise<void> {
  const main = page.getByRole("main");
  const values = main.getByRole("definition");
  await expect(main.getByRole("term")).toHaveText(["用户名", "角色"]);
  await expect(values).toHaveCount(2);
  await expect(values).toHaveText(["curator", "管理员"]);
  const bodyFontSize = await page.locator("body").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
  expect(bodyFontSize, "正文必须有可测量字号").toBeGreaterThan(0);
  for (const value of await values.all()) {
    await expect(value).toBeVisible();
    const fontSize = await value.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
    expect(fontSize, "账号值必须有可测量字号").toBeGreaterThan(0);
    expect(fontSize, `账号值“${await value.innerText()}”字号不超过正文的 1.2 倍`).toBeLessThanOrEqual(bodyFontSize * 1.2);
  }
}

/** 只验证不需要滚动的抽屉，不通过滚动或只查 margin-top 样式来伪造贴底效果。 */
async function expectDrawerUserAtBottom(page: Page): Promise<void> {
  const dialog = page.getByRole("dialog", { name: "菜单", exact: true });
  await expect(dialog).toBeVisible();
  const { scrollHeight, clientHeight } = await dialog.evaluate((element) => ({ scrollHeight: element.scrollHeight, clientHeight: element.clientHeight }));
  expect(clientHeight, "菜单抽屉有可测量高度").toBeGreaterThan(0);
  expect(scrollHeight, "贴底回归的前提：菜单抽屉无需纵向滚动").toBeLessThanOrEqual(clientHeight);
  const user = dialog.getByRole("region", { name: "当前用户", exact: true }).getByRole("button", { name: "curator 管理员", exact: true });
  await expect(user).toBeVisible();
  const dialogBounds = await dialog.boundingBox();
  const userBounds = await user.boundingBox();
  if (dialogBounds === null || userBounds === null) throw new Error("菜单抽屉与用户按钮必须有可测量边界");
  expect(userBounds.height, "用户按钮有可测量高度").toBeGreaterThan(0);
  const bottomGap = dialogBounds.y + dialogBounds.height - userBounds.y - userBounds.height;
  expect(bottomGap, "用户按钮底边不能超出菜单抽屉").toBeGreaterThanOrEqual(0);
  expect(bottomGap, "用户按钮距菜单抽屉底边不超过 24px").toBeLessThanOrEqual(24);
}

test.describe("设置与用户菜单：布局和截图", () => {
  for (const width of widths) {
    for (const scene of settingsScenes) {
      test(`${scene} @${width}px`, async ({ page, backend }, testInfo) => {
        await page.setViewportSize({ width, height: 1000 });
        await backend.start({ signedInAs: "curator" });
        await openSettingsScene(page, scene);
        await page.evaluate(() => document.fonts.ready);
        const label = `${scene} @${width}px`;
        await expectNoHorizontalOverflow(page, label);
        await expectNoDocumentScroll(page, label);
        await expectMinimumFontSize(page, label);
        if (scene === "设置账号") await expectAccountValueFontSize(page);
        if (scene === "用户菜单" && width === 375) await expectDrawerUserAtBottom(page);
        for (const overlay of await page.locator('dialog[open], [role="menu"]').all()) {
          const bounds = await overlay.boundingBox();
          expect(bounds, `${label}：浮层有边界`).not.toBeNull();
          if (bounds === null) throw new Error("浮层不可见");
          expect(bounds.x).toBeGreaterThanOrEqual(0);
          expect(bounds.y).toBeGreaterThanOrEqual(0);
          expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
          expect(bounds.y + bounds.height).toBeLessThanOrEqual(1000);
          expect(await overlay.evaluate((element) => element.scrollWidth - element.clientWidth), `${label}：浮层内部无横向溢出`).toBeLessThanOrEqual(0);
        }
        if (width === 375 || width === 1440) {
          const directory = resolve(import.meta.dirname, "../test-results/settings-screenshots");
          await mkdir(directory, { recursive: true });
          const path = resolve(directory, `${scene}-${width}.png`);
          await page.screenshot({ path, fullPage: true });
          await testInfo.attach(`${scene}-${width}`, { path, contentType: "image/png" });
        }
      });
    }
  }
});

// 自检只改当前浏览器页面的临时 CSS，不触碰产品文件；确认几何断言确实能拦住截图审阅发现的旧样式。
test.describe("设置样式回归断言自检", () => {
  test("账号值恢复 32px 时，用户名和角色的字号检查分别失败", async ({ page, backend }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await backend.start({ signedInAs: "curator" });
    await openSettingsScene(page, "设置账号");
    await expectAccountValueFontSize(page);
    for (const [index, value] of ["curator", "管理员"].entries()) {
      const style = await page.addStyleTag({ content: `main dl > div:nth-child(${index + 1}) > dd { font-size: 32px !important; }` });
      try {
        await expect(page.getByRole("main").getByRole("definition").nth(index)).toHaveCSS("font-size", "32px");
        await expect(expectAccountValueFontSize(page)).rejects.toThrow(`账号值“${value}”字号不超过正文的 1.2 倍`);
      } finally {
        await style.evaluate((element) => element.parentNode?.removeChild(element));
      }
      await expectAccountValueFontSize(page);
    }
  });

  test("375px 用户菜单取消自动上边距时，展开和折叠状态的贴底检查都失败", async ({ page, backend }) => {
    await page.setViewportSize({ width: 375, height: 1000 });
    await backend.start({ signedInAs: "curator" });
    await openSettingsScene(page, "用户菜单");
    const menu = page.getByRole("menu", { name: "账号", exact: true });
    for (const expanded of [true, false]) {
      if (!expanded) await page.keyboard.press("Escape");
      if (expanded) await expect(menu).toBeVisible();
      else await expect(menu).toBeHidden();
      await expectDrawerUserAtBottom(page);
      const style = await page.addStyleTag({ content: 'dialog[open] div:has(> section[aria-label="当前用户"]) { margin-top: 0 !important; }' });
      try {
        await expect(expectDrawerUserAtBottom(page)).rejects.toThrow("用户按钮距菜单抽屉底边不超过 24px");
      } finally {
        await style.evaluate((element) => element.parentNode?.removeChild(element));
      }
      await expectDrawerUserAtBottom(page);
    }
  });
});
