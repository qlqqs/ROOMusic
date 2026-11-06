import { describe, expect, it } from "vitest";
import type { BackupFileDTO, BackupRetentionDTO } from "../features/system/api/decoders";
import { respondToSystem, retentionPlan, systemDataOf } from "./system-data";

const now = () => "2026-11-06T12:00:00Z";
const files: BackupFileDTO[] = [
  { name: "roomusic-20261106-120000.dump", created_at: now(), source: "scheduled", size_bytes: 1 },
  { name: "roomusic-20261105-120000-manual.dump", created_at: "2026-11-05T12:00:00Z", source: "manual", size_bytes: 1 },
  { name: "roomusic-20261001-120000-pre-restore.dump", created_at: "2026-10-01T12:00:00Z", source: "pre_restore", size_bytes: 1 },
  { name: "roomusic-20260901-120000-pre-migration-1.dump", created_at: "2026-09-01T12:00:00Z", source: "pre_migration", size_bytes: 1 },
];

describe("备份替身的保留计划与服务端确认语义", () => {
  it.each<[BackupRetentionDTO, number[]]>([
    [{ keep_days: null, keep_count: null }, []],
    [{ keep_days: 14, keep_count: null }, [2, 3]],
    [{ keep_days: null, keep_count: 3 }, [3]],
    [{ keep_days: 14, keep_count: 3 }, [2, 3]],
    [{ keep_days: 14, keep_count: 1 }, [1, 2, 3]],
  ])("天数与份数都是上限：%j", (rule, indexes) => {
    expect(retentionPlan(files, rule, now())).toEqual(indexes.map((index) => files[index]));
  });

  it("所有备份过期时仍保留最新一份；空列表没有计划", () => {
    const oldFiles = files.slice(2);
    expect(retentionPlan(oldFiles, { keep_days: 1, keep_count: 1 }, now())).toEqual(oldFiles.slice(1));
    expect(retentionPlan([], { keep_days: 1, keep_count: 1 }, now())).toEqual([]);
  });

  it("未确认不修改规则与文件，确认后立即清理并保存规则", () => {
    const data = systemDataOf({ backups: [...files] });
    const rule = { keep_days: null, keep_count: 3 };
    expect(respondToSystem(data, "PUT", "/api/v1/system/backups/retention", now, { ...rule, confirm: false })).toEqual({
      status: 200, body: { confirmation_required: true, deletions: 1 },
    });
    expect(data.backups).toEqual(files);
    expect(data.retention).toEqual({ keep_days: 14, keep_count: null });
    expect(respondToSystem(data, "PUT", "/api/v1/system/backups/retention", now, { ...rule, confirm: true })?.status).toBe(200);
    expect(data.backups).toEqual(files.slice(0, 3));
    expect(data.retention).toEqual(rule);
  });
});
