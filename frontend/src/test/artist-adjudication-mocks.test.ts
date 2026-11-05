import { describe, expect, it, vi } from "vitest";
import { installApiMock, type ApiMockRoute } from "../../e2e/support/api-mock";
import { decodeArtistAlbumsPage, decodeArtistPage, decodeArtistParticipationAlbumsPage } from "../features/artists/api/decoders";
import { decodeTrackSummaries } from "../features/catalog/api/track-summary";
import { decodeArtistMergedSources, decodeArtistSplitPreview } from "../features/governance/api/decoders";
import { loadApiSample, sampleBody } from "./api-samples";
import { artistDataOf, artistTestId, primaryArtistId, seedArtist, seedArtistIntegration, type ArtistData, type ArtistResponse } from "./artist-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "./fake-api";

const prefix = "artists/adjudication/";
const body = (name: string) => sampleBody(prefix + name);
const origin = "http://localhost";
type Actor = "curator" | "listener" | null;
type SendOptions = { origin?: string | null; method?: string; key?: string };
type Send = (path: string, input?: unknown, options?: SendOptions) => Promise<ArtistResponse>;

/** 使用真实适配器入口；e2e 只运行已注册的 route 回调，不创建浏览器或网络请求。 */
async function harness(kind: "fake" | "e2e", data: ArtistData, actor: Actor = "curator", browser = false): Promise<Send> {
  if (kind === "fake") {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: actor });
    installArtistsApi(api, data, session, { browserOrigin: browser ? origin : null });
    // eslint-disable-next-line no-restricted-properties -- 这里验证测试 transport，不是产品 API 的调用入口。
    const transport = vi.mocked(globalThis.fetch);
    return async (path, input, options = {}) => {
      const headers = new Headers();
      const suppliedOrigin = options.origin === undefined ? (browser ? null : origin) : options.origin;
      if (suppliedOrigin !== null) headers.set("Origin", suppliedOrigin);
      if (options.key !== undefined) headers.set("Idempotency-Key", options.key);
      const response = await transport(path, { method: options.method ?? (input === undefined ? "GET" : "POST"), headers, ...(input === undefined ? {} : { body: JSON.stringify(input) }) });
      const result: unknown = await response.json();
      return { status: response.status, body: result };
    };
  }
  let routeHandler: (route: ApiMockRoute) => Promise<void> = async () => { throw new Error("没有注册路由"); };
  const mock = await installApiMock({ route: async (_pattern, handler) => { routeHandler = handler; } }, { signedInAs: actor });
  mock.artists = data;
  return async (path, input, options = {}) => {
    const headers: Record<string, string> = {};
    const suppliedOrigin = options.origin === undefined ? origin : options.origin;
    if (suppliedOrigin !== null) headers.origin = suppliedOrigin;
    if (options.key !== undefined) headers["idempotency-key"] = options.key;
    const replies: ArtistResponse[] = [];
    await routeHandler({
      request: () => ({ url: () => new URL(path, origin).href, method: () => options.method ?? (input === undefined ? "GET" : "POST"), headers: () => headers, postData: () => input === undefined ? null : JSON.stringify(input), postDataJSON: () => structuredClone(input) }),
      fulfill: async (response) => {
        if (typeof response?.body !== "string") throw new Error("期望 JSON 响应");
        replies.push({ status: response.status ?? 200, body: JSON.parse(response.body) as unknown });
      },
      abort: async () => { throw new Error("意外中断请求"); },
    });
    const response = replies[0];
    if (response === undefined) throw new Error("路由没有响应");
    return response;
  };
}

function matchesSample(response: ArtistResponse, name: string) {
  const expected = loadApiSample(prefix + name);
  expect(response.status).toBe(expected.status);
  if (expected.status < 400) expect(response.body).toEqual(expected.body);
  else {
    // request_id 与安全 message 可因请求而异；状态、code 及错误信封形状不得漂移。
    expect(response.body).toEqual({ error: { code: expect.any(String), message: expect.any(String) }, request_id: expect.any(String) });
    expect(response.body).toMatchObject({ error: { code: Reflect.get(Reflect.get(expected.body as object, "error") as object, "code") } });
  }
}
function first<T>(items: readonly T[]): T {
  const item = items[0];
  if (item === undefined) throw new Error("真实样本缺少首项");
  return item;
}
function albumFixture() {
  const data = artistDataOf();
  const any = decodeArtistParticipationAlbumsPage(body("albums-any"));
  const defaults = decodeArtistAlbumsPage(body("albums-default"));
  const summary = data.details.get(primaryArtistId())?.artist;
  if (!summary) throw new Error("缺少艺人种子");
  seedArtist(data, { ...summary, id: any.artist_id });
  data.albums.set(any.artist_id, defaults.items);
  seedArtistIntegration(data, any.artist_id, { participationAlbums: any.items });
  return { data, any, defaults, path: `/api/v1/artists/${any.artist_id}/albums` };
}
function trackFixture(name = "tracks-summaries") {
  const data = artistDataOf();
  const tracks = decodeTrackSummaries(body(name)).items;
  data.tracks.clear();
  data.tracks.set("sample:track_artist", tracks);
  return { data, tracks, ids: tracks.map((track) => track.track_id) };
}

// 每一个边界都穿过 Vitest FakeApi 与 e2e route 的鉴权/分派，不能只验证共享 responder。
describe.each(["fake", "e2e"] as const)("I1/I2/I4/I6 %s 适配器与真实样本", (kind) => {
  it.each(["merged-sources", "merged-sources-empty", "merged-sources-after-restore", "merged-sources-empty-keys"])("I1 %s 保留完整来源和空数组", async (name) => {
    const data = artistDataOf(), id = primaryArtistId();
    seedArtistIntegration(data, id, { mergedSources: decodeArtistMergedSources(body(name)).items });
    const send = await harness(kind, data);
    matchesSample(await send(`/api/v1/artists/${id}/merged-sources`), name);
  });
  it.each([
    [null, "valid", "ready", "merged-sources-unauthorized"],
    ["listener", "valid", "ready", "merged-sources-forbidden"],
    ["curator", "invalid", "ready", "merged-sources-invalid-id"],
    ["curator", "unknown", "ready", "merged-sources-not-found"],
    ["curator", "valid", "preparing", "merged-sources-preparing"],
  ] as const)("I1 %s/%s/%s", async (actor, idKind, state, sample) => {
    const data = artistDataOf(); data.state = state;
    const id = idKind === "valid" ? primaryArtistId() : idKind === "invalid" ? "not-uuid" : artistTestId(9876);
    matchesSample(await (await harness(kind, data, actor))(`/api/v1/artists/${id}/merged-sources`), sample);
  });
  it("I1 参数校验先于准备态，鉴权先于参数校验，UUID 大小写规范化", async () => {
    const data = artistDataOf(); data.state = "preparing";
    matchesSample(await (await harness(kind, data))('/api/v1/artists/not-uuid/merged-sources'), "merged-sources-invalid-id");
    matchesSample(await (await harness(kind, data, null))('/api/v1/artists/not-uuid/merged-sources'), "merged-sources-unauthorized");
    data.state = "ready";
    const detail = first([...data.details.values()]);
    const id = artistTestId(222); seedArtist(data, { ...detail.artist, id });
    matchesSample(await (await harness(kind, data))(`/api/v1/artists/${id.toUpperCase()}/merged-sources`), "merged-sources-empty");
  });
  it("I1 合并→恢复→再合并→拆给新人不留过期来源/名字键", async () => {
    const data = artistDataOf(), id = primaryArtistId();
    const target = data.details.get(id);
    if (!target) throw new Error("缺少目标");
    const source = seedArtist(data, { ...target.artist, id: artistTestId(333), name: "Teresa Teng" });
    const send = await harness(kind, data);
    const merge = async (n: number) => {
      expect((await send(`/api/v1/artists/${id}/merge`, { expected_revision: target.revision, source_artist_id: source.artist.id, source_expected_revision: source.revision }, { key: artistTestId(n) })).status).toBe(200);
    };
    const split = async (restore: boolean, n: number) => {
      const sources = decodeArtistMergedSources((await send(`/api/v1/artists/${id}/merged-sources`)).body).items;
      expect(sources).toHaveLength(1);
      const keys = first(sources).name_key_ids;
      const scope = { mode: "aliases", name_key_ids: keys, expected_revision: target.revision };
      const preview = decodeArtistSplitPreview((await send(`/api/v1/artists/${id}/split-preview`, scope)).body);
      expect((await send(`/api/v1/artists/${id}/split`, { ...scope, basis: preview.basis, target: restore ? { kind: "restore", artist_id: source.artist.id, expected_revision: source.revision } : { kind: "new", display_name: "拆开的身份" } }, { key: artistTestId(n) })).status).toBe(200);
    };
    await merge(9001); await split(true, 9002);
    matchesSample(await send(`/api/v1/artists/${id}/merged-sources`), "merged-sources-after-restore");
    await merge(9003); await split(false, 9004);
    const items = decodeArtistMergedSources((await send(`/api/v1/artists/${id}/merged-sources`)).body).items;
    expect(items).toHaveLength(1);
    expect(first(items)).toMatchObject({ revision: 4, name_key_ids: [], operation_id: artistTestId(90003) });
    expect(first(items).name_key_ids).toEqual(first(decodeArtistMergedSources(body("merged-sources-empty-keys")).items).name_key_ids);
  });

  it("I2 默认/any/再默认的形状、数量与排序；非参与代表不被替换", async () => {
    const { data, path, any } = albumFixture(); const send = await harness(kind, data, "listener");
    matchesSample(await send(path), "albums-default");
    matchesSample(await send(path + "?participation=any"), "albums-any");
    matchesSample(await send(path), "albums-default-after-any");
    expect(new Set(any.items.map((item) => item.group.id)).size).toBe(any.items.length);
    expect(any.items.some((item) => item.credited_release_id !== item.group.representative.id)).toBe(true);
    expect(any.items.some((item) => item.roles.includes("composer") && item.roles.includes("performer"))).toBe(true);
  });
  it.each([
    ["default", "merged-sources"], ["default", "artist-candidates"],
    ["any", "merged-sources"], ["any", "artist-candidates"],
  ] as const)("I2 成员按 %s 模式搜索标题 %s 不误触管理员路由", async (mode, keyword) => {
    const { data, path, any, defaults } = albumFixture();
    const title = `现场 ${keyword} 录音`;
    first(any.items).group.representative.title = title;
    first(defaults.items).group.representative.title = title;
    seedArtistIntegration(data, any.artist_id, { participationAlbums: any.items });
    const send = await harness(kind, data, "listener");
    const query = new URLSearchParams({ q: keyword });
    if (mode === "any") query.set("participation", "any");
    const response = await send(`${path}?${query}`);
    expect(response.status).toBe(200);
    const page = mode === "any" ? decodeArtistParticipationAlbumsPage(response.body) : decodeArtistAlbumsPage(response.body);
    expect(page.items).toEqual(mode === "any" ? [first(any.items)] : defaults.items);
    expect(page.pagination).toEqual({ page: 1, page_size: 50, total: 1 });
  });
  it.each(["merged-sources", "artist-candidates"] as const)("成员读取真实管理员路径 %s 仍为403", async (route) => {
    const data = artistDataOf();
    const path = route === "merged-sources" ? `/api/v1/artists/${primaryArtistId()}/merged-sources` : "/api/v1/artist-candidates";
    const send = await harness(kind, data, "listener");
    matchesSample(await send(`${path}?q=普通专辑`), "merged-sources-forbidden");
  });
  it("I2 q 两种模式均筛专辑标题；分页/空结果保留总数与版本身份", async () => {
    const { data, path, any } = albumFixture(); const send = await harness(kind, data);
    for (const [query, sample] of [["participation=any&q=英文", "albums-any-query"], ["participation=any&page=2&page_size=1", "albums-any-page"]]) {
      const actual = await send(`${path}?${query}`);
      const page = decodeArtistParticipationAlbumsPage(actual.body), expected = decodeArtistParticipationAlbumsPage(body(sample ?? ""));
      expect(actual.status).toBe(200);
      expect(page.pagination).toEqual(expected.pagination);
      // recorder 对每个文件独立规范化 UUID：跨样本用标题/角色比对，再断言完整项仍是本快照里的原项。
      expect(page.items.map((item) => [item.group.representative.title, item.roles])).toEqual(expected.items.map((item) => [item.group.representative.title, item.roles]));
      expect(page.items).toEqual(any.items.slice(1));
    }
    matchesSample(await send(`${path}?q=英文`), "albums-default-query");
    matchesSample(await send(`${path}?participation=any&q=不存在`), "albums-any-empty");
    matchesSample(await send(`${path}?participation=any&q=曲目`), "albums-any-empty");
  });
  it("I2 %/_/~ 是字面包含，任一可见成员标题命中不改代表，200 UTF-8 字节可用", async () => {
    const { data, path, any, defaults } = albumFixture(); const send = await harness(kind, data);
    const item = first(any.items);
    data.albumTitles.set(item.group.id, ["另一版 %_~ MiXeD"]);
    for (const mode of ["", "participation=any&"]) {
      for (const q of ["%", "_", "~", "%_~", " mixed "]) {
        const response = await send(`${path}?${mode}q=${encodeURIComponent(q)}`);
        expect(response.status).toBe(200);
        const page = mode ? decodeArtistParticipationAlbumsPage(response.body) : decodeArtistAlbumsPage(response.body);
        expect(page.items).toEqual(mode ? [item] : defaults.items);
        expect(page.pagination.total).toBe(1);
      }
      const empty = await send(`${path}?${mode}q=${encodeURIComponent("中".repeat(66) + "ab")}`);
      expect(empty.status).toBe(200); expect(empty.body).toMatchObject({ items: [], pagination: { total: 0 } });
    }
    expect(first(any.items).group.representative.title).toBe("同名乙专辑");
  });
  it.each([
    ["participation=", "albums-empty-participation"], ["participation=all", "albums-invalid-participation"],
    ["participation=any&participation=any", "albums-repeated-participation"], ["q=a&q=b", "albums-repeated-query"], ["q=&q=", "albums-repeated-query"],
    ["q=" + "a".repeat(201), "albums-long-query"], ["q=" + encodeURIComponent("中".repeat(67)), "albums-long-query"], ["q=%00", "albums-long-query"], ["q=%FF", "albums-long-query"],
    ...["page=0", "page=-1", "page=1.5", "page=abc", "page_size=0", "page_size=101", "page_size=2.5", "page=9223372036854775807&page_size=100", "page=bad&page=1", "page_size=101&page_size=1"].map((q) => [q, "albums-invalid-pagination"]),
  ])("I2 参数 %s", async (query, sample) => {
    const { data, path } = albumFixture(); data.state = "preparing";
    matchesSample(await (await harness(kind, data))(`${path}?${query}`), sample ?? "");
  });
  it("I2 分页首值/默认值与准备态按真实 handler 行为", async () => {
    const { data, path } = albumFixture(); const send = await harness(kind, data);
    matchesSample(await send(`${path}?page=1&page=bad&page_size=50&page_size=101`), "albums-default");
    matchesSample(await send(`${path}?page=&page_size=`), "albums-default");
    data.state = "preparing";
    matchesSample(await send(`${path}?participation=any`), "albums-any-preparing");
  });

  it.each(["tracks-summaries", "tracks-summaries-after-merge", "tracks-summaries-preparing"])("I4 %s 按安全摘要完整返回", async (name) => {
    const { data, ids } = trackFixture(name); if (name.endsWith("preparing")) data.state = "preparing";
    matchesSample(await (await harness(kind, data, "listener"))("/api/v1/tracks/summaries", { track_ids: ids }), name);
  });
  it("I4 首次输入顺序/小写去重/unknown 省略/missing 保留", async () => {
    const { data, tracks, ids } = trackFixture(); const send = await harness(kind, data, "listener");
    const item = { ...structuredClone(first(tracks)), track_id: artistTestId(444) };
    data.tracks.set("uppercase:track_artist", [item]);
    const response = await send("/api/v1/tracks/summaries", { track_ids: [item.track_id.toUpperCase(), ids[2], ids[0], artistTestId(9999), item.track_id, ids[0]] });
    expect(response.status).toBe(200);
    expect(decodeTrackSummaries(response.body).items).toEqual([item, tracks[2], tracks[0]]);
    expect(decodeTrackSummaries(response.body).items.some((track) => !track.present)).toBe(true);
    matchesSample(await send("/api/v1/tracks/summaries", { track_ids: [artistTestId(9999)] }), "tracks-summaries-unknown");
  });
  it("I4 preparing 清空曲目与发行的全部 refs，不修改 ready 种子、不返回503", async () => {
    const { data, tracks, ids } = trackFixture(); const before = structuredClone(tracks); data.state = "preparing";
    const send = await harness(kind, data, "listener");
    const response = await send("/api/v1/tracks/summaries", { track_ids: ids });
    expect(response.status).toBe(200);
    expect(decodeTrackSummaries(response.body).items).toEqual(before.map((track) => ({ ...track, artist_refs: [], release: { ...track.release, artist_refs: [], album_artist_refs: [] } })));
    expect(tracks).toEqual(before); data.state = "ready";
    matchesSample(await send("/api/v1/tracks/summaries", { track_ids: ids }), "tracks-summaries");
  });
  it.each([{}, null, [], { track_ids: null }, { track_ids: [] }, { track_ids: "bad" }, { track_ids: [null] }, { track_ids: [1] }, { track_ids: ["not-uuid"] }, { track_ids: ["00000000000040008000000000000001"] }, { track_ids: [artistTestId(1)], extra: true }])("I4 错误形状/UUID %j", async (input) => {
    const { data } = trackFixture();
    matchesSample(await (await harness(kind, data, "listener"))("/api/v1/tracks/summaries", input), "tracks-summaries-invalid-id");
  });
  it("I4 0/1000/1001 不同 UUID 与1001次重复同一ID", async () => {
    const { data, tracks } = trackFixture(); const send = await harness(kind, data, "listener");
    const expanded = Array.from({ length: 1000 }, (_, i) => ({ ...structuredClone(first(tracks)), track_id: artistTestId(10_000 + i) }));
    data.tracks.set("boundary:track_artist", expanded);
    const ids = expanded.map((track) => track.track_id).reverse();
    matchesSample(await send("/api/v1/tracks/summaries", { track_ids: [] }), "tracks-summaries-empty");
    const response = await send("/api/v1/tracks/summaries", { track_ids: [...ids, first(ids).toUpperCase()] });
    expect(response.status).toBe(200);
    expect(decodeTrackSummaries(response.body).items.map((track) => track.track_id)).toEqual(ids);
    matchesSample(await send("/api/v1/tracks/summaries", { track_ids: [...ids, artistTestId(99999)] }), "tracks-summaries-over-limit");
    const duplicate = await send("/api/v1/tracks/summaries", { track_ids: Array.from({ length: 1001 }, (_, i) => i % 2 ? first(ids).toUpperCase() : first(ids)) });
    expect(duplicate.status).toBe(200); expect(decodeTrackSummaries(duplicate.body).items).toHaveLength(1);
    matchesSample(await send("/api/v1/tracks/summaries", { track_ids: [first(ids), "invalid"] }), "tracks-summaries-invalid-id");
  });
  it.each([
    ["listener", null, "tracks-summaries-origin-required"], [null, null, "tracks-summaries-origin-required"],
    ["listener", "https://untrusted.invalid", "tracks-summaries-origin-forbidden"], [null, "https://untrusted.invalid", "tracks-summaries-origin-forbidden"],
    [null, origin, "tracks-summaries-unauthorized"],
  ] as const)("I4 Origin/鉴权 %s/%s", async (actor, requestOrigin, sample) => {
    const { data, ids } = trackFixture();
    matchesSample(await (await harness(kind, data, actor))("/api/v1/tracks/summaries", { track_ids: ids }, { origin: requestOrigin }), sample);
  });
  it("I4 正常页面模拟由 transport 补同源，不要求产品代码设置 Origin", async () => {
    const { data, ids } = trackFixture();
    matchesSample(await (await harness(kind, data, "listener", true))("/api/v1/tracks/summaries", { track_ids: ids }), "tracks-summaries");
  });
  it("I6 album_artist 联合发行和专辑角色，release_artist 仅自身且 DTO 不改词表", async () => {
    const data = artistDataOf(); data.details.clear();
    for (const artist of decodeArtistPage(body("role-album-artist")).items) seedArtist(data, artist);
    const send = await harness(kind, data, "listener");
    matchesSample(await send("/api/v1/artists?role=album_artist"), "role-album-artist");
    matchesSample(await send("/api/v1/artists?role=release_artist"), "role-release-artist");
    const invalid = await send("/api/v1/artists?role=album_artist&role=release_artist");
    expect(invalid.status).toBe(400); expect(invalid.body).toMatchObject({ error: { code: "invalid_artist_role" } });
  });
});
