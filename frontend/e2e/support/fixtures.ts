import { expect, test as base, type Page } from "@playwright/test";
import { installApiMock, type ApiMock, type MockOptions } from "./api-mock";

type Fixtures = {
  /** 安装受控后端：必须在 page.goto 之前调用。测试结束时统一断言没有第三方请求、没有脚本错误。 */
  backend: { start: (options?: MockOptions) => Promise<ApiMock> };
};

export const test = base.extend<Fixtures>({
  // 回调参数不叫 use：react-hooks 的 lint 规则会把名为 use 的函数当成 React Hook。
  backend: async ({ context, baseURL }, provide) => {
    const origin = new URL(baseURL ?? "http://127.0.0.1").origin;
    const external: string[] = [];
    const problems: string[] = [];

    const watch = (page: Page) => {
      page.on("pageerror", (error) => problems.push(`脚本错误：${error.message}`));
      page.on("console", (message) => {
        // 浏览器会把 401、403、409 等预期的接口状态记成控制台错误，它们不是页面的问题。
        if (message.type() === "error" && !message.text().startsWith("Failed to load resource")) {
          problems.push(`控制台错误：${message.text()}`);
        }
      });
    };
    context.pages().forEach(watch);
    context.on("page", watch);
    context.on("request", (request) => {
      const url = new URL(request.url());
      if ((url.protocol === "http:" || url.protocol === "https:") && url.origin !== origin) external.push(request.url());
    });

    await provide({ start: (options) => installApiMock(context, options) });

    expect(external, "页面不应请求任何第三方地址").toEqual([]);
    expect(problems, "页面不应出现脚本错误或控制台错误").toEqual([]);
  },
});

export { expect };
