import AxeBuilder from "@axe-core/playwright";
import type { Page, TestInfo } from "@playwright/test";
import { expectNoDocumentScroll, expectNoHorizontalOverflow, expectVisibleFocusRing } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { tabTo } from "./support/keyboard";
import { feedbackWidths, longAlbumTitle, longRootName, seedScanFeedback, setScanFeedback } from "./support/scan-feedback-data";

const progress = (page: Page) => page.getByRole("progressbar", { name: "扫描进度" });
const report = (page: Page) => page.getByRole("region", { name: "扫描报告" });

/**
 * 最近新增是一行横排（第 15 条）：接口给的卡片全部渲染、顺序不变，所有卡片在同一行（同一个 top）；
 * 放不下时在横排内部横向滚动（页面本身的横向溢出由 verifyLayout 检查），标题行有两个翻页按钮。
 */
async function expectRecentShelf(page: Page, supplied: readonly { id: string }[]) {
  const recent = page.getByRole("region", { name: "最近新增" });
  const expectedHrefs = supplied.map(({ id }) => `/albums/${id}`);
  const cards = recent.getByRole("listitem").getByRole("link");
  await expect(cards).toHaveCount(supplied.length);
  expect(await cards.evaluateAll((links) => links.map((link) => link.getAttribute("href")))).toEqual(expectedHrefs);
  await expect.poll(() => recent.getByRole("listitem").evaluateAll((items) => new Set(items.map((item) => item.getBoundingClientRect().top)).size), {
    message: "最近新增的卡片都在同一行",
  }).toBe(1);
  const list = recent.getByRole("list");
  expect(await list.evaluate((element) => element.scrollWidth > element.clientWidth), "八张卡片放不下，横排内部可以横向滚动").toBe(true);
  await expect(recent.getByRole("button", { name: "最近新增：向前" })).toBeVisible();
  await expect(recent.getByRole("button", { name: "最近新增：向后" })).toBeVisible();
  return expectedHrefs;
}

/** Tab 顺序：“全部专辑”、两个翻页按钮，然后依次是每张卡片链接与播放键；最后一个播放键之后离开这一块。 */
async function expectRecentTabOrder(page: Page, expectedHrefs: readonly string[]) {
  const recent = page.getByRole("region", { name: "最近新增" });
  await tabTo(page, (name) => name === "全部专辑", 40);
  await expect(recent.getByRole("link", { name: "全部专辑" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(recent.getByRole("button", { name: "最近新增：向前" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(recent.getByRole("button", { name: "最近新增：向后" })).toBeFocused();
  const focusedHrefs: (string | null)[] = [];
  for (const href of expectedHrefs) {
    await page.keyboard.press("Tab");
    const link = recent.locator(`a[href="${href}"]`);
    await expect(link).toBeFocused();
    focusedHrefs.push(await page.evaluate(() => document.activeElement?.getAttribute("href") ?? null));
    await page.keyboard.press("Tab");
    await expect(recent.getByRole("listitem").filter({ has: page.locator(`a[href="${href}"]`) }).getByRole("button", { name: /^播放《/ })).toBeFocused();
  }
  expect(focusedHrefs, "Tab 顺序依次经过每张专辑卡片").toEqual(expectedHrefs);
  // 聚焦到横排里看不见的卡片时，浏览器把它滚进横排的可视范围。
  const last = recent.getByRole("listitem").last();
  const lastBox = await last.boundingBox();
  const listBox = await recent.getByRole("list").boundingBox();
  if (lastBox === null || listBox === null) throw new Error("量不到最近新增的位置");
  expect(lastBox.x + lastBox.width, "最后一张卡片聚焦后在横排的可视范围内").toBeLessThanOrEqual(listBox.x + listBox.width + 1);
  await page.keyboard.press("Tab");
  expect(await recent.evaluate((element) => element.contains(document.activeElement)), "最后一张之后立即离开最近新增").toBe(false);
  await page.keyboard.press("Shift+Tab");
  await expect(last.getByRole("button", { name: /^播放《/ })).toBeFocused();
}

async function verifyLayout(page: Page, label: string) {
  await expectNoHorizontalOverflow(page, label);
  await expectNoDocumentScroll(page, label);
  for (const table of await page.getByRole("table").all()) {
    expect(await table.evaluate((element) => element.scrollWidth - element.clientWidth), `${label}：表格自身不溢出`).toBeLessThanOrEqual(0);
  }
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(violations.map(({ id, nodes }) => ({ id, targets: nodes.map(({ target }) => target) })), `${label}：axe 无违规`).toEqual([]);
}

async function capture(page: Page, info: TestInfo, name: string) {
  const path = info.outputPath(`${name}.png`);
  await page.screenshot({ path, fullPage: true });
  await info.attach(name, { path, contentType: "image/png" });
}

for (const width of feedbackWidths) {
  test(`${width}px：计数、读取、终态报告与最近新增没有溢出或 axe 违规`, async ({ page, backend }, info) => {
    await page.setViewportSize({ width, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    const scan = seedScanFeedback(mock);
    setScanFeedback(mock, scan, "counting-admin");
    await page.goto(`/settings/library/scans/${scan.id}`);
    await expect(progress(page)).toHaveAttribute("aria-valuetext", /正在数文件.*目录/);
    await expect(page.getByText(`当前目录：${longRootName}`, { exact: false })).toBeVisible();
    await verifyLayout(page, `${width} 数文件`);
    await capture(page, info, "counting");

    setScanFeedback(mock, scan, "reading_tags-admin");
    await page.reload();
    await expect(progress(page)).toHaveAttribute("aria-valuenow", "12");
    await expect(progress(page)).toHaveAttribute("aria-valuetext", /读取标签.*50 \/ 400.*12%/);
    await verifyLayout(page, `${width} 读取标签`);
    await capture(page, info, "reading-tags");

    setScanFeedback(mock, scan, "terminal-failed");
    await page.reload();
    await expect(progress(page)).toHaveAttribute("aria-valuenow", "49");
    await expect(progress(page)).toHaveAttribute("aria-valuetext", /停在.*读取标签/);
    await expect(report(page)).toContainText("这次的报告没能生成，扫描结果已经保存。");
    await verifyLayout(page, `${width} 失败冻结`);
    await capture(page, info, "failed");

    setScanFeedback(mock, scan, "terminal-succeeded", { report_status: "ready" });
    await page.reload();
    await expect(progress(page)).toHaveAttribute("aria-valuenow", "100");
    await expect(report(page).getByRole("link", { name: longAlbumTitle, exact: true })).toBeVisible();
    await expect(report(page)).toContainText("至少 100 条");
    await expect(report(page)).toContainText("现在看不到了");
    await verifyLayout(page, `${width} 扫描报告`);
    await capture(page, info, "report");

    await page.goto("/");
    await expect(page.getByRole("region", { name: "最近新增" })).toBeVisible();
    await expect(page.getByRole("region", { name: "最近新增" }).getByRole("link", { name: new RegExp("^" + longAlbumTitle) })).toBeVisible();
    const expectedHrefs = await expectRecentShelf(page, mock.recentlyAdded.slice(0, 8));
    await verifyLayout(page, `${width} 最近新增`);
    await capture(page, info, "recently-added");
    await expectRecentTabOrder(page, expectedHrefs);
  });
}

test("纯键盘打开报告、翻页并把焦点送回新增专辑标题", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const scan = seedScanFeedback(mock);
  setScanFeedback(mock, scan, "terminal-succeeded", { report_status: "ready" });
  await page.goto("/settings/library");
  await expect(page.getByRole("table", { name: "扫描记录" })).toBeVisible();
  await tabTo(page, (name) => name.startsWith("查看详情"), 40);
  await expectVisibleFocusRing(page, "打开扫描报告");
  await page.keyboard.press("Enter");
  await expect(report(page)).toBeVisible();
  const pagination = page.getByRole("navigation", { name: "新增专辑分页" });
  await tabTo(page, (name) => name === "下一页", 40);
  await expect(pagination.getByRole("button", { name: "下一页" })).toBeFocused();
  await expectVisibleFocusRing(page, "报告下一页");
  await page.keyboard.press("Enter");
  await expect(pagination).toContainText("第 2 / 2 页");
  await expect(report(page).getByRole("heading", { name: "新增的专辑" })).toBeFocused();
  await expect(report(page).getByRole("link", { name: "新增专辑 21", exact: true })).toBeVisible();
  await tabTo(page, (name) => name === "上一页", 8);
  await page.keyboard.press("Enter");
  await expect(pagination).toContainText("第 1 / 2 页");
  await expect(report(page).getByRole("heading", { name: "新增的专辑" })).toBeFocused();
  await expect(report(page).getByRole("link", { name: longAlbumTitle, exact: true })).toBeVisible();
});

for (const signedInAs of ["curator", "listener"] as const) {
  test(`${signedInAs} 能看进度和一行横排的最近新增，“全部专辑”使用加入时间排序`, async ({ page, backend }) => {
    await page.setViewportSize({ width: 1280, height: 1000 });
    const mock = await backend.start({ signedInAs });
    seedScanFeedback(mock);
    await page.goto("/");
    await expect(progress(page)).toHaveAttribute("aria-valuenow", "12");
    await expect(progress(page)).toHaveAttribute("aria-valuetext", /第 1 \/ 共 2 个目录/);
    await expect(page.getByText(longRootName, { exact: false })).toHaveCount(0);
    const recent = page.getByRole("region", { name: "最近新增" });
    await expectRecentShelf(page, mock.recentlyAdded.slice(0, 8));
    await expect(recent.getByRole("listitem").first()).toContainText(longAlbumTitle);
    await expect(recent).not.toContainText("新增专辑 02");
    await recent.getByRole("link", { name: "全部专辑" }).click();
    await expect(page).toHaveURL(/\/albums\?sort=added_at_desc$/);
    await expect(page.getByRole("button", { name: "排序：最近新增", exact: true })).toBeVisible();
    const albums = page.getByRole("region", { name: "全部专辑" });
    await expect(albums.getByRole("listitem").first()).toContainText(longAlbumTitle);
    expect(mock.callsTo("GET", "/api/v1/release-groups").some((call) => new URLSearchParams(call.search).get("sort") === "added_at_desc")).toBe(true);
    await page.getByRole("button", { name: "排序：最近新增", exact: true }).click();
    await page.getByRole("menuitem", { name: "按标题", exact: true }).click();
    await expect(page).toHaveURL(/\/albums$/);
    await page.goBack();
    await expect(page.getByRole("button", { name: "排序：最近新增", exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: "排序：最近新增", exact: true })).toBeVisible();
    if (signedInAs === "listener") {
      expect(mock.callsTo("GET", /\/scans\/[^/]+\/report$/)).toHaveLength(0);
      expect(mock.callsTo("GET", "/api/v1/library-roots")).toHaveLength(0);
    }
  });
}

for (const state of ["failed", "canceled", "incomplete"] as const) {
  test(`${state} 终态进度冻结且停止轮询，不伪装成 100%`, async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const scan = seedScanFeedback(mock);
    const snapshot = setScanFeedback(mock, scan, `terminal-${state}`);
    await page.clock.install();
    await page.goto(`/settings/library/scans/${scan.id}`);
    const percent = String(Math.floor((snapshot.progress?.percent_bp ?? -1) / 100));
    await expect(progress(page)).toHaveAttribute("aria-valuenow", percent);
    await expect(progress(page)).not.toHaveAttribute("aria-valuenow", "100");
    await expect(progress(page)).toHaveAttribute("aria-valuetext", /停在/);
    const reads = mock.callsTo("GET", `/api/v1/scans/${scan.id}`).length;
    await page.clock.fastForward(30_000);
    await expect(progress(page)).toHaveAttribute("aria-valuenow", percent);
    expect(mock.callsTo("GET", `/api/v1/scans/${scan.id}`)).toHaveLength(reads);
  });
}

test("跨目录阶段切回读取时整体不倒退，轮询不抢键盘焦点", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const scan = seedScanFeedback(mock);
  setScanFeedback(mock, scan, "first-root-saved");
  await page.clock.install();
  await page.goto(`/settings/library/scans/${scan.id}`);
  await expect(progress(page)).toHaveAttribute("aria-valuenow", "28");
  await tabTo(page, (name) => name === "取消扫描", 40);
  const cancel = page.getByRole("button", { name: "取消扫描", exact: true });
  await expect(cancel).toBeFocused();
  for (const [sample, percent] of [["second-root-started", "28"], ["second-root-reading", "49"]] as const) {
    setScanFeedback(mock, scan, sample);
    await expect(async () => {
      await page.clock.fastForward(2000);
      await expect(progress(page)).toHaveAttribute("aria-valuenow", percent, { timeout: 500 });
      await expect(progress(page)).toHaveAttribute("aria-valuetext", /第 2 \/ 共 2 个目录，读取标签/, { timeout: 500 });
    }).toPass({ timeout: 10_000 });
    await expect(cancel).toBeFocused();
  }
});
