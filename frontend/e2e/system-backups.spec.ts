import type { Locator } from "@playwright/test";
import { expectNoHorizontalOverflow, expectNoSeriousViolations } from "./support/checks";
import { expect, test } from "./support/fixtures";

/** 只量可见文字的实际行数：按钮有最小高度，用高度判断会漏掉中文被挤成两行。 */
async function expectSingleTextLine(locator: Locator) {
  const lines = await locator.evaluate((element) => {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const tops: number[] = [];
    let node: Node | null;
    while ((node = walker.nextNode()) !== null) {
      if (!node.textContent?.trim()) continue;
      const parent = node.parentElement;
      if (parent?.closest('[aria-hidden="true"]') || (parent && getComputedStyle(parent).position === "absolute")) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      for (const rect of range.getClientRects()) {
        if (rect.width > 0 && rect.height > 0 && !tops.some((top) => Math.abs(top - rect.top) < 2)) tops.push(rect.top);
      }
    }
    return tops.length;
  });
  expect(lines).toBe(1);
}

for (const width of [1440, 375]) {
  test(`备份页 ${width}：渲染、立即备份、附件下载与文字布局`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    mock.system.status.last_failure = null;
    mock.system.backupFinishesAfterReads = null;
    await page.goto("/system/backups");
    await expect(page.getByRole("heading", { name: "备份", exact: true, level: 1 })).toBeVisible();
    const table = page.getByRole("table", { name: "备份文件" });
    await expect(table.getByRole("row")).toHaveCount(5);
    await expect(page.getByText("开发版", { exact: true })).toBeVisible();
    for (const label of ["自动", "手动", "版本切换前", "恢复前"]) {
      const source = table.getByText(label, { exact: true });
      await expect(source).toBeVisible();
      await expectSingleTextLine(source);
    }
    const button = page.getByRole("button", { name: "立即备份" });
    await expectSingleTextLine(button);
    for (const link of await table.getByRole("link", { name: /^下载/ }).all()) await expectSingleTextLine(link);
    for (const time of await table.getByRole("rowheader").all()) await expectSingleTextLine(time);
    // 下载操作必须停留在同一行的时间之后，而不是被挤到文件表外或与其他行重叠。
    for (const row of await table.getByRole("row").all()) {
      const download = row.getByRole("link", { name: /^下载/ });
      if (await download.count() === 0) continue;
      const time = await row.getByRole("rowheader").boundingBox();
      const action = await download.boundingBox();
      if (time === null || action === null) throw new Error("备份行不可见");
      if (width >= 768) expect(Math.abs(action.y - time.y)).toBeLessThan(20);
      else expect(action.y).toBeGreaterThanOrEqual(time.y);
    }
    await expectNoHorizontalOverflow(page, `备份页 ${width}`);
    await expectNoSeriousViolations(page, `备份页 ${width}`);
    await page.screenshot({ path: `/tmp/roomusic-b3-${width}.png`, fullPage: true });

    const file = mock.system.backups[0];
    if (file === undefined) throw new Error("缺少下载测试种子");
    const downloadEvent = page.waitForEvent("download");
    await table.getByRole("link", { name: /^下载/ }).first().click();
    const download = await downloadEvent;
    expect(download.suggestedFilename()).toBe(file.name);
    expect(await download.failure()).toBeNull();
    const localPath = await download.path();
    if (localPath === null) throw new Error("下载没有本地文件");
    // mock层只验证原生下载触发与文件名；附件字节与Range由A6真实handler集成测试验证。
    await expect(page).toHaveURL(/\/system\/backups$/);

    await button.click();
    const busy = page.getByRole("button", { name: "正在备份…" });
    await expect(busy).toHaveAttribute("aria-busy", "true");
    await expect(busy).not.toHaveAttribute("disabled");
    await expectSingleTextLine(busy);
    await busy.click({ force: true });
    expect(mock.callsTo("POST", "/api/v1/system/backups")).toHaveLength(1);
    mock.system.backupFinishesAfterReads = 1;
    await expect(page.getByRole("button", { name: "立即备份" })).toBeVisible({ timeout: 6000 });
    await expect(table.getByRole("row")).toHaveCount(6);
    await expectNoHorizontalOverflow(page, `备份页 ${width}`);
  });
}
