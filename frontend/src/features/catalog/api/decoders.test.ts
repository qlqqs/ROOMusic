import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeReleaseDetail, decodeReleaseEvidence, decodeReleaseGroupDetail, decodeReleaseGroupPage } from "./decoders";

describe("decodeReleaseGroupPage", () => {
  const page = sampleBody("release-groups/list-one-with-artwork");

  it("解码空列表与单项列表，总数在 pagination.total", () => {
    const empty = decodeReleaseGroupPage(sampleBody("release-groups/list-empty"));
    expect(empty.items).toEqual([]);
    expect(empty.pagination).toEqual({ page: 1, page_size: 1, total: 0 });
    const one = decodeReleaseGroupPage(sampleBody("release-groups/list-one"));
    expect(one.pagination.total).toBe(1);
    expect(one.items[0]?.representative.artwork).toBeNull();
  });

  it("解码带封面的代表版本", () => {
    const [group] = decodeReleaseGroupPage(page).items;
    expect(group?.release_count).toBe(1);
    expect(group?.representative).toMatchObject({
      title: "Sample Preludes",
      artist: "Sample Pianist",
      year: 2001,
      artwork: { mime: "image/webp", width: 640, height: 480 },
    });
  });

  it("每页最多 100 个组", () => {
    const first = (page as { items: unknown[] }).items[0];
    const many = (count: number) => withValue(page, ["items"], Array.from({ length: count }, () => structuredClone(first)));
    expect(decodeReleaseGroupPage(many(100)).items).toHaveLength(100);
    expect(() => decodeReleaseGroupPage(many(101))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 pagination", without(page, ["pagination"])],
    ["总数为负", withValue(page, ["pagination", "total"], -1)],
    ["page_size 超过 100", withValue(page, ["pagination", "page_size"], 101)],
    ["page 为 0", withValue(page, ["pagination", "page"], 0)],
    ["组的版本数为 0", withValue(page, ["items", 0, "release_count"], 0)],
    ["缺少代表版本", without(page, ["items", 0, "representative"])],
    ["代表版本缺少标题", without(page, ["items", 0, "representative", "title"])],
    ["年份为 0", withValue(page, ["items", 0, "representative", "year"], 0)],
    ["年份是字符串", withValue(page, ["items", 0, "representative", "year"], "2001")],
    ["封面缺少宽度", without(page, ["items", 0, "representative", "artwork", "width"])],
    ["封面高度为 0", withValue(page, ["items", 0, "representative", "artwork", "height"], 0)],
    ["attention_count 为负", withValue(page, ["items", 0, "attention_count"], -1)],
    ["组 id 不是 UUID", withValue(page, ["items", 0, "id"], "group-1")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeReleaseGroupPage(input)).toThrow(d.DecodeError);
  });
});

// 用样本里的一项克隆出 count 项，作为数组上限的正反例。
function cloneItems<T>(item: T, count: number): T[] {
  return Array.from({ length: count }, () => structuredClone(item));
}

describe("decodeReleaseGroupDetail", () => {
  const single = sampleBody("release-groups/get");
  const multiple = sampleBody("release-groups/get-multiple");

  it("解码单版本的组：代表版本就是唯一的成员", () => {
    const group = decodeReleaseGroupDetail(single);
    expect(group.release_count).toBe(1);
    expect(group.releases).toHaveLength(1);
    expect(group.releases[0]).toEqual(group.representative);
    expect(group.representative).toMatchObject({ title: "Sample Symphonies", year: 1998, medium_count: 2, track_count: 3 });
  });

  it("解码多版本的组：成员按代表版本规则排序，代表版本排在最前", () => {
    const group = decodeReleaseGroupDetail(multiple);
    expect(group.release_count).toBe(2);
    expect(group.releases.map((release) => release.title)).toEqual(["Sample Symphonies", "Sample Preludes"]);
    expect(group.representative.id).toBe(group.releases[0]?.id);
  });

  it("成员摘要至少一个、最多 100 个；release_count 是完整数量，可以大于成员数", () => {
    const member = (multiple as { releases: unknown[] }).releases[0];
    const withMembers = (count: number) => withValue(multiple, ["releases"], cloneItems(member, count));
    expect(decodeReleaseGroupDetail(withMembers(100)).releases).toHaveLength(100);
    expect(() => decodeReleaseGroupDetail(withMembers(101))).toThrow(d.DecodeError);
    expect(() => decodeReleaseGroupDetail(withMembers(0))).toThrow(d.DecodeError);
    expect(decodeReleaseGroupDetail(withValue(multiple, ["release_count"], 250)).release_count).toBe(250);
  });

  it.each([
    ["缺少 releases", without(multiple, ["releases"])],
    ["缺少代表版本", without(multiple, ["representative"])],
    ["成员缺少标题", without(multiple, ["releases", 1, "title"])],
    ["成员的封面宽度为 0", withValue(single, ["releases", 0, "artwork"], { resource_id: "a.png", mime: "image/png", width: 0, height: 1 })],
    ["release_count 为 0", withValue(multiple, ["release_count"], 0)],
    ["组 id 不是 UUID", withValue(multiple, ["id"], "group")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeReleaseGroupDetail(input)).toThrow(d.DecodeError);
  });
});

describe("decodeReleaseDetail", () => {
  const double = sampleBody("releases/get");
  const single = sampleBody("releases/get-single");
  const cue = sampleBody("releases/get-cue");
  const firstTrack = (double as { media: { tracks: unknown[] }[] }).media[0]?.tracks[0];
  const firstMedium = (double as { media: unknown[] }).media[0];

  it("解码双碟专辑：版本信息、两个介质、曲目与署名", () => {
    const release = decodeReleaseDetail(double);
    expect(release).toMatchObject({
      title: "Sample Symphonies",
      album_artist: "Sample Orchestra",
      year: 1998,
      genre: "Classical",
      label: "Sample Label",
      catalog_number: "SL-0001",
      barcode: "0123456789012",
      edition: "Remastered",
      candidate_kind: "ordinary_directory",
      medium_count: 2,
      track_count: 3,
      artwork: null,
    });
    expect(release.media.map((medium) => medium.tracks.map((track) => track.title))).toEqual([["Allegro", "Andante"], ["Finale"]]);
    const [first] = release.media[0]?.tracks ?? [];
    expect(first?.credits.map((credit) => credit.role)).toEqual(["composer", "conductor", "performer"]);
    expect(first).toMatchObject({ duration_seconds: 240, codec: "flac", bit_depth: 16, sample_rate: 44100, channels: 2, bitrate: null, cue: null });
    expect(release.credits).toEqual([{ role: "album_artist", name: "Sample Orchestra" }]);
    expect(release.evidence).toHaveLength(9);
  });

  it("解码单曲专辑：大多数版本字段为空，封面存在", () => {
    const release = decodeReleaseDetail(single);
    expect(release).toMatchObject({ genre: null, catalog_number: null, edition: null, label: null, barcode: null, media_type: null });
    expect(release.artwork).toEqual({ resource_id: "sample-cover.webp", mime: "image/webp", width: 640, height: 480 });
    expect(release.media[0]?.tracks[0]?.credits).toEqual([]);
  });

  it("解码 CUE 分轨：帧数与折算的秒数", () => {
    const [first, second] = decodeReleaseDetail(cue).media[0]?.tracks ?? [];
    expect(first).toMatchObject({ source_kind: "cue_virtual", cue: { index_frames: 0, end_frames: 9000, start_seconds: 0, end_seconds: 120, isrc: "XXAAA0500001" } });
    expect(second?.cue).toMatchObject({ index_frames: 9000, end_frames: 18000, start_seconds: 120, end_seconds: 240 });
  });

  it("词表字段按字符串解码：后端新增的来源、处理方式与分类不会让解码失败", () => {
    const changed = withValue(withValue(withValue(double, ["evidence", 0, "source"], "future_source"), ["evidence", 0, "action"], "future_action"), ["candidate_kind"], "future_kind");
    expect(decodeReleaseDetail(changed)).toMatchObject({ candidate_kind: "future_kind" });
  });

  it("介质最多 256 个", () => {
    const withMedia = (count: number) => withValue(double, ["media"], cloneItems(firstMedium, count));
    expect(decodeReleaseDetail(withMedia(256)).media).toHaveLength(256);
    expect(() => decodeReleaseDetail(withMedia(257))).toThrow(d.DecodeError);
  });

  it("曲目上限是整张发行版本的合计，不是每个介质各一份", () => {
    // 曲目不带署名：署名也有自己的合计上限，这里只测曲目总数。
    const bareTrack = withValue(firstTrack, ["credits"], []);
    const mediumWith = (count: number) => withValue(firstMedium, ["tracks"], cloneItems(bareTrack, count));
    const release = (...counts: number[]) => withValue(double, ["media"], counts.map(mediumWith));
    expect(decodeReleaseDetail(release(10_000)).media[0]?.tracks).toHaveLength(10_000);
    expect(decodeReleaseDetail(release(5_000, 5_000)).media).toHaveLength(2);
    expect(() => decodeReleaseDetail(release(5_000, 5_001))).toThrow(d.DecodeError);
    expect(() => decodeReleaseDetail(release(10_001))).toThrow(d.DecodeError);
  });

  it("曲目署名也按整张发行版本合计限制（每首最多 100 条，合计最多 10,000 条）", () => {
    const credit = { role: "performer", name: "Someone" };
    const trackWith = (count: number) => withValue(firstTrack, ["credits"], cloneItems(credit, count));
    const release = (trackCount: number, creditCount: number) =>
      withValue(double, ["media"], [withValue(firstMedium, ["tracks"], cloneItems(trackWith(creditCount), trackCount))]);
    expect(decodeReleaseDetail(release(100, 100)).media[0]?.tracks).toHaveLength(100);
    expect(() => decodeReleaseDetail(release(101, 100))).toThrow(d.DecodeError);
    expect(() => decodeReleaseDetail(release(1, 101))).toThrow(d.DecodeError);
  });

  it("Release 署名与证据摘要各最多 100 条", () => {
    const credit = { role: "producer", name: "Someone" };
    const evidence = (double as { evidence: unknown[] }).evidence[0];
    expect(decodeReleaseDetail(withValue(double, ["credits"], cloneItems(credit, 100))).credits).toHaveLength(100);
    expect(() => decodeReleaseDetail(withValue(double, ["credits"], cloneItems(credit, 101)))).toThrow(d.DecodeError);
    expect(decodeReleaseDetail(withValue(double, ["evidence"], cloneItems(evidence, 100))).evidence).toHaveLength(100);
    expect(() => decodeReleaseDetail(withValue(double, ["evidence"], cloneItems(evidence, 101)))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 media", without(double, ["media"])],
    ["缺少 candidate_kind", without(double, ["candidate_kind"])],
    ["缺少 credits", without(double, ["credits"])],
    ["缺少 evidence", without(double, ["evidence"])],
    ["label 是数字", withValue(double, ["label"], 5)],
    ["曲目缺少 id", without(double, ["media", 0, "tracks", 0, "id"])],
    ["曲目 id 不是 UUID", withValue(double, ["media", 0, "tracks", 0, "id"], "track-1")],
    ["曲目缺少 source_kind", without(double, ["media", 0, "tracks", 0, "source_kind"])],
    ["时长为负", withValue(double, ["media", 0, "tracks", 0, "duration_seconds"], -1)],
    ["时长是字符串", withValue(double, ["media", 0, "tracks", 0, "duration_seconds"], "240")],
    ["位深是小数", withValue(double, ["media", 0, "tracks", 0, "bit_depth"], 16.5)],
    ["曲目署名缺少角色", without(double, ["media", 0, "tracks", 0, "credits", 0, "role"])],
    ["介质缺少曲目列表", without(double, ["media", 0, "tracks"])],
    ["封面宽度为 0", withValue(single, ["artwork", "width"], 0)],
    ["CUE 起始帧是字符串", withValue(cue, ["media", 0, "tracks", 0, "cue", "index_frames"], "0")],
    ["CUE 缺少 isrc 字段", without(cue, ["media", 0, "tracks", 0, "cue", "isrc"])],
    ["证据缺少规则", without(double, ["evidence", 0, "rule_id"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeReleaseDetail(input)).toThrow(d.DecodeError);
  });
});

describe("decodeReleaseEvidence", () => {
  const evidence = sampleBody("releases/evidence");
  const field = (evidence as { fields: unknown[] }).fields[0];

  it("解码管理员证据：字段值、候选、分组规则与来源引用", () => {
    const decoded = decodeReleaseEvidence(evidence);
    expect(decoded.truncated).toBe(false);
    expect(decoded.fields).toHaveLength(9);
    expect(decoded.fields[0]).toEqual({
      field: "album_artist",
      source: "tag",
      confidence: "high",
      action: "auto_apply",
      rule_id: "majority_v1",
      value: "Sample Orchestra",
      candidates: ["Sample Orchestra"],
      reason_code: null,
    });
    expect(decoded.grouping).toEqual({
      candidate_kind: "ordinary_directory",
      rule_id: "organizer_v2",
      source_refs: ["double-album/d1-01.flac", "double-album/d1-02.flac", "double-album/d2-01.flac"],
      reason_codes: [],
    });
  });

  it("没有分组证据时 grouping 为 null；truncated 原样保留", () => {
    const decoded = decodeReleaseEvidence(withValue(withValue(evidence, ["grouping"], null), ["truncated"], true));
    expect(decoded.grouping).toBeNull();
    expect(decoded.truncated).toBe(true);
  });

  it("字段证据最多 100 项，候选与原因码最多 20 个，来源引用最多 100 个", () => {
    expect(decodeReleaseEvidence(withValue(evidence, ["fields"], cloneItems(field, 100))).fields).toHaveLength(100);
    expect(() => decodeReleaseEvidence(withValue(evidence, ["fields"], cloneItems(field, 101)))).toThrow(d.DecodeError);
    expect(decodeReleaseEvidence(withValue(evidence, ["fields", 0, "candidates"], Array.from({ length: 20 }, (_, index) => `候选 ${index}`))).fields[0]?.candidates).toHaveLength(20);
    expect(() => decodeReleaseEvidence(withValue(evidence, ["fields", 0, "candidates"], Array.from({ length: 21 }, (_, index) => `候选 ${index}`)))).toThrow(d.DecodeError);
    expect(decodeReleaseEvidence(withValue(evidence, ["grouping", "reason_codes"], Array.from({ length: 20 }, (_, index) => `原因 ${index}`))).grouping?.reason_codes).toHaveLength(20);
    expect(() => decodeReleaseEvidence(withValue(evidence, ["grouping", "reason_codes"], Array.from({ length: 21 }, (_, index) => `原因 ${index}`)))).toThrow(d.DecodeError);
    const refs = (count: number) => Array.from({ length: count }, (_, index) => `album/${index}.flac`);
    expect(decodeReleaseEvidence(withValue(evidence, ["grouping", "source_refs"], refs(100))).grouping?.source_refs).toHaveLength(100);
    expect(() => decodeReleaseEvidence(withValue(evidence, ["grouping", "source_refs"], refs(101)))).toThrow(d.DecodeError);
  });

  it("值、候选与来源引用都不超过 500 个字符", () => {
    expect(() => decodeReleaseEvidence(withValue(evidence, ["fields", 0, "value"], "长".repeat(501)))).toThrow(d.DecodeError);
    expect(() => decodeReleaseEvidence(withValue(evidence, ["fields", 0, "candidates", 0], "长".repeat(501)))).toThrow(d.DecodeError);
    expect(() => decodeReleaseEvidence(withValue(evidence, ["grouping", "source_refs", 0], `album/${"a".repeat(500)}`))).toThrow(d.DecodeError);
  });

  it.each([
    ["绝对路径", "/srv/music/album/01.flac"],
    ["以双斜杠开头", "//server/share/01.flac"],
    ["file 协议", "file:///srv/music/01.flac"],
    ["盘符", "C:/music/01.flac"],
    ["反斜杠", "album\\01.flac"],
    ["上级目录", "../album/01.flac"],
    ["中间的上级目录", "album/../../01.flac"],
    ["当前目录段", "album/./01.flac"],
    ["空段", "album//01.flac"],
    ["末尾的斜杠", "album/"],
    ["空字符串", ""],
  ])("拒绝不安全的来源引用：%s", (_name, ref) => {
    expect(() => decodeReleaseEvidence(withValue(evidence, ["grouping", "source_refs", 0], ref))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 release_id", without(evidence, ["release_id"])],
    ["release_id 不是 UUID", withValue(evidence, ["release_id"], "release")],
    ["缺少 fields", without(evidence, ["fields"])],
    ["缺少 truncated", without(evidence, ["truncated"])],
    ["truncated 是字符串", withValue(evidence, ["truncated"], "false")],
    ["缺少 grouping", without(evidence, ["grouping"])],
    ["字段证据缺少候选", without(evidence, ["fields", 0, "candidates"])],
    ["字段证据缺少 reason_code", without(evidence, ["fields", 0, "reason_code"])],
    ["字段值是数字", withValue(evidence, ["fields", 0, "value"], 5)],
    ["分组缺少规则", without(evidence, ["grouping", "rule_id"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeReleaseEvidence(input)).toThrow(d.DecodeError);
  });
});
