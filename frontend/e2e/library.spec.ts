import { sampleBody } from "../src/test/api-samples";
import type { DiagnosticsPageDTO } from "../src/features/library/api/decoders";
import { expect, test } from "./support/fixtures";
import { expectNoSeriousViolations } from "./support/checks";
import { heading, uuidPattern } from "./support/locators";
import type { Page } from "@playwright/test";

const rootsTable = (page: Page) => page.getByRole("table", { name: "音乐目录列表" });
const phaseTag = (page: Page) => page.getByRole("region", { name: "状态" }).getByRole("status");

test.describe("目录", () => {
  test("浏览并选择后添加", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/library");
    const input = page.getByRole("combobox", { name: "目录路径" });
    await input.click();
    const browser = page.getByRole("dialog", { name: "选择文件夹" });
    await expect(browser).toBeVisible();
    await browser.getByRole("button", { name: "classical-a", exact: true }).click();
    await expect(browser.getByRole("button", { name: "上一级" })).toBeVisible();
    await expect(browser.getByRole("button", { name: "选择 album", exact: true })).toBeVisible();
    await expectNoSeriousViolations(page, "打开文件夹浏览器");
    await browser.getByRole("button", { name: "选择 album", exact: true }).click();

    await expect(browser).toBeHidden();
    await expect(input).toHaveValue("/music/classical-a/album");
    await expect(input).toBeFocused();
    await expect(input).toHaveAttribute("aria-expanded", "false");
    await page.getByRole("button", { name: "添加目录" }).click();
    await expect(page.getByText(/已添加目录“album”/)).toBeVisible();
    await expect(rootsTable(page).getByRole("rowheader", { name: "album", exact: true })).toBeVisible();
    expect(mock.callsTo("POST", "/api/v1/library-roots").map((call) => call.body)).toEqual([{ path: "/music/classical-a/album" }]);
  });

  test("添加目录：路径被拒绝显示在字段旁，添加成功出现在列表与操作记录，写请求带幂等键", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/library");
    await expect(heading(page, "扫描目录")).toBeVisible();
    await expect(rootsTable(page).getByRole("rowheader", { name: "classical-a" })).toBeVisible();

    await page.getByLabel("目录路径").fill("/etc");
    await page.getByRole("button", { name: "添加目录" }).click();
    await expect(page.getByText("这个路径不在允许的音乐目录里，或者不是绝对路径。")).toBeVisible();
    await expect(page.getByLabel("目录路径")).toHaveValue("/etc");

    await page.getByLabel("目录路径").fill("/music/new-album");
    await page.getByRole("button", { name: "添加目录" }).click();
    await expect(page.getByText(/已添加目录“new-album”/)).toBeVisible();
    await expect(rootsTable(page).getByRole("rowheader", { name: "new-album" })).toBeVisible();
    await expect(page.getByLabel("目录路径")).toHaveValue("");
    await expect(page.getByRole("table", { name: "目录操作记录" }).getByRole("row").nth(1)).toContainText("新增");

    const created = mock.callsTo("POST", "/api/v1/library-roots").filter((call) => (call.body as { path: string }).path === "/music/new-album");
    expect(created).toHaveLength(1);
    expect(created[0]?.idempotencyKey).toMatch(uuidPattern);
  });

  test("停用要确认，恢复直接执行；写请求带 revision 与幂等键", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/library");
    await rootsTable(page).getByRole("button", { name: "停用目录 classical-a" }).click();
    const dialog = page.getByRole("dialog", { name: "停用目录“classical-a”？" });
    await expect(dialog).toContainText("恢复后就能再播");
    await dialog.getByRole("button", { name: "停用目录" }).click();
    await expect(page.getByText(/已停用目录“classical-a”/)).toBeVisible();
    await expect(dialog).toBeHidden();
    await expect(rootsTable(page).getByRole("row", { name: /classical-a/ })).toContainText("已停用");

    const [disable] = mock.callsTo("PATCH", /\/library-roots\/[^/]+$/);
    expect(disable?.body).toEqual({ status: "disabled", expected_revision: 1 });
    expect(disable?.idempotencyKey).toMatch(uuidPattern);

    await rootsTable(page).getByRole("button", { name: "恢复目录 classical-b" }).click();
    await expect(page.getByText(/已恢复目录“classical-b”/)).toBeVisible();
    await expect(rootsTable(page).getByRole("row", { name: /classical-b/ })).toContainText("启用");
    expect(mock.callsTo("POST", /\/restore$/)[0]?.body).toEqual({ expected_revision: 2 });
  });

  test("目录在确认期间被别处改动（409）：提示目录已变化并刷新列表", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/library");
    await rootsTable(page).getByRole("button", { name: "停用目录 classical-a" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    mock.roots = mock.roots.map((root) => (root.name === "classical-a" ? { ...root, revision: 7 } : root));

    await dialog.getByRole("button", { name: "停用目录" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "目录刚有变化，列表已刷新" })).toBeVisible();
    await expect(dialog).toBeHidden();
    await expect(rootsTable(page).getByRole("row", { name: /classical-a/ })).toContainText("启用");
  });
});

test.describe("扫描记录", () => {
  test("翻页写进地址；后退、前进与刷新都重新显示对应的页；页码超出末页时替换为末页", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.seedHistory(45);
    await page.goto("/settings/library");
    const nav = () => page.getByRole("navigation", { name: "扫描记录分页" });
    const rows = () => page.getByRole("table", { name: "扫描记录" }).getByRole("row");
    await expect(nav().getByText("第 1 / 3 页")).toBeVisible();
    await expect(rows()).toHaveCount(21);

    await nav().getByRole("link", { name: "下一页" }).click();
    await expect(page).toHaveURL(/\/settings\/library\?scan_page=2$/);
    await expect(nav().getByText("第 2 / 3 页")).toBeVisible();

    await page.goBack();
    await expect(page).toHaveURL(/\/settings\/library$/);
    await expect(nav().getByText("第 1 / 3 页")).toBeVisible();
    await page.goForward();
    await expect(nav().getByText("第 2 / 3 页")).toBeVisible();
    await page.reload();
    await expect(nav().getByText("第 2 / 3 页")).toBeVisible();

    // 超出末页：用替换历史的方式改到末页，后退不会回到越界的页码。
    await page.goto("/settings/library?scan_page=99");
    await expect(page).toHaveURL(/\/settings\/library\?scan_page=3$/);
    await expect(nav().getByText("第 3 / 3 页")).toBeVisible();
    await expect(rows()).toHaveCount(6);
  });

  test("记录显示最新的在前、状态与诊断数；开始扫描后在详情页结束，回到目录页记录已是终态", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.seedHistory(3);
    await page.clock.install();
    await page.goto("/settings/library");
    const table = page.getByRole("table", { name: "扫描记录" });
    await expect(table.getByRole("row")).toHaveCount(4);
    await expect(table.getByRole("row").nth(1)).toContainText("不完整");

    await page.getByRole("button", { name: "开始扫描" }).click();
    await expect(heading(page, "扫描详情")).toBeVisible();
    await expect(async () => {
      await page.clock.fastForward(2000);
      await expect(phaseTag(page)).toHaveText("成功", { timeout: 500 });
    }).toPass({ timeout: 10_000 });
    await page.getByRole("link", { name: "返回扫描目录" }).click();

    await expect(table.getByRole("row")).toHaveCount(5);
    const newest = table.getByRole("row").nth(1);
    await expect(newest).toContainText("成功");
    await expect(newest).not.toContainText("进行中");
  });
});

test.describe("扫描", () => {
  test("开始扫描 → 详情页轮询 → 终态后停止（成功）", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.clock.install();
    await page.goto("/settings/library");
    await page.getByRole("button", { name: "开始扫描" }).click();

    await expect(heading(page, "扫描详情")).toBeVisible();
    await expect(page).toHaveURL(/\/settings\/library\/scans\/[0-9a-f-]{36}$/);
    await expect(phaseTag(page)).toHaveText("运行中");
    const progress = page.getByRole("progressbar", { name: "扫描进度" });
    await expect(progress).toHaveAttribute("aria-valuetext", "正在准备");
    await expect(progress).not.toHaveAttribute("aria-valuenow");
    await expect(page.getByRole("region", { name: "状态" })).not.toContainText("不显示百分比或剩余时间");

    // 每次快进 2 秒触发一次轮询；受控后端在第二次读取时把扫描置为终态。
    await expect(async () => {
      await page.clock.fastForward(2000);
      await expect(phaseTag(page)).toHaveText("成功", { timeout: 500 });
    }).toPass({ timeout: 10_000 });
    await expect(page.getByText("本次扫描没有发现需要检查的问题。")).toBeVisible();

    const scanReads = () => mock.callsTo("GET", /\/api\/v1\/scans\/[0-9a-f-]{36}$/).length;
    const settled = scanReads();
    await page.clock.fastForward(30_000);
    await expect.poll(scanReads).toBe(settled);

    // 回到扫描目录页：没有活动扫描，可以再次开始。
    await page.getByRole("link", { name: "返回扫描目录" }).click();
    await expect(page.getByRole("button", { name: "开始扫描" })).toBeVisible();
  });

  test("扫描不完整：说明没扫到的曲目保持原样，列出诊断汇总与明细（路径只显示相对路径）", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.scanOutcome = "incomplete";
    await page.clock.install();
    await page.goto("/settings/library");
    await page.getByRole("button", { name: "开始扫描" }).click();
    await expect(async () => {
      await page.clock.fastForward(2000);
      await expect(phaseTag(page)).toHaveText("不完整", { timeout: 500 });
    }).toPass({ timeout: 10_000 });

    await expect(page.getByRole("region", { name: "状态" })).toContainText("没扫到的曲目保持原样");
    const summary = page.getByRole("list", { name: "按类别汇总" });
    await expect(summary).toContainText("音频解析失败");
    const table = page.getByRole("table", { name: "诊断明细" });
    await expect(table.getByRole("row", { name: /broken\.flac/ })).toBeVisible();
    await expect(page.getByRole("region", { name: "诊断" })).not.toContainText("/");
  });

  test("取消扫描：显示“取消请求中”，下一次轮询读到已取消；诊断可以手动刷新", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const scan = mock.seedScan({}, true);
    mock.diagnostics.set(scan.id, sampleBody("scans/diagnostics") as DiagnosticsPageDTO);
    await page.clock.install();
    await page.goto(`/settings/library/scans/${scan.id}`);
    await expect(phaseTag(page)).toHaveText("运行中");

    await page.getByRole("button", { name: "取消扫描" }).click();
    await expect(phaseTag(page)).toHaveText("取消请求中");
    await expect(page.getByRole("region", { name: "状态" })).toContainText("正在取消，当前这一步做完就会停下");
    await expect(page.getByRole("button", { name: "取消扫描" })).toHaveCount(0);

    await expect(async () => {
      await page.clock.fastForward(2000);
      await expect(phaseTag(page)).toHaveText("已取消", { timeout: 500 });
    }).toPass({ timeout: 10_000 });
    await expect(page.getByRole("region", { name: "状态" })).toContainText("没扫到的曲目保持原样");

    const before = mock.callsTo("GET", `/api/v1/scans/${scan.id}/diagnostics`).length;
    await page.getByRole("button", { name: "刷新诊断" }).click();
    await expect.poll(() => mock.callsTo("GET", `/api/v1/scans/${scan.id}/diagnostics`).length).toBeGreaterThan(before);
  });

  test("扫描 ID 不是 UUID 或服务器没有这个扫描：显示“扫描不存在”", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/library/scans/not-a-uuid");
    await expect(heading(page, "扫描不存在")).toBeVisible();
    expect(mock.calls.filter((call) => call.path.startsWith("/api/v1/scans/"))).toHaveLength(0);

    await page.goto("/settings/library/scans/00000000-0000-4000-8000-0000000000ff");
    await expect(heading(page, "扫描不存在")).toBeVisible();
    await expect(page.getByRole("link", { name: "返回扫描目录" })).toBeVisible();
    await page.getByRole("navigation", { name: "设置", exact: true }).getByRole("link", { name: "扫描目录" }).click();
    await expect(heading(page, "扫描目录")).toBeVisible();
  });
});
