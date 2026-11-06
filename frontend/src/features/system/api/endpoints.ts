import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeBackupCommand,
  decodeBackupOverview,
  decodeBuildInfo,
  type BackupCommandDTO,
  type BackupOverviewDTO,
  type BuildInfoDTO,
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
export function backupDownloadPath(name: string): string {
  return `/api/v1/system/backups/${encodeURIComponent(name)}`;
}
