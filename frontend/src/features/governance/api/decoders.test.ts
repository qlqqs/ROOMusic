import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { CORRECTABLE_FIELDS, decodeCorrectionResult, decodeCorrectionState, decodeGroupChangeResult } from "./decoders";

describe("decodeCorrectionState", () => {
  const initial = sampleBody("corrections/get");
  const corrected = sampleBody("corrections/get-corrected");

  it("解码没有修正的状态：八个字段按固定顺序，修正为 null，有效值等于扫描值", () => {
    const state = decodeCorrectionState(initial);
    expect(state.revision).toBe(1);
    expect(state.fields.map((field) => field.field)).toEqual([...CORRECTABLE_FIELDS]);
    for (const field of state.fields) {
      expect(field.correction).toBeNull();
      expect(field.effective_value).toEqual(field.scan_value);
    }
    expect(state.fields[2]).toMatchObject({ field: "year", scan_value: 1998 });
  });

  it("解码修正后的状态：改成某个值、明确设为空（没有 value）、没有修正三种互不混淆", () => {
    const state = decodeCorrectionState(corrected);
    const byName = Object.fromEntries(state.fields.map((field) => [field.field, field]));
    expect(byName["title"]).toMatchObject({ scan_value: "Sample Symphonies", effective_value: "Sample Symphonies (corrected)", correction: { mode: "set", value: "Sample Symphonies (corrected)" } });
    expect(byName["year"]).toMatchObject({ scan_value: 1998, effective_value: null, correction: { mode: "null" } });
    expect(byName["year"]?.correction).not.toHaveProperty("value");
    expect(byName["genre"]?.correction).toBeNull();
  });

  it.each([
    ["缺少 revision", without(initial, ["revision"])],
    ["revision 为 0", withValue(initial, ["revision"], 0)],
    ["缺少 fields", without(initial, ["fields"])],
    ["字段多于八个", withValue(initial, ["fields"], Array.from({ length: 9 }, () => (initial as { fields: unknown[] }).fields[0]))],
    ["未知字段名", withValue(initial, ["fields", 0, "field"], "source_type")],
    ["release_id 不是 UUID", withValue(initial, ["release_id"], "release-1")],
    ["缺少 uncertain", without(initial, ["fields", 0, "uncertain"])],
    ["uncertain 不是布尔值", withValue(initial, ["fields", 0, "uncertain"], "false")],
    ["缺少 scan_value", without(initial, ["fields", 0, "scan_value"])],
    ["缺少 effective_value", without(initial, ["fields", 0, "effective_value"])],
    ["缺少 correction", without(initial, ["fields", 0, "correction"])],
    ["未知的修正方式", withValue(corrected, ["fields", 0, "correction", "mode"], "clear")],
    ["年份值是小数", withValue(initial, ["fields", 2, "scan_value"], 1998.5)],
    ["年份值超界", withValue(initial, ["fields", 2, "scan_value"], 10000)],
    ["值是布尔值", withValue(initial, ["fields", 3, "scan_value"], true)],
    ["值超过 4096 个字符", withValue(initial, ["fields", 3, "scan_value"], "x".repeat(4097))],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeCorrectionState(input)).toThrow(d.DecodeError);
  });
});

describe("decodeCorrectionResult", () => {
  const patched = sampleBody("corrections/patched");

  it("解码修正成功的响应：新的状态加 operation_id", () => {
    const result = decodeCorrectionResult(patched);
    expect(result.revision).toBe(2);
    expect(result.operation_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(result.fields.filter((field) => field.correction !== null).map((field) => field.field)).toEqual(["title", "year", "label"]);
  });

  it.each([
    ["缺少 operation_id", without(patched, ["operation_id"])],
    ["operation_id 不是 UUID", withValue(patched, ["operation_id"], "op-1")],
    ["缺少 fields", without(patched, ["fields"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeCorrectionResult(input)).toThrow(d.DecodeError);
  });

  it("读取状态的响应没有 operation_id，不能当作修正结果", () => {
    expect(() => decodeCorrectionResult(sampleBody("corrections/get"))).toThrow(d.DecodeError);
  });
});

describe("decodeGroupChangeResult", () => {
  const joined = sampleBody("group-changes/joined");
  const detached = sampleBody("group-changes/detached");

  it("解码归入与移出的响应：现在的组、原来的组、新的版本号和操作 ID", () => {
    const joinedResult = decodeGroupChangeResult(joined);
    const detachedResult = decodeGroupChangeResult(detached);
    expect(joinedResult.revision).toBe(2);
    expect(detachedResult.revision).toBe(3);
    expect(joinedResult.group_id).not.toBe(joinedResult.previous_group_id);
    expect(detachedResult.release_id).toBe(joinedResult.release_id);
  });

  it.each([
    ["缺少 group_id", without(joined, ["group_id"])],
    ["group_id 不是 UUID", withValue(joined, ["group_id"], "group-1")],
    ["缺少 previous_group_id", without(joined, ["previous_group_id"])],
    ["缺少 operation_id", without(joined, ["operation_id"])],
    ["revision 为 0", withValue(joined, ["revision"], 0)],
    ["revision 是字符串", withValue(joined, ["revision"], "2")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeGroupChangeResult(input)).toThrow(d.DecodeError);
  });
});
