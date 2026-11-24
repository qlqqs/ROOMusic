import { openSettingsScene, settingsScenes } from "./support/settings";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { accountLayoutScenes, expectLogoutInViewport, openAccountScene } from "./support/account-password";
import type { Page } from "@playwright/test";
import { sampleBody } from "../src/test/api-samples";
import { albumGroup, albumGroups } from "../src/test/catalog-data";
import { personalTracksOf, PERSONAL_LISTENER_ID, seedFavoriteTracks, seedPersonalTracks, seedPlaylist } from "../src/test/personal-data";
import { respondToCorrect } from "../src/test/governance-data";
import type { DiagnosticsPageDTO } from "../src/features/library/api/decoders";
import { LONG_TITLE, longContentGroup, multiVersionGroup, seedLongContent, seedVersions, singleVersionGroup } from "./support/albums-data";
import type { ApiMock, MockOptions } from "./support/api-mock";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";
import { openQueueScene } from "./support/queue";

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

/** 长内容的列表与详情：A7（超过 1000 字的标题、超长艺人名、无空格长串、中日英混排、十二张介质）都用它们。 */
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
      await page.getByRole("button", { name: "最近播放的更多操作" }).click();
      await page.getByRole("menuitem", { name: "清除播放历史" }).click();
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
  {
    name: "我喜欢的音乐",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      const group = albumGroup(90, { title: "个人页测试专辑", artwork: true, track_count: 2 });
      mock.groups = [group];
      const tracks = personalTracksOf(mock.personal.catalog).slice(0, 2);
      seedPersonalTracks(mock.personal, tracks);
      seedFavoriteTracks(mock.personal, PERSONAL_LISTENER_ID, tracks);
    },
    path: () => "/me/liked",
    title: "我喜欢的音乐",
  },
  {
    name: "歌单详情",
    options: { signedInAs: "listener" },
    prepare: (mock) => {
      const group = albumGroup(91, { title: "个人页测试专辑", artwork: true, track_count: 2 });
      mock.groups = [group];
      const tracks = personalTracksOf(mock.personal.catalog).slice(0, 2);
      seedPersonalTracks(mock.personal, tracks);
      seedPlaylist(mock.personal, PERSONAL_LISTENER_ID, { id: "00000000-0000-4000-8000-000000000091", name: "个人页测试歌单", trackIds: tracks.map((track) => track.track_id) });
    },
    path: () => "/me/playlists/00000000-0000-4000-8000-000000000091",
    title: "个人页测试歌单",
  },
  { name: "登录", options: {}, path: () => "/login", title: "登录" },
  { name: "初始化", options: { setupRequired: true }, path: () => "/setup", title: "创建管理员" },
  { name: "概览（管理员）", options: { signedInAs: "curator" }, path: () => "/", title: "概览" },
  { name: "概览（成员）", options: { signedInAs: "listener" }, path: () => "/", title: "概览" },
  ...recentPlaysCases,
  { name: "音乐目录", options: { signedInAs: "curator" }, path: () => "/settings/library", title: "音乐目录" },
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
    name: "音乐目录（扫描记录有数据，多页）",
    options: { signedInAs: "curator" },
    prepare: (mock) => {
      mock.seedHistory(25);
    },
    path: () => "/settings/library",
    title: "音乐目录",
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

/** 账号页一张卡片里的两行只读值：名称在左、值在右（窄卡片时值换到名称下方）。 */
const accountValues = [["用户名", "curator"], ["角色", "管理员"]] as const;

/** 账号值是正文信息，不应恢复成统计数字；限定 main 并核对两项内容，避免误选侧栏或空列表。 */
async function expectAccountValueFontSize(page: Page): Promise<void> {
  const main = page.getByRole("main");
  const bodyFontSize = await page.locator("body").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
  expect(bodyFontSize, "正文必须有可测量字号").toBeGreaterThan(0);
  for (const [label, text] of accountValues) {
    const name = main.getByText(label, { exact: true });
    const value = main.getByText(text, { exact: true });
    await expect(name).toBeVisible();
    await expect(value).toHaveCount(1);
    await expect(value).toBeVisible();
    // 值在名称这一行：与名称竖直方向重叠（同一行），或紧接在名称下方（窄卡片换行）。
    const [nameBox, valueBox] = await Promise.all([name.boundingBox(), value.boundingBox()]);
    if (nameBox === null || valueBox === null) throw new Error(`账号“${label}”的名称与值必须有可测量边界`);
    expect(valueBox.y, `账号值“${text}”在“${label}”这一行`).toBeLessThanOrEqual(nameBox.y + nameBox.height + 16);
    expect(valueBox.y + valueBox.height, `账号值“${text}”在“${label}”这一行`).toBeGreaterThanOrEqual(nameBox.y);
    const fontSize = await value.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
    expect(fontSize, "账号值必须有可测量字号").toBeGreaterThan(0);
    expect(fontSize, `账号值“${text}”字号不超过正文的 1.2 倍`).toBeLessThanOrEqual(bodyFontSize * 1.2);
  }
}

/**
 * 窄屏“我的”页：标签栏贴在窗口底部，滚到底时最后一行（退出登录）整行在标签栏上方，不被遮住。
 * 量真实位置，不只查 padding 样式。
 */
async function expectMePageClearsTabBar(page: Page): Promise<void> {
  const bar = page.getByRole("navigation", { name: "主导航", exact: true });
  const logout = page.getByRole("main").getByRole("button", { name: "退出登录", exact: true });
  await expect(bar).toBeVisible();
  await page.evaluate(() => {
    for (const element of [document.scrollingElement, document.getElementById("main")]) if (element !== null) element.scrollTop = element.scrollHeight;
  });
  const viewport = page.viewportSize();
  const [barBounds, rowBounds] = await Promise.all([bar.boundingBox(), logout.boundingBox()]);
  if (viewport === null || barBounds === null || rowBounds === null) throw new Error("标签栏与“退出登录”必须有可测量边界");
  expect(Math.abs(barBounds.y + barBounds.height - viewport.height), "标签栏贴在窗口底部").toBeLessThanOrEqual(1);
  expect(rowBounds.y + rowBounds.height, "最后一行在标签栏上方").toBeLessThanOrEqual(barBounds.y + 1);
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
        if (scene === "用户菜单" && width === 375) await expectMePageClearsTabBar(page);
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

test("设置胶囊标签行 @375px：当前标签完整露出，两端按滚动位置渐隐；宽屏竖排不渐隐", async ({ page, backend }) => {
  await page.setViewportSize({ width: 375, height: 1000 });
  await backend.start({ signedInAs: "curator" });
  const nav = page.getByRole("navigation", { name: "设置", exact: true });
  const row = nav.locator(":scope > div").first();
  const fullyInside = async (name: string) => {
    const tab = await nav.getByRole("link", { name, exact: true }).boundingBox();
    const box = await row.boundingBox();
    if (tab === null || box === null) throw new Error("标签行不可见");
    return tab.x >= box.x && tab.x + tab.width <= box.x + box.width;
  };
  await page.goto("/settings/account");
  await expect(heading(page, "账号")).toBeVisible();
  await expect(row).toHaveAttribute("data-fade-end", "");
  await expect(row).not.toHaveAttribute("data-fade-start", "");
  expect(await row.evaluate((element) => getComputedStyle(element).maskImage)).not.toBe("none");
  // 排在末尾的“备份”：换过去后滚到它，完整露出，不只露出一半。
  await nav.getByRole("link", { name: "备份", exact: true }).click();
  await expect(heading(page, "备份")).toBeVisible();
  await expect.poll(() => fullyInside("备份")).toBe(true);
  await expect(row).toHaveAttribute("data-fade-start", "");
  await expect(row).not.toHaveAttribute("data-fade-end", "");

  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(row).not.toHaveAttribute("data-fade-start", "");
  await expect(row).not.toHaveAttribute("data-fade-end", "");
  expect(await row.evaluate((element) => getComputedStyle(element).maskImage)).toBe("none");
});

// 自检只改当前浏览器页面的临时 CSS，不触碰产品文件；确认几何断言确实能拦住截图审阅发现的旧样式。
test.describe("设置样式回归断言自检", () => {
  test("账号值恢复 32px 时，用户名和角色的字号检查分别失败", async ({ page, backend }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await backend.start({ signedInAs: "curator" });
    await openSettingsScene(page, "设置账号");
    await expectAccountValueFontSize(page);
    for (const [, value] of accountValues) {
      // 值的元素是 CSS Modules 的哈希类名，临时打一个属性给样式选中，结束时连同样式一起去掉。
      const target = page.getByRole("main").getByText(value, { exact: true });
      await target.evaluate((element) => element.setAttribute("data-check-value", ""));
      const style = await page.addStyleTag({ content: "main [data-check-value] { font-size: 32px !important; }" });
      try {
        await expect(target).toHaveCSS("font-size", "32px");
        await expect(expectAccountValueFontSize(page)).rejects.toThrow(`账号值“${value}”字号不超过正文的 1.2 倍`);
      } finally {
        await style.evaluate((element) => element.parentNode?.removeChild(element));
        await target.evaluate((element) => element.removeAttribute("data-check-value"));
      }
      await expectAccountValueFontSize(page);
    }
  });

  test("375px “我的”页去掉主内容的底部留白时，标签栏遮挡检查失败", async ({ page, backend }) => {
    // 窗口矮于页面内容，最后一行要滚到底才看得到，去掉留白后才会被标签栏盖住。
    await page.setViewportSize({ width: 375, height: 640 });
    await backend.start({ signedInAs: "curator" });
    await openSettingsScene(page, "用户菜单");
    await expectMePageClearsTabBar(page);
    const style = await page.addStyleTag({ content: "#main { padding-bottom: 0 !important; }" });
    try {
      await expect(expectMePageClearsTabBar(page)).rejects.toThrow("最后一行在标签栏上方");
    } finally {
      await style.evaluate((element) => element.parentNode?.removeChild(element));
    }
    await expectMePageClearsTabBar(page);
  });
});

test.describe("概览的版面顺序", () => {
  /** 各块的位置：继续播放、最近播放、最近新增、资料库卡片与资料库状态。 */
  async function boxes(page: Page) {
    const of = async (name: string) => {
      const box = await page.getByRole("region", { name, exact: true }).boundingBox();
      if (box === null) throw new Error(`量不到“${name}”`);
      return box;
    };
    return { resume: await of("继续播放"), recent: await of("最近播放"), added: await of("最近新增"), stats: await of("资料库"), status: await of("资料库状态") };
  }

  test("1440px：资料库卡片在右栏顶部与继续播放同高起，状态表在卡片下面", async ({ page, backend }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    mock.seedRecentPlays(3);
    mock.recentlyAdded = mock.groups.slice(0, 3).map((group) => ({ ...group, added_at: "2026-01-02T00:00:00Z" }));
    await page.goto("/");
    await expect(page.getByRole("region", { name: "资料库", exact: true }).getByRole("link", { name: /张专辑/ })).toBeVisible();
    await expect(page.getByRole("region", { name: "最近新增", exact: true }).getByRole("list")).toBeVisible();
    const { resume, recent, added, stats, status } = await boxes(page);
    expect(resume.y, "继续播放在最近播放上方").toBeLessThan(recent.y);
    expect(recent.y + recent.height, "最近新增在最近播放下方").toBeLessThanOrEqual(added.y);
    expect(stats.x, "资料库卡片在主栏右侧").toBeGreaterThanOrEqual(recent.x + recent.width);
    expect(Math.abs(stats.y - resume.y), "资料库卡片与继续播放顶端对齐").toBeLessThanOrEqual(1);
    expect(stats.y + stats.height, "状态表在卡片下方").toBeLessThanOrEqual(status.y);
    expect(Math.abs(stats.x - status.x), "卡片与状态表同属右栏").toBeLessThanOrEqual(1);
    expect(Math.abs(stats.width - status.width), "卡片与状态表等宽").toBeLessThanOrEqual(1);
  });

  test("390px：从上到下是继续播放、最近播放、最近新增、资料库卡片、资料库状态", async ({ page, backend }) => {
    await page.setViewportSize({ width: 390, height: 1000 });
    const mock = await backend.start({ signedInAs: "listener" });
    mock.seedRecentPlays(3);
    mock.recentlyAdded = mock.groups.slice(0, 3).map((group) => ({ ...group, added_at: "2026-01-02T00:00:00Z" }));
    await page.goto("/");
    await expect(page.getByRole("region", { name: "资料库", exact: true }).getByRole("link", { name: /张专辑/ })).toBeVisible();
    await expect(page.getByRole("region", { name: "最近新增", exact: true }).getByRole("list")).toBeVisible();
    const { resume, recent, added, stats, status } = await boxes(page);
    expect(resume.y + resume.height).toBeLessThanOrEqual(recent.y);
    expect(recent.y + recent.height).toBeLessThanOrEqual(added.y);
    expect(added.y + added.height).toBeLessThanOrEqual(stats.y);
    expect(stats.y + stats.height).toBeLessThanOrEqual(status.y);
  });
});


for (const width of [390, 768, 1440]) {
  test(`${width}px：队列抽屉三段与展开的已播放列表不撑出面板`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const drawer = await openQueueScene(page, mock);
    await drawer.getByRole("button", { name: "已播放 1 首", exact: true }).click();
    await expect(drawer.getByRole("listitem")).toHaveCount(4);
    await expect(drawer.getByText("4 首 · 4 分钟", { exact: true })).toBeVisible();
    await expectNoHorizontalOverflow(page, "队列抽屉三段");
    await expectNoDocumentScroll(page, "队列抽屉三段");
    expect(await drawer.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(0);
    const box = await drawer.boundingBox();
    if (box === null) throw new Error("队列面板不可见");
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    expect(box.y).toBeGreaterThanOrEqual(width < 768 ? 0 : 48);
    expect(box.y + box.height).toBeLessThanOrEqual(width < 768 ? 900 : 852);
    await expectMinimumFontSize(page, "队列抽屉三段");
  });
}
