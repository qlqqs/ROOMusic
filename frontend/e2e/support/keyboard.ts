import { expect, type Locator, type Page } from "@playwright/test";

/**
 * 原生模态 <dialog> 把背后的页面设为 inert：Tab 走到末尾时焦点会交给浏览器界面（页面里表现为 body），
 * 但绝不会落到对话框外的页面元素上。这里断言焦点始终在对话框内或在 body，并且至少有两次落在对话框内的不同元素上。
 */
export async function expectFocusStaysInDialog(page: Page, dialog: Locator, tabs: number): Promise<void> {
  const insideTargets = new Set<string>();
  for (let step = 0; step < tabs; step += 1) {
    await page.keyboard.press("Tab");
    const state = await dialog.evaluate((element) => {
      const active = document.activeElement;
      return {
        inside: active !== null && element.contains(active),
        onBody: active === document.body,
        label: active?.getAttribute("aria-label") ?? active?.textContent?.trim() ?? "",
      };
    });
    expect(state.inside || state.onBody, `第 ${step + 1} 次 Tab 后焦点落到了对话框外的页面元素上：${state.label}`).toBe(true);
    if (state.inside) insideTargets.add(state.label);
  }
  expect(insideTargets.size, "焦点应在对话框内的多个元素之间移动").toBeGreaterThanOrEqual(2);
}

/** 聚焦元素的名称：优先 aria-label，表单控件取 label 的文字，其余取自己的文字。 */
export const focusedName = (page: Page) =>
  page.evaluate(() => {
    const element = document.activeElement;
    // 表单控件的名称来自它的 label，而不是自己的文字。
    const label = element instanceof HTMLInputElement ? element.labels?.[0]?.textContent?.trim() : undefined;
    return element?.getAttribute("aria-label") ?? label ?? element?.textContent?.trim() ?? element?.tagName ?? "";
  });

/** 一直按 Tab，直到聚焦元素的名称满足条件（最多 limit 次）：失败时说明最后停在了哪里。 */
export async function tabTo(page: Page, matches: (name: string) => boolean, limit = 12): Promise<void> {
  for (let step = 0; step < limit; step += 1) {
    await page.keyboard.press("Tab");
    if (matches(await focusedName(page))) return;
  }
  throw new Error(`按了 ${limit} 次 Tab 仍没有到达目标，最后聚焦的是“${await focusedName(page)}”`);
}
