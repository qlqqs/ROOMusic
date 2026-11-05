import { expect, type Locator, type Page } from "@playwright/test";
import { loadApiSample } from "../../src/test/api-samples";
import type { ApiMock } from "./api-mock";
import { seedArtistsE2E } from "./artist-seed";
import { artistKeyActivate as keyActivate } from "./artists";
import { heading } from "./locators";

export const changedPassword = "changed-password-2026";
export const passwordDialog = (page: Page) => page.getByRole("dialog", { name: "修改密码", exact: true });
export const memberRow = (page: Page, username: string) => page.getByRole("table", { name: "成员列表" }).getByRole("row", { name: new RegExp(username) });

/** 窄屏从真实菜单进入身份区，保留菜单模态，不绕过响应式导航。 */
export async function openPasswordDialog(page: Page): Promise<Locator> {
  const menu = page.getByRole("button", { name: "菜单", exact: true });
  if (await menu.isVisible()) await keyActivate(page, menu);
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

export const accountLayoutScenes = ["身份区", "身份区成功提示", "修改密码对话框", "成员三按钮", "重置密码对话框", "停用成员重置对话框", "候选待处理", "候选已忽略"] as const;
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
    await page.goto("/members");
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
  await page.goto("/");
  await expect(heading(page, "概览")).toBeVisible();
  if (scene === "身份区") {
    if (await page.getByRole("button", { name: "菜单", exact: true }).isVisible()) await keyActivate(page, page.getByRole("button", { name: "菜单", exact: true }));
    await expect(page.getByRole("region", { name: "当前用户" })).toContainText("curator");
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
  else await expect(page.getByRole("region", { name: "当前用户" }).getByRole("status")).toContainText("密码已修改。");
}
