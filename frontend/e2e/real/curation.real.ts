import { expect, test } from "@playwright/test";
import { decodeCurationSettings, decodeCurationSources } from "../../src/features/curation/api/decoders";
import { body, origin, prepareLibrary } from "./support";

// M7 E1 的真实服务用例：隔离服务、隔离 PostgreSQL、真实 Chromium。总开关始终保持关闭：打开它会让后台向真实的 MusicBrainz 等外部服务发请求，
// 自动化测试不允许这样做（外部服务只在父任务收尾的试用里使用）。所以这里只验证界面与真实后端之间的读写：取消确认不写入、源的开关保存后刷新仍在、进度与空列表。

test.describe.configure({ mode: "serial" });

test("自动整理设置：真实后端下取消打开不写入，源的开关保存后保持，进度写“未打开”", async ({ page }) => {
  // 单独运行时服务还没有管理员：需要时初始化并登录（目录已登记时接口返回已有目录）。
  await prepareLibrary(page.request);
  const settingsBefore = decodeCurationSettings(await body(await page.request.get("/api/v1/admin/curation/settings")));
  expect(settingsBefore.enabled).toBe(false);

  const writes: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().includes("/api/v1/admin/curation/settings")) writes.push(request.url());
  });
  await page.goto("/settings/curation");
  await expect(page.getByRole("heading", { name: "自动整理", exact: true, level: 1 })).toBeVisible();

  // 打开前要确认；取消之后没有写入，开关仍是关闭。
  const toggle = page.getByRole("switch", { name: "自动整理" });
  await toggle.click();
  const consent = page.getByRole("dialog", { name: "打开自动整理？" });
  await expect(consent).toBeVisible();
  await consent.getByRole("button", { name: "取消" }).click();
  await expect(toggle).not.toBeChecked();
  expect(writes).toEqual([]);

  // 进度：后台循环刚启动时可能先是空闲，随即读到“未打开”。
  await expect.poll(async () => page.getByRole("region", { name: "进度", exact: true }).textContent(), { timeout: 70_000 }).toContain("未打开");
  await expect(page.getByText("还没有自动整理的记录。")).toBeVisible();
  await expect(page.getByText("没有标记。")).toBeVisible();

  // 关掉一个源：刷新之后仍是关闭；最后恢复原状（真实用例共用一个数据库）。
  const lrclib = page.getByRole("switch", { name: "LRCLIB" });
  await expect(lrclib).toBeChecked();
  await lrclib.click();
  await expect(lrclib).not.toBeChecked();
  await page.reload();
  await expect(page.getByRole("switch", { name: "LRCLIB" })).not.toBeChecked();
  const sources = decodeCurationSources(await body(await page.request.get("/api/v1/admin/curation/sources")));
  expect(sources.items.find((source) => source.id === "lrclib")?.enabled).toBe(false);
  await page.request.put("/api/v1/admin/curation/sources/lrclib", { data: { enabled: true }, headers: { Origin: origin } });
  const settingsAfter = decodeCurationSettings(await body(await page.request.get("/api/v1/admin/curation/settings")));
  expect(settingsAfter.enabled).toBe(false);
});
