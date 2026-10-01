import { sampleBody } from "../src/test/api-samples";
import type { DiagnosticsPageDTO } from "../src/features/library/api/decoders";
import type { ApiMock, MockOptions } from "./support/api-mock";
import { expectNoHorizontalOverflow, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

// 每个页面在五种宽度下都不能出现横向滚动（页面与主内容区都检查）。
// 页面按 URL 到达，状态由受控后端预置；扫描详情用一个带多类诊断的失败扫描，让表格与汇总都有内容。

type Case = { name: string; options: MockOptions; path: (mock: ApiMock) => string; title: string; prepare?: (mock: ApiMock) => void };

const cases: Case[] = [
  { name: "登录", options: {}, path: () => "/login", title: "登录" },
  { name: "初始化", options: { setupRequired: true }, path: () => "/setup", title: "创建管理员" },
  { name: "概览（管理员）", options: { signedInAs: "curator" }, path: () => "/", title: "概览" },
  { name: "概览（成员）", options: { signedInAs: "listener" }, path: () => "/", title: "概览" },
  { name: "扫描目录", options: { signedInAs: "curator" }, path: () => "/library", title: "扫描目录" },
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
      for (const width of widths) {
        await page.setViewportSize({ width, height: 900 });
        await expect(heading(page, item.title)).toBeVisible();
        await page.evaluate(() => document.fonts.ready);
        await expectNoHorizontalOverflow(page, `${item.name} @${width}px`);
      }
    });
  }
});
