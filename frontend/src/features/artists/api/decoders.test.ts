import { describe, expect, it } from "vitest";
import { decode as d, decodeErrorEnvelope } from "../../../shared/api";
import { loadApiSample, sampleBody } from "../../../test/api-samples";
import { artistBio, artistImage } from "../../../test/artist-media-data";
import { withValue, without } from "../../../test/mutate";
import {
  decodeArtistAlbumsPage, decodeArtistDetail, decodeArtistNamesPage, decodeArtistPage,
  decodeArtistParticipationAlbumsPage, decodeArtistQueue, decodeArtistTracksPage,
} from "./decoders";

type Decoder = (input: unknown) => unknown;
const contracts: readonly [string, Decoder][] = [
  ["list-name", decodeArtistPage], ["list-album-count", decodeArtistPage], ["list-initial", decodeArtistPage],
  ["list-role", decodeArtistPage], ["list-empty", decodeArtistPage], ["list-out-of-range", decodeArtistPage],
  ["detail", decodeArtistDetail], ["detail-redirect", decodeArtistDetail], ["detail-unavailable", decodeArtistDetail],
  ["detail-with-media", decodeArtistDetail],
  ["names", decodeArtistNamesPage], ["names-out-of-range", decodeArtistNamesPage],
  ["albums", decodeArtistAlbumsPage], ["role-tracks", decodeArtistTracksPage], ["role-tracks-missing", decodeArtistTracksPage],
  ["queue", decodeArtistQueue], ["queue-random", decodeArtistQueue], ["queue-empty", decodeArtistQueue],
];
const detail = decodeArtistDetail(sampleBody("artists/detail"));
const queue = decodeArtistQueue(sampleBody("artists/queue"));
const albumPage = decodeArtistAlbumsPage(sampleBody("artists/albums"));
const roles = ["release_artist", "album_artist", "track_artist", "lyricist", "composer", "arranger", "conductor", "performer", "producer"];
// participation=any 暂无真实样本：只在真实专辑摘要上补已裁决的角色，不能把它报告为真实响应往返。
const participation = { ...albumPage, items: albumPage.items.map((item) => ({ ...item, roles: ["composer"] })) };

function first<T>(items: readonly T[]): T {
  const item = items[0];
  if (item === undefined) throw new Error("测试需要至少一项真实样本");
  return item;
}
const idOf = (index: number) => `abcdef00-0000-4000-8000-${String(index + 1).padStart(12, "0")}`;
const pages: readonly [string, Decoder, { items: readonly unknown[]; pagination: { page: number; page_size: number; total: number } }][] = [
  ["列表", decodeArtistPage, decodeArtistPage(sampleBody("artists/list-name"))],
  ["名字", decodeArtistNamesPage, decodeArtistNamesPage(sampleBody("artists/names"))],
  ["专辑", decodeArtistAlbumsPage, albumPage],
  ["任何身份专辑", decodeArtistParticipationAlbumsPage, participation],
  ["曲目", decodeArtistTracksPage, decodeArtistTracksPage(sampleBody("artists/role-tracks"))],
];

function queueOf(total: number, missingTotal = 0) {
  const returned = Math.min(total, 10_000);
  return { ...queue, items: Array.from({ length: returned }, (_, index) => ({ ...first(queue.items), track_id: idOf(index) })),
    total, returned, truncated: total > returned, missing_total: missingTotal };
}

describe("艺人读取真实响应", () => {
  it.each(contracts)("%s 完整解码与 JSON 往返", (name, decode) => {
    const sample = loadApiSample(`artists/${name}`);
    expect(sample.status).toBe(200);
    expect(decode(sample.body)).toStrictEqual(sample.body);
    expect(decode(JSON.parse(JSON.stringify(sample.body)))).toStrictEqual(sample.body);
  });
  it.each(["list-preparing", "detail-preparing", "queue-preparing"])("%s 是503整理状态，不伪装成空数据", (name) => {
    const sample = loadApiSample(`artists/${name}`);
    expect(sample.status).toBe(503);
    expect(decodeErrorEnvelope(sample.body)).toStrictEqual(sample.body);
    expect(decodeErrorEnvelope(sample.body).error.code).toBe("artist_catalog_preparing");
    expect(() => decodeArtistPage(sample.body)).toThrow(d.DecodeError);
  });
  it.each(contracts)("%s 顶层字段全部必传，拒绝null及非对象", (name, decode) => {
    const body = sampleBody(`artists/${name}`);
    if (typeof body !== "object" || body === null) throw new Error("样本必须是对象");
    for (const field of Object.keys(body)) expect(() => decode(without(body, [field])), field).toThrow(d.DecodeError);
    for (const input of [null, [], "artist", 1]) expect(() => decode(input)).toThrow(d.DecodeError);
  });
  it("不可用、重定向和缺失身份保留，不与不存在混淆", () => {
    expect(decodeArtistDetail(sampleBody("artists/detail-unavailable")).artist.available).toBe(false);
    expect(decodeArtistDetail(sampleBody("artists/detail-redirect")).redirect_artist_id).not.toBeNull();
    const missing = decodeArtistTracksPage(sampleBody("artists/role-tracks-missing"));
    expect(missing.items.some((track) => !track.present)).toBe(true);
    expect(queue.items.every((track) => track.present)).toBe(true);
    expect(queue.missing_total).toBeGreaterThan(0);
  });
});

describe("艺人摘要、名字与详情边界", () => {
  it.each([
    [["artist", "id"], "bad-id"], [["artist", "initial_group"], "a"], [["artist", "initial_group"], "汉字"],
    [["artist", "roles"], ["singer"]], [["artist", "roles"], null], [["artist", "available"], "true"],
    [["artist", "album_count"], -1], [["artist", "track_count"], 1.5], [["artist", "name"], null],
    [["revision"], 0], [["revision"], "1"], [["name_source"], "guessed"], [["name_source"], null],
    [["redirect_artist_id"], "bad-id"], [["names"], null], [["role_counts"], null],
    [["names", 0, "id"], "bad-id"], [["names", 0, "key_id"], null], [["names", 0, "is_display_name"], 1],
    [["names", 0, "origin"], ""], [["names", 0, "origin"], "x".repeat(33)],
    [["role_counts", 0, "role"], "singer"], [["role_counts", 0, "present"], -1], [["role_counts", 0, "total"], null],
  ] satisfies [readonly (string | number)[], unknown][])("拒绝非法字段 %j=%j", (path, value) => {
    expect(() => decodeArtistDetail(withValue(detail, path, value))).toThrow(d.DecodeError);
  });
  it("摘要、名字和角色计数内部字段不能省略", () => {
    for (const [prefix, value] of [[ ["artist"], detail.artist ], [["names", 0], first(detail.names)], [["role_counts", 0], first(detail.role_counts)]] as const) {
      for (const field of Object.keys(value)) expect(() => decodeArtistDetail(without(detail, [...prefix, field])), field).toThrow(d.DecodeError);
    }
  });
  it("九种角色与合法首字母可接受，角色数组十项拒绝", () => {
    for (const initial of [..."ABCDEFGHIJKLMNOPQRSTUVWXYZ", "kana", "#"]) {
      expect(decodeArtistDetail({ ...detail, artist: { ...detail.artist, initial_group: initial, roles } }).artist.roles).toEqual(roles);
    }
    const roleCounts = roles.map((role) => ({ ...first(detail.role_counts), role }));
    for (const size of [8, 9]) {
      const body = { ...detail, artist: { ...detail.artist, roles: roles.slice(0, size) }, role_counts: roleCounts.slice(0, size) };
      expect(decodeArtistDetail(body)).toStrictEqual(body);
    }
    expect(() => decodeArtistDetail({ ...detail, artist: { ...detail.artist, roles: [...roles, "composer"] } })).toThrow(d.DecodeError);
    expect(() => decodeArtistDetail({ ...detail, role_counts: [...roleCounts, first(roleCounts)] })).toThrow(d.DecodeError);
  });
  it("详情首批名字最多20条，总数不可少于已返回名字", () => {
    const names = Array.from({ length: 20 }, (_, index) => ({ ...first(detail.names), id: idOf(index) }));
    const body = { ...detail, names, names_pagination: { page: 1, page_size: 20, total: 21 } };
    expect(decodeArtistDetail(body).names).toHaveLength(20);
    expect(() => decodeArtistDetail({ ...body, names: [...names, first(detail.names)] })).toThrow(d.DecodeError);
    expect(() => decodeArtistDetail({ ...body, names_pagination: { page: 1, page_size: 21, total: 21 } })).toThrow(d.DecodeError);
    expect(() => decodeArtistDetail({ ...body, names_pagination: { page: 1, page_size: 20, total: 19 } })).toThrow(d.DecodeError);
  });
  it("详情名字数量不可超过声明的页大小", () => {
    expect(() => decodeArtistDetail({ ...detail, names: [first(detail.names), { ...first(detail.names), id: idOf(1) }],
      names_pagination: { page: 1, page_size: 1, total: 2 } })).toThrow(d.DecodeError);
  });
  it("角色present是total的子集，不得大于total", () => {
    expect(() => decodeArtistDetail({ ...detail, role_counts: [{ role: "composer", total: 1, present: 2 }] })).toThrow(d.DecodeError);
  });
});

describe("艺人分页边界", () => {
  it.each(pages)("%s 接受100项，拒绝101项、页大小或总数不足", (_name, decode, page) => {
    const item = first(page.items);
    const full = { ...page, items: Array(100).fill(item), pagination: { page: 1, page_size: 100, total: 100 } };
    expect(decode(full)).toStrictEqual(full);
    expect(() => decode({ ...full, items: Array(101).fill(item), pagination: { page: 1, page_size: 100, total: 101 } })).toThrow(d.DecodeError);
    expect(() => decode({ ...full, pagination: { page: 1, page_size: 101, total: 100 } })).toThrow(d.DecodeError);
    expect(() => decode({ ...full, pagination: { page: 1, page_size: 99, total: 100 } })).toThrow(d.DecodeError);
    expect(() => decode({ ...full, pagination: { page: 1, page_size: 100, total: 99 } })).toThrow(d.DecodeError);
  });
  it.each(pages)("%s 拒绝坏分页与缺字段，接受超末页保留总数", (_name, decode, page) => {
    for (const field of ["page", "page_size", "total"]) {
      expect(() => decode(without(page, ["pagination", field]))).toThrow(d.DecodeError);
      for (const value of [null, -1, 1.5, "2"]) expect(() => decode(withValue(page, ["pagination", field], value))).toThrow(d.DecodeError);
    }
    for (const field of ["page", "page_size"]) expect(() => decode(withValue(page, ["pagination", field], 0))).toThrow(d.DecodeError);
    const beyond = { ...page, items: [], pagination: { page: 99, page_size: 100, total: 1 } };
    expect(decode(beyond)).toStrictEqual(beyond);
  });
  it("任何身份专辑必须提供非空合法角色，不丢实际署名版本", () => {
    expect(decodeArtistParticipationAlbumsPage(participation)).toStrictEqual(participation);
    expect(first(decodeArtistParticipationAlbumsPage(participation).items).credited_release_id).toBe(first(albumPage.items).credited_release_id);
    expect(() => decodeArtistParticipationAlbumsPage(without(participation, ["items", 0, "roles"]))).toThrow(d.DecodeError);
    for (const size of [8, 9]) {
      const body = withValue(participation, ["items", 0, "roles"], roles.slice(0, size));
      expect(decodeArtistParticipationAlbumsPage(body)).toStrictEqual(body);
    }
    for (const invalidRoles of [null, [], ["singer"], [...roles, "composer"]]) {
      expect(() => decodeArtistParticipationAlbumsPage(withValue(participation, ["items", 0, "roles"], invalidRoles))).toThrow(d.DecodeError);
    }
  });
});

describe("艺人播放队列合同", () => {
  it.each([0, 1, 1000, 10_000, 10_001])("总候选%s时精确返回min(total,10000)，缺失不计入total", (total) => {
    const body = queueOf(total, 3);
    expect(decodeArtistQueue(body)).toStrictEqual(body);
    expect(body.returned).toBe(Math.min(total, 10_000));
    expect(body.truncated).toBe(total > 10_000);
  });
  it.each([
    [["order"], "shuffle"], [["seed"], "not-a-uuid"], [["items"], null], [["total"], -1],
    [["returned"], 1.5], [["limit"], 9999], [["missing_total"], -1], [["missing_total"], null], [["truncated"], "false"],
    [["items", 0, "present"], false], [["items", 0, "artist_refs"], null], [["items", 0, "source", "codec"], 1],
  ] satisfies [readonly (string | number)[], unknown][])("拒绝队列非法字段 %j=%j", (path, value) => {
    expect(() => decodeArtistQueue(withValue(queue, path, value))).toThrow(d.DecodeError);
  });
  it("random必须有UUID seed，album必须为null，缺字段也拒绝", () => {
    const seed = idOf(1);
    expect(decodeArtistQueue({ ...queue, order: "random", seed }).seed).toBe(seed);
    expect(decodeArtistQueue({ ...queue, order: "album", seed: null }).seed).toBeNull();
    expect(() => decodeArtistQueue({ ...queue, order: "random", seed: null })).toThrow(d.DecodeError);
    expect(() => decodeArtistQueue({ ...queue, order: "album", seed })).toThrow(d.DecodeError);
    expect(() => decodeArtistQueue(without(queue, ["seed"]))).toThrow(d.DecodeError);
  });
  it("returned、items、total和truncated必须相符，10001项数组拒绝", () => {
    const body = queueOf(1);
    expect(() => decodeArtistQueue({ ...body, returned: 0 })).toThrow(d.DecodeError);
    expect(() => decodeArtistQueue({ ...body, total: 0 })).toThrow(d.DecodeError);
    expect(() => decodeArtistQueue({ ...body, truncated: true })).toThrow(d.DecodeError);
    expect(() => decodeArtistQueue({ ...body, total: 2 })).toThrow(d.DecodeError);
    expect(() => decodeArtistQueue({ ...body, items: Array(10_001).fill(first(queue.items)), returned: 10_001, total: 10_001 })).toThrow(d.DecodeError);
  });
  it.each([2, 10_001])("不能把只返回1条的短队列标记truncated来冒充完整候选窗口（total=%s）", (total) => {
    expect(() => decodeArtistQueue({ ...queueOf(1), total, truncated: true })).toThrow(d.DecodeError);
  });
});

describe("M8 艺人图片与简介合同", () => {
  const mediaDetail = { ...detail, artist: { ...detail.artist, image: artistImage.resource }, image: artistImage, bio: artistBio };
  const list = decodeArtistPage(sampleBody("artists/list-name"));

  it("列表摘要保留图片 ID 或 null，拒绝缺字段和非法 ID", () => {
    for (const image of [artistImage.resource, null]) {
      const body = withValue(list, ["items", 0, "image"], image);
      expect(decodeArtistPage(body)).toStrictEqual(body);
    }
    for (const image of ["", "x".repeat(256), 23]) {
      expect(() => decodeArtistPage(withValue(list, ["items", 0, "image"], image))).toThrow(d.DecodeError);
    }
    expect(() => decodeArtistPage(without(list, ["items", 0, "image"]))).toThrow(d.DecodeError);
  });

  it("保留完整图片与简介、各允许的来源及可空署名", () => {
    expect(decodeArtistDetail(mediaDetail)).toStrictEqual(mediaDetail);
    const noMedia = { ...mediaDetail, artist: { ...mediaDetail.artist, image: null }, image: null, bio: null };
    expect(decodeArtistDetail(noMedia)).toStrictEqual(noMedia);
    for (const source of ["fanarttv", "theaudiodb", "commons"]) {
      const image = { ...artistImage, source, author: null, license: null, license_url: null };
      expect(decodeArtistDetail({ ...mediaDetail, image }).image).toEqual(image);
    }
    const bio = { ...artistBio, source: "theaudiodb", lang: "en", license: null, license_url: null };
    expect(decodeArtistDetail({ ...mediaDetail, bio }).bio).toEqual(bio);
  });

  it.each([
    ["image", "source", "wikipedia"], ["image", "resource", "a".repeat(256)], ["image", "author", "a".repeat(501)],
    ["image", "license", "a".repeat(201)], ["image", "source_url", "a".repeat(2049)],
    ["bio", "source", "commons"], ["bio", "lang", "ja"], ["bio", "text", "字".repeat(4001)],
    ["bio", "license", "a".repeat(201)], ["bio", "license_url", "a".repeat(2049)],
  ])("拒绝非法 %s.%s", (field, key, value) => {
    expect(() => decodeArtistDetail(withValue(mediaDetail, [field, key], value))).toThrow(d.DecodeError);
  });

  it("图片与简介对象的字段必须齐备，包括可空的署名字段", () => {
    for (const field of ["image", "bio"] as const) {
      expect(() => decodeArtistDetail(without(mediaDetail, [field]))).toThrow(d.DecodeError);
      for (const key of Object.keys(mediaDetail[field])) {
        expect(() => decodeArtistDetail(without(mediaDetail, [field, key])), `${field}.${key}`).toThrow(d.DecodeError);
      }
    }
  });
});

it("接受后端长度上限内含非 BMP 字符的简介", () => {
  const bio = { ...artistBio, text: "𠮷".repeat(4000) };
  expect(decodeArtistDetail({ ...detail, bio }).bio).toEqual(bio);
});
