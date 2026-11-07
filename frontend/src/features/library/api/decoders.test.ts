import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import {
  decodeActiveScan,
  decodeDiagnosticsPage,
  decodeFolderListing,
  decodeRootChanged,
  decodeRootCreated,
  decodeRootList,
  decodeRootOperationList,
  decodeScan,
  decodeScanPage,
} from "./decoders";

function repeated(list: unknown, key: string, count: number): unknown {
  const first = (list as Record<string, unknown[]>)[key]?.[0];
  return withValue(list, [key], Array.from({ length: count }, () => structuredClone(first)));
}

describe("目录", () => {
  const roots = sampleBody("library-roots/list");

  it("解码启用与停用的目录，只有目录名没有绝对路径", () => {
    const decoded = decodeRootList(roots);
    expect(decoded.items.map((item) => [item.name, item.status, item.revision])).toEqual([
      ["classical-a", "active", 1],
      ["classical-b", "disabled", 2],
    ]);
    expect(decodeRootList(sampleBody("library-roots/list-empty")).items).toEqual([]);
  });

  it("目录列表没有上限", () => {
    expect(decodeRootList(repeated(roots, "items", 2500)).items).toHaveLength(2500);
  });

  it.each([
    ["缺少 revision", without(roots, ["items", 0, "revision"])],
    ["revision 为 0", withValue(roots, ["items", 0, "revision"], 0)],
    ["revision 不是整数", withValue(roots, ["items", 0, "revision"], 1.5)],
    ["未知状态", withValue(roots, ["items", 0, "status"], "archived")],
    ["缺少 name", without(roots, ["items", 0, "name"])],
    ["id 不是 UUID", withValue(roots, ["items", 0, "id"], "abc")],
    ["updated_at 缺失", without(roots, ["items", 1, "updated_at"])],
  ])("目录列表拒绝：%s", (_name, input) => {
    expect(() => decodeRootList(input)).toThrow(d.DecodeError);
  });

  it("解码新增、停用与恢复的结果，新增可能返回已停用的目录", () => {
    expect(decodeRootCreated(sampleBody("library-roots/create"))).toMatchObject({ status: "active", revision: 1 });
    expect(decodeRootCreated(sampleBody("library-roots/create-existing-disabled"))).toMatchObject({ status: "disabled", revision: 2 });
    expect(decodeRootChanged(sampleBody("library-roots/disable"))).toMatchObject({ status: "disabled", revision: 2 });
    expect(decodeRootChanged(sampleBody("library-roots/restore"))).toMatchObject({ status: "active", revision: 3 });
  });

  it.each([
    ["新增缺少 revision", () => decodeRootCreated(without(sampleBody("library-roots/create"), ["revision"]))],
    ["新增状态未知", () => decodeRootCreated(withValue(sampleBody("library-roots/create"), ["status"], "gone"))],
    ["变更缺少 path", () => decodeRootChanged(without(sampleBody("library-roots/disable"), ["path"]))],
    ["变更 revision 为 0", () => decodeRootChanged(withValue(sampleBody("library-roots/disable"), ["revision"], 0))],
  ])("拒绝：%s", (_name, run) => {
    expect(run).toThrow(d.DecodeError);
  });
});

describe("文件夹浏览列表", () => {
  it("解码多个允许目录的起点列表，当前路径和上级均可为空", () => {
    const listing = {
      path: null,
      parent: null,
      can_go_up: false,
      registered: false,
      truncated: false,
      items: [
        { name: "/music", path: "/music", registered: true },
        { name: "/srv/audio", path: "/srv/audio", registered: false },
      ],
    };
    expect(decodeFolderListing(listing)).toEqual(listing);
  });

  it("拒绝缺少子文件夹路径的列表", () => {
    expect(() => decodeFolderListing(without(sampleBody("library-folders/start"), ["items", 0, "path"]))).toThrow(d.DecodeError);
  });
});

describe("目录操作记录", () => {
  const history = sampleBody("library-root-operations/list");

  it("解码成功与失败的记录，root_id、revision 与 error_code 可以缺失", () => {
    const decoded = decodeRootOperationList(history);
    expect(decoded.items.map((item) => [item.operation, item.status])).toEqual([
      ["disable", "failed"],
      ["restore", "succeeded"],
      ["create", "succeeded"],
      ["disable", "succeeded"],
      ["create", "succeeded"],
      ["create", "succeeded"],
    ]);
    const failed = decoded.items[0];
    expect(failed?.error_code).toBe("operation_failed");
    expect(failed && Object.hasOwn(failed, "revision")).toBe(false);
    expect(decodeRootOperationList(sampleBody("library-root-operations/list-empty")).items).toEqual([]);
  });

  it("最多 100 条", () => {
    expect(decodeRootOperationList(repeated(history, "items", 100)).items).toHaveLength(100);
    expect(() => decodeRootOperationList(repeated(history, "items", 101))).toThrow(d.DecodeError);
  });

  it.each([
    ["未知操作", withValue(history, ["items", 0, "operation"], "delete")],
    ["未知结果", withValue(history, ["items", 0, "status"], "pending")],
    ["缺少 created_at", without(history, ["items", 0, "created_at"])],
    ["revision 为 0", withValue(history, ["items", 1, "revision"], 0)],
    ["error_code 为 null", withValue(history, ["items", 0, "error_code"], null)],
    ["root_id 不是 UUID", withValue(history, ["items", 1, "root_id"], "x")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeRootOperationList(input)).toThrow(d.DecodeError);
  });
});

describe("扫描", () => {
  const scanNames = [
    ["scans/get-running", "running"],
    ["scans/get-cancel-requested", "running"],
    ["scans/get-succeeded", "succeeded"],
    ["scans/get-failed", "failed"],
    ["scans/get-canceled", "canceled"],
    ["scans/get-incomplete", "incomplete"],
  ] as const;

  it.each(scanNames)("解码 %s", (name, status) => {
    expect(decodeScan(sampleBody(name)).status).toBe(status);
  });

  it("取消请求中的扫描仍是 running，并带 cancel_requested_at", () => {
    const decoded = decodeScan(sampleBody("scans/get-cancel-requested"));
    expect(decoded.status).toBe("running");
    expect(decoded.cancel_requested_at).not.toBeNull();
    expect(decoded.finished_at).toBeNull();
  });

  it("失败的扫描带诊断汇总", () => {
    const decoded = decodeScan(sampleBody("scans/get-failed"));
    expect(decoded.diagnostics.total).toBe(4);
    expect(decoded.diagnostics.counts.map((entry) => entry.kind)).toEqual([
      "database_error",
      "parse_failure",
      "permission_error",
      "unsupported_format",
    ]);
  });

  const scan = sampleBody("scans/get-incomplete");

  it.each([
    ["未知状态", withValue(scan, ["status"], "paused")],
    ["缺少 started_at", without(scan, ["started_at"])],
    ["finished_at 不是时间", withValue(scan, ["finished_at"], "later")],
    ["缺少 cancel_requested_at", without(scan, ["cancel_requested_at"])],
    ["缺少 diagnostics", without(scan, ["diagnostics"])],
    ["诊断总数为负", withValue(scan, ["diagnostics", "total"], -1)],
    ["诊断类别含大写", withValue(scan, ["diagnostics", "counts", 0, "kind"], "Parse-Failure")],
    ["诊断类别超过 100 项", withValue(scan, ["diagnostics", "counts"], Array.from({ length: 101 }, (_, index) => ({ kind: `kind_${index}`, count: 1 })))],
    ["scan_run_id 不是 UUID", withValue(scan, ["scan_run_id"], "1")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeScan(input)).toThrow(d.DecodeError);
  });

  it("活动扫描可以为空", () => {
    expect(decodeActiveScan(sampleBody("scans/active-none"))).toEqual({ scan: null });
    expect(decodeActiveScan(sampleBody("scans/active-running")).scan?.status).toBe("running");
    expect(() => decodeActiveScan({})).toThrow(d.DecodeError);
    expect(() => decodeActiveScan({ scan: {} })).toThrow(d.DecodeError);
  });
});

describe("扫描记录", () => {
  const list = sampleBody("scans/list");

  it("解码一页扫描：条目与单个扫描相同，覆盖运行中（取消请求中）、成功、不完整与失败", () => {
    const decoded = decodeScanPage(list);
    expect(decoded.items.map((item) => item.status)).toEqual(["running", "succeeded", "incomplete", "failed"]);
    expect(decoded.items[0]?.cancel_requested_at).not.toBeNull();
    expect(decoded.items[0]?.finished_at).toBeNull();
    expect(decoded.items[3]?.diagnostics.total).toBe(4);
    expect(decoded.pagination).toEqual({ page: 1, page_size: 20, total: 4 });
  });

  it("空列表：没有条目，总数为 0", () => {
    const decoded = decodeScanPage(sampleBody("scans/list-empty"));
    expect(decoded.items).toEqual([]);
    expect(decoded.pagination.total).toBe(0);
  });

  it("每页最多 100 条", () => {
    expect(decodeScanPage(repeated(list, "items", 100)).items).toHaveLength(100);
    expect(() => decodeScanPage(repeated(list, "items", 101))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 items", without(list, ["items"])],
    ["缺少 pagination", without(list, ["pagination"])],
    ["页码为 0", withValue(list, ["pagination", "page"], 0)],
    ["页大小超过 100", withValue(list, ["pagination", "page_size"], 101)],
    ["总数为负", withValue(list, ["pagination", "total"], -1)],
    ["总数不是整数", withValue(list, ["pagination", "total"], 1.5)],
    ["条目缺少状态", without(list, ["items", 0, "status"])],
    ["条目状态未知", withValue(list, ["items", 1, "status"], "paused")],
    ["条目 id 不是 UUID", withValue(list, ["items", 2, "id"], "3")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeScanPage(input)).toThrow(d.DecodeError);
  });
});

describe("诊断", () => {
  const page = sampleBody("scans/diagnostics-multiple-kinds");

  it("解码明细，路径可以为空，未知类别原样保留", () => {
    const decoded = decodeDiagnosticsPage(page);
    expect(decoded.items.map((item) => [item.kind, item.path])).toEqual([
      ["parse_failure", "disc-1/01.flac"],
      ["permission_error", "disc-2"],
      ["database_error", null],
      ["unsupported_format", "extras/cover.dsf"],
    ]);
    expect(decodeDiagnosticsPage(sampleBody("scans/diagnostics-empty")).items).toEqual([]);
  });

  it("明细最多 100 条", () => {
    expect(decodeDiagnosticsPage(repeated(page, "items", 100)).items).toHaveLength(100);
    expect(() => decodeDiagnosticsPage(repeated(page, "items", 101))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 summary", without(page, ["summary"])],
    ["缺少 message", without(page, ["items", 0, "message"])],
    ["path 缺失", without(page, ["items", 0, "path"])],
    ["path 为空字符串", withValue(page, ["items", 0, "path"], "")],
    ["类别含空格", withValue(page, ["items", 0, "kind"], "parse failure")],
    ["id 不是字符串", withValue(page, ["items", 0, "id"], 101)],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeDiagnosticsPage(input)).toThrow(d.DecodeError);
  });
});
