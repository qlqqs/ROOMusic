import type { Locator } from "@playwright/test";

/**
 * 精确指针设备上，曲目行的播放、心形与“更多”平时透明且不接收指针（pointer-events: none），鼠标移到所在行上才出现。
 * Playwright 点击前先检查命中目标，这时鼠标还没移过去，命中的是外层容器，点击会一直重试。
 * 所以先把鼠标移到按钮的位置（经过所在行，和用户的鼠标一样），再正常点击：点击仍做完整的可见与命中检查。
 */
export async function clickRowAction(button: Locator): Promise<void> {
  await button.hover({ force: true });
  await button.click();
}
