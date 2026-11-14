import { expect, type Page } from "@playwright/test";
import { focusedName } from "./keyboard";

// 专辑详情页头：“‹ 专辑”、封面、标题与一条操作行（圆形播放、随机播放、收藏、添加到歌单、“更多操作”）。
// “更多操作”里是下一首播放、加入队列，管理员另有“管理”分组（编辑发行信息、设为默认版本……）。曲目行也有名为“更多操作”的按钮，
// 所以这里都限定在页头里找。

/** 专辑详情的页头：含唯一 h1 的那个 header。 */
export const albumHeader = (page: Page) => page.locator("header", { has: page.getByRole("heading", { level: 1 }) });

/** 页头操作行里的“更多操作”。 */
export const albumMoreButton = (page: Page) => albumHeader(page).getByRole("button", { name: "更多操作", exact: true });

/** 打开着的“更多操作”菜单。 */
export const openMenu = (page: Page) => page.getByRole("menu", { name: "更多操作", exact: true });

/** 用鼠标打开页头的“更多操作”并选择一项。 */
export async function chooseAlbumAction(page: Page, name: string | RegExp): Promise<void> {
  await albumMoreButton(page).click();
  await openMenu(page).getByRole("menuitem", { name, exact: typeof name === "string" }).click();
}

/** 焦点已在页头的“更多操作”上：Enter 打开菜单，方向键移到这一项（最多 limit 次），停在这一项上，不选择。 */
export async function arrowToAlbumAction(page: Page, name: string, limit = 10): Promise<void> {
  await expect(albumMoreButton(page)).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(openMenu(page)).toBeVisible();
  for (let step = 0; step <= limit; step += 1) {
    if ((await focusedName(page)) === name) return;
    await page.keyboard.press("ArrowDown");
  }
  throw new Error(`方向键按了 ${limit} 次仍没有到达菜单项“${name}”，最后聚焦的是“${await focusedName(page)}”`);
}
