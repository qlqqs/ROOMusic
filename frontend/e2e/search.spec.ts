import AxeBuilder from "@axe-core/playwright";
import type { Locator, Page } from "@playwright/test";
import { expect, test } from "./support/fixtures";
import { expectNoDocumentScroll, expectNoHorizontalOverflow, expectVisibleFocusRing } from "./support/checks";
import { installSearchPlayback, seedSearch } from "./support/search";
import { mediaState, observeMedia } from "./real/support";

const visibleSearch = (page: Page) => page.locator('input[data-global-search]:visible');

async function expectAxeClean(page: Page) {
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target) }))).toEqual([]);
}

async function textLines(element: Locator) {
  return element.evaluate((node) => {
    const tops = new Set<number>();
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
    for (let text = walker.nextNode(); text !== null; text = walker.nextNode()) {
      if (!text.textContent?.trim()) continue;
      const range = document.createRange();
      range.selectNodeContents(text);
      for (const rect of range.getClientRects()) if (rect.width > 0 && rect.height > 0) tops.add(Math.round(rect.top));
    }
    return tops.size;
  });
}

async function tabTo(page: Page, target: Locator) {
  for (let i = 0; i < 100; i += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) return;
    await page.keyboard.press("Tab");
  }
  throw new Error("纯键盘无法到达播放按钮");
}

// 设计的五宽度，加上交付截图要求的 390px。
for (const width of [320, 375, 390, 768, 1024, 1440]) {
  test(`${width}px：搜索框、长建议与两栏结果无溢出且 axe 无违规`, async ({ page, backend }, info) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const { album, track } = seedSearch(mock, true);
    await page.goto("/albums");
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
    await page.keyboard.press("/");
    const input = visibleSearch(page);
    await expect(input).toBeFocused();
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    if (width >= 768) {
      const rect = await input.boundingBox();
      expect(rect).not.toBeNull();
      expect(rect?.x).toBeGreaterThanOrEqual(0);
      expect((rect?.x ?? 0) + (rect?.width ?? 0)).toBeLessThanOrEqual(216);
    } else {
      await expect(page.getByRole("dialog", { name: "菜单" })).toBeVisible();
      expect(await page.getByRole("dialog").evaluate((node) => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(0);
    }
    await input.fill("爱");
    const popup = page.getByRole("listbox", { name: "搜索建议", exact: true });
    await expect(popup.getByRole("option")).toHaveCount(3);
    const rect = await popup.boundingBox();
    expect(rect?.width).toBeCloseTo(Math.min(380, width - 16), 0);
    expect(rect?.x).toBeGreaterThanOrEqual(8);
    expect((rect?.x ?? 0) + (rect?.width ?? 0)).toBeLessThanOrEqual(width - 8);
    expect(rect?.y).toBeGreaterThan((await input.boundingBox())?.y ?? 0);
    expect(await popup.evaluate((node) => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(0);
    // Range 也会返回被 line-clamp 裁掉的行；用真实可见高度÷行高量两行限制，原始行盒证明长夹具确实触发截断。
    const options = popup.getByRole("option");
    const fullTexts = [
      [album.group.representative.title, album.group.representative.artist, `作曲：${album.match.original}`],
      [track.title, `${track.artist} · ${track.release.title}`, `厂牌：${track.match.original}`],
    ];
    for (const [index, texts] of fullTexts.entries()) {
      const option = options.nth(index);
      const spans = option.locator(":scope > span");
      await expect(spans).toHaveCount(3);
      const primary = spans.nth(0);
      expect(await textLines(primary), "长标题应实际超过两行，避免夹具过短造成假通过").toBeGreaterThan(2);
      for (let line = 0; line < 3; line += 1) {
        const span = spans.nth(line);
        const expected = texts[line];
        if (expected === undefined) throw new Error("缺少建议文字夹具");
        await expect(span).toHaveText(expected);
        const metrics = await span.evaluate((node) => {
          const style = getComputedStyle(node);
          return { lines: node.getBoundingClientRect().height / Number.parseFloat(style.lineHeight),
            overflow: style.overflow, ellipsis: style.textOverflow, clamp: style.webkitLineClamp,
            clipped: node.scrollWidth > node.clientWidth };
        });
        expect(metrics.lines).toBeGreaterThan(0);
        expect(metrics.lines).toBeLessThanOrEqual((line === 0 ? 2 : 1) + 0.05);
        expect(metrics.overflow).toBe("hidden");
        if (line === 0) expect(metrics.clamp).toBe("2");
        else {
          expect(await textLines(span)).toBe(1);
          expect(metrics.ellipsis).toBe("ellipsis");
          expect(metrics.clipped, "副行与匹配原文足够长，确实产生省略号").toBe(true);
        }
      }
      // 不把截断后的视觉短串当作名称：读屏必须能读出完整的标题、艺人/专辑与命中原文。
      await expect(option).toHaveAccessibleName(texts.join(" ").replace(/\s+/g, " ").trim());
    }
    // 本夹具两条建议（每条2+1+1行）及分组/查看全部，不能再膨胀到旧版约500px。
    expect(rect?.height).toBeLessThanOrEqual(320);
    expect((rect?.y ?? 0) + (rect?.height ?? 0)).toBeLessThanOrEqual(892);
    const first = await popup.getByRole("option").nth(0).boundingBox();
    const second = await popup.getByRole("option").nth(1).boundingBox();
    expect(second?.y).toBeGreaterThanOrEqual((first?.y ?? 0) + (first?.height ?? 0));
    await expectNoHorizontalOverflow(page, "建议浮层");
    if (width === 1440 || width === 390) await page.screenshot({ path: info.outputPath(`search-${width}-suggestions.png`), fullPage: true });
    await expectAxeClean(page);
    await input.press("Enter");
    await expect(page).toHaveURL(/\/search\?q=/);
    await expect(page.getByRole("table", { name: "曲目结果" })).toBeVisible();
    const albums = page.getByRole("heading", { name: "专辑", exact: true });
    const tracks = page.getByRole("heading", { name: "曲目", exact: true });
    const albumBox = await albums.boundingBox();
    const trackBox = await tracks.boundingBox();
    expect(albumBox).not.toBeNull();
    expect(trackBox).not.toBeNull();
    expect(trackBox?.y).toBeGreaterThan((albumBox?.y ?? 0) + (albumBox?.height ?? 0));
    // D6 只约束建议：结果页曲目标题、艺人与匹配说明仍可自然换行。
    const table = page.getByRole("table", { name: "曲目结果" });
    for (const text of [track.title, track.artist, `厂牌：${track.match.original}`]) {
      const content = table.getByText(text, { exact: true });
      expect(await textLines(content)).toBeGreaterThan(1);
      expect(await content.evaluate((node) => getComputedStyle(node).webkitLineClamp)).toBe("none");
    }
    const action = page.getByRole("button", { name: /^播放 / });
    expect(await textLines(action)).toBe(1);
    await expectNoHorizontalOverflow(page, "两栏结果");
    await expectNoDocumentScroll(page, "两栏结果");
    if (width === 1440 || width === 390) await page.screenshot({ path: info.outputPath(`search-${width}-results.png`), fullPage: true });
    await expectAxeClean(page);
    // 时长是一个数值，不应被长标题或专辑名挤成“3:0 / 4”。保留截图复核发现的产品回归。
    const duration = page.getByRole("table", { name: "曲目结果" }).getByText("3:04", { exact: true });
    await expect(duration).toBeVisible();
    expect.soft(await textLines(duration), "曲目时长保持一行").toBe(1);
    if (width < 768) {
      await page.locator("#main").focus();
      await page.keyboard.press("/");
      await expect(page.locator('#main input[data-global-search]')).toBeFocused();
      await expect(page.getByRole("dialog")).toHaveCount(0);
    }
  });
}

for (const width of [1440, 390]) {
  test(`${width}px：纯键盘搜索并播放`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const { track } = seedSearch(mock);
    await installSearchPlayback(page);
    await observeMedia(page);
    await page.goto("/albums");
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
    await page.keyboard.press("/");
    await expect(visibleSearch(page)).toBeFocused();
    await page.keyboard.type("爱");
    const popup = page.getByRole("listbox", { name: "搜索建议", exact: true });
    await expect(popup.getByRole("option")).toHaveCount(3);
    await page.keyboard.press("ArrowUp");
    await expect(popup.getByRole("option", { name: /全部结果/ })).toHaveAttribute("aria-selected", "true");
    await page.keyboard.press("Enter");
    const play = page.getByRole("button", { name: /^播放 / });
    await expect(play).toBeEnabled();
    await tabTo(page, play);
    await expectVisibleFocusRing(page, "搜索播放按钮");
    await page.keyboard.press("Enter");
    await expect.poll(() => page.evaluate(() => window.playbackTestEvents.some((event) => event.type === "playing"))).toBe(true);
    expect((await mediaState(page))?.src).toContain(track.track_id);
    await expect(page.getByRole("region", { name: "播放器", exact: true })).toContainText(track.title);
  });
}

test("选择曲目建议只打开所在专辑，不直接播放", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { track } = seedSearch(mock);
  await page.goto("/albums");
  await visibleSearch(page).fill("爱");
  const popup = page.getByRole("listbox", { name: "搜索建议", exact: true });
  await expect(popup.getByRole("option")).toHaveCount(3);
  await visibleSearch(page).press("ArrowDown");
  await visibleSearch(page).press("ArrowDown");
  await visibleSearch(page).press("Enter");
  await expect(page).toHaveURL(new RegExp(`/albums/${track.group_id}\\?release=${track.release.id}`));
  await expect(page.getByRole("heading", { name: track.release.title, exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "播放整张专辑", exact: true })).toBeEnabled();
  await expect(page.getByRole("region", { name: "播放器", exact: true })).toHaveCount(0);
  expect(mock.callsTo("GET", /\/playback$/)).toHaveLength(0);
});
