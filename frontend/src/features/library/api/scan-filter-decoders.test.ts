import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { without } from "../../../test/mutate";
import {
  decodeActiveScan,
  decodeRootExclusions,
  decodeRootList,
  decodeRootOperationList,
  decodeScan,
  decodeScanPage,
} from "./decoders";

const root = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "测试音乐",
  path: "测试音乐",
  status: "active",
  revision: 3,
  created_at: "2026-11-08T00:00:00Z",
  updated_at: "2026-11-08T00:00:00Z",
  excluded_paths: ["Artist/Bootlegs", "附录"],
};

const scan = {
  id: "00000000-0000-4000-8000-000000000002",
  scan_run_id: "00000000-0000-4000-8000-000000000002",
  status: "succeeded",
  mode: "incremental",
  trigger: "manual",
  started_at: "2026-11-08T00:00:00Z",
  finished_at: "2026-11-08T00:01:00Z",
  cancel_requested_at: null,
  diagnostics: { total: 2, counts: [{ kind: "parse_failure", count: 2 }] },
  skipped_files: 2,
};

const exclusionDecoders = [
  ["目录列表", (input: unknown) => decodeRootList({ items: [input] }).items[0]],
  ["排除项命令结果", decodeRootExclusions],
] as const;

describe.each(exclusionDecoders)("%s 的 excluded_paths", (_name, decode) => {
  it.each([{ excluded_paths: [] }, { excluded_paths: root.excluded_paths }])("保留排除项 $excluded_paths", ({ excluded_paths }) => {
    expect(decode({ ...root, excluded_paths })?.excluded_paths).toEqual(excluded_paths);
  });

  it("字段必需，不能把缺失的排除项当成空列表", () => {
    expect(() => decode(without(root, ["excluded_paths"]))).toThrow(d.DecodeError);
  });

  it.each([null, "附录", [""], [7]].map((excluded_paths) => ({ excluded_paths })))("拒绝格式错误的排除项 $excluded_paths", ({ excluded_paths }) => {
    expect(() => decode({ ...root, excluded_paths })).toThrow(d.DecodeError);
  });

  it("接受 100 项，拒绝超过上限", () => {
    const paths = Array.from({ length: 100 }, (_, index) => `附录-${index}`);
    expect(decode({ ...root, excluded_paths: paths })?.excluded_paths).toHaveLength(100);
    expect(() => decode({ ...root, excluded_paths: [...paths, "更多附录"] })).toThrow(d.DecodeError);
  });
});

describe("排除项操作记录", () => {
  it.each(["exclude", "unexclude"])("接受新操作类型 %s", (operation) => {
    const item = {
      id: "00000000-0000-4000-8000-000000000003",
      operation,
      status: "succeeded",
      created_at: "2026-11-08T00:00:00Z",
      root_id: root.id,
      revision: root.revision,
    };
    expect(decodeRootOperationList({ items: [item] }).items).toEqual([item]);
  });
});

describe("扫描 skipped_files", () => {
  it.each([null, 0, 2])("详情、活动扫描与记录列表都保留跳过数 %s", (skipped_files) => {
    const input = { ...scan, skipped_files };
    expect(decodeScan(input).skipped_files).toBe(skipped_files);
    expect(decodeActiveScan({ scan: input }).scan?.skipped_files).toBe(skipped_files);
    expect(decodeScanPage({ items: [input], pagination: { page: 1, page_size: 20, total: 1 } }).items[0]?.skipped_files).toBe(skipped_files);
  });

  it("历史扫描明确返回 null，字段不可省略", () => {
    expect(() => decodeScan(without(scan, ["skipped_files"]))).toThrow(d.DecodeError);
  });

  it.each([-1, 1.5, "2", Number.MAX_SAFE_INTEGER + 1])("拒绝非负安全整数之外的值 %s", (skipped_files) => {
    expect(() => decodeScan({ ...scan, skipped_files })).toThrow(d.DecodeError);
  });
});
