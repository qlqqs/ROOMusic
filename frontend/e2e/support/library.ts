import { expect, type Locator, type Page } from "@playwright/test";

// 扫描目录页的“音乐目录”卡片：每个目录一行（列表项，不是表格），停用、恢复、改路径与不扫描的文件夹在行尾“目录 名 的更多操作”菜单里；
// 添加目录经卡片标题行的“添加目录…”打开的对话框完成。

export const rootList = (page: Page) => page.getByRole("list", { name: "音乐目录列表", exact: true });

/** 名为 name 的目录那一行。 */
export const rootItem = (page: Page, name: string) => rootList(page).getByRole("listitem").filter({ has: page.getByText(name, { exact: true }) });

/** 目录行尾的“更多”。 */
export const rootMenuButton = (page: Page, name: string) => rootList(page).getByRole("button", { name: `目录 ${name} 的更多操作`, exact: true });

/** 打开目录行尾的“更多”并选择一项（停用目录、恢复目录、改路径…、不扫描的文件夹…）。 */
export async function chooseRootAction(page: Page, name: string, action: string): Promise<void> {
  await rootMenuButton(page, name).click();
  await page.getByRole("menu", { name: `目录 ${name} 的更多操作`, exact: true }).getByRole("menuitem", { name: action, exact: true }).click();
}

export const addRootDialog = (page: Page) => page.getByRole("dialog", { name: "添加音乐目录", exact: true });

/** 点“添加目录…”打开添加对话框，返回对话框。 */
export async function openAddRoot(page: Page): Promise<Locator> {
  await page.getByRole("button", { name: "添加目录…", exact: true }).click();
  const dialog = addRootDialog(page);
  await expect(dialog).toBeVisible();
  return dialog;
}
