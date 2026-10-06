import { defineConfig, devices } from "@playwright/test";

// 服务、数据库和合成素材由 playback-e2e.sh 拥有；不连接开发服务或复用已有服务器。
const baseURL = process.env["ROOMUSIC_PLAYBACK_BASE_URL"];
if (!baseURL || !/^http:\/\/127\.0\.0\.1:[0-9]+$/.test(baseURL) || !process.env["ROOMUSIC_PLAYBACK_FIXTURE_ROOT"]) {
  throw new Error("请通过 scripts/playback-e2e.sh 启动隔离的真实播放测试");
}
export default defineConfig({
  testDir: "e2e/real",
  testMatch: "**/*.real.ts",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: Boolean(process.env["CI"]),
  timeout: 45_000,
  expect: { timeout: 10_000 },
  outputDir: "coverage/playback-real/results",
  reporter: [["list"], ["html", { outputFolder: "coverage/playback-real/report", open: "never" }]],
  use: { baseURL, locale: "zh-CN", trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
