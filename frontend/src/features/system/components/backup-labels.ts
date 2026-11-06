import type { BackupSource } from "../api/decoders";

export const backupSourceLabels: Record<BackupSource, string> = {
  scheduled: "自动",
  manual: "手动",
  pre_migration: "版本切换前",
  pre_restore: "恢复前",
};
