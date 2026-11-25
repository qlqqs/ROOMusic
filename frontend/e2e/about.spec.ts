import { sampleBody } from "../src/test/api-samples";
import { expectNoHorizontalOverflow, expectNoSeriousViolations } from "./support/checks";
import { expect, test } from "./support/fixtures";

const repo = "https://github.com/qlqqs/ROOMusic";
const commit = "b6654580039af4cd484397f8d5d42de14eca3641";
const diagnosticsPath = "/api/v1/admin/diagnostics";
const clientErrorsPath = "/api/v1/diagnostics/client-errors";

for (const width of [1440, 375]) {
  test(`管理员的关于页显示版本并复制诊断信息 @${width}px`, async ({ page, context, backend }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    mock.system.version = { version: "0.8.1", commit };
    await page.goto("/settings/about");

    await expect(page.getByRole("heading", { name: "关于", level: 1 })).toBeVisible();
    const about = page.getByRole("region", { name: "ROOMusic" });
    const project = page.getByRole("region", { name: "项目" });
    const feedback = page.getByRole("region", { name: "反馈" });
    await expect(about.getByRole("heading", { name: "ROOMusic", level: 2 })).toBeVisible();
    await expect(about.locator("img")).toBeVisible();
    await expect(about.getByText("版本 0.8.1", { exact: true })).toBeVisible();
    await expect(project.getByRole("link", { name: "0.8.1 的更新说明", exact: true })).toHaveAttribute("href", `${repo}/releases/tag/v0.8.1`);
    await expect(about.getByRole("link", { name: "b665458" })).toHaveAttribute("href", `${repo}/commit/${commit}`);
    await expect(feedback.getByRole("link", { name: "反馈问题", exact: true })).toHaveAttribute("href", /template=bug_report\.yml&version=0\.8\.1\+%28b665458%29$/);
    await page.evaluate(() => document.fonts.ready);
    await expectNoHorizontalOverflow(page, `管理员的关于页 @${width}px`);
    await expectNoSeriousViolations(page, `管理员的关于页 @${width}px`);

    const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === diagnosticsPath && candidate.request().method() === "GET");
    await feedback.getByRole("button", { name: "复制诊断信息", exact: true }).click();
    expect((await response).status()).toBe(200);
    await expect(feedback.getByRole("button", { name: "已复制诊断信息", exact: true })).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`\`\`\`json\n${JSON.stringify(sampleBody("diagnostics/admin"), null, 2)}\n\`\`\`\n`);
  });

  test(`成员的关于页显示项目和反馈链接 @${width}px`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    await page.goto("/settings/about");

    await expect(page.getByRole("heading", { name: "关于", level: 1 })).toBeVisible();
    const about = page.getByRole("region", { name: "ROOMusic" });
    const project = page.getByRole("region", { name: "项目" });
    const feedback = page.getByRole("region", { name: "反馈" });
    await expect(about.getByRole("heading", { name: "ROOMusic", level: 2 })).toBeVisible();
    await expect(about.locator("img")).toBeVisible();
    for (const name of ["源代码", "更新说明", "许可 AGPL-3.0", "第三方许可"]) {
      await expect(project.getByRole("link", { name, exact: true })).toBeVisible();
    }
    const feedbackLink = feedback.getByRole("link", { name: "反馈问题", exact: true });
    await expect(feedbackLink).toBeVisible();
    await expect(feedbackLink).toHaveAttribute("href", `${repo}/issues/new?template=bug_report.yml`);
    const main = page.getByRole("main");
    await expect(main.getByText(/^版本(?:\s|$)/)).toHaveCount(0);
    await expect(main.getByRole("button", { name: /诊断信息/ })).toHaveCount(0);
    await page.evaluate(() => document.fonts.ready);
    await expectNoHorizontalOverflow(page, `成员的关于页 @${width}px`);
    await expectNoSeriousViolations(page, `成员的关于页 @${width}px`);
    expect(mock.calls.filter(({ path }) => path === diagnosticsPath || path === "/api/v1/system/version")).toHaveLength(0);
  });
}

test("浏览器上报成员的页面错误并忽略原生 AbortError", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await page.goto("/settings/about?source=feedback");
  await expect(page.getByRole("region", { name: "ROOMusic" })).toBeVisible();

  const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === clientErrorsPath);
  await page.evaluate(() => {
    window.dispatchEvent(new PromiseRejectionEvent("unhandledrejection", {
      promise: Promise.resolve(),
      reason: new DOMException("请求取消", "AbortError"),
    }));
    const error = new TypeError("页面读取失败");
    window.dispatchEvent(new ErrorEvent("error", { error, message: error.message }));
  });
  expect((await response).status()).toBe(204);
  expect(mock.calls.filter(({ path }) => path === clientErrorsPath).map(({ method, body }) => ({ method, body }))).toEqual([
    { method: "POST", body: { kind: "error", name: "TypeError", message: "页面读取失败", route: "/settings/about" } },
  ]);
});
