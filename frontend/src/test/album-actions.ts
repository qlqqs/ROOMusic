import { screen, waitFor, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";

// 专辑详情页头的“更多操作”菜单：下一首播放、加入队列，管理员另有“管理”分组（编辑发行信息、设定默认版本、归组、查看问题）。
// 曲目行也有同名的“更多操作”，所以都限定在 h1 所在的页头里。

/** 页头里的“更多操作”按钮；页头或按钮还没出现时抛错（配合 waitFor）。同一个测试里渲染过别的页面时，取带这个按钮的那个页头。 */
export function albumMoreButton(): HTMLElement {
  for (const heading of screen.getAllByRole("heading", { level: 1 })) {
    const header = heading.closest("header");
    const button = header === null ? null : within(header).queryByRole("button", { name: "更多操作" });
    if (button !== null) return button;
  }
  throw new Error("专辑页头里没有“更多操作”");
}

/** 打开页头的“更多操作”，返回菜单。 */
export async function openAlbumMenu(user: UserEvent): Promise<HTMLElement> {
  await user.click(await waitFor(albumMoreButton));
  return screen.getByRole("menu", { name: "更多操作" });
}

/** 打开页头的“更多操作”并选一项（等到这一项出现）。 */
export async function chooseAlbumAction(user: UserEvent, name: string | RegExp): Promise<void> {
  const menu = await openAlbumMenu(user);
  await user.click(await within(menu).findByRole("menuitem", { name }));
}

/**
 * 打开页头的“更多操作”，等 ready 这一项出现（数据到齐），读出“管理”分组里各项的名字，再按 Esc 关上。
 * 普通成员没有“管理”分组，返回空数组。
 */
export async function albumAdminActions(user: UserEvent, ready: string = "加入队列"): Promise<string[]> {
  const menu = await openAlbumMenu(user);
  await within(menu).findByRole("menuitem", { name: ready });
  const group = within(menu).queryByRole("group", { name: "管理" });
  const names = group === null ? [] : within(group).getAllByRole("menuitem").map((item) => item.textContent);
  await user.keyboard("{Escape}");
  return names;
}
