import AxeBuilder from "@axe-core/playwright";
import { expect, type Locator, type Page } from "@playwright/test";

/** 设计要求验证的五种宽度（DESIGN.md 响应式一节）。 */
export const widths = [375, 768, 1024, 1280, 1440] as const;

/** 页面与主内容区都不能产生横向滚动：主内容区在宽屏下是自己的滚动容器，页面级检查看不到它里面的溢出。 */
export async function expectNoHorizontalOverflow(page: Page, label: string): Promise<void> {
  const overflow = await page.evaluate(() => {
    const main = document.getElementById("main");
    return {
      page: document.documentElement.scrollWidth - window.innerWidth,
      main: main === null ? 0 : main.scrollWidth - main.clientWidth,
    };
  });
  expect(overflow.page, `${label}：页面横向溢出 ${overflow.page}px`).toBeLessThanOrEqual(0);
  expect(overflow.main, `${label}：主内容区横向溢出 ${overflow.main}px`).toBeLessThanOrEqual(0);
}

/**
 * 桌面外壳里只有主内容区自己滚动，窗口本身不能再滚动：否则会滚出一片空白。
 * 典型原因是“仅供读屏”的绝对定位元素（VisuallyHidden、表格的 caption）没有定位祖先：它们的包含块是整个文档，
 * 不受主内容区裁剪，长页面下半部分的这类元素会把文档撑得比窗口还高。窄屏的外壳让整页滚动，不在检查范围内。
 */
export async function expectNoDocumentScroll(page: Page, label: string): Promise<void> {
  const extra = await page.evaluate(() => {
    const main = document.getElementById("main");
    const mainScrolls = main !== null && getComputedStyle(main).overflowY === "auto";
    return mainScrolls ? document.documentElement.scrollHeight - window.innerHeight : 0;
  });
  expect(extra, `${label}：窗口本身还能再滚动 ${extra}px`).toBeLessThanOrEqual(0);
}

const axeTags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

/** axe 扫描（WCAG 2.1 A 与 AA）：serious 或 critical 即失败，失败信息列出规则、说明与元素。 */
export async function expectNoSeriousViolations(page: Page, label: string): Promise<void> {
  const { violations } = await new AxeBuilder({ page }).withTags(axeTags).analyze();
  const serious = violations.filter((violation) => violation.impact === "serious" || violation.impact === "critical");
  const described = serious.map((violation) => `${violation.id}: ${violation.help}（${violation.nodes.map((node) => node.target.join(" ")).join("；")}）`);
  expect(described, `${label}：axe 发现严重问题`).toEqual([]);
}

/** 所有有文字的元素的计算字号都不小于 minimum（设计的最小字号 12px）。 */
export async function expectMinimumFontSize(page: Page, label: string, minimum = 12): Promise<void> {
  const tooSmall = await page.evaluate((limit) => {
    const found: string[] = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
      const text = (node.textContent ?? "").trim();
      const element = node.parentElement;
      if (text === "" || element === null) continue;
      const size = Number.parseFloat(getComputedStyle(element).fontSize);
      if (size < limit) found.push(`<${element.tagName.toLowerCase()}> ${size}px “${text.slice(0, 20)}”`);
    }
    return found;
  }, minimum);
  expect(tooSmall, `${label}：字号不应小于 ${minimum}px`).toEqual([]);
}

/** 焦点环可见：当前聚焦元素有实线轮廓，宽度不小于 2px。 */
export async function expectVisibleFocusRing(page: Page, label: string): Promise<void> {
  const ring = await page.evaluate(() => {
    const element = document.activeElement;
    if (element === null) return null;
    const style = getComputedStyle(element);
    return { style: style.outlineStyle, width: Number.parseFloat(style.outlineWidth) };
  });
  expect(ring, `${label}：没有聚焦元素`).not.toBeNull();
  expect(ring?.style, `${label}：焦点环样式`).not.toBe("none");
  expect(ring?.width ?? 0, `${label}：焦点环宽度`).toBeGreaterThanOrEqual(2);
}

/** 只量可见文字的实际行数：按钮有最小高度，用高度判断会漏掉中文被挤成两行。 */
export async function expectSingleTextLine(locator: Locator, includeAriaHidden = false) {
  const lines = await locator.evaluate((element, includeHidden) => {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const tops: number[] = [];
    let node: Node | null;
    while ((node = walker.nextNode()) !== null) {
      if (!node.textContent?.trim()) continue;
      const parent = node.parentElement;
      if ((!includeHidden && parent?.closest('[aria-hidden="true"]')) || (parent && getComputedStyle(parent).position === "absolute")) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      for (const rect of range.getClientRects()) {
        if (rect.width > 0 && rect.height > 0 && !tops.some((top) => Math.abs(top - rect.top) < 2)) tops.push(rect.top);
      }
    }
    return tops.length;
  }, includeAriaHidden);
  expect(lines, `${await locator.innerText()}：可见文字保持单行`).toBe(1);
}
