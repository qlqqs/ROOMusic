import { expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue } from "../../../test/mutate";
import { decodeCurationSource, decodeCurationSources } from "./decoders";

it("艺人资料源保留图片和简介能力，密钥 optional 可以省略或传布尔值", () => {
  const sample = sampleBody("curation/sources");
  const sources = decodeCurationSources(sample);
  expect(sources).toStrictEqual(sample);
  expect(sources.items.find(({ id }) => id === "theaudiodb")?.capabilities).toEqual(["artist_image", "artist_bio"]);
  const fanart = sources.items.find(({ id }) => id === "fanarttv");
  expect(fanart?.keys).toEqual([
    { configured: false, last_four: "", name: "project", status: "unchecked" },
    { configured: false, last_four: "", name: "personal", status: "unchecked", optional: true },
  ]);
  if (fanart?.keys === undefined) throw new Error("缺少 Fanart.tv 密钥样本");
  for (const optional of [true, false]) {
    const input = { ...fanart, keys: fanart.keys.map((key) => key.name === "project" ? { ...key, optional } : key) };
    expect(decodeCurationSource(input)).toStrictEqual(input);
  }

  expect(() => decodeCurationSource(withValue(fanart, ["capabilities", 0], "artist_thumbnail"))).toThrow(d.DecodeError);
  for (const optional of ["true", null]) {
    expect(() => decodeCurationSource(withValue(fanart, ["keys", 1, "optional"], optional))).toThrow(d.DecodeError);
  }
});

it("解码网络搜索的模型与搜索依赖及 search_unconfigured 状态", () => {
  const source = decodeCurationSources(sampleBody("curation/sources")).items.find(({ id }) => id === "web_search");
  if (source === undefined) throw new Error("缺少网络搜索来源样本");
  expect(source.requirements).toEqual([{ kind: "model", ready: false }, { kind: "search", ready: false }]);
  const missing = {
    ...source,
    inactive_reason: "search_unconfigured",
    requirements: [{ kind: "model", ready: true }, { kind: "search", ready: false }],
  };
  expect(decodeCurationSource(missing)).toStrictEqual(missing);
  expect(() => decodeCurationSource(withValue(missing, ["requirements", 0, "kind"], "reviewer"))).toThrow(d.DecodeError);
  expect(() => decodeCurationSource(withValue(missing, ["requirements", 0, "ready"], "false"))).toThrow(d.DecodeError);
});
