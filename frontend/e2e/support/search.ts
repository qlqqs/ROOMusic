import { readFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import { decodeAlbumResults, decodeSearchSuggestions, decodeTrackResults } from "../../src/features/search/api/decoders";
import { sampleBody } from "../../src/test/api-samples";
import { groupDetailOf, releaseDetailOf } from "../../src/test/catalog-data";
import { searchAlbumItem, searchAlbumPage, searchRequestKey, searchTrackItem, searchTrackPage } from "../../src/test/search-data";
import type { ApiMock } from "./api-mock";

/** 显式登记“爱”的两栏与建议；长文案只用于排版压力测试，不模拟后端规范化算法。 */
export function seedSearch(mock: ApiMock, long = false) {
  const album = searchAlbumItem();
  const track = searchTrackItem();
  // 两种建议故意指向不同专辑，防止错用专辑建议的目标也能通过导航断言。
  track.group_id = "00000000-0000-4000-8000-000000000091";
  track.release.id = "00000000-0000-4000-8000-000000000092";
  if (long) {
    album.group.representative.title = "爱之交响曲・Concert " + "繁简全半角長標題".repeat(8);
    album.group.representative.artist = "演奏者 " + "LongUnbrokenArtist".repeat(8);
    track.title = "爱之乐章 " + "LongUnbrokenTrack".repeat(8);
    track.artist = "音乐家 ".repeat(20);
    track.release.title = album.group.representative.title;
    album.match = { ...album.match, field: "credit", role: "composer", original: "爱之作曲家 " + "長署名Composer".repeat(6) };
    track.match = { ...track.match, field: "label", role: null, original: "爱之厂牌 " + "LongRecordLabel".repeat(20) };
  }
  const suggestions = decodeSearchSuggestions(sampleBody("search/suggestions"));
  suggestions.query = "爱";
  suggestions.albums.items = [album];
  suggestions.tracks.items = [track];
  mock.groups = [album.group];
  const trackGroup = { id: track.group_id, release_count: 2, attention_count: 0, representative: album.group.representative };
  mock.groupDetails.set(track.group_id, groupDetailOf(trackGroup, [album.group.representative, track.release]));
  mock.releases.set(track.release.id, releaseDetailOf(track.release));
  const register = (path: string, params: Record<string, string>, body: unknown) =>
    mock.search.responses.set(searchRequestKey(path, new URLSearchParams(params)), { status: 200, body });
  register("/api/v1/search/suggestions", { q: "爱" }, suggestions);
  register("/api/v1/search", { q: "爱", type: "albums", page_size: "12" }, decodeAlbumResults(searchAlbumPage({ items: [album], pagination: { page_size: 12 } })));
  register("/api/v1/search", { q: "爱", type: "tracks", page_size: "20" }, decodeTrackResults(searchTrackPage({ items: [track], pagination: { page_size: 20 } })));
  return { album, track };
}

/** 路由只提供预检和合成音频，浏览器仍用原生播放器解码与播放。 */
export async function installSearchPlayback(page: Page) {
  const flac = readFileSync(new URL("../../../backend/testdata/playback/flac-16-44100.flac", import.meta.url));
  await page.route("**/api/v1/tracks/*/playback", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2);
    return route.fulfill({ json: { track_id: id, representation: {
      kind: "original", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: flac.length,
    } } });
  });
  await page.route("**/api/v1/tracks/*/audio", (route) => {
    const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers()["range"] ?? "");
    const start = Number(range?.[1] ?? 0);
    const end = Math.min(Number(range?.[2] || flac.length - 1), flac.length - 1);
    if (start > end) return route.fulfill({ status: 416, headers: { "Content-Range": `bytes */${flac.length}` } });
    return route.fulfill({ status: range ? 206 : 200, contentType: "audio/flac", body: flac.subarray(start, end + 1), headers: {
      "Accept-Ranges": "bytes", "Content-Length": String(end - start + 1),
      ...(range ? { "Content-Range": `bytes ${start}-${end}/${flac.length}` } : {}),
    } });
  });
  await page.route("**/api/v1/tracks/*/lyrics", (route) => route.fulfill({ json: {
    track_id: new URL(route.request().url()).pathname.split("/").at(-2), lyrics: null,
  } }));
}
