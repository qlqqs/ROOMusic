import { decodeArtistResults } from "../../src/features/search/api/decoders";
import { sampleBody } from "../../src/test/api-samples";
import {
  artistCatalogState, artistDataOf, artistTestId, primaryArtistId, respondToArtistFavorites, respondToArtists,
  seedArtist, seedArtistCandidates, seedArtistParticipation,
} from "../../src/test/artist-data";
import { groupDetailOf, releaseDetailOf } from "../../src/test/catalog-data";
import { searchAlbumPage, searchRequestKey, searchTrackPage } from "../../src/test/search-data";
import type { ApiMock } from "./api-mock";

/**
 * 只装配共享 artist-data 的样本与行为，不重写艺人响应逻辑。
 * merged 用共享合并命令生成 I1 来源；favorite 用共享收藏命令登记当前会话。
 * 截图使用：seedArtistsE2E(mock, { merged: scene === "split" || scene === "search", favorite: scene === "favorites" })。
 */
export function seedArtistsE2E(mock: ApiMock, options: { merged?: boolean; favorite?: boolean } = {}) {
  const data = artistDataOf();
  mock.artists = data;
  const primary = data.details.get(primaryArtistId());
  const other = [...data.details.values()].find((detail) => detail.artist.name === "Teresa Teng");
  if (primary === undefined || other === undefined) throw new Error("艺人样本缺少邓丽君或 Teresa Teng");
  const artist = structuredClone(primary.artist);
  const source = structuredClone(other.artist);
  const sourceKeys = [...new Set((data.names.get(source.id) ?? []).map((name) => name.key_id))];
  const query = source.name;
  seedArtistCandidates(data);
  seedArtist(data, { ...artist, id: artistTestId(30_001), name: "かなの演奏者", initial_group: "kana", roles: ["performer"] });
  seedArtist(data, { ...artist, id: artistTestId(30_002), name: "# 合成乐队", initial_group: "#", roles: ["performer"] });
  seedArtist(data, { ...artist, id: artistTestId(30_003), name: "LongUnbrokenArtist".repeat(10), initial_group: "L", roles: ["composer"] });
  const albums = seedArtistParticipation(data, artist.id);
  const tracks = data.tracks.get(`${artist.id}:track_artist`) ?? [];
  const album = data.albums.get(artist.id)?.[0];
  if (album === undefined) throw new Error("艺人样本缺少专辑");
  // 样本逐文件规范化 ID；进入专辑页时把其署名与当前艺人身份显式装配到同一份目录。
  mock.groups = [structuredClone(album.group)];
  mock.groupDetails.set(album.group.id, groupDetailOf(album.group));
  const release = releaseDetailOf(album.group.representative);
  mock.releases.set(release.id, release);
  for (const track of tracks) mock.personal.tracks.set(track.track_id, track);
  if (options.merged) {
    const response = respondToArtists(data, "POST", new URL(`/api/v1/artists/${artist.id}/merge`, "http://mock.local"),
      { expected_revision: primary.revision, source_artist_id: source.id, source_expected_revision: other.revision }, artistTestId(99_001));
    if (response.status !== 200) throw new Error(`艺人合并种子失败：${response.status}`);
  }
  if (options.favorite) {
    const userId = mock.session === null ? undefined : mock.accounts.get(mock.session)?.id;
    if (userId === undefined) throw new Error("艺人收藏种子需要已登录会话");
    const response = respondToArtistFavorites(data, "PUT", new URL(`/api/v1/me/favorites/artists/${artist.id}`, "http://mock.local"), null, userId);
    if (response.status !== 200) throw new Error(`艺人收藏种子失败：${response.status}`);
  }
  const result = decodeArtistResults(sampleBody("search/artists-alias-match"));
  const match = result.items[0]?.match;
  if (match === undefined) throw new Error("别名搜索样本缺少匹配说明");
  result.items = [{ artist, match: { ...match, artist_id: artist.id, alias: query, original: query } }];
  result.pagination = { page: 1, page_size: 12, total: 1, total_relation: "eq", has_more: false };
  result.query = query;
  result.catalog_state = artistCatalogState(data);
  const register = (type: string, pageSize: number, body: unknown) => mock.search.responses.set(
    searchRequestKey("/api/v1/search", new URLSearchParams({ q: query, type, page_size: String(pageSize) })), { status: 200, body },
  );
  register("artists", 12, result);
  register("albums", 12, searchAlbumPage({ query, catalog_state: artistCatalogState(data), items: [], pagination: { page_size: 12 } }));
  register("tracks", 20, searchTrackPage({ query, catalog_state: artistCatalogState(data), items: [], pagination: { page_size: 20 } }));
  return { data, artist, source, sourceKeys, query, albums, tracks };
}
