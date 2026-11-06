import { expect, type Locator, type Page } from "@playwright/test";
import { loadApiSample } from "../../src/test/api-samples";
import type { ApiMock } from "./api-mock";
import { seedArtistsE2E } from "./artist-seed";
import { artistKeyActivate as keyActivate } from "./artists";
import { heading } from "./locators";
import { openSettings, userTrigger } from "./settings";

export const changedPassword = "changed-password-2026";
export const passwordDialog = (page: Page) => page.getByRole("dialog", { name: "修改密码", exact: true });
export const memberRow = (page: Page, username: string) => page.getByRole("table", { name: "成员列表" }).getByRole("row", { name: new RegExp(username) });

/** 从用户菜单进入设置账号栏，不绕过窄屏模态导航。 */
export async function openPasswordDialog(page: Page): Promise<Locator> {
  if (!new URL(page.url()).pathname.startsWith("/settings/account")) await openSettings(page);
  await keyActivate(page, page.getByRole("button", { name: "修改密码", exact: true }));
  const dialog = passwordDialog(page);
  await expect(dialog).toBeVisible();
  return dialog;
}

export async function fillPasswordChange(page: Page, current = "curator-password", password = changedPassword, confirmation = password): Promise<void> {
  const dialog = passwordDialog(page);
  await dialog.getByLabel("当前密码", { exact: true }).fill(current);
  await dialog.getByLabel("新密码", { exact: true }).fill(password);
  await dialog.getByLabel("确认新密码", { exact: true }).fill(confirmation);
}

export const accountLayoutScenes = ["账号栏", "对话框内成功", "成功关闭后账号栏", "修改密码对话框", "成员三按钮", "重置密码对话框", "停用成员重置对话框", "候选待处理", "候选已忽略"] as const;
export const accountAxeScenes = [...accountLayoutScenes, "当前密码错误", "新密码字段错误", "改密码结果未确认", "重置密码字段错误", "重置密码成功提示", "重置密码失败提示", "候选已忽略空态"] as const;
export type AccountScene = (typeof accountAxeScenes)[number];

/** 响应式、axe 共用同一套真实交互；响应和种子仍由项目既有样本辅助提供。 */
export async function openAccountScene(page: Page, mock: ApiMock, scene: AccountScene): Promise<void> {
  if (scene.startsWith("候选")) {
    seedArtistsE2E(mock);
    await page.goto("/artist-candidates");
    await expect(page.getByRole("button", { name: /^忽略“/ })).toBeVisible();
    if (scene === "候选待处理") return;
    if (scene !== "候选已忽略空态") {
      await page.getByRole("button", { name: /^忽略“/ }).click();
      await expect(page.getByText("现在没有需要确认的候选。", { exact: true })).toBeVisible();
    }
    await page.getByRole("navigation", { name: "候选状态" }).getByRole("link", { name: "已忽略", exact: true }).click();
    await expect(page.getByRole("navigation", { name: "候选状态" }).getByRole("link", { name: "已忽略", exact: true })).toHaveAttribute("aria-current", "page");
    if (scene === "候选已忽略空态") await expect(page.getByText("没有忽略过的候选。", { exact: true })).toBeVisible();
    else await expect(page.getByRole("button", { name: /^恢复“/ })).toBeVisible();
    return;
  }
  if (scene.startsWith("成员") || scene.includes("重置")) {
    await page.goto("/settings/members");
    await expect(memberRow(page, "listener").getByRole("button")).toHaveCount(3);
    if (scene === "成员三按钮") {
      await memberRow(page, "listener").scrollIntoViewIfNeeded();
      return;
    }
    const username = scene === "停用成员重置对话框" ? "archivist" : "listener";
    await memberRow(page, username).getByRole("button", { name: `重置成员 ${username} 的密码`, exact: true }).click();
    const dialog = page.getByRole("dialog", { name: `重置“${username}”的密码`, exact: true });
    await expect(dialog).toBeVisible();
    if (scene === "重置密码对话框" || scene === "停用成员重置对话框") return;
    const password = scene === "重置密码字段错误" ? "short" : changedPassword;
    await dialog.getByLabel("新密码", { exact: true }).fill(password);
    await dialog.getByLabel("确认新密码", { exact: true }).fill(password);
    if (scene === "重置密码失败提示") {
      const sample = loadApiSample("errors/not-found-user");
      await page.route("**/api/v1/users/*/password", (route) => route.fulfill({ status: sample.status, json: sample.body }));
    }
    await dialog.getByRole("button", { name: "重置密码", exact: true }).click();
    if (scene === "重置密码字段错误") await expect(dialog.getByText("密码至少要 12 位。", { exact: true })).toBeVisible();
    else if (scene === "重置密码成功提示") await expect(page.getByText("已重置“listener”的密码。", { exact: true })).toBeVisible();
    else await expect(page.getByRole("alert")).toContainText("成员已不存在，列表已刷新。");
    return;
  }
  await page.goto("/settings/account");
  await expect(heading(page, "账号")).toBeVisible();
  if (scene === "账号栏") {
    await expect(page.getByRole("navigation", { name: "设置", exact: true }).getByRole("link", { name: "账号", exact: true })).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("main")).toContainText("curator");
    return;
  }
  const dialog = await openPasswordDialog(page);
  if (scene === "修改密码对话框") return;
  await fillPasswordChange(page, scene === "当前密码错误" ? "wrong-current-password" : "curator-password", scene === "新密码字段错误" ? "short" : changedPassword);
  if (scene === "改密码结果未确认") {
    const sample = loadApiSample("errors/database-unavailable");
    await page.route("**/api/v1/auth/password", (route) => route.fulfill({ status: sample.status, json: sample.body }));
  }
  await dialog.getByRole("button", { name: "修改密码", exact: true }).click();
  if (scene === "当前密码错误") await expect(dialog.getByText("当前密码不对。", { exact: true })).toBeVisible();
  else if (scene === "新密码字段错误") await expect(dialog.getByText("密码至少要 12 位。", { exact: true })).toBeVisible();
  else if (scene === "改密码结果未确认") await expect(dialog.getByRole("alert")).toContainText("结果未确认");
  else {
    const done = await expectPasswordChanged(page);
    if (scene === "成功关闭后账号栏") {
      await done.press("Enter");
      await expect(dialog).toBeHidden();
      await expect(page.getByRole("main")).not.toContainText("密码已修改。");
      await expect(page.getByText("密码已修改。", { exact: true })).toHaveCount(0);
    }
  }
}

/** 成功结果不是 live region：焦点移到唯一的“完成”，由其描述关联读出结果。 */
export async function expectPasswordChanged(page: Page): Promise<Locator> {
  const dialog = passwordDialog(page);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "修改密码", exact: true })).toBeVisible();
  const result = dialog.getByText("密码已修改。", { exact: true });
  await expect(result).toBeVisible();
  await expect(dialog.getByRole("status")).toHaveCount(0);
  await expect(result).not.toHaveAttribute("role", "status");
  await expect(dialog.locator("input[type=password]")).toHaveCount(0);
  const done = dialog.getByRole("button", { name: "完成", exact: true });
  await expect(done).toBeFocused();
  await expect(done).toHaveAccessibleDescription("密码已修改。");
  const resultId = await result.getAttribute("id");
  if (resultId === null || resultId === "") throw new Error("成功结果必须有 id，供完成按钮关联描述");
  await expect(done).toHaveAttribute("aria-describedby", resultId);
  return done;
}

/** 不借助滚动把用户按钮移进来：整个按钮（包括底边）原本就在视口里。 */
export async function expectLogoutInViewport(page: Page): Promise<void> {
  const bounds = await userTrigger(page).boundingBox();
  const viewport = page.viewportSize();
  if (bounds === null || viewport === null) throw new Error("用户按钮与视口必须有可测量边界");
  expect(bounds.x, "用户按钮左边界").toBeGreaterThanOrEqual(0);
  expect(bounds.y, "用户按钮上边界").toBeGreaterThanOrEqual(0);
  expect(bounds.width, "用户按钮宽度").toBeGreaterThan(0);
  expect(bounds.height, "用户按钮高度").toBeGreaterThan(0);
  expect(bounds.x + bounds.width, "用户按钮右边界").toBeLessThanOrEqual(viewport.width);
  expect(bounds.y + bounds.height, "用户按钮底边界").toBeLessThanOrEqual(viewport.height);
}
