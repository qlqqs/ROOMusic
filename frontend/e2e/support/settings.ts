import { expect, type Page } from "@playwright/test";
import { artistKeyActivate as keyActivate } from "./artists";
import { heading } from "./locators";

/** 窄屏（<768px）没有侧栏：个人入口、设置与退出登录在底部标签栏的“我的”页里。 */
export const isNarrow = (page: Page) => (page.viewportSize()?.width ?? 1280) < 768;

/** 宽屏顶栏的头像按钮，读屏名是用户名与角色。 */
export const userTrigger = (page: Page) => page.getByRole("button", { name: /，(?:管理员|成员)$/ });
/** 窄屏底部标签栏（侧栏此时不显示，看得见的“主导航”只有它）。 */
export const tabBar = (page: Page) => page.getByRole("navigation", { name: "主导航", exact: true });

/** 宽屏：用键盘从头像打开“账号”菜单。 */
export async function openUserMenu(page: Page): Promise<void> {
  await expect(page.getByRole("main")).toBeVisible();
  await keyActivate(page, userTrigger(page));
  await expect(page.getByRole("menu", { name: "账号", exact: true })).toBeVisible();
}

/** 窄屏：从底部标签栏进入“我的”页。 */
export async function openMePage(page: Page): Promise<void> {
  await expect(page.getByRole("main")).toBeVisible();
  await keyActivate(page, tabBar(page).getByRole("link", { name: "我的", exact: true }));
  await expect(heading(page, "我的")).toBeVisible();
  await expect(page).toHaveURL(/\/me$/);
}

/** 进入设置（账号栏）：宽屏从头像菜单进入，窄屏经“我的”页的“设置”。 */
export async function openSettings(page: Page): Promise<void> {
  if (isNarrow(page)) {
    await openMePage(page);
    await keyActivate(page, page.getByRole("main").getByRole("link", { name: "设置", exact: true }));
  } else {
    await openUserMenu(page);
    await page.keyboard.press("ArrowDown");
    await expect(page.getByRole("menuitem", { name: "设置", exact: true })).toBeFocused();
    await page.keyboard.press("Enter");
  }
  await expect(heading(page, "账号")).toBeVisible();
  await expect(page).toHaveURL(/\/settings\/account$/);
}

/** 退出登录：宽屏从头像菜单，窄屏从“我的”页。 */
export async function logoutFromMenu(page: Page): Promise<void> {
  if (isNarrow(page)) {
    await openMePage(page);
    await page.getByRole("main").getByRole("button", { name: "退出登录", exact: true }).click();
    return;
  }
  await openUserMenu(page);
  await page.getByRole("menuitem", { name: "退出登录", exact: true }).click();
}

export const settingsScenes = ["设置账号", "设置备份", "备份行菜单", "备份删除确认框", "备份批量选择", "备份批量删除确认框", "自动清理确认框", "用户菜单"] as const;
export type SettingsScene = (typeof settingsScenes)[number];

/** axe 与响应式检查共用状态，所有确认框都由实际交互打开。 */
export async function openSettingsScene(page: Page, scene: SettingsScene): Promise<void> {
  // 宽屏打开头像菜单，窄屏进入“我的”页。
  if (scene === "用户菜单") {
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();
    if (isNarrow(page)) await openMePage(page);
    else await openUserMenu(page);
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
  if (scene === "备份行菜单" || scene === "备份删除确认框") {
    await files.getByRole("button", { name: /的备份的更多操作$/ }).first().click();
    const menu = page.getByRole("menu", { name: /的备份的更多操作$/ });
    await expect(menu).toBeVisible();
    if (scene === "备份行菜单") return;
    await menu.getByRole("menuitem", { name: "删除…", exact: true }).click();
    await expect(page.getByRole("dialog", { name: /^删除 .* 的备份？$/ })).toBeVisible();
  } else if (scene === "备份批量选择" || scene === "备份批量删除确认框") {
    await files.getByRole("checkbox", { name: "全选", exact: true }).check();
    if (scene === "备份批量删除确认框") {
      await files.getByRole("button", { name: /^删除\s*所选的/ }).click();
      await expect(page.getByRole("dialog", { name: /^删除 \d+ 份备份？$/ })).toBeVisible();
    }
  } else if (scene === "自动清理确认框") {
    const retention = page.getByRole("region", { name: "自动清理", exact: true });
    // 即改即存：勾上“不限”立即保存；取消份数的“不限”、填 1 后回车保存，会删除旧备份时弹出确认。
    await retention.getByRole("group", { name: "保留天数", exact: true }).getByRole("checkbox", { name: "不限" }).check();
    await expect(retention.locator('p[role="status"]')).toHaveText("已保存。");
    await retention.getByRole("group", { name: "保留份数", exact: true }).getByRole("checkbox", { name: "不限" }).uncheck();
    const count = retention.getByRole("spinbutton", { name: "保留份数", exact: true });
    await count.fill("1");
    await count.press("Enter");
    await expect(page.getByRole("dialog", { name: "保存清理设置？", exact: true })).toBeVisible();
  }
}
