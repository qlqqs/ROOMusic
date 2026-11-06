import { describe, expect, it } from "vitest";
import { withValue, without } from "../../../test/mutate";
import { systemDataOf } from "../../../test/system-data";
import { decodeBackupDeletions, decodeBackupOverview, decodeRetentionSave } from "./decoders";

function overview() {
  const data = systemDataOf();
  return { status: data.status, retention: data.retention, backups: data.backups, total: data.backups.length };
}

describe("备份 Overview 与清理规则解码", () => {
  it("保留两个上限，null规则表示读不到设置", () => {
    const value = overview();
    expect(decodeBackupOverview(value)).toEqual(value);
    expect(decodeBackupOverview({ ...value, retention: null }).retention).toBeNull();
    expect(decodeBackupOverview({ ...value, retention: { keep_days: null, keep_count: null } }).retention).toEqual({ keep_days: null, keep_count: null });
  });
  it("拒绝缺失规则字段而非当成不限", () => {
    expect(() => decodeBackupOverview(without(overview(), ["retention"]))).toThrow();
    expect(() => decodeBackupOverview(without(overview(), ["retention", "keep_days"]))).toThrow();
    expect(() => decodeBackupOverview(without(overview(), ["retention", "keep_count"]))).toThrow();
  });
  it.each([
    ["keep_days", 0], ["keep_days", 3651], ["keep_days", 1.5], ["keep_days", "3"],
    ["keep_count", 0], ["keep_count", 1001], ["keep_count", 1.5], ["keep_count", false],
  ])("拒绝规则%s=%s", (field, value) => {
    expect(() => decodeBackupOverview(withValue(overview(), ["retention", field], value))).toThrow();
  });
});

describe("批量删除解码", () => {
  const value = () => ({ deleted: ["roomusic-20260101-120000.dump"], missing: ["not-a-backup"], failed: [], overview: overview() });
  it("保留三种名单和删除后的Overview", () => expect(decodeBackupDeletions(value())).toEqual(value()));
  it.each(["deleted", "missing", "failed", "overview"])("拒绝缺失%s", (field) => {
    expect(() => decodeBackupDeletions(without(value(), [field]))).toThrow();
  });
  it("拒绝非字符串名单和超过500个名字", () => {
    expect(() => decodeBackupDeletions({ ...value(), deleted: [123] })).toThrow();
    expect(() => decodeBackupDeletions({ ...value(), failed: Array.from({ length: 501 }, () => "backup") })).toThrow();
  });
});

describe("保存规则判别联合解码", () => {
  it("只需确认时无需Overview，保存成功时保留实际数量", () => {
    const required = { confirmation_required: true, deletions: 3 };
    const saved = { confirmation_required: false, deleted: 2, failed: 1, overview: overview() };
    expect(decodeRetentionSave(required)).toEqual(required);
    expect(decodeRetentionSave(saved)).toEqual(saved);
  });
  it.each([
    {}, { confirmation_required: "true", deletions: 3 }, { confirmation_required: true, deletions: 0 },
    { confirmation_required: true, deletions: 1.5 }, { confirmation_required: false, deleted: 0, failed: 0 },
    { confirmation_required: false, deleted: -1, failed: 0, overview: overview() },
    { confirmation_required: false, deleted: 0, failed: "1", overview: overview() },
  ])("拒绝缺字段、错误判别或非法数量：%j", (value) => expect(() => decodeRetentionSave(value)).toThrow());
});
