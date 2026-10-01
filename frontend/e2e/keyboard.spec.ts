import type { Locator, Page } from "@playwright/test";
import { expectVisibleFocusRing } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading, mainNav } from "./support/locators";

// 只用键盘完成主要路径：登录、跳到主要内容、导航、停用确认对话框（焦点进入、约束、Esc 关闭、焦点恢复）、窄屏菜单。

/**
 * 原生模态 <dialog> 把背后的页面设为 inert：Tab 走到末尾时焦点会交给浏览器界面（页面里表现为 body），
 * 但绝不会落到对话框外的页面元素上。这里断言焦点始终在对话框内或在 body，并且至少有两次落在对话框内的不同元素上。
 */
async function expectFocusStaysInDialog(page: Page, dialog: Locator, tabs: number): Promise<void> {
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

const focusedName = (page: Page) =>
  page.evaluate(() => {
    const element = document.activeElement;
    return element?.getAttribute("aria-label") ?? element?.textContent?.trim() ?? element?.tagName ?? "";
  });

test.describe("键盘", () => {
  test("只用键盘登录：Tab 到各字段，Enter 提交，焦点环可见", async ({ page, backend }) => {
    await backend.start();
    await page.goto("/");
    await expect(heading(page, "登录")).toBeVisible();

    await page.keyboard.press("Tab");
    await expect(page.getByLabel("用户名")).toBeFocused();
    await expectVisibleFocusRing(page, "用户名输入框");
    await page.keyboard.type("curator");
    await page.keyboard.press("Tab");
    await expect(page.getByLabel("密码")).toBeFocused();
    await page.keyboard.type("curator-password");
    await page.keyboard.press("Enter");
    await expect(heading(page, "概览")).toBeVisible();
  });

  test("跳转链接是第一个可聚焦元素，激活后焦点进入主内容区；导航用链接，Enter 激活", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();

    await page.keyboard.press("Tab");
    await expect(page.getByRole("link", { name: "跳到主要内容" })).toBeFocused();
    await expectVisibleFocusRing(page, "跳转链接");
    await page.keyboard.press("Enter");
    await expect(page.locator("main#main")).toBeFocused();

    await mainNav(page).getByRole("link", { name: "成员" }).focus();
    await expectVisibleFocusRing(page, "导航链接");
    await page.keyboard.press("Enter");
    await expect(heading(page, "成员")).toBeVisible();
    // 导航后焦点回到主内容区，读屏用户得到页面变化的反馈。
    await expect(page.locator("main#main")).toBeFocused();
  });

  test("停用确认对话框：焦点进入且被约束，Esc 关闭并把焦点还给触发按钮，Enter 确认", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/library");
    const trigger = page.getByRole("button", { name: "停用目录 classical-a" });
    await expect(trigger).toBeVisible();
    await trigger.focus();
    await page.keyboard.press("Enter");

    const dialog = page.getByRole("dialog", { name: "停用目录“classical-a”？" });
    await expect(dialog).toBeVisible();
    // 初始焦点在“取消”上，避免回车直接确认。
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await expectVisibleFocusRing(page, "对话框的取消按钮");

    await expectFocusStaysInDialog(page, dialog, 6);

    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();

    // 再次打开，用键盘走到确认按钮并确认。
    await page.keyboard.press("Enter");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Tab");
    expect(await focusedName(page)).toBe("停用目录");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/已停用目录“classical-a”/)).toBeVisible();
    await expect(dialog).toBeHidden();
  });

  test("窄屏菜单：Enter 打开，焦点进入并被约束，Esc 关闭并把焦点还给“菜单”按钮；选择链接后菜单关闭", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto("/");
    const menu = page.getByRole("button", { name: "菜单" });
    await menu.focus();
    await page.keyboard.press("Enter");

    const dialog = page.getByRole("dialog", { name: "菜单" });
    await expect(dialog).toBeVisible();
    await expectFocusStaysInDialog(page, dialog, 6);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(menu).toBeFocused();

    await page.keyboard.press("Enter");
    await dialog.getByRole("link", { name: "扫描目录" }).focus();
    await page.keyboard.press("Enter");
    await expect(heading(page, "扫描目录")).toBeVisible();
    await expect(dialog).toBeHidden();
  });

  test("成员页：键盘完成新建（Tab 顺序与 Enter 提交）与对话框确认", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/members");
    await page.getByLabel("用户名").focus();
    await page.keyboard.type("keyboard-user");
    await page.keyboard.press("Tab");
    await page.keyboard.type("correct horse battery");
    await page.keyboard.press("Tab");
    await page.keyboard.type("correct horse battery");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/已创建成员“keyboard-user”/)).toBeVisible();

    const trigger = page.getByRole("button", { name: "撤销会话 keyboard-user" });
    await trigger.focus();
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "撤销成员“keyboard-user”的全部会话？" });
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  });
});
