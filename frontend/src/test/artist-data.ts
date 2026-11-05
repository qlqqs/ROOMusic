import { decodeArtistAlbumsPage, decodeArtistDetail, decodeArtistPage, decodeArtistTracksPage, type ArtistAlbumDTO, type ArtistDetailDTO, type ArtistNameDTO, type ArtistParticipationAlbumDTO } from "../features/artists/api/decoders";
import { artistInitialGroups, artistRoles, type ArtistSummaryDTO as ArtistSummary } from "../features/catalog/api/artist-summary";
import type { TrackSummaryDTO } from "../features/catalog/api/track-summary";
import { decodeArtistCandidatePage, decodeArtistSplitPreview, type ArtistCandidateDTO, type ArtistMergedSourceDTO } from "../features/governance/api/decoders";
import { loadApiSample, sampleBody } from "./api-samples";

export type ArtistResponse = { status: number; body: unknown };
export type ArtistData = {
  epoch: number;
  favorites: Map<string, { revision: number; anchors: Map<string, string> }>;
  state: "ready" | "preparing" | "unavailable";
  details: Map<string, ArtistDetailDTO>;
  names: Map<string, ArtistNameDTO[]>;
  albums: Map<string, ArtistAlbumDTO[]>;
  participationAlbums: Map<string, ArtistParticipationAlbumDTO[]>;
  /** 显式登记非代表可见版本的标题；仅供 I2 字面筛选，不重建发行图。 */
  albumTitles: Map<string, string[]>;
  tracks: Map<string, TrackSummaryDTO[]>;
  mergedSources: Map<string, ArtistMergedSourceDTO[]>;
  candidates: ArtistCandidateDTO[];
  ignored: Set<string>;
  replays: Map<string, { fingerprint: string; response: ArtistResponse }>;
  scopeVersion: number;
  nextOperation: number;
};

export const artistTestId = (n: number) => `a0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const firstDetail = () => decodeArtistDetail(sampleBody("artists/detail"));
export const primaryArtistId = () => firstDetail().artist.id;

/** 真实响应样本只读。详情缺失的列表项按相同合同显式构造，不模拟身份图或数据库事务。 */
export function seedArtist(data: ArtistData, summary: ArtistSummary, options: Partial<Omit<ArtistDetailDTO, "artist">> = {}): ArtistDetailDTO {
  const names: ArtistNameDTO[] = [{ id: artistTestId(data.details.size * 2 + 100), key_id: artistTestId(data.details.size * 2 + 101), name: summary.name, is_display_name: true, origin: "scan" }];
  const detail: ArtistDetailDTO = { artist: structuredClone(summary), revision: 1, name_source: "auto", names, names_pagination: { page: 1, page_size: 20, total: names.length }, role_counts: [], redirect_artist_id: null, ...options };
  data.details.set(summary.id, detail);
  data.names.set(summary.id, structuredClone(detail.names));
  return detail;
}

export function artistDataOf(): ArtistData {
  const data: ArtistData = { epoch: 1, favorites: new Map(), state: "ready", details: new Map(), names: new Map(), albums: new Map(), participationAlbums: new Map(), albumTitles: new Map(), tracks: new Map(), mergedSources: new Map(), candidates: [], ignored: new Set(), replays: new Map(), scopeVersion: 1, nextOperation: 1 };
  for (const summary of decodeArtistPage(sampleBody("artists/list-name")).items) seedArtist(data, summary);
  const detail = firstDetail();
  data.details.set(detail.artist.id, detail);
  data.names.set(detail.artist.id, detail.names);
  data.albums.set(detail.artist.id, decodeArtistAlbumsPage(sampleBody("artists/albums")).items);
  data.tracks.set(`${detail.artist.id}:track_artist`, decodeArtistTracksPage(sampleBody("artists/role-tracks")).items);
  return data;
}

/** I1/I2 显式登记接口快照；真实基准见 artists/adjudication，分页扩展另由场景构造。 */
export function seedArtistIntegration(data: ArtistData, artistId: string, options: { mergedSources?: ArtistMergedSourceDTO[]; participationAlbums?: ArtistParticipationAlbumDTO[] }): void {
  if (options.mergedSources !== undefined) data.mergedSources.set(artistId, structuredClone(options.mergedSources));
  if (options.participationAlbums !== undefined) data.participationAlbums.set(artistId, structuredClone(options.participationAlbums));
}

export function seedArtistCandidates(data: ArtistData): ArtistCandidateDTO[] {
  data.candidates = decodeArtistCandidatePage(sampleBody("governance/artist-candidates")).items;
  for (const candidate of data.candidates) for (const artist of [candidate.left, candidate.right]) seedArtist(data, artist);
  return data.candidates;
}

export const artistError = (code: string, status = 409): ArtistResponse => ({ status, body: { error: { code, message: code }, request_id: "artist-test" } });
const ok = (body: unknown): ArtistResponse => ({ status: 200, body: structuredClone(body) });
const record = (value: unknown): Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value) ? Object.fromEntries(Object.entries(value)) : {};
const strings = (value: unknown): string[] => Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
const text = (value: unknown): string => typeof value === "string" ? value : "";
const pairKey = (left: string, right: string) => [left, right].sort().join(":");
function page<T>(items: T[], query: URLSearchParams, fallback = 50) {
  const page = Number(query.get("page") || 1);
  const pageSize = Number(query.get("page_size") || fallback);
  return { items: items.slice((page - 1) * pageSize, page * pageSize), pagination: { page, page_size: pageSize, total: items.length } };
}
const uuid = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);

function readParameterError(url: URL): ArtistResponse | null {
  const query = url.searchParams;
  // 后端共用分页器读取首个值；空值用默认，不擅自把重复分页改成错误。
  const page = query.get("page") || "1", size = query.get("page_size") || "50";
  if (![page, size].every((value) => /^[+-]?\d+$/.test(value))) return artistError("invalid_pagination", 400);
  const p = BigInt(page), n = BigInt(size), max = 9223372036854775807n;
  if (p < 1n || p > max || n < 1n || n > 100n || (p - 1n) * n > max) return artistError("invalid_pagination", 400);
  if (url.pathname.endsWith("/albums")) {
    const values = query.getAll("participation");
    if (values.length > 1 || (values.length === 1 && values[0] !== "any")) return artistError("invalid_artist_participation", 400);
    if (query.getAll("q").length > 1) return artistError("invalid_query", 400);
  }
  const q = query.get("q") ?? "";
  try {
    // URLSearchParams 会把非法 UTF-8 换成替换字符，须在替换前检查原始查询编码。
    for (const part of url.search.slice(1).split("&")) if (decodeURIComponent(part.split("=")[0] ?? "") === "q") decodeURIComponent(part.slice(part.indexOf("=") + 1).replaceAll("+", " "));
  } catch { return artistError("invalid_query", 400); }
  if ([...q].some((char) => /\p{Cc}/u.test(char) && !/\s/u.test(char)) || new TextEncoder().encode(q.trim()).length > 200) return artistError("invalid_query", 400);
  if (url.pathname === "/api/v1/artists") {
    for (const [key, allowed, code] of [["role", artistRoles, "invalid_artist_role"], ["initial", artistInitialGroups, "invalid_artist_initial"], ["sort", ["name", "album_count"], "invalid_artist_sort"]] as const) {
      const values = query.getAll(key);
      if (values.length > 1 || (values.length === 1 && !allowed.some((value) => value === values[0]))) return artistError(code, 400);
    }
  }
  return null;
}

/** HTTP 同源门由两个适配器调用；默认浏览器补头与显式缺头由各适配器区分。 */
export function artistSummaryOriginError(origin: string | null, expectedOrigin: string): ArtistResponse | null {
  if (!origin) return artistError("origin_required", 403);
  return origin === expectedOrigin ? null : artistError("origin_forbidden", 403);
}

function preview(data: ArtistData, detail: ArtistDetailDTO, input: Record<string, unknown>) {
  const original = decodeArtistSplitPreview(sampleBody("governance/artist-split-preview-aliases"));
  return { ...original, artist_id: detail.artist.id, revision: detail.revision, basis: `scope-${detail.revision}-${data.scopeVersion}`, scope: { ...original.scope, mode: input.mode === "albums" ? "albums" as const : "aliases" as const, name_key_ids: strings(input.name_key_ids), name_count: strings(input.name_key_ids).length, groups: strings(input.group_ids).map((groupId) => ({ group_id: groupId, release_ids: [], revision: 1 })) } };
}

/** Vitest 与 Playwright 共用的状态替身。只处理艺人域；未注册路由拒绝，不吞掉遗漏。鉴权由适配器处理。 */
export function respondToArtists(data: ArtistData, method: string, url: URL, body: unknown = null, key: string | null = null): ArtistResponse {
  const path = url.pathname;
  const query = url.searchParams;
  const input = record(body);
  const write = method !== "GET" && !path.endsWith("/split-preview");
  const fingerprint = JSON.stringify([method, path, body]);
  if (write && key !== null) {
    const previous = data.replays.get(key);
    if (previous !== undefined) return previous.fingerprint === fingerprint ? structuredClone(previous.response) : artistError("idempotency_conflict");
  }
  const match = /^\/api\/v1\/artists\/([^/]+)(?:\/([^/]+))?$/.exec(path);
  const requestedId = (match?.[1] ?? "").toLowerCase();
  if (method === "GET") {
    if (match !== null && !uuid(requestedId)) return artistError("invalid_id", 400);
    if (path === "/api/v1/artists" || match?.[2] === "albums") {
      const error = readParameterError(url);
      if (error !== null) return error;
    }
  }
  if (data.state !== "ready") return artistError(write ? "artist_catalog_not_ready" : "artist_catalog_preparing", write ? 409 : 503);
  if (path === "/api/v1/artist-candidates" && method === "GET") return ok(page(data.candidates
    .map((item) => ({ ...item, key_pairs: item.key_pairs.filter((pair) => !data.ignored.has(pairKey(pair.left_key_id, pair.right_key_id))) }))
    .filter((item) => item.key_pairs.length > 0 && !data.details.get(item.left.id)?.redirect_artist_id && !data.details.get(item.right.id)?.redirect_artist_id), query));
  if (path === "/api/v1/artists" && method === "GET") {
    let items = [...data.details.values()].filter((item) => item.artist.available && item.redirect_artist_id === null).map((item) => item.artist);
    const q = query.get("q")?.trim().toLocaleLowerCase();
    if (q) items = items.filter((item) => [item.name, ...(data.names.get(item.id) ?? []).map((name) => name.name)].some((name) => name.toLocaleLowerCase().includes(q)));
    if (query.has("initial")) items = items.filter((item) => item.initial_group === query.get("initial"));
    if (query.has("role")) items = items.filter((item) => item.roles.some((role) => role === query.get("role") || (query.get("role") === "album_artist" && role === "release_artist")));
    items.sort((a, b) => (query.get("sort") === "album_count" ? b.album_count - a.album_count : artistInitialGroups.indexOf(a.initial_group) - artistInitialGroups.indexOf(b.initial_group)) || a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
    return ok(page(items, query));
  }
  let detail = data.details.get(requestedId);
  if (detail === undefined) return artistError("not_found", 404);
  const action = match?.[2];
  if (method === "GET" && action === undefined) {
    const names = data.names.get(requestedId) ?? [];
    return ok({ ...detail, names: names.slice(0, 20), names_pagination: { page: 1, page_size: 20, total: names.length } });
  }
  if (!write && detail.redirect_artist_id !== null) detail = data.details.get(detail.redirect_artist_id) ?? detail;
  const id = detail.artist.id;
  if (method === "GET") {
    if (action === "names") return ok({ artist_id: id, ...page(data.names.get(id) ?? [], query) });
    if (action === "merged-sources") return ok({ items: data.mergedSources.get(id) ?? [] });
    if (action === "albums") {
      const q = query.get("q")?.trim().toLocaleLowerCase() ?? "";
      const items = query.get("participation") === "any" ? data.participationAlbums.get(id) ?? [] : data.albums.get(id) ?? [];
      // 输入快照已按后端顺序、按组去重；筛选不改变代表版本、署名版本或角色。
      return ok({ artist_id: id, ...page(items.filter((item) => [item.group.representative.title, ...(data.albumTitles.get(item.group.id) ?? [])].some((title) => title.toLocaleLowerCase().includes(q))), query) });
    }
    if (action === "tracks") return ok({ artist_id: id, ...page(data.tracks.get(`${id}:${query.get("role")}`) ?? [], query) });
    if (action === "queue") {
      const all = [...new Map([...data.tracks.entries()].filter(([key]) => key.startsWith(`${id}:`)).flatMap(([, items]) => items).map((item) => [item.track_id, item])).values()];
      const present = all.filter((item) => item.present);
      const order = query.get("order") === "random" ? "random" : "album";
      // 可复现的种子排序替身：只验证请求 seed 与“先排全部再截断”，不是后端随机算法证明。
      const seed = order === "random" ? query.get("seed") : null;
      if (order === "random") present.sort((a, b) => hash(`${seed}${a.track_id}`) - hash(`${seed}${b.track_id}`));
      const items = present.slice(0, 10_000);
      return ok({ artist_id: id, order, seed, items, returned: items.length, total: present.length, missing_total: all.length - present.length, limit: 10_000, truncated: present.length > items.length });
    }
  }
  if (action === "split-preview" && method === "POST") return ok(preview(data, detail, input));
  if (!write) return artistError("not_found", 404);
  if (key === null) return artistError("invalid_idempotency_key", 400);
  if (detail.redirect_artist_id !== null) return artistError("artist_redirected");
  if (input.expected_revision !== detail.revision) return artistError("revision_conflict");
  const operationId = artistTestId(90_000 + data.nextOperation);
  const result = { artist_id: id, operation_id: operationId, revision: detail.revision + 1 };
  let response: ArtistResponse;
  if (action === "name" && method === "PATCH") {
    const name = input.mode === "auto" ? (data.names.get(id)?.[0]?.name ?? detail.artist.name) : text(input.name).trim();
    if (!name) return artistError("invalid_artist_name", 400);
    detail.artist.name = name;
    detail.name_source = input.mode === "auto" ? "auto" : "manual";
    response = ok({ ...result, display_name: name, name_source: detail.name_source });
  } else if (action === "merge" && method === "POST") {
    const source = data.details.get(text(input.source_artist_id));
    if (source === undefined) return artistError("not_found", 404);
    if (source.artist.id === id) return artistError("same_artist");
    if (source.revision !== input.source_expected_revision) return artistError("revision_conflict");
    if (source.redirect_artist_id !== null) return artistError("artist_redirected");
    source.revision += 1;
    source.redirect_artist_id = id;
    const names = data.names.get(source.artist.id) ?? [];
    data.names.set(id, [...(data.names.get(id) ?? []), ...names.map((name) => ({ ...name, is_display_name: false }))]);
    data.mergedSources.set(id, [...(data.mergedSources.get(id) ?? []).filter((item) => item.artist_id !== source.artist.id), { artist_id: source.artist.id, name: source.artist.name, revision: source.revision, name_key_ids: names.map((name) => name.key_id), operation_id: operationId, merged_at: "2026-11-04T12:00:00Z" }]);
    response = ok({ ...result, source: { id: source.artist.id, revision: source.revision, redirect_artist_id: id } });
  } else if (action === "split" && method === "POST") {
    const current = preview(data, detail, input);
    if (input.basis !== current.basis) return artistError("artist_scope_changed");
    const target = record(input.target);
    let restored: ArtistDetailDTO;
    if (target.kind === "restore") {
      const candidate = data.details.get(text(target.artist_id));
      if (candidate === undefined || candidate.redirect_artist_id !== id) return artistError("invalid_artist_scope", 400);
      if (candidate.revision !== target.expected_revision) return artistError("revision_conflict");
      restored = candidate;
      restored.revision += 1;
      restored.redirect_artist_id = null;
    } else {
      restored = seedArtist(data, { ...detail.artist, id: artistTestId(80_000 + data.nextOperation), name: text(target.display_name) || "拆开的艺人" });
    }
    const keys = strings(input.name_key_ids);
    const moved = (data.names.get(id) ?? []).filter((name) => keys.includes(name.key_id));
    data.names.set(restored.artist.id, moved);
    if (input.mode === "aliases") data.names.set(id, (data.names.get(id) ?? []).filter((name) => !keys.includes(name.key_id)));
    data.mergedSources.set(id, (data.mergedSources.get(id) ?? []).filter((item) => item.artist_id !== restored.artist.id).map((item) => ({ ...item, name_key_ids: input.mode === "aliases" ? item.name_key_ids.filter((key) => !keys.includes(key)) : item.name_key_ids })));
    response = ok({ ...result, target: { id: restored.artist.id, revision: restored.revision }, scope_summary: current.scope });
  } else if (action === "candidate-ignore" && method === "POST") {
    const other = data.details.get(text(input.other_artist_id));
    if (other === undefined) return artistError("not_found", 404);
    if (other.revision !== input.other_expected_revision) return artistError("revision_conflict");
    // 忽略绑定看到的名字键对，而不是永久屏蔽整对艺人；新名字键对仍可提示。
    const pairs = Array.isArray(input.key_pairs) ? input.key_pairs.map(record) : [];
    for (const pair of pairs) {
      const identity = pairKey(text(pair.left_key_id), text(pair.right_key_id));
      if (input.ignored === true) data.ignored.add(identity); else data.ignored.delete(identity);
    }
    other.revision += 1;
    response = ok({ ...result, other_revision: other.revision, ignored: input.ignored === true });
  } else return loadApiSample("errors/not-found-api");
  // 合并/拆回会改变收藏投影；同键重放在函数入口返回，不再次推进集合版本。
  if (action === "merge" || action === "split") {
    const touched = new Set([id, text(input.source_artist_id), text(record(input.target).artist_id)]);
    for (const user of data.favorites.values()) {
      if ([...user.anchors.keys()].some((anchor) => touched.has(anchor) || touched.has(data.details.get(anchor)?.redirect_artist_id ?? anchor))) user.revision += 1;
    }
  }
  detail.revision += 1;
  data.nextOperation += 1;
  data.replays.set(key, { fingerprint, response: structuredClone(response) });
  return response;
}

function hash(value: string): number {
  let result = 2166136261;
  for (const char of value) result = Math.imul(result ^ char.charCodeAt(0), 16777619);
  return result >>> 0;
}

/** 目录状态与发布版本由测试显式推进；不隐含触发扫描。 */
export const artistCatalogState = (data: ArtistData) => ({ epoch: data.epoch, search_mode: "normalized" as const, artists_state: data.state });

/** I4 复用显式登记的安全摘要，按小写首次去重顺序返回；不模拟数据库查询。 */
export function respondToArtistSummaries(data: ArtistData, body: unknown): ArtistResponse {
  const input = record(body);
  const values = input.track_ids;
  if (Object.keys(input).length !== 1 || !Array.isArray(values) || !values.every(uuid)) return artistError("invalid_input", 400);
  const ids = [...new Set(values.map((id) => id.toLowerCase()))];
  if (ids.length < 1 || ids.length > 1000) return artistError("invalid_input", 400);
  const tracks = new Map([...data.tracks.values()].flat().map((item) => [item.track_id, item]));
  const items = ids.flatMap((id) => {
    const item = tracks.get(id);
    if (item === undefined) return [];
    return [data.state === "ready" ? item : { ...item, artist_refs: [], release: { ...item.release, artist_refs: [], album_artist_refs: [] } }];
  });
  return ok({ items });
}

/** 艺人收藏专用适配，曲目/专辑 lookup 应继续交给 personal 的既有替身。 */
export function respondToArtistFavorites(data: ArtistData, method: string, url: URL, body: unknown = null, userId = "artist-test-user"): ArtistResponse {
  const user = data.favorites.get(userId) ?? { revision: 0, anchors: new Map<string, string>() };
  data.favorites.set(userId, user);
  const resolve = (id: string) => data.details.get(id)?.redirect_artist_id ?? id;
  const favorited = (id: string) => [...user.anchors.keys()].some((anchor) => resolve(anchor) === resolve(id));
  const write = method === "PUT" || method === "DELETE";
  if (data.state !== "ready") return artistError(write ? "artist_catalog_not_ready" : "artist_catalog_preparing", write ? 409 : 503);
  if (url.pathname.endsWith("/lookup") && method === "POST") return ok({ revision: user.revision, tracks: [], albums: [], artists: strings(record(body).artist_ids).map((id) => ({ artist_id: id, resolved_artist_id: data.details.has(id) ? resolve(id) : null, favorited: favorited(id) })) });
  if (url.pathname === "/api/v1/me/favorites/artists" && method === "GET") {
    const revision = url.searchParams.get("revision");
    if (revision !== null && Number(revision) !== user.revision) return artistError("favorites_changed");
    const unique = new Map<string, { artist: ArtistSummary; available: boolean; created_at: string }>();
    for (const [anchor, createdAt] of user.anchors) {
      const detail = data.details.get(resolve(anchor));
      if (detail === undefined) continue;
      const previous = unique.get(detail.artist.id);
      // 合并只解析身份、不改原锚点时间；展示取等价锚点的较晚时间（design §11.2）。
      if (previous === undefined || Date.parse(createdAt) > Date.parse(previous.created_at)) unique.set(detail.artist.id, { artist: detail.artist, available: detail.artist.available, created_at: createdAt });
    }
    const items = [...unique.values()].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at) || a.artist.id.localeCompare(b.artist.id));
    return ok({ ...page(items, url.searchParams), revision: user.revision });
  }
  const id = url.pathname.split("/").at(-1) ?? "";
  if (!data.details.has(id)) return artistError("not_found", 404);
  if (!write) return artistError("not_found", 404);
  const next = method === "PUT";
  if (favorited(id) !== next) user.revision += 1;
  if (next && !favorited(id)) user.anchors.set(id, new Date(Date.UTC(2026, 10, 4, 0, 0, user.revision)).toISOString());
  if (!next) for (const anchor of user.anchors.keys()) if (resolve(anchor) === resolve(id)) user.anchors.delete(anchor);
  return ok({ target_kind: "artist", target_id: resolve(id), favorited: next, created_at: next ? [...user.anchors.entries()].find(([anchor]) => resolve(anchor) === resolve(id))?.[1] ?? null : null, revision: user.revision });
}

/** 供浏览器与 I2 流程共用的显式合成分页种子（21 张，包含只作曲/演奏的专辑）。 */
export function seedArtistParticipation(data: ArtistData, artistId = primaryArtistId(), count = 21): ArtistParticipationAlbumDTO[] {
  const album = data.albums.get(primaryArtistId())?.[0];
  if (!album) throw new Error("缺少艺人专辑样本");
  const items: ArtistParticipationAlbumDTO[] = Array.from({ length: count }, (_, i) => ({ ...structuredClone(album), group: { ...structuredClone(album.group), id: artistTestId(50_000 + i), representative: { ...structuredClone(album.group.representative), title: `参与专辑 ${String(i + 1).padStart(2, "0")}` } }, roles: i % 2 === 0 ? ["composer"] : ["performer"] }));
  seedArtistIntegration(data, artistId, { participationAlbums: items });
  return items;
}
