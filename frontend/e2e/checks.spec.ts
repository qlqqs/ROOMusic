import { expect, test } from "@playwright/test";
import { expectMinimumFontSize, expectNoDocumentScroll, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing } from "./support/checks";

// 自检：一组检查全部通过并不说明页面没问题，也可能是检查本身没有生效。
// 这里用故意有问题的页面确认每项检查会失败，用干净的页面确认它们不会误报。

const clean = `<main id="main"><h1>标题</h1><p style="font-size:16px">正文</p><button style="outline:2px solid #000">按钮</button></main>`;

test.describe("检查工具自检", () => {
  test("axe：缺少替代文字的图片会被报告，干净页面通过", async ({ page }) => {
    await page.setContent(`<html lang="zh-CN"><head><title>t</title></head><body>${clean}<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw="></body></html>`);
    await expect(expectNoSeriousViolations(page, "自检")).rejects.toThrow(/image-alt/);

    await page.setContent(`<html lang="zh-CN"><head><title>t</title></head><body>${clean}</body></html>`);
    await expectNoSeriousViolations(page, "自检");
  });

  test("最小字号：小于 12px 的文字会被报告", async ({ page }) => {
    await page.setContent(`${clean}<small style="font-size:11px">细小文字</small>`);
    await expect(expectMinimumFontSize(page, "自检")).rejects.toThrow(/11px/);

    await page.setContent(`${clean}<small style="font-size:12px">刚好够大</small>`);
    await expectMinimumFontSize(page, "自检");
  });

  test("横向溢出：页面与主内容区内部的溢出都会被报告", async ({ page }) => {
    await page.setContent(`<main id="main" style="width:200px;overflow:auto"><div style="width:500px">宽内容</div></main>`);
    await expect(expectNoHorizontalOverflow(page, "自检")).rejects.toThrow(/主内容区横向溢出/);

    await page.setContent(`<main id="main"><div style="width:5000px">宽内容</div></main>`);
    await expect(expectNoHorizontalOverflow(page, "自检")).rejects.toThrow(/页面横向溢出/);

    await page.setContent(clean);
    await expectNoHorizontalOverflow(page, "自检");
  });

  test("窗口纵向滚动：主内容区自己滚动时，文档比窗口高会被报告", async ({ page }) => {
    const shell = (extra: string) =>
      `<!doctype html><style>html,body{margin:0;height:100%}</style><main id="main" style="height:100%;overflow-y:auto"><div style="height:3000px">长内容</div></main>${extra}`;
    // 一个不在主内容区里、也没有被裁剪的元素把文档撑高：窗口本身出现了滚动条。
    await page.setContent(shell(`<div style="position:absolute;top:3000px;width:1px;height:1px"></div>`));
    await expect(expectNoDocumentScroll(page, "自检")).rejects.toThrow(/窗口本身还能再滚动/);

    // 只有主内容区滚动：通过。
    await page.setContent(shell(""));
    await expectNoDocumentScroll(page, "自检");

    // 主内容区不是滚动容器（窄屏整页滚动）时不检查。
    await page.setContent(`<!doctype html><main id="main"><div style="height:3000px">长内容</div></main>`);
    await expectNoDocumentScroll(page, "自检");
  });

  test("焦点环：没有轮廓的聚焦元素会被报告", async ({ page }) => {
    await page.setContent(`<button style="outline:none">按钮</button>`);
    await page.keyboard.press("Tab");
    await expect(expectVisibleFocusRing(page, "自检")).rejects.toThrow(/焦点环样式/);

    await page.setContent(clean);
    await page.keyboard.press("Tab");
    await expectVisibleFocusRing(page, "自检");
  });
});
