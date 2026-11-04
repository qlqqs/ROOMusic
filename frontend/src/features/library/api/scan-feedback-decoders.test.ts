import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeScan, decodeScanReport } from "./decoders";

const reading = sampleBody("scan-feedback/reading_tags-admin");
const report = sampleBody("scan-feedback/report-ready");
const invalidCounts = [-1, 0.5, Number.MAX_SAFE_INTEGER + 1, Number.NaN, Number.POSITIVE_INFINITY, "1", null];

describe("扫描反馈解码边界", () => {
  it("兼容旧服务缺省与旧扫描的 null，不伪造进度", () => {
    const old = without(without(without(reading, ["progress"]), ["root_progress"]), ["report_status"]);
    expect(decodeScan(old)).not.toHaveProperty("progress");
    expect(decodeScan(old)).not.toHaveProperty("report_status");
    expect(decodeScan(sampleBody("scan-feedback/legacy"))).toMatchObject({ progress: null, root_progress: null, report_status: "legacy" });
  });

  it("成员响应省略目录身份，管理员的目录名称与 ID 完整保留", () => {
    const member = decodeScan(sampleBody("scan-feedback/reading_tags-member"));
    expect(member.root_progress).not.toHaveProperty("name");
    expect(member.root_progress).not.toHaveProperty("id");
    expect(decodeScan(reading).root_progress).toMatchObject({ id: expect.any(String), name: expect.any(String) });
  });

  it("保留未知分母、零目录、文件漂移，并忽略未来未知键", () => {
    expect(decodeScan(sampleBody("scan-feedback/counting-member")).progress?.files_total).toBeNull();
    expect(decodeScan(sampleBody("scan-feedback/no-roots")).root_progress).toMatchObject({ index: null, total: 0 });
    const drift = decodeScan(sampleBody("scan-feedback/files-drift"));
    expect(drift.progress?.files_changed).toBe(true);
    expect(decodeScan({ ...decodeScan(reading), future_field: true })).toEqual(decodeScan(reading));
  });

  it.each(["version", "phase", "percent_bp", "completed", "total", "unit", "files_counted", "files_total", "files_processed", "count_complete", "files_changed", "updated_at"])("已有 progress 时 %s 不可缺失", (key) => {
    expect(() => decodeScan(without(reading, ["progress", key]))).toThrow(d.DecodeError);
  });

  it.each(["index", "total"])("已有 root_progress 时 %s 不可缺失", (key) => {
    expect(() => decodeScan(without(reading, ["root_progress", key]))).toThrow(d.DecodeError);
  });

  it.each(["completed", "total", "files_counted", "files_processed"])("%s 必须为非负安全整数", (key) => {
    for (const value of invalidCounts) expect(() => decodeScan(withValue(reading, ["progress", key], value))).toThrow(d.DecodeError);
    expect(() => decodeScan(withValue(reading, ["progress", key], Number.MAX_SAFE_INTEGER))).not.toThrow();
  });

  it("files_total 允许 null，不接受负数、小数或不安全整数", () => {
    for (const value of invalidCounts.filter((value) => value !== null)) expect(() => decodeScan(withValue(reading, ["progress", "files_total"], value))).toThrow(d.DecodeError);
    expect(decodeScan(withValue(reading, ["progress", "files_total"], null)).progress?.files_total).toBeNull();
  });

  it.each([
    ["progress", "version", 2], ["progress", "phase", "done"], ["progress", "unit", "bytes"],
    ["progress", "percent_bp", -1], ["progress", "percent_bp", 10_001], ["progress", "percent_bp", 1.5],
    ["progress", "updated_at", "昨天"], ["progress", "count_complete", 1], ["progress", "files_changed", "true"],
    ["root_progress", "index", 0], ["root_progress", "index", -1], ["root_progress", "index", 1.5],
    ["root_progress", "index", Number.MAX_SAFE_INTEGER + 1], ["root_progress", "total", -1],
    ["root_progress", "name", ""], ["root_progress", "name", "长".repeat(1025)], ["root_progress", "id", "not-a-uuid"],
  ] as const)("拒绝 %s.%s=%s", (section, key, value) => {
    expect(() => decodeScan(withValue(reading, [section, key], value))).toThrow(d.DecodeError);
  });

  it("拒绝未知报告状态和超过目录总数的序号", () => {
    expect(() => decodeScan(withValue(reading, ["report_status"], "complete"))).toThrow(d.DecodeError);
    expect(() => decodeScan(withValue(withValue(reading, ["root_progress", "total"], 2), ["root_progress", "index"], 3))).toThrow(d.DecodeError);
  });

  it.each([
    ["counting", "roots"], ["reading_tags", "files"], ["organizing", "roots"], ["saving", "scopes"],
  ] as const)("%s 只接受 %s 单位", (phase, unit) => {
    const base = withValue(reading, ["progress", "phase"], phase);
    for (const candidate of ["roots", "files", "scopes"]) {
      const run = () => decodeScan(withValue(base, ["progress", "unit"], candidate));
      if (candidate === unit) expect(run).not.toThrow();
      else expect(run).toThrow(d.DecodeError);
    }
  });
});

describe("扫描报告解码边界", () => {
  it.each(["scan_run_id", "schema_version", "status", "scan_status", "finalized_at", "missing_reconciled", "files", "albums", "tracks", "issues", "added_albums"])("必需字段 %s 不可缺失", (key) => {
    expect(() => decodeScanReport(without(report, [key]))).toThrow(d.DecodeError);
  });

  it.each([
    ["schema_version", 2], ["status", "unavailable"], ["status", "pending"], ["scan_status", "paused"],
    ["scan_run_id", "wrong-id"], ["finalized_at", "later"], ["missing_reconciled", "true"],
  ] as const)("拒绝 %s=%s", (key, value) => {
    expect(() => decodeScanReport(withValue(report, [key], value))).toThrow(d.DecodeError);
  });

  it.each([
    ["files", "counted"], ["files", "processed"], ["albums", "added"], ["albums", "updated"], ["albums", "missing"],
    ["tracks", "added"], ["tracks", "updated"], ["tracks", "missing"], ["issues", "recorded"],
  ] as const)("%s.%s 必须存在且为非负安全整数", (section, key) => {
    expect(() => decodeScanReport(without(report, [section, key]))).toThrow(d.DecodeError);
    for (const value of invalidCounts) expect(() => decodeScanReport(withValue(report, [section, key], value))).toThrow(d.DecodeError);
  });

  it.each(["group_id_at_scan", "added_at", "title_at_scan", "artist_at_scan", "release_id", "current_group_id", "album", "claim_hints"])("新增专辑的 %s 不可缺失", (key) => {
    expect(() => decodeScanReport(without(report, ["added_albums", "items", 0, key]))).toThrow(d.DecodeError);
  });

  it("报告分页最多 100 条、认领提示最多 3 条", () => {
    const item = decodeScanReport(report).added_albums.items[0];
    if (item === undefined) throw new Error("报告样本缺少新增专辑");
    const page = (length: number) => withValue(report, ["added_albums", "items"], Array.from({ length }, () => item));
    expect(decodeScanReport(page(100)).added_albums.items).toHaveLength(100);
    expect(() => decodeScanReport(page(101))).toThrow(d.DecodeError);
    const hint = { id: item.release_id, title: "旧版本" };
    const hints = (length: number) => withValue(report, ["added_albums", "items", 0, "claim_hints"], Array.from({ length }, () => hint));
    expect(decodeScanReport(hints(3)).added_albums.items[0]?.claim_hints).toHaveLength(3);
    expect(() => decodeScanReport(hints(4))).toThrow(d.DecodeError);
  });

  it.each([["page", 0], ["page", 1.5], ["page_size", 0], ["page_size", 101], ["total", -1], ["total", Number.MAX_SAFE_INTEGER + 1]] as const)("拒绝分页 %s=%s", (key, value) => {
    expect(() => decodeScanReport(withValue(report, ["added_albums", "pagination", key], value))).toThrow(d.DecodeError);
  });

  it("当前不可见项保留历史，报告未知键不改变结果", () => {
    const invisible = decodeScanReport(sampleBody("scan-feedback/report-historical-invisible"));
    expect(invisible.added_albums.items.some((item) => item.album === null && item.current_group_id === null)).toBe(true);
    expect(decodeScanReport({ ...decodeScanReport(report), future_field: "ignored" })).toEqual(decodeScanReport(report));
  });
});
