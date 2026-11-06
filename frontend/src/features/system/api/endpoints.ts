import type { ApiPath, ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeBackupCommand,
  decodeBackupDeletions,
  decodeBackupOverview,
  decodeBuildInfo,
  decodeRetentionSave,
  type BackupCommandDTO,
  type BackupDeletionsDTO,
  type BackupOverviewDTO,
  type BackupRetentionDTO,
  type BuildInfoDTO,
  type RetentionSaveDTO,
} from "./decoders";

/** 备份状态与最近的备份文件（仅管理员）。 */
export function fetchBackupOverview(signal?: AbortSignal): Promise<ApiResponse<BackupOverviewDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/system/backups", signal }, decodeBackupOverview);
}

/** 立即备份：202 表示开始了一份，200 表示已有备份在进行。没有请求体与幂等键，重复点击得到同一份进行中的备份。 */
export function startBackup(): Promise<ApiResponse<BackupCommandDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/system/backups" }, decodeBackupCommand);
}

/** 当前运行的版本（仅管理员）。 */
export function fetchBuildInfo(signal?: AbortSignal): Promise<ApiResponse<BuildInfoDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/system/version", signal }, decodeBuildInfo);
}

/** 下载一份备份的直链：不经过 apiRequest，它不是 JSON；同源请求带着会话 Cookie。 */
export function backupDownloadPath(name: string): ApiPath {
  return `/api/v1/system/backups/${encodeURIComponent(name)}`;
}

/** 删除一份备份：成功返回删除后的状态与列表（与读取同形）；已经不在是 404 backup_not_found。路径与下载相同。 */
export function deleteBackup(name: string): Promise<ApiResponse<BackupOverviewDTO>> {
  return apiRequest({ method: "DELETE", path: backupDownloadPath(name) }, decodeBackupOverview);
}

/** 一次删除多份：部分失败也是 200，结果按名单分好；响应带删除后的状态与列表。 */
export function deleteBackups(names: readonly string[]): Promise<ApiResponse<BackupDeletionsDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/system/backups/deletions", body: { names } }, decodeBackupDeletions);
}

/** 保存自动清理的规则。confirm 为 false 时，只要会删除文件，服务器就什么都不改并回答将删除几份；为 true 时保存并立即清理。 */
export function saveRetention(rule: BackupRetentionDTO, confirm: boolean): Promise<ApiResponse<RetentionSaveDTO>> {
  return apiRequest(
    { method: "PUT", path: "/api/v1/system/backups/retention", body: { keep_days: rule.keep_days, keep_count: rule.keep_count, confirm } },
    decodeRetentionSave,
  );
}
