import type { Locator, Page } from "@playwright/test";
import { decodeAlbumResults, decodeSearchSuggestions, decodeTrackResults } from "../../src/features/search/api/decoders";
import { sampleBody } from "../../src/test/api-samples";
import { albumGroup, groupDetailOf, releaseDetailOf } from "../../src/test/catalog-data";
import { PERSONAL_LISTENER_ID, personalTracksOf, seedPlaylist } from "../../src/test/personal-data";
import { searchAlbumItem, searchAlbumPage, searchRequestKey, searchTrackItem, searchTrackPage } from "../../src/test/search-data";
import type { ApiMock } from "./api-mock";
import { expect } from "./fixtures";

export const SEARCH_STEP4_PLAYLIST = "搜索键盘歌单";
export const SEARCH_STEP4_CREDIT = "爱之作曲家・" + "合成署名Composer長い名前 ".repeat(50);

/** 四碟八十首，建议指向非代表版本的末碟倒数第二首；只复用合成目录与安全摘要，不读取音乐文件。 */
export function seedSearchStep4(mock: ApiMock) {
  const albumGroupFixture = albumGroup(401, { title: "爱之合成组曲", artist: "合成室内乐团", album_artist: "合成室内乐团", year: 2026, track_count: 3 });
  const group = albumGroup(402, { title: "多碟组曲代表版", release_count: 2, track_count: 3 });
  const release = albumGroup(403, { title: "多碟组曲完整录音", artist: "测试乐团", album_artist: "测试乐团", medium_count: 4, track_count: 80 }).representative;
  const detail = releaseDetailOf(release);
  const target = detail.media[3]?.tracks[18];
  if (target === undefined) throw new Error("多碟定位夹具缺少目标曲目");
  target.title = "爱之终章・键盘定位目标";
  mock.groups = [albumGroupFixture, group];
  mock.groupDetails.set(group.id, groupDetailOf(group, [group.representative, release]));
  mock.releases.set(release.id, detail);
  const summary = personalTracksOf(mock).find((track) => track.track_id === target.id);
  if (summary === undefined) throw new Error("多碟定位夹具缺少安全曲目摘要");
  const album = { ...searchAlbumItem(), group: albumGroupFixture, matched_release_id: albumGroupFixture.representative.id };
  album.match = { ...album.match, field: "credit", role: "composer", original: SEARCH_STEP4_CREDIT, release_id: album.matched_release_id, track_id: null };
  const track = { ...searchTrackItem(), ...summary };
  track.match = { ...track.match, field: "track_title", role: null, original: target.title, release_id: release.id, track_id: target.id };
  const suggestions = decodeSearchSuggestions(sampleBody("search/suggestions"));
  suggestions.query = "爱";
  suggestions.albums.items = [album];
  suggestions.tracks.items = [track];
  const register = (path: string, params: Record<string, string>, body: unknown) =>
    mock.search.responses.set(searchRequestKey(path, new URLSearchParams(params)), { status: 200, body });
  register("/api/v1/search/suggestions", { q: "爱" }, decodeSearchSuggestions(suggestions));
  register("/api/v1/search", { q: "爱", type: "albums", page_size: "12" }, decodeAlbumResults(searchAlbumPage({ items: [album], pagination: { page_size: 12 } })));
  register("/api/v1/search", { q: "爱", type: "tracks", page_size: "20" }, decodeTrackResults(searchTrackPage({ items: [track], pagination: { page_size: 20 } })));
  const playlist = seedPlaylist(mock.personal, PERSONAL_LISTENER_ID, { name: SEARCH_STEP4_PLAYLIST });
  const releaseURL = `/albums/${group.id}?release=${release.id}`;
  return { album, track, detail, target, playlist, releaseURL, locatedURL: `${releaseURL}&track=${target.id}` };
}

export const visibleSearchStep4 = (page: Page) => page.locator("input[data-global-search]:visible");
export const searchSuggestionsStep4 = (page: Page) => page.getByRole("listbox", { name: "搜索建议", exact: true });
export const locatedTrackStep4 = (page: Page, title: string) => page.getByRole("listitem").filter({ has: page.getByText(title, { exact: true }) });

/** 从当前焦点继续 Tab，不调用 focus/click，避免绕开真实的键盘顺序。 */
export async function keyboardTargetStep4(page: Page, target: Locator, activate = true) {
  await expect(target).toBeAttached();
  await expect(target).toBeEnabled();
  for (let index = 0; index < 160; index += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) {
      if (activate) await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("Tab");
  }
  throw new Error("纯键盘 Tab 未抵达目标");
}

export async function keyboardMenuStep4(page: Page, trigger: Locator, name: string) {
  await keyboardTargetStep4(page, trigger);
  const menu = page.getByRole("menu");
  const item = menu.getByRole("menuitem", { name, exact: true });
  await expect(item).toBeEnabled();
  for (let index = 0; index < 10; index += 1) {
    if (await item.evaluate((element) => element === document.activeElement)) {
      await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("ArrowDown");
  }
  throw new Error(`方向键未抵达菜单项 ${name}`);
}

export async function chooseTrackSuggestionStep4(page: Page, title: string) {
  await page.keyboard.press("/");
  await expect(visibleSearchStep4(page)).toBeFocused();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("爱");
  const popup = searchSuggestionsStep4(page);
  await expect(popup.getByRole("option")).toHaveCount(3);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await expect(popup.getByRole("option").filter({ hasText: title })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Enter");
}

/** 文字 Range 计行；只在实际存在裁剪时与裁剪框相交，移除 line-clamp 会让可见行数真实增加。 */
export async function textLineMetricsStep4(locator: Locator) {
  return locator.evaluate((element) => {
    const all = new Set<number>();
    const visible = new Set<number>();
    const style = getComputedStyle(element);
    const box = element.getBoundingClientRect();
    const clips = ["hidden", "clip", "auto", "scroll"].includes(style.overflowY);
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    for (let text = walker.nextNode(); text !== null; text = walker.nextNode()) {
      if (!text.textContent?.trim()) continue;
      const range = document.createRange();
      range.selectNodeContents(text);
      for (const rect of range.getClientRects()) {
        if (rect.width <= 0 || rect.height <= 0) continue;
        const top = Math.round(rect.top);
        all.add(top);
        if (!clips || (rect.top < box.bottom - 1 && rect.bottom > box.top + 1)) visible.add(top);
      }
    }
    return { all: all.size, visible: visible.size, clamp: style.webkitLineClamp };
  });
}

/** 对空队列也保留精确快照，不能只靠“播放器没出现”推断导航没有偷偷改队列。 */
export function queueSnapshotStep4(mock: ApiMock) {
  return mock.zones.queueSnapshot();
}
