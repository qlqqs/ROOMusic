import AxeBuilder from "@axe-core/playwright";
import { expect, type Locator, type Page } from "@playwright/test";
import { expectNoDocumentScroll, expectNoHorizontalOverflow } from "./checks";

export const artistWidths = [320, 375, 768, 1024, 1440] as const;
export const artistScenes = ["list", "detail", "merge", "split", "rename", "candidates", "favorites", "search"] as const;
export type ArtistScene = (typeof artistScenes)[number];
export type ArtistSceneData = {
  artist: { id: string; name: string };
  source: { id: string; name: string };
  query: string;
};

/** 真正沿 Tab 顺序前进，不用 focus()/click() 跳过不可达的控件；每个键盘流程共用。 */
export async function artistTabTo(page: Page, target: Locator, limit = 160): Promise<void> {
  await expect(target).toBeVisible();
  for (let index = 0; index < limit; index += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) return;
    await page.keyboard.press("Tab");
  }
  throw new Error(`纯键盘在 ${limit} 次 Tab 内不能到达 ${await target.getAttribute("aria-label") ?? await target.textContent()}`);
}

export async function artistKeyActivate(page: Page, target: Locator, key = "Enter"): Promise<void> {
  await expect(target).toBeEnabled();
  await artistTabTo(page, target);
  await page.keyboard.press(key);
}

export async function expectArtistLayout(page: Page, label: string): Promise<void> {
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await expectNoHorizontalOverflow(page, label);
  await expectNoDocumentScroll(page, label);
  for (const dialog of await page.getByRole("dialog").all()) {
    await expect(dialog).toBeVisible();
    expect(await dialog.evaluate((node) => node.scrollWidth - node.clientWidth), `${label}：对话框内部不溢出`).toBeLessThanOrEqual(0);
    const bounds = await dialog.boundingBox();
    expect(bounds, `${label}：对话框有可见边界`).not.toBeNull();
    expect(bounds?.x ?? -1).toBeGreaterThanOrEqual(0);
    expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(page.viewportSize()?.width ?? 0);
  }
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target) })), `${label}：axe 无违规`).toEqual([]);
}

/** 八个截图场景共用真实 UI 导航；种子由 seedArtistsE2E 提供，拆开场景须先 seed 合并来源。 */
export async function openArtistScene(page: Page, data: ArtistSceneData, scene: ArtistScene): Promise<void> {
  const { artist, source, query } = data;
  if (scene === "list") {
    await page.goto("/artists");
    await expect(page.getByRole("heading", { name: "全部艺人", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: artist.name, exact: true })).toBeVisible();
    return;
  }
  if (scene === "candidates") {
    await page.goto("/artist-candidates");
    await expect(page.getByRole("heading", { name: "重复艺人", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: /^忽略“/ }).first()).toBeVisible();
    return;
  }
  if (scene === "favorites") {
    await page.goto("/me/favorites?tab=artists");
    await expect(page.getByRole("heading", { name: "收藏的艺人", exact: true })).toBeVisible();
    await expect(page.locator("#main").getByRole("link", { name: artist.name, exact: true })).toBeVisible();
    return;
  }
  if (scene === "search") {
    await page.goto(`/search?q=${encodeURIComponent(query)}`);
    await expect(page.getByRole("heading", { name: "艺人", exact: true })).toBeVisible();
    await expect(page.locator("#main").getByRole("link", { name: artist.name, exact: true }).first()).toBeVisible();
    await expect(page.getByText(`别名：${query}`, { exact: true }).first()).toBeVisible();
    return;
  }
  await page.goto(`/artists/${artist.id}`);
  await expect(page.getByRole("heading", { name: artist.name, exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: `收藏“${artist.name}”`, exact: true })).toBeEnabled();
  if (scene === "detail") return;
  const actions = { merge: "合并艺人…", split: "拆开…", rename: "修改主名…" };
  const titles = { merge: "合并艺人", split: "拆开艺人", rename: "修改主名" };
  await page.getByRole("button", { name: actions[scene], exact: true }).click();
  const dialog = page.getByRole("dialog", { name: titles[scene], exact: true });
  await expect(dialog).toBeVisible();
  if (scene === "merge") {
    await dialog.getByRole("textbox", { name: "查找艺人", exact: true }).fill(source.name);
    await dialog.getByRole("textbox", { name: "查找艺人", exact: true }).press("Enter");
    await dialog.getByRole("radio", { name: new RegExp(source.name) }).check();
    await expect(dialog.getByRole("button", { name: "合并", exact: true })).toBeEnabled();
  }
  if (scene === "split") {
    await dialog.getByRole("button", { name: `拆回 ${source.name}`, exact: true }).click();
    await dialog.getByRole("button", { name: "预览", exact: true }).click();
    await expect(dialog.getByRole("button", { name: "拆开", exact: true })).toBeEnabled();
  }
}

/**
 * 艺人概况（“1 张专辑 · 2 首曲目 · 专辑艺人、曲目艺人”）里每一段各自占几行：数量（“1 张专辑”）与每个身份名（“曲目艺人”）。
 * 逐字取位置，一段里的字落在不同的行就是段内换行（例如“曲目艺 / 人”），那一段的行数大于 1。返回 [段文字, 行数]。
 */
export async function artistMetaSegmentLines(scope: Locator): Promise<[string, number][]> {
  return scope.evaluate((root) => {
    const count = /^[\d,]+ (?:张专辑|首曲目)$/;
    const role = /^(?:专辑艺人|曲目艺人|作曲|指挥|演奏|制作人)(?:、(?:专辑艺人|曲目艺人|作曲|指挥|演奏|制作人))*$/;
    /** 文字节点里 [start, end) 这些字落在几行（按每个字的上沿归行）。 */
    const linesOf = (node: Text, start: number, end: number) => {
      const tops: number[] = [];
      for (let i = start; i < end; i += 1) {
        const range = document.createRange();
        range.setStart(node, i);
        range.setEnd(node, i + 1);
        const rect = range.getBoundingClientRect();
        if (rect.width > 0 && rect.height > 0) tops.push(rect.top);
      }
      tops.sort((a, b) => a - b);
      let lines = 0;
      let last = -Infinity;
      for (const top of tops) {
        if (top - last > 4) lines += 1;
        last = top;
      }
      return lines;
    };
    const result: [string, number][] = [];
    for (const span of Array.from(root.querySelectorAll("span"))) {
      const node = span.firstChild;
      if (span.childNodes.length !== 1 || !(node instanceof Text)) continue;
      const text = node.data;
      if (count.test(text)) result.push([text, linesOf(node, 0, text.length)]);
      else if (role.test(text)) {
        let offset = 0;
        for (const label of text.split("、")) {
          result.push([label, linesOf(node, offset, offset + label.length)]);
          offset += label.length + 1;
        }
      }
    }
    return result;
  });
}
