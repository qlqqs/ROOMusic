import { defineConfig, devices } from "@playwright/test";

const port = 4173;
const origin = `http://127.0.0.1:${port}`;
const inCI = Boolean(process.env["CI"]);

// 浏览器冒烟对生产构建运行：webServer 先构建（产物就是内嵌进后端的那份），再用 vite preview 同源托管。
// API 全部由 e2e/support/api-mock.ts 受控响应（取自后端样本），不依赖数据库或后端进程。
export default defineConfig({
  testDir: "e2e",
  // 每个测试各自安装受控后端，互不共享状态，可以并行。
  fullyParallel: true,
  forbidOnly: inCI,
  retries: inCI ? 1 : 0,
  reporter: inCI ? [["list"], ["html", { open: "never" }]] : "list",
  outputDir: "test-results",
  use: {
    baseURL: origin,
    locale: "zh-CN",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: `npm run build && npm run preview -- --host 127.0.0.1 --port ${port} --strictPort`,
    url: origin,
    reuseExistingServer: !inCI,
    timeout: 120_000,
  },
});
