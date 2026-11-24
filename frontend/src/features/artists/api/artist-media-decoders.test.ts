import { expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { decodeArtistDetail, decodeArtistLinkPreview, decodeArtistMediaReject, decodeArtistMediaWrite } from "./decoders";

const manualDetail = decodeArtistDetail(sampleBody("artists/detail-manual-media"));

it("读取人工图片与简介、可空来源地址和当前外部对应", () => {
  expect(manualDetail.image).toMatchObject({ source: "manual", source_url: null });
  expect(manualDetail.bio).toMatchObject({ source: "manual", source_url: null });
  expect(manualDetail.links).toEqual({
    musicbrainz: "00000000-0000-4000-8000-000000000002",
    wikidata: "Q900001",
  });
  const links = { musicbrainz: null, wikidata: null };
  expect(decodeArtistDetail({ ...manualDetail, links }).links).toEqual(links);
});

it("保留图片和简介的插件来源", () => {
  const source = "plugin:artist-media-01";
  const detail = decodeArtistDetail({
    ...manualDetail,
    image: { ...manualDetail.image, source },
    bio: { ...manualDetail.bio, source },
  });
  expect(detail.image?.source).toBe(source);
  expect(detail.bio?.source).toBe(source);
});

it.each(["plugin:", "plugin:ab", "plugin:ABC", "plugin:artist_media", `plugin:${"a".repeat(41)}`])(
  "拒绝非法的插件来源 %s",
  (source) => {
    expect(() => decodeArtistDetail({ ...manualDetail, image: { ...manualDetail.image, source } })).toThrow(/image\.source/);
    expect(() => decodeArtistDetail({ ...manualDetail, bio: { ...manualDetail.bio, source } })).toThrow(/bio\.source/);
  },
);

it("拒绝不合法的对应条目号", () => {
  expect(() => decodeArtistDetail({ ...manualDetail, links: { ...manualDetail.links, musicbrainz: "Q1" } })).toThrow(/links\.musicbrainz/);
  expect(() => decodeArtistDetail({ ...manualDetail, links: { ...manualDetail.links, wikidata: "Q0" } })).toThrow(/links\.wikidata/);
});

it.each(["set-image", "set-bio", "set-links"])("解码写入结果 %s，拒绝非法版本号", (name) => {
  const sample = sampleBody(`artists/${name}`);
  const result = decodeArtistMediaWrite(sample);
  expect(result).toEqual(sample);
  expect(() => decodeArtistMediaWrite({ ...result, revision: 0 })).toThrow(/revision/);
});

it("解码标记结果中的人工层与自动层，拒绝未知层", () => {
  const sample = sampleBody("artists/reject-media");
  const result = decodeArtistMediaReject(sample);
  expect(result).toEqual(sample);
  expect(result.layer).toBe("manual");
  expect(decodeArtistMediaReject({ ...result, layer: "auto" }).layer).toBe("auto");
  expect(() => decodeArtistMediaReject({ ...result, layer: "plugin" })).toThrow(/layer/);
});

it("解码预览及可空字段，拒绝未知来源和非布尔的名字匹配结果", () => {
  const sample = sampleBody("artists/link-preview");
  const preview = decodeArtistLinkPreview(sample);
  expect(preview).toEqual(sample);
  const wikidata = { ...preview, source: "wikidata", external_id: "Q42", wikidata: "Q42", musicbrainz: null, description: null };
  expect(decodeArtistLinkPreview(wikidata)).toEqual(wikidata);
  expect(() => decodeArtistLinkPreview({ ...preview, source: "manual" })).toThrow(/source/);
  expect(() => decodeArtistLinkPreview({ ...preview, name_matches: "false" })).toThrow(/name_matches/);
});
