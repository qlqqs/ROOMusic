import { releaseSummaryDecoder, type ReleaseGroupSummaryDTO, type ReleaseSummaryDTO } from "../features/catalog/api/decoders";
import type { TrackSummaryDTO } from "../features/catalog/api/track-summary";
import type {
  ItemRefDTO, ItemRestoredDTO, PersonalAlbumDTO, PersonalQueueDTO, PlaylistStateDTO, PlaylistSummaryDTO,
} from "../features/personal/api/decoders";
import { sampleBody } from "./api-samples";
import {
  albumGroup, catalogDataOf, failure, groupDetailFor, releaseDetailOf, type ApiResult, type CatalogData,
} from "./catalog-data";

// 收藏与歌单的共享纯替身。只依赖纯 DTO、catalog fixture 与样本，不导入组件、CSS 或 Vitest。
// 模拟页面需要的状态与协议；不复制数据库锁、SQL 排序键、原始 JSON/来源守卫等已由 Go 测试覆盖的实现。
const TRACK_LIMIT = 10_000;
const PLAYLIST_LIMIT = 1_000;
export const PERSONAL_LISTENER_ID = "00000000-0000-4000-8000-000000000003";
export const PERSONAL_ADMIN_ID = "00000000-0000-4000-8000-000000000002";

type Undo = { item: ItemRefDTO; previousTrackId: string | null; removalRevision: number; result?: ApiResult };
export type PersonalPlaylist = {
  state: PlaylistStateDTO;
  /** position 始终是全歌单从 1 起的序号，不是当前页内序号。 */
  items: ItemRefDTO[];
  undo: Map<string, Undo>;
};
export type PersonalUser = {
  revision: number;
  /** 值是收藏时间，按时间倒序、同时间 ID 升序读取。 */
  tracks: Map<string, string>;
  albums: Map<string, string>;
  playlists: Map<string, PersonalPlaylist>;
  /** 成功回执独立于歌单保存，删除歌单不能删除重放依据。 */
  receipts: Map<string, { fingerprint: string; result: ApiResult }>;
};
export type PersonalData = {
  /** 引用而非快照：重新扫描/归组后读取当前 catalog。 */
  catalog: CatalogData;
  /** 显式登记的安全摘要覆盖 catalog，用于 missing、未知时长、不可用等场景。 */
  tracks: Map<string, TrackSummaryDTO>;
  albums: Map<string, PersonalAlbumDTO>;
  users: Map<string, PersonalUser>;
  sequence: number;
  clock: number;
};
export type PersonalRequest = { method: string; path: string; body?: unknown; idempotencyKey?: string | null };

export function personalDataOf(catalog: CatalogData = catalogDataOf()): PersonalData {
  return { catalog, tracks: new Map(), albums: new Map(), users: new Map(), sequence: 0, clock: 0 };
}

export function personalUserOf(data: PersonalData, userId: string): PersonalUser {
  const key = userId.toLowerCase();
  let user = data.users.get(key);
  if (user === undefined) {
    user = { revision: 0, tracks: new Map(), albums: new Map(), playlists: new Map(), receipts: new Map() };
    data.users.set(key, user);
  }
  return user;
}

const uuidOf = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;
const newId = (data: PersonalData) => uuidOf("9", ++data.sequence);
const now = (data: PersonalData) => new Date(Date.UTC(2026, 10, 4) + ++data.clock * 1000).toISOString();
const uuid = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const safeRevision = (value: unknown, min = 1): value is number => typeof value === "number" && Number.isSafeInteger(value) && value >= min;
const invalid = () => failure(400, "invalid_input", "请求参数无效");
const notFound = () => failure(404, "not_found", "目标不存在");
const conflict = () => failure(409, "revision_conflict", "歌单刚在别处改过");
const success = (body: unknown, status = 200): ApiResult => ({ status, body });
const snapshot = <T>(value: T): T => structuredClone(value);

/** 合成独立曲目；使用 catalog 页面曲目时应优先 personalTracksOf，以免生成另一套身份。 */
const sampleTracks = sampleBody("favorites/tracks") as { items: { track: TrackSummaryDTO }[] };
export function personalTrack(index: number, overrides: Partial<TrackSummaryDTO> = {}): TrackSummaryDTO {
  const base = sampleTracks.items[0]?.track;
  if (base === undefined) throw new Error("收藏样本没有曲目");
  const group = albumGroup(1);
  return { ...base, track_id: uuidOf("f", index), title: `曲目 ${index}`, group_id: group.id, release: group.representative, ...overrides };
}

/** 取当前 catalog 的安全曲目摘要，ID 与页面 mediaOf/releaseDetailOf 完全相同；不凭输入 ID 猜造曲目。 */
export function personalTracksOf(catalog: CatalogData): TrackSummaryDTO[] {
  const placements = new Map<string, { groupId: string; summary: ReleaseSummaryDTO }>();
  for (const group of catalog.groups) placements.set(group.representative.id, { groupId: group.id, summary: group.representative });
  for (const group of catalog.groupDetails.values()) {
    for (const release of group.releases) placements.set(release.id, { groupId: group.id, summary: release });
  }
  const tracks = new Map<string, TrackSummaryDTO>();
  for (const [releaseId, placement] of placements) {
    const detail = catalog.releases.get(releaseId) ?? releaseDetailOf(placement.summary);
    // 发行 DTO 附加的 revision/media 等字段不能泄漏进曲目安全摘要。
    const summary = releaseSummaryDecoder(detail, "$.release");
    for (const medium of detail.media) {
      for (const track of medium.tracks) {
        tracks.set(track.id, {
          track_id: track.id, title: track.title, artist: track.artist, group_id: placement.groupId,
          present: !catalog.hiddenReleases.has(releaseId), duration_seconds: track.duration_seconds, release: summary,
          source: { source_kind: track.source_kind, codec: track.codec, bit_depth: track.bit_depth, sample_rate: track.sample_rate, channels: track.channels, bitrate: track.bitrate },
        });
      }
    }
  }
  return [...tracks.values()];
}

function trackIndex(data: PersonalData): Map<string, TrackSummaryDTO> {
  return new Map([...personalTracksOf(data.catalog).map((track): [string, TrackSummaryDTO] => [track.track_id, track]), ...data.tracks]);
}

/** 登记/更新摘要不会自动收藏，也不会改变个人 revision；用于模拟扫描事实变化。 */
export function seedPersonalTracks(data: PersonalData, tracks: readonly TrackSummaryDTO[]): void {
  for (const track of tracks) data.tracks.set(track.track_id.toLowerCase(), snapshot({ ...track, track_id: track.track_id.toLowerCase() }));
}

export function personalAlbumOf(group: ReleaseGroupSummaryDTO): PersonalAlbumDTO {
  return { group_id: group.id, available: true, title: group.representative.title, artist: group.representative.artist, release_count: group.release_count, representative: snapshot(group.representative) };
}

export function seedPersonalAlbums(data: PersonalData, albums: readonly PersonalAlbumDTO[]): void {
  for (const album of albums) data.albums.set(album.group_id.toLowerCase(), snapshot(album));
}

function albumFor(data: PersonalData, id: string): PersonalAlbumDTO | undefined {
  const explicit = data.albums.get(id);
  if (explicit !== undefined) return explicit;
  const group = groupDetailFor(data.catalog, id);
  if (group === undefined) return undefined;
  const visible = group.releases.filter((release) => !data.catalog.hiddenReleases.has(release.id));
  const representative = visible[0];
  return representative === undefined
    ? { group_id: id, available: false, title: group.representative.title, artist: group.representative.artist, release_count: 0, representative: null }
    : { group_id: id, available: true, title: representative.title, artist: representative.artist, release_count: visible.length, representative };
}

/** 输入顺序就是首屏显示顺序；传相同 createdAt 可以构造时间并列的 UUID 排序场景。 */
export function seedFavoriteTracks(data: PersonalData, userId: string, tracks: readonly (TrackSummaryDTO | string)[], createdAt?: string): void {
  seedPersonalTracks(data, tracks.filter((track): track is TrackSummaryDTO => typeof track !== "string"));
  const index = trackIndex(data);
  const ids = tracks.map((track) => (typeof track === "string" ? track : track.track_id).toLowerCase());
  if (ids.some((id) => !index.has(id))) throw new Error("收藏种子包含未登记曲目");
  const user = personalUserOf(data, userId);
  for (const id of [...ids].reverse()) {
    if (user.tracks.has(id)) continue;
    user.tracks.set(id, createdAt ?? now(data));
    user.revision += 1;
  }
}

export function seedFavoriteAlbums(data: PersonalData, userId: string, albums: readonly (PersonalAlbumDTO | ReleaseGroupSummaryDTO | string)[], createdAt?: string): void {
  const ids = albums.map((album) => {
    if (typeof album === "string") return album.toLowerCase();
    const value = "group_id" in album ? album : personalAlbumOf(album);
    seedPersonalAlbums(data, [value]);
    return value.group_id.toLowerCase();
  });
  if (ids.some((id) => albumFor(data, id) === undefined)) throw new Error("收藏种子包含未登记专辑");
  const user = personalUserOf(data, userId);
  for (const id of [...ids].reverse()) {
    if (user.albums.has(id)) continue;
    user.albums.set(id, createdAt ?? now(data));
    user.revision += 1;
  }
}

function reindex(playlist: PersonalPlaylist): void {
  playlist.items = playlist.items.map((item, index) => ({ ...item, position: index + 1 }));
  playlist.state.track_count = playlist.items.length;
  playlist.state.over_limit_count = Math.max(0, playlist.items.length - TRACK_LIMIT);
}

export type PlaylistSeed = { name: string; trackIds?: readonly string[]; id?: string; revision?: number; createdAt?: string; updatedAt?: string };
/** 可直接构造合法撤销后的超限状态；普通 API 新增仍受 10,000 首限制。 */
export function seedPlaylist(data: PersonalData, userId: string, input: PlaylistSeed): PersonalPlaylist {
  const ids = [...new Set((input.trackIds ?? []).map((id) => id.toLowerCase()))];
  const tracks = trackIndex(data);
  if (ids.some((id) => !tracks.has(id))) throw new Error("歌单种子包含未登记曲目");
  const created = input.createdAt ?? now(data);
  const playlist: PersonalPlaylist = {
    state: { id: input.id?.toLowerCase() ?? newId(data), name: input.name, revision: input.revision ?? 1, track_count: ids.length, track_limit: TRACK_LIMIT, over_limit_count: Math.max(0, ids.length - TRACK_LIMIT), created_at: created, updated_at: input.updatedAt ?? created },
    items: ids.map((trackId, index) => ({ id: newId(data), track_id: trackId, position: index + 1, added_at: created })),
    undo: new Map(),
  };
  personalUserOf(data, userId).playlists.set(playlist.state.id, playlist);
  return playlist;
}

function summaryOf(data: PersonalData, playlist: PersonalPlaylist, tracks: Map<string, TrackSummaryDTO>): PlaylistSummaryDTO {
  let duration = 0;
  let unknown = 0;
  let missing = 0;
  const covers = new Map<string, PlaylistSummaryDTO["cover_groups"][number]>();
  for (const item of playlist.items) {
    const track = tracks.get(item.track_id);
    if (track === undefined) throw new Error("个人关系引用的曲目事实缺失");
    if (track.duration_seconds === null || !Number.isFinite(track.duration_seconds) || track.duration_seconds <= 0) unknown += 1;
    else duration += track.duration_seconds;
    if (!track.present) missing += 1;
    if (covers.size < 4 && !covers.has(track.group_id)) {
      const album = albumFor(data, track.group_id);
      covers.set(track.group_id, { group_id: track.group_id, artwork: album === undefined ? track.release.artwork : album.representative?.artwork ?? null });
    }
  }
  return { ...playlist.state, duration_seconds: duration, unknown_duration_count: unknown, missing_count: missing, cover_groups: [...covers.values()] };
}

function queueOf(ids: readonly string[], tracks: Map<string, TrackSummaryDTO>): PersonalQueueDTO {
  const all = ids.map((id) => {
    const track = tracks.get(id);
    if (track === undefined) throw new Error("个人关系引用的曲目事实缺失");
    return track;
  });
  const eligible = all.filter((track) => track.present);
  const items = eligible.slice(0, TRACK_LIMIT);
  const omitted = eligible.length - items.length;
  return { items, total: all.length, missing_count: all.length - eligible.length, eligible_count: eligible.length, returned_count: items.length, omitted_count: omitted, limit: TRACK_LIMIT, truncated: omitted > 0 };
}

const sortedFavorites = (favorites: Map<string, string>) => [...favorites].sort(([leftId, leftTime], [rightId, rightTime]) => Date.parse(rightTime) - Date.parse(leftTime) || leftId.localeCompare(rightId));
const sortedPlaylists = (user: PersonalUser) => [...user.playlists.values()].sort((left, right) => Date.parse(right.state.updated_at) - Date.parse(left.state.updated_at) || left.state.id.localeCompare(right.state.id));

type RouteKind = "lookup" | "favoriteTracks" | "favoriteAlbums" | "favoriteQueue" | "favoriteTrack" | "favoriteAlbum" | "playlists" | "playlist" | "items" | "item" | "restore" | "move" | "queue" | "containing";
const routes: { pattern: RegExp; kind: RouteKind; methods: string[] }[] = [
  { pattern: /^\/api\/v1\/me\/favorites\/lookup$/, kind: "lookup", methods: ["POST"] },
  { pattern: /^\/api\/v1\/me\/favorites\/tracks\/queue$/, kind: "favoriteQueue", methods: ["GET"] },
  { pattern: /^\/api\/v1\/me\/favorites\/tracks$/, kind: "favoriteTracks", methods: ["GET"] },
  { pattern: /^\/api\/v1\/me\/favorites\/albums$/, kind: "favoriteAlbums", methods: ["GET"] },
  { pattern: /^\/api\/v1\/me\/favorites\/tracks\/([^/]+)$/, kind: "favoriteTrack", methods: ["PUT", "DELETE"] },
  { pattern: /^\/api\/v1\/me\/favorites\/albums\/([^/]+)$/, kind: "favoriteAlbum", methods: ["PUT", "DELETE"] },
  { pattern: /^\/api\/v1\/me\/playlists$/, kind: "playlists", methods: ["GET", "POST"] },
  { pattern: /^\/api\/v1\/me\/playlists\/containing-track\/([^/]+)$/, kind: "containing", methods: ["GET"] },
  { pattern: /^\/api\/v1\/me\/playlists\/([^/]+)$/, kind: "playlist", methods: ["GET", "PATCH", "DELETE"] },
  { pattern: /^\/api\/v1\/me\/playlists\/([^/]+)\/items$/, kind: "items", methods: ["GET", "POST"] },
  { pattern: /^\/api\/v1\/me\/playlists\/([^/]+)\/items\/([^/]+)$/, kind: "item", methods: ["DELETE"] },
  { pattern: /^\/api\/v1\/me\/playlists\/([^/]+)\/restore$/, kind: "restore", methods: ["POST"] },
  { pattern: /^\/api\/v1\/me\/playlists\/([^/]+)\/items\/([^/]+)\/move$/, kind: "move", methods: ["POST"] },
  { pattern: /^\/api\/v1\/me\/playlists\/([^/]+)\/queue$/, kind: "queue", methods: ["GET"] },
];

function objectBody(body: unknown, fields: readonly string[], emptyAllowed = false): Record<string, unknown> | null {
  if (body === undefined && emptyAllowed) return {};
  if (typeof body !== "object" || body === null || Array.isArray(body)) return null;
  return Object.keys(body).every((key) => fields.includes(key)) ? body as Record<string, unknown> : null;
}
function trackIds(value: unknown, optional = false): string[] | null {
  if (value === undefined && optional) return [];
  if (!Array.isArray(value) || value.length > TRACK_LIMIT || (!optional && value.length === 0) || !value.every(uuid)) return null;
  return value.map((id: string) => id.toLowerCase());
}
function validName(name: unknown): name is string {
  return typeof name === "string" && name.trim().length > 0 && [...name.trim()].length <= 100 && !/[\p{Cc}]/u.test(name.trim());
}

type Command =
  | { action: "create"; name: string; trackIds: string[] }
  | { action: "rename"; name: string; revision: number }
  | { action: "delete"; revision: number }
  | { action: "remove"; revision: number }
  | { action: "add"; revision: number; trackIds: string[] }
  | { action: "restore"; token: string }
  | { action: "move"; revision: number; move: "up" | "down" | "first" | "last" | "before"; beforeId: string | null };

function commandOf(kind: RouteKind, request: PersonalRequest): Command | null {
  const action = kind === "playlists" ? "create" : kind === "playlist" ? (request.method === "PATCH" ? "rename" : "delete") : kind === "items" ? "add" : kind === "item" ? "remove" : kind;
  const fields = { create: ["name", "track_ids"], rename: ["name", "expected_revision"], delete: ["expected_revision"], add: ["track_ids", "expected_revision"], remove: ["expected_revision"], restore: ["token"], move: ["expected_revision", "action", "before_item_id"] };
  if (!(action in fields)) return null;
  const body = objectBody(request.body, fields[action as keyof typeof fields]);
  if (body === null) return null;
  if (action === "restore") return uuid(body.token) ? { action, token: body.token.toLowerCase() } : null;
  if (action === "create") {
    const ids = trackIds(body.track_ids, true);
    return validName(body.name) && ids !== null ? { action, name: body.name.trim(), trackIds: ids } : null;
  }
  const revision = body.expected_revision;
  if (!safeRevision(revision)) return null;
  if (action === "rename") return validName(body.name) ? { action, name: body.name.trim(), revision } : null;
  if (action === "add") {
    const ids = trackIds(body.track_ids);
    return ids === null ? null : { action, trackIds: ids, revision };
  }
  if (action === "delete" || action === "remove") return { action, revision };
  if (action === "move") {
    if (body.action === "before") {
      if (body.before_item_id !== null && !uuid(body.before_item_id)) return null;
      return { action, revision, move: "before", beforeId: typeof body.before_item_id === "string" ? body.before_item_id.toLowerCase() : null };
    }
    if ((body.action === "up" || body.action === "down" || body.action === "first" || body.action === "last") && !("before_item_id" in body)) return { action, revision, move: body.action, beforeId: null };
  }
  return null;
}

type Page = { page: number; size: number; revision: number | null };
function pageOf(query: URLSearchParams, size: number, max: number): Page | null {
  const integer = (text: string) => /^\d+$/.test(text) ? Number(text) : NaN;
  const page = integer(query.get("page") ?? "1");
  const pageSize = integer(query.get("page_size") ?? String(size));
  const revision = query.has("revision") ? integer(query.get("revision") ?? "") : null;
  if (!Number.isSafeInteger(page) || page < 1 || !Number.isSafeInteger(pageSize) || pageSize < 1 || pageSize > max || !Number.isSafeInteger((page - 1) * pageSize)) return null;
  return { page, size: pageSize, revision };
}

function changed(data: PersonalData, playlist: PersonalPlaylist): void {
  playlist.state.revision += 1;
  playlist.state.updated_at = now(data);
  reindex(playlist);
}

function execute(data: PersonalData, user: PersonalUser, userId: string, command: Command, playlistId: string, itemId: string): ApiResult {
  const tracks = trackIndex(data);
  if (command.action === "create") {
    if (user.playlists.size >= PLAYLIST_LIMIT) return failure(409, "playlist_limit_reached", "最多创建 1,000 个歌单");
    const unique = [...new Set(command.trackIds)];
    if (unique.some((id) => !tracks.has(id))) return notFound();
    const playlist = seedPlaylist(data, userId, { name: command.name, trackIds: unique });
    return success({ playlist: playlist.state, added_count: unique.length, existing_count: 0, duplicate_count: command.trackIds.length - unique.length }, 201);
  }
  const playlist = user.playlists.get(playlistId);
  if (playlist === undefined) return notFound();
  if (command.action !== "restore" && command.revision !== playlist.state.revision) return conflict();
  // 测试种子可以设置边界版本；不能让替身产生前端无法精确表示的版本。
  if (playlist.state.revision === Number.MAX_SAFE_INTEGER) return failure(503, "database_unavailable", "暂时无法保存");
  if (command.action === "rename") {
    const didChange = playlist.state.name !== command.name;
    if (didChange) { playlist.state.name = command.name; changed(data, playlist); }
    return success({ playlist: playlist.state, changed: didChange });
  }
  if (command.action === "delete") {
    user.playlists.delete(playlistId);
    return success({ playlist_id: playlistId, deleted: true });
  }
  if (command.action === "add") {
    const unique = [...new Set(command.trackIds)];
    if (unique.some((id) => !tracks.has(id))) return notFound();
    const existing = new Set(playlist.items.map((item) => item.track_id));
    const added = unique.filter((id) => !existing.has(id));
    if (added.length > 0 && playlist.items.length + added.length > TRACK_LIMIT) return failure(409, "playlist_track_limit_reached", "每个歌单最多添加 10,000 首");
    if (added.length > 0) {
      const time = now(data);
      playlist.items.push(...added.map((trackId) => ({ id: newId(data), track_id: trackId, position: 0, added_at: time })));
      changed(data, playlist);
    }
    return success({ playlist: playlist.state, added_count: added.length, existing_count: unique.length - added.length, duplicate_count: command.trackIds.length - unique.length });
  }
  if (command.action === "restore") {
    const undo = playlist.undo.get(command.token);
    if (undo === undefined) return notFound();
    if (undo.result !== undefined) return snapshot(undo.result);
    const present = playlist.items.find((item) => item.track_id === undo.item.track_id);
    let body: ItemRestoredDTO;
    if (present !== undefined) body = { playlist: playlist.state, outcome: "already_present", item: present, placement: "unchanged" };
    else {
      let index = undo.item.position - 1;
      let placement: ItemRestoredDTO["placement"] = "original";
      if (playlist.state.revision !== undo.removalRevision) {
        const previous = playlist.items.findIndex((item) => item.track_id === undo.previousTrackId);
        if (previous >= 0) { index = previous + 1; placement = "after_previous"; }
        else { placement = index > playlist.items.length ? "end" : "original_index"; index = Math.min(index, playlist.items.length); }
      }
      playlist.items.splice(index, 0, snapshot(undo.item));
      changed(data, playlist);
      const restored = playlist.items[index];
      if (restored === undefined) throw new Error("撤销未恢复条目");
      body = { playlist: playlist.state, outcome: "restored", item: restored, placement };
    }
    undo.result = snapshot(success(body));
    return undo.result;
  }
  const index = playlist.items.findIndex((item) => item.id === itemId);
  const item = playlist.items[index];
  if (item === undefined) return notFound();
  if (command.action === "remove") {
    const previousTrackId = playlist.items[index - 1]?.track_id ?? null;
    playlist.items.splice(index, 1);
    changed(data, playlist);
    const token = newId(data);
    playlist.undo.set(token, { item: snapshot(item), previousTrackId, removalRevision: playlist.state.revision });
    return success({ playlist: playlist.state, removed: item, undo: { token, removal_revision: playlist.state.revision } });
  }
  let target = command.move === "up" ? Math.max(0, index - 1) : command.move === "down" ? Math.min(playlist.items.length - 1, index + 1) : command.move === "first" ? 0 : playlist.items.length - 1;
  if (command.move === "before" && command.beforeId !== null) {
    const anchor = playlist.items.findIndex((candidate) => candidate.id === command.beforeId);
    if (anchor < 0) return notFound();
    target = anchor > index ? anchor - 1 : anchor;
  }
  const didChange = target !== index;
  if (didChange) {
    playlist.items.splice(index, 1);
    playlist.items.splice(target, 0, item);
    changed(data, playlist);
  }
  return success({ playlist: playlist.state, moved: playlist.items[target], changed: didChange });
}

function respond(data: PersonalData, userId: string | null, request: PersonalRequest): ApiResult {
  const url = new URL(request.path, "http://localhost");
  const route = routes.find((candidate) => candidate.pattern.test(url.pathname));
  if (route === undefined) return failure(404, "not_found", "接口不存在");
  if (!route.methods.includes(request.method)) return failure(405, "method_not_allowed", "请求方法不支持");
  if (userId === null) return failure(401, "unauthorized", "请先登录");
  const captures = route.pattern.exec(url.pathname)?.slice(1) ?? [];
  if (!captures.every(uuid)) return invalid();
  const [targetId = "", itemId = ""] = captures.map((id) => id.toLowerCase());
  const { kind } = route;
  const paginated = request.method === "GET" && ["favoriteTracks", "favoriteAlbums", "playlists", "items"].includes(kind);
  const allowedQueries = paginated ? (kind === "playlists" ? ["page", "page_size"] : ["page", "page_size", "revision"]) : [];
  const query = url.searchParams;
  if ([...query.keys()].some((key) => !allowedQueries.includes(key) || query.getAll(key).length !== 1 || query.get(key) === "")) return invalid();
  if (query.has("revision") && (!/^\d+$/.test(query.get("revision") ?? "") || !safeRevision(Number(query.get("revision")), kind === "items" ? 1 : 0))) return invalid();
  const page = pageOf(query, kind === "playlists" ? 20 : 50, kind === "playlists" ? 20 : 100);
  if (page === null) return failure(400, "invalid_pagination", "分页参数无效");
  const user = personalUserOf(data, userId);
  if (kind === "lookup") {
    const body = objectBody(request.body, ["track_ids", "group_ids"]);
    if (body === null) return invalid();
    const ids = trackIds(body.track_ids, true);
    const groups = body.group_ids === undefined ? [] : body.group_ids;
    if (ids === null || !Array.isArray(groups) || groups.length > 1_000 || !groups.every(uuid)) return invalid();
    return success({ revision: user.revision, tracks: [...new Set(ids)].map((id) => ({ track_id: id, favorited: user.tracks.has(id) })), albums: [...new Set(groups.map((id: string) => id.toLowerCase()))].map((id) => ({ group_id: id, favorited: user.albums.has(id) })) });
  }
  if (kind === "favoriteTrack" || kind === "favoriteAlbum") {
    if (objectBody(request.body, [], true) === null) return invalid();
    const favorites = kind === "favoriteTrack" ? user.tracks : user.albums;
    const favorited = request.method === "PUT";
    if (favorited && !favorites.has(targetId) && (kind === "favoriteTrack" ? !trackIndex(data).has(targetId) : albumFor(data, targetId) === undefined)) return notFound();
    if (favorites.has(targetId) !== favorited) {
      if (user.revision === Number.MAX_SAFE_INTEGER) return failure(503, "database_unavailable", "暂时无法保存");
      if (favorited) favorites.set(targetId, now(data)); else favorites.delete(targetId);
      user.revision += 1;
    }
    return success({ target_kind: kind === "favoriteTrack" ? "track" : "album", target_id: targetId, favorited, created_at: favorites.get(targetId) ?? null, revision: user.revision });
  }
  if (kind === "favoriteTracks" || kind === "favoriteAlbums") {
    if (page.revision !== null && page.revision !== user.revision) return failure(409, "favorites_changed", "收藏列表有更新");
    const all = sortedFavorites(kind === "favoriteTracks" ? user.tracks : user.albums);
    const tracks = kind === "favoriteTracks" ? trackIndex(data) : new Map<string, TrackSummaryDTO>();
    const items = all.slice((page.page - 1) * page.size, page.page * page.size).map(([id, created]) => {
      const value = kind === "favoriteTracks" ? tracks.get(id) : albumFor(data, id);
      if (value === undefined) throw new Error("个人关系引用的目录事实缺失");
      return kind === "favoriteTracks" ? { track: value, created_at: created } : { album: value, created_at: created };
    });
    return success({ items, pagination: { page: page.page, page_size: page.size, total: all.length }, revision: user.revision });
  }
  if (kind === "favoriteQueue") return success({ ...queueOf(sortedFavorites(user.tracks).map(([id]) => id), trackIndex(data)), revision: user.revision });
  if (request.method !== "GET") {
    const command = commandOf(kind, request);
    const rawKey = request.idempotencyKey?.trim();
    if (command === null || !uuid(rawKey)) return invalid();
    const key = rawKey.toLowerCase();
    const fingerprint = JSON.stringify([kind, targetId, itemId, command]);
    const receipt = user.receipts.get(key);
    if (receipt !== undefined) return receipt.fingerprint === fingerprint ? snapshot(receipt.result) : failure(409, "idempotency_conflict", "幂等键已用于其他请求");
    const result = snapshot(execute(data, user, userId, command, targetId, itemId));
    if (result.status < 300) user.receipts.set(key, { fingerprint, result: snapshot(result) });
    return result;
  }
  if (kind === "containing") return success({ track_id: targetId, items: sortedPlaylists(user).filter((playlist) => playlist.items.some((item) => item.track_id === targetId)).map((playlist) => ({ playlist_id: playlist.state.id, revision: playlist.state.revision })) });
  const tracks = trackIndex(data);
  if (kind === "playlists") {
    const all = sortedPlaylists(user);
    return success({ items: all.slice((page.page - 1) * page.size, page.page * page.size).map((playlist) => summaryOf(data, playlist, tracks)), pagination: { page: page.page, page_size: page.size, total: all.length } });
  }
  const playlist = user.playlists.get(targetId);
  if (playlist === undefined) return notFound();
  if (kind === "playlist") return success({ playlist: summaryOf(data, playlist, tracks) });
  if (kind === "queue") return success({ playlist: playlist.state, ...queueOf(playlist.items.map((item) => item.track_id), tracks) });
  if (page.revision !== null && page.revision !== playlist.state.revision) return conflict();
  const items = playlist.items.slice((page.page - 1) * page.size, page.page * page.size).map((item) => {
    const track = tracks.get(item.track_id);
    if (track === undefined) throw new Error("个人关系引用的曲目事实缺失");
    return { id: item.id, position: item.position, added_at: item.added_at, track };
  });
  return success({ playlist: playlist.state, items, pagination: { page: page.page, page_size: page.size, total: playlist.items.length }, page_after_item_id: playlist.items[page.page * page.size]?.id ?? null });
}

/** 两个适配器共用全部路由；响应快照不会被下一次写命令悄悄修改。 */
export function respondToPersonal(data: PersonalData, userId: string | null, request: PersonalRequest): ApiResult {
  return snapshot(respond(data, userId, request));
}
