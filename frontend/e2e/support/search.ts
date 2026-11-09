import { installZoneAudio } from "./zones";
import type { Page } from "@playwright/test";
import { decodeAlbumResults, decodeArtistResults, decodeSearchSuggestions, decodeTrackResults } from "../../src/features/search/api/decoders";
import { sampleBody } from "../../src/test/api-samples";
import { groupDetailOf, releaseDetailOf } from "../../src/test/catalog-data";
import { searchAlbumItem, searchAlbumPage, searchArtistPage, searchRequestKey, searchTrackItem, searchTrackPage } from "../../src/test/search-data";
import type { ApiMock } from "./api-mock";

/** 显式登记“爱”的专辑/曲目、空艺人栏与建议；长文案只用于排版压力测试，不模拟后端规范化算法。 */
export function seedSearch(mock: ApiMock, long = false) {
  const album = searchAlbumItem();
  const track = searchTrackItem();
  // 两种建议故意指向不同专辑，防止错用专辑建议的目标也能通过导航断言。
  track.group_id = "00000000-0000-4000-8000-000000000091";
  track.release.id = "00000000-0000-4000-8000-000000000092";
  if (long) {
    album.group.representative.title = "爱之交响曲・Concert " + "繁简全半角長標題".repeat(12);
    album.group.representative.artist = "演奏者 " + "LongUnbrokenArtist".repeat(8);
    track.title = "爱之乐章 " + "LongUnbrokenTrack".repeat(8);
    track.artist = "音乐家 ".repeat(20);
    track.release.title = album.group.representative.title;
    // 就绪时 ArtistNames 显示 ref.name；单艺人长文案夹具必须同步原文与引用，不能退回空 refs 绕过链接排版。
    const albumRef = album.group.representative.artist_refs?.[0];
    const trackRef = track.artist_refs?.[0];
    if (album.group.representative.artist_refs?.length !== 1 || track.artist_refs?.length !== 1 || albumRef === undefined || trackRef === undefined) {
      throw new Error("长署名搜索夹具需要一位专辑艺人与一位曲目艺人的引用");
    }
    album.group.representative.artist_refs = [{ ...albumRef, name: album.group.representative.artist, display_name: album.group.representative.artist }];
    track.artist_refs = [{ ...trackRef, name: track.artist, display_name: track.artist }];
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
  register("/api/v1/search", { q: "爱", type: "artists", page_size: "12" }, decodeArtistResults(searchArtistPage({ items: [], pagination: { page_size: 12 } })));
  register("/api/v1/search", { q: "爱", type: "albums", page_size: "12" }, decodeAlbumResults(searchAlbumPage({ items: [album], pagination: { page_size: 12 } })));
  register("/api/v1/search", { q: "爱", type: "tracks", page_size: "20" }, decodeTrackResults(searchTrackPage({ items: [track], pagination: { page_size: 20 } })));
  mock.zones.register([track]);
  return { album, track };
}

/** 搜索入口使用服务端播放区与合成渲染流，保留原生媒体播放。 */
export async function installSearchPlayback(page: Page) {
  await installZoneAudio(page);
  await page.route("**/api/v1/tracks/*/lyrics", (route) => route.fulfill({ json: {
    track_id: new URL(route.request().url()).pathname.split("/").at(-2), lyrics: null,
  } }));
}
