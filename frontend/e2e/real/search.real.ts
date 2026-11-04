import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { decodeCatalogState } from "../../src/features/catalog/api/decoders";
import { decodeTrackResults } from "../../src/features/search/api/decoders";
import { decodeFavoriteTracksPage } from "../../src/features/personal/api/decoders";
import { body, fixtureRoot, login, mediaState, observeMedia, origin, prepareLibrary, scan, syntheticWav } from "./support";

const folder = join(fixtureRoot, "search-normalization");
const examples = [
  { title: "愛與樂章", q: "爱与乐章", file: "traditional.wav", fill: 91 },
  { title: "ABC123", q: "ＡＢＣ１２３", file: "width.wav", fill: 92 },
];

test.describe("真实搜索规范化", () => {
  test.beforeAll(async ({ request }) => {
    await prepareLibrary(request);
    await mkdir(folder, { recursive: true });
    for (const item of examples) await writeFile(join(folder, item.file), syntheticWav({ title: item.title, album: "搜索规范化实播", artist: "合成演奏者" }, item.fill, 12));
    await scan(request);
    // 新库回填可能与扫描交错，只有原子发布后才断言规范化命中。
    await expect.poll(async () => decodeCatalogState(await body(await request.get("/api/v1/catalog/status"))).search_mode, { timeout: 30_000 }).toBe("normalized");
  });
  test.afterAll(async ({ request }) => {
    await login(request);
    await rm(folder, { recursive: true, force: true });
    await scan(request);
  });
  for (const item of examples) {
    test(`${item.q} 找到 ${item.title} 并收藏持久化、从结果实播`, async ({ page }) => {
      await login(page.request);
      const result = decodeTrackResults(await body(await page.request.get(`/api/v1/search?type=tracks&q=${encodeURIComponent(item.q)}`)));
      expect(result.catalog_state.search_mode).toBe("normalized");
      const hit = result.items.find((track) => track.title === item.title);
      expect(hit).toBeDefined();
      expect(result.items.map((track) => track.title)).not.toContain(examples.find((other) => other.file !== item.file)?.title);
      const empty = decodeTrackResults(await body(await page.request.get("/api/v1/search?type=tracks&q=SEARCH_NO_MATCH_987654321")));
      expect(empty.items).toEqual([]);
      expect(empty.pagination.total).toBe(0);
      if (hit === undefined) throw new Error("没有找到合成搜索曲目");
      await body(await page.request.delete(`/api/v1/me/favorites/tracks/${hit.track_id}`, { headers: { Origin: origin } }));
      await observeMedia(page);
      await page.goto(`/search?q=${encodeURIComponent(item.q)}`);
      const row = page.getByRole("row").filter({ has: page.getByText(item.title, { exact: true }) });
      const heart = row.getByRole("button", { name: `收藏《${item.title}》`, exact: true });
      await expect(heart).toHaveAttribute("aria-pressed", "false");
      await heart.click();
      await expect(heart).toHaveAttribute("aria-pressed", "true");
      // 等服务端读回落盘结果，不能只把乐观心形当作成功；刷新后仍在搜索页验证。
      await expect.poll(async () => decodeFavoriteTracksPage(await body(await page.request.get("/api/v1/me/favorites/tracks"))).items.map((entry) => entry.track.track_id)).toContain(hit.track_id);
      await page.reload();
      await expect(heart).toHaveAttribute("aria-pressed", "true");
      await expect(heart).toBeEnabled();
      await row.getByRole("button", { name: /^播放 / }).click();
      await expect.poll(async () => (await mediaState(page))?.paused).toBe(false);
      await expect.poll(async () => (await mediaState(page))?.time).toBeGreaterThan(0);
      expect((await mediaState(page))?.src).toContain(hit?.track_id);
      expect(await page.evaluate(() => window.playbackTestEvents.some((event) => event.type === "playing"))).toBe(true);
    });
  }
});
