import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { listApiSampleNames, sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeCatalogState } from "./decoders";

describe("目录状态 decoder", () => {
  const state = sampleBody("search/catalog-status-ready");
  it.each(listApiSampleNames().filter((name) => name.includes("catalog-status")))("真实样本 %s 完整往返", (name) => {
    expect(decodeCatalogState(sampleBody(name))).toEqual(sampleBody(name));
  });
  it.each(["epoch", "search_mode", "artists_state"])("拒绝缺少或 null 的 %s", (field) => {
    expect(() => decodeCatalogState(without(state, [field]))).toThrow(d.DecodeError);
    expect(() => decodeCatalogState(withValue(state, [field], null))).toThrow(d.DecodeError);
  });
  it.each([
    ["epoch", -1], ["epoch", 1.5], ["epoch", "2"], ["epoch", Number.MAX_SAFE_INTEGER + 1],
    ["search_mode", "ready"], ["artists_state", "failed"],
  ])("拒绝非法 %s=%s", (field, value) => {
    expect(() => decodeCatalogState(withValue(state, [String(field)], value))).toThrow(d.DecodeError);
  });
  it("接受未知代次零与⑤预留的整理、就绪状态，不把 epoch 当算法版本", () => {
    for (const artists_state of ["unavailable", "preparing", "ready"]) {
      expect(decodeCatalogState({ epoch: 0, search_mode: "legacy", artists_state })).toEqual({ epoch: 0, search_mode: "legacy", artists_state });
    }
  });
});
