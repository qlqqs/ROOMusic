import { expect, type Page } from "@playwright/test";
import { artistKeyActivate as keyActivate } from "./artists";
import { heading } from "./locators";

export const userTrigger = (page: Page) => page.getByRole("region", { name: "当前用户" }).getByRole("button", { name: "curator 管理员", exact: true });

/** 从可见的外壳入口操作用户菜单，窄屏必须先打开模态导航。 */
export async function openUserMenu(page: Page): Promise<void> {
  await expect(page.getByRole("main")).toBeVisible();
  const drawerTrigger = page.getByRole("button", { name: "菜单", exact: true });
  if (await drawerTrigger.isVisible() && !await page.getByRole("dialog", { name: "菜单", exact: true }).isVisible()) await keyActivate(page, drawerTrigger);
  await keyActivate(page, userTrigger(page));
  await expect(page.getByRole("menu", { name: "账号", exact: true })).toBeVisible();
}

export async function openSettings(page: Page): Promise<void> {
  await openUserMenu(page);
  await keyActivate(page, page.getByRole("menuitem", { name: "设置", exact: true }));
  await expect(heading(page, "账号")).toBeVisible();
  await expect(page).toHaveURL(/\/settings\/account$/);
  await expect(page.getByRole("dialog", { name: "菜单", exact: true })).toBeHidden();
}

export async function logoutFromMenu(page: Page): Promise<void> {
  await openUserMenu(page);
  await page.getByRole("menuitem", { name: "退出登录", exact: true }).click();
}

export const settingsScenes = ["设置账号", "设置备份", "备份删除确认框", "备份批量选择", "备份批量删除确认框", "自动清理确认框", "用户菜单"] as const;
export type SettingsScene = (typeof settingsScenes)[number];

/** axe 与响应式检查共用状态，所有确认框都由实际交互打开。 */
export async function openSettingsScene(page: Page, scene: SettingsScene): Promise<void> {
  if (scene === "用户菜单") {
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();
    await openUserMenu(page);
    const drawer = page.getByRole("dialog", { name: "菜单", exact: true });
    if (await drawer.isVisible()) await expect(drawer.getByRole("menu", { name: "账号", exact: true })).toBeVisible();
    return;
  }
  if (scene === "设置账号") {
    await page.goto("/settings/account");
    await expect(heading(page, "账号")).toBeVisible();
    return;
  }
  await page.goto("/settings/backups");
  await expect(heading(page, "备份")).toBeVisible();
  const files = page.getByRole("region", { name: "备份文件", exact: true });
  await expect(files.getByRole("table")).toBeVisible();
  if (scene === "备份删除确认框") {
    await files.getByRole("button", { name: /^删除 .* 的备份$/ }).first().click();
    await expect(page.getByRole("dialog", { name: /^删除 .* 的备份？$/ })).toBeVisible();
  } else if (scene === "备份批量选择" || scene === "备份批量删除确认框") {
    await files.getByRole("checkbox", { name: "全选", exact: true }).check();
    if (scene === "备份批量删除确认框") {
      await files.getByRole("button", { name: /^删除所选/ }).click();
      await expect(page.getByRole("dialog", { name: /^删除 \d+ 份备份？$/ })).toBeVisible();
    }
  } else if (scene === "自动清理确认框") {
    const retention = page.getByRole("region", { name: "自动清理", exact: true });
    await retention.getByRole("group", { name: "保留天数", exact: true }).getByRole("checkbox", { name: "不限" }).check();
    await retention.getByRole("group", { name: "保留份数", exact: true }).getByRole("checkbox", { name: "不限" }).uncheck();
    await retention.getByRole("spinbutton", { name: "保留份数", exact: true }).fill("1");
    await retention.getByRole("button", { name: "保存", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "保存清理设置？", exact: true })).toBeVisible();
  }
}
