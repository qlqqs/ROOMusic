import type { Locator, Page } from "@playwright/test";
import { expectNoHorizontalOverflow, expectNoSeriousViolations, expectSingleTextLine } from "./support/checks";
import { expect, test } from "./support/fixtures";

for (const width of [1440, 375]) {
  test(`备份页 ${width}：渲染、立即备份、附件下载与文字布局`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    mock.system.status.last_failure = null;
    mock.system.backupFinishesAfterReads = null;
    await page.goto("/settings/backups");
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
    for (const time of await table.getByRole("rowheader").all()) await expectSingleTextLine(time);
    await expectBackupControlsLayout(page, width);
    await expectNoHorizontalOverflow(page, `备份页 ${width}`);
    await expectNoSeriousViolations(page, `备份页 ${width}`);
    await page.screenshot({ path: `/tmp/roomusic-b3-${width}.png`, fullPage: true });

    const file = mock.system.backups[0];
    if (file === undefined) throw new Error("缺少下载测试种子");
    const downloadEvent = page.waitForEvent("download");
    await chooseRowAction(page, backupRows(page).first(), "下载");
    const download = await downloadEvent;
    expect(download.suggestedFilename()).toBe(file.name);
    expect(await download.failure()).toBeNull();
    const localPath = await download.path();
    if (localPath === null) throw new Error("下载没有本地文件");
    // mock层只验证原生下载触发与文件名；附件字节与Range由A6真实handler集成测试验证。
    await expect(page).toHaveURL(/\/settings\/backups$/);

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

const filesPanel = (page: Page) => page.getByRole("region", { name: "备份文件", exact: true });
// 删除结果的读屏播报：分组卡片之后紧跟的那个仅供读屏的 status（卡片 SettingsGroup 不接收额外的子节点）。
const filesAnnouncement = (page: Page) => filesPanel(page).locator("xpath=following-sibling::*[1]").getByRole("status");
const retentionPanel = (page: Page) => page.getByRole("region", { name: "自动清理", exact: true });
const singleDeleteDialog = (page: Page) => page.getByRole("dialog", { name: /^删除 .* 的备份？$/ });
const bulkDeleteDialog = (page: Page) => page.getByRole("dialog", { name: /^删除 \d+ 份备份？$/ });
const retentionDialog = (page: Page) => page.getByRole("dialog", { name: "保存清理设置？", exact: true });
const backupRows = (page: Page) => filesPanel(page).getByRole("row").filter({ has: page.getByRole("rowheader") });
const rowCheckboxes = (page: Page) => filesPanel(page).getByRole("checkbox", { name: /^选择 .* 的备份$/ });
// 表头与操作条各有一个全选，按容器宽度只显示其中一个；getByRole 只认显示着的那个。
const allCheckbox = (page: Page) => filesPanel(page).getByRole("checkbox", { name: "全选", exact: true });
// 名称里“所选的 n 份备份”是只给读屏的文字，浏览器计算名称时在它两侧加空格。
const selectedDelete = (page: Page, count: number) => filesPanel(page).getByRole("button", { name: new RegExp(`^删除\\s*所选的 ${count} 份备份\\s*…$`) });
const anySelectedDelete = (page: Page) => filesPanel(page).getByRole("button", { name: /^删除\s*所选的/ });
const rowMenuButton = (row: Locator) => row.getByRole("button", { name: /的备份的更多操作$/ });

/** 打开备份行尾的“更多”菜单并选一项（“下载”“删除…”）。 */
async function chooseRowAction(page: Page, row: Locator, action: string): Promise<void> {
  await rowMenuButton(row).click();
  await page.getByRole("menu", { name: /的备份的更多操作$/ }).getByRole("menuitem", { name: action, exact: true }).click();
}

test("手机备份行菜单靠近视口底边时保持打开，不因首项聚焦滚动而关闭", async ({ page, backend }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await backend.start({ signedInAs: "curator" });
  await page.goto("/settings/backups");
  const trigger = rowMenuButton(backupRows(page).first());
  await trigger.scrollIntoViewIfNeeded();
  await trigger.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  await trigger.click();
  const menu = page.getByRole("menu", { name: /的备份的更多操作$/ });
  await expect(menu.getByRole("menuitem", { name: "下载", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(menu.getByRole("menuitem", { name: "删除…", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).not.toBeVisible();
  await expect(trigger).toBeFocused();
});

/** 不只检查页面滚动宽度：控件必须同排且不互相覆盖，文字必须保持一行。 */
async function expectSameRow(left: Locator, right: Locator, label: string): Promise<void> {
  const a = await left.boundingBox();
  const b = await right.boundingBox();
  if (a === null || b === null) throw new Error(`${label}：控件必须可见`);
  expect(Math.abs(a.y + a.height / 2 - b.y - b.height / 2), `${label}：纵向居中、没有换排`).toBeLessThan(3);
  expect(a.x + a.width, `${label}：左右控件没有重叠`).toBeLessThanOrEqual(b.x);
}

async function expectBackupControlsLayout(page: Page, width: number): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
  // 每行的下载与删除收在行尾的“更多”里：按钮与时间在同一行（窄容器堆叠时它在行标题旁）。
  for (const row of await backupRows(page).all()) {
    const time = row.getByRole("rowheader");
    await expectSingleTextLine(time);
    await expect(rowMenuButton(row)).toBeVisible();
    const a = await time.boundingBox();
    const b = await rowMenuButton(row).boundingBox();
    if (a === null || b === null) throw new Error("备份行不可见");
    expect(Math.abs(a.y + a.height / 2 - b.y - b.height / 2), "时间 / 更多：同一行").toBeLessThan(12);
    expect(a.x + a.width, "时间 / 更多：没有重叠").toBeLessThanOrEqual(b.x);
  }
  const files = filesPanel(page);
  await expect(allCheckbox(page)).toHaveCount(1);
  const bulk = anySelectedDelete(page);
  if (await bulk.count() > 0) {
    const selected = files.getByText(/^已选 \d+ 个$/);
    const download = files.getByRole("button", { name: /^下载\s*所选的/ });
    for (const element of [selected, download, bulk]) await expectSingleTextLine(element);
    await expectSameRow(download, bulk, "下载 / 删除");
  }
  for (const name of ["保留天数", "保留份数"]) {
    const field = retentionPanel(page).getByRole("group", { name, exact: true });
    const input = field.getByRole("spinbutton", { name, exact: true });
    const unit = field.locator('span[aria-hidden="true"]');
    const unlimited = field.locator("label");
    // 名称是设置行左侧的文字（组经 aria-labelledby 以它为名），不再是 fieldset 的 legend。
    await expectSingleTextLine(retentionPanel(page).getByText(name, { exact: true }));
    await expectSingleTextLine(unit, true);
    await expectSingleTextLine(unlimited);
    await expectSameRow(input, unit, `${name}：数字框 / 单位`);
    await expectSameRow(unit, unlimited, `${name}：单位 / 不限`);
  }
  // 即改即存：没有“保存”按钮。
  await expect(retentionPanel(page).getByRole("button", { name: "保存", exact: true })).toHaveCount(0);
  await expectNoHorizontalOverflow(page, `备份工具行与自动清理 @${width}`);
}

async function expectConfirmationLayout(page: Page, dialog: Locator, width: number): Promise<void> {
  await expect(dialog.getByRole("button", { name: "取消", exact: true })).toBeFocused();
  for (const button of await dialog.getByRole("button").all()) await expectSingleTextLine(button);
  expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(0);
  const bounds = await dialog.boundingBox();
  if (bounds === null) throw new Error("确认框不可见");
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
  await expectNoHorizontalOverflow(page, `备份确认框 @${width}`);
  await expectNoSeriousViolations(page, `备份确认框 @${width}`);
}

for (const width of [1440, 375]) {
  test(`备份删除 ${width}：取消不请求，提交忙碌不重复，完成后刷新与恢复焦点`, async ({ page, backend }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    const file = mock.system.backups[0];
    if (file === undefined) throw new Error("缺少删除种子");
    await page.goto("/settings/backups");
    await expect(backupRows(page)).toHaveCount(4);
    await expectBackupControlsLayout(page, width);
    const firstRow = backupRows(page).first();
    const trigger = rowMenuButton(firstRow);
    const time = await firstRow.getByRole("rowheader").innerText();
    await chooseRowAction(page, firstRow, "删除…");
    const dialog = singleDeleteDialog(page);
    await expect(dialog).toHaveAccessibleName(`删除 ${time} 的备份？`);
    await expect(dialog.getByText("删除后不能恢复。", { exact: true })).toBeVisible();
    await expectConfirmationLayout(page, dialog, width);
    await testInfo.attach(`单删确认-${width}`, { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    await dialog.getByRole("button", { name: "取消", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    expect(mock.callsTo("DELETE", `/api/v1/system/backups/${file.name}`)).toHaveLength(0);

    let release = () => {};
    const held = new Promise<void>((resolve) => { release = resolve; });
    await page.route(`**/api/v1/system/backups/${file.name}`, async (route) => {
      if (route.request().method() !== "DELETE") return route.fallback();
      await held;
      await route.fallback();
    });
    await chooseRowAction(page, firstRow, "删除…");
    const confirm = dialog.getByRole("button", { name: "删除", exact: true });
    const request = page.waitForRequest((item) => item.method() === "DELETE" && item.url().endsWith(file.name));
    await confirm.click();
    await request;
    try {
      await expect(confirm).toHaveAttribute("aria-busy", "true");
      await expect(confirm).not.toHaveAttribute("disabled");
      await expect(dialog.getByRole("button", { name: "取消", exact: true })).toBeDisabled();
      await confirm.click({ force: true });
      await page.keyboard.press("Escape");
      await expect(dialog).toBeVisible();
    } finally {
      release();
    }
    await expect(dialog).toBeHidden();
    await expect(backupRows(page)).toHaveCount(3);
    await expect(filesPanel(page).getByRole("heading", { name: "备份文件", exact: true })).toBeFocused();
    await expect(filesAnnouncement(page)).toHaveText("已删除。");
    expect(mock.callsTo("DELETE", `/api/v1/system/backups/${file.name}`)).toHaveLength(1);
    expect(mock.system.backups.some((backup) => backup.name === file.name)).toBe(false);
    expect(mock.system.status.last_success_at).toBe(mock.system.backups[0]?.created_at);
  });

  test(`备份批删 ${width}：全选和半选、取消、一次请求删除全部、清空选择`, async ({ page, backend }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    const names = mock.system.backups.map((file) => file.name);
    await page.goto("/settings/backups");
    await expect(rowCheckboxes(page)).toHaveCount(4);
    await rowCheckboxes(page).first().check();
    await expect(allCheckbox(page)).toHaveJSProperty("indeterminate", true);
    await allCheckbox(page).check();
    for (const checkbox of await rowCheckboxes(page).all()) await expect(checkbox).toBeChecked();
    await expect(allCheckbox(page)).toHaveJSProperty("indeterminate", false);
    await allCheckbox(page).uncheck();
    await expect(selectedDelete(page, 4)).toHaveCount(0);
    for (const checkbox of await rowCheckboxes(page).all()) await expect(checkbox).not.toBeChecked();
    await allCheckbox(page).check();
    await expectBackupControlsLayout(page, width);
    await testInfo.attach(`批量选择-${width}`, { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    await selectedDelete(page, 4).click();
    const dialog = bulkDeleteDialog(page);
    await expect(dialog).toHaveAccessibleName("删除 4 份备份？");
    await expect(dialog.getByText("删除后不能恢复。", { exact: true })).toBeVisible();
    await expectConfirmationLayout(page, dialog, width);
    await dialog.getByRole("button", { name: "取消", exact: true }).click();
    expect(mock.callsTo("POST", "/api/v1/system/backups/deletions")).toHaveLength(0);
    await expect(selectedDelete(page, 4)).toBeFocused();
    let release = () => {};
    const held = new Promise<void>((resolve) => { release = resolve; });
    await page.route("**/api/v1/system/backups/deletions", async (route) => {
      await held;
      await route.fallback();
    });
    await selectedDelete(page, 4).click();
    const confirm = dialog.getByRole("button", { name: "删除", exact: true });
    const sent = page.waitForRequest((request) => request.method() === "POST" && request.url().endsWith("/system/backups/deletions"));
    await confirm.click();
    await sent;
    try {
      await expect(confirm).toHaveAttribute("aria-busy", "true");
      await expect(confirm).not.toHaveAttribute("disabled");
      await expect(dialog.getByRole("button", { name: "取消", exact: true })).toBeDisabled();
      await confirm.click({ force: true });
      await page.keyboard.press("Escape");
      await expect(dialog).toBeVisible();
    } finally {
      release();
    }
    await expect(dialog).toBeHidden();
    await expect(filesPanel(page).getByText("还没有备份。", { exact: true })).toBeVisible();
    await expect(anySelectedDelete(page)).toHaveCount(0);
    await expect(filesPanel(page).getByRole("heading", { name: "备份文件", exact: true })).toBeFocused();
    await expect(filesAnnouncement(page)).toHaveText("已删除 4 份。");
    expect(mock.callsTo("POST", "/api/v1/system/backups/deletions").map(({ body }) => body)).toEqual([{ names }]);
  });

  test(`自动清理 ${width}：字段单行、勾上不限即保存、取消不限等输入、确认前不删、取消恢复原值、确认立即清理`, async ({ page, backend }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const mock = await backend.start({ signedInAs: "curator" });
    const originalNames = mock.system.backups.map((file) => file.name);
    await page.goto("/settings/backups");
    const panel = retentionPanel(page);
    const saved = panel.locator('p[role="status"]');
    const days = panel.getByRole("spinbutton", { name: "保留天数", exact: true });
    const count = panel.getByRole("spinbutton", { name: "保留份数", exact: true });
    const daysUnlimited = panel.getByRole("group", { name: "保留天数", exact: true }).getByRole("checkbox", { name: "不限" });
    const countUnlimited = panel.getByRole("group", { name: "保留份数", exact: true }).getByRole("checkbox", { name: "不限" });
    const puts = () => mock.callsTo("PUT", "/api/v1/system/backups/retention").map(({ body }) => body);
    await expect(days).toHaveValue("14");
    await expect(count).toBeDisabled();
    await expect(countUnlimited).toBeChecked();
    await daysUnlimited.check();
    await expect(days).toBeDisabled();
    await expect(saved).toHaveText("已保存。");
    await expectBackupControlsLayout(page, width);
    await expect(retentionDialog(page)).toBeHidden();
    expect(mock.system.retention).toEqual({ keep_days: null, keep_count: null });
    expect(puts()).toEqual([{ keep_days: null, keep_count: null, confirm: false }]);
    await countUnlimited.uncheck();
    await expect(count).toBeFocused();
    // 取消“不限”不立即保存，等输入后回车或离开。
    expect(puts()).toHaveLength(1);
    await count.fill("1");
    await count.press("Enter");
    const dialog = retentionDialog(page);
    await expect(dialog.getByText("将删除 3 份旧备份。删除后不能恢复。", { exact: true })).toBeVisible();
    await expectConfirmationLayout(page, dialog, width);
    await testInfo.attach(`自动清理确认-${width}`, { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
    expect(mock.system.backups.map((file) => file.name)).toEqual(originalNames);
    expect(mock.system.retention).toEqual({ keep_days: null, keep_count: null });
    await dialog.getByRole("button", { name: "取消", exact: true }).click();
    expect(puts()).toHaveLength(2);
    // 不保存：恢复原值。
    await expect(countUnlimited).toBeChecked();
    await expect(count).toBeDisabled();
    await countUnlimited.uncheck();
    await count.fill("1");
    await count.press("Enter");
    await dialog.getByRole("button", { name: "保存并删除", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(saved).toHaveText("已保存，删除了 3 份旧备份。");
    await expect(backupRows(page)).toHaveCount(1);
    expect(mock.system.backups.map((file) => file.name)).toEqual(originalNames.slice(0, 1));
    await expect(count).toHaveValue("1");
    expect(mock.system.retention).toEqual({ keep_days: null, keep_count: 1 });
    expect(puts()).toEqual([
      { keep_days: null, keep_count: null, confirm: false },
      { keep_days: null, keep_count: 1, confirm: false },
      { keep_days: null, keep_count: 1, confirm: false },
      { keep_days: null, keep_count: 1, confirm: true },
    ]);
    await expectBackupControlsLayout(page, width);
    await expectNoSeriousViolations(page, `清理后 @${width}`);
  });
}

test("单删：不可写留在确认框可重试；文件已被别处删除时 404 关闭并刷新，无错误", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const file = mock.system.backups[0];
  if (file === undefined) throw new Error("缺少删除种子");
  mock.system.undeletable = new Set([file.name]);
  await page.goto("/settings/backups");
  await chooseRowAction(page, backupRows(page).first(), "删除…");
  const dialog = singleDeleteDialog(page);
  await dialog.getByRole("button", { name: "删除", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("备份目录不能写入，没有删除。");
  await expect(backupRows(page)).toHaveCount(4);
  mock.system.undeletable = new Set();
  mock.system.backups = mock.system.backups.filter((backup) => backup.name !== file.name);
  const reads = mock.callsTo("GET", "/api/v1/system/backups").length;
  await dialog.getByRole("button", { name: "删除", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(backupRows(page)).toHaveCount(3);
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(mock.callsTo("GET", "/api/v1/system/backups").length).toBeGreaterThan(reads);
  expect(mock.callsTo("DELETE", `/api/v1/system/backups/${file.name}`)).toHaveLength(2);
});

test("批删：missing 算成功，部分失败保留时间与勾选，重试只请求失败的备份", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const [missing, failed] = mock.system.backups;
  if (missing === undefined || failed === undefined) throw new Error("缺少批删种子");
  const names = mock.system.backups.map((file) => file.name);
  mock.system.undeletable = new Set([failed.name]);
  await page.goto("/settings/backups");
  const failedTime = await backupRows(page).nth(1).getByRole("rowheader").innerText();
  await allCheckbox(page).check();
  await selectedDelete(page, 4).click();
  mock.system.backups = mock.system.backups.filter((file) => file.name !== missing.name);
  await bulkDeleteDialog(page).getByRole("button", { name: "删除", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "删除 1 份备份？", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("alert")).toContainText("有 1 份没能删除");
  await expect(dialog.getByRole("listitem")).toHaveText([failedTime]);
  await expect(backupRows(page)).toHaveCount(1);
  await expect(rowCheckboxes(page)).toBeChecked();
  mock.system.undeletable = new Set();
  await dialog.getByRole("button", { name: "删除", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(filesPanel(page).getByText("还没有备份。", { exact: true })).toBeVisible();
  expect(mock.callsTo("POST", "/api/v1/system/backups/deletions").map(({ body }) => body)).toEqual([{ names }, { names: [failed.name] }]);
});

test("自动清理：越界不请求，损坏规则可覆盖，保存失败恢复原值后可重试，清理部分失败提示", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  mock.system.retention = null;
  await page.goto("/settings/backups");
  const panel = retentionPanel(page);
  const saved = panel.locator('p[role="status"]');
  await expect(panel.getByText("没能读取当前设置。", { exact: true })).toBeVisible();
  const days = panel.getByRole("spinbutton", { name: "保留天数", exact: true });
  const count = panel.getByRole("spinbutton", { name: "保留份数", exact: true });
  const countUnlimited = panel.getByRole("group", { name: "保留份数", exact: true }).getByRole("checkbox", { name: "不限" });
  const puts = () => mock.callsTo("PUT", "/api/v1/system/backups/retention");
  for (const invalid of ["0", "3651", "1.5", ""]) {
    await days.fill(invalid);
    await days.press("Enter");
    await expect(days).toHaveAttribute("aria-invalid", "true");
    await expect(days).toHaveAccessibleDescription("填 1 到 3650 之间的整数。");
    await expect(days).toBeFocused();
  }
  expect(puts()).toHaveLength(0);
  // 离开数字框也保存：读不到的规则直接被覆盖。
  await days.fill("3650");
  await days.blur();
  await expect(saved).toHaveText("已保存。");
  await expect(panel.getByText("没能读取当前设置。", { exact: true })).toHaveCount(0);
  expect(mock.system.retention).toEqual({ keep_days: 3650, keep_count: null });
  await countUnlimited.uncheck();
  await expect(count).toBeFocused();
  for (const invalid of ["0", "1001", "1.5", ""]) {
    await count.fill(invalid);
    await count.press("Enter");
    await expect(count).toHaveAttribute("aria-invalid", "true");
    await expect(count).toHaveAccessibleDescription("填 1 到 1000 之间的整数。");
    await expect(count).toBeFocused();
  }
  expect(puts()).toHaveLength(1);
  await count.fill("1000");
  mock.system.retentionNotWritable = true;
  await count.press("Enter");
  await expect(panel.getByRole("alert")).toContainText("备份目录不能写入，没有保存。");
  await expect(retentionDialog(page)).toBeHidden();
  // 保存失败：恢复原值（份数又是“不限”），再改一次就是重试。
  await expect(countUnlimited).toBeChecked();
  mock.system.retentionNotWritable = false;
  await countUnlimited.uncheck();
  await count.fill("1000");
  await count.press("Enter");
  await expect(saved).toHaveText("已保存。");
  await expect(panel.getByRole("alert")).toHaveCount(0);
  expect(mock.system.retention).toEqual({ keep_days: 3650, keep_count: 1000 });

  const failed = mock.system.backups[1];
  if (failed === undefined) throw new Error("缺少清理失败种子");
  mock.system.undeletable = new Set([failed.name]);
  await count.fill("1");
  await count.press("Enter");
  const dialog = retentionDialog(page);
  await expect(dialog).toBeVisible();
  mock.system.retentionNotWritable = true;
  await dialog.getByRole("button", { name: "保存并删除", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("备份目录不能写入，没有保存。");
  mock.system.retentionNotWritable = false;
  await dialog.getByRole("button", { name: "保存并删除", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(saved).toHaveText("已保存，删除了 2 份旧备份。");
  await expect(panel.getByText("有 1 份旧备份没能删除。", { exact: true })).toBeVisible();
  await expect(backupRows(page)).toHaveCount(2);
});

test("轮询移除备份后自动裁掉勾选，不误删已消失的文件", async ({ page, backend }) => {
  await page.clock.install();
  const mock = await backend.start({ signedInAs: "curator" });
  const removed = mock.system.backups[0];
  const kept = mock.system.backups[1];
  if (removed === undefined || kept === undefined) throw new Error("缺少轮询种子");
  mock.system.status.running = { source: "manual", started_at: "2026-11-06T04:00:00Z" };
  mock.system.backupFinishesAfterReads = null;
  await page.goto("/settings/backups");
  await expect(backupRows(page)).toHaveCount(4);
  await rowCheckboxes(page).nth(0).check();
  await rowCheckboxes(page).nth(1).check();
  await expect(selectedDelete(page, 2)).toBeVisible();
  mock.system.backups = mock.system.backups.filter((file) => file.name !== removed.name);
  await page.clock.fastForward(2000);
  await expect(backupRows(page)).toHaveCount(3);
  await expect(selectedDelete(page, 1)).toBeVisible();
  await expect(allCheckbox(page)).toHaveJSProperty("indeterminate", true);
  // 列表按时间倒序，被移除的是第一份，留下的那份现在排在第一行。
  await expect(backupRows(page).first().getByRole("checkbox", { name: /^选择 / })).toBeChecked();
  await selectedDelete(page, 1).click();
  await bulkDeleteDialog(page).getByRole("button", { name: "删除", exact: true }).click();
  await expect(bulkDeleteDialog(page)).toBeHidden();
  expect(mock.callsTo("POST", "/api/v1/system/backups/deletions").map(({ body }) => body)).toEqual([{ names: [kept.name] }]);
});
