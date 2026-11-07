import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
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
  decodeScanReport,
  type DiagnosticsPageDTO,
  type FolderListingDTO,
  type RootChangedDTO,
  type RootCreatedDTO,
  type RootDTO,
  type RootOperationDTO,
  type ScanDTO,
  type ScanPageDTO,
  type ScanReportDTO,
} from "./decoders";

export function fetchRoots(signal?: AbortSignal): Promise<ApiResponse<{ items: RootDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-roots", signal }, decodeRootList);
}

export function fetchRootOperations(signal?: AbortSignal): Promise<ApiResponse<{ items: RootOperationDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-root-operations", signal }, decodeRootOperationList);
}

/** 服务器上的文件夹（管理员）：path 为 null 时从允许的音乐目录开始；只回传接口给出的路径。 */
export function fetchFolders(path: string | null, signal?: AbortSignal): Promise<ApiResponse<FolderListingDTO>> {
  const query = path === null ? "" : `?${new URLSearchParams({ path }).toString()}`;
  return apiRequest({ method: "GET", path: `/api/v1/library-folders${query}`, signal }, decodeFolderListing);
}

// 目录的新增、停用与恢复都要求幂等键：同一个键、同一个请求体重放会得到第一次的响应，不会重复执行。

/** 新增目录（201）。path 是服务器上的绝对路径，必须在允许的音乐根目录内；已登记的路径返回已有目录。 */
export function createRoot(path: string, idempotencyKey: string): Promise<ApiResponse<RootCreatedDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/library-roots", body: { path }, idempotencyKey }, decodeRootCreated);
}

/** 停用目录：必须带界面上看到的 revision，目录在此期间变化时返回 409 revision_conflict。 */
export function disableRoot(id: string, expectedRevision: number, idempotencyKey: string): Promise<ApiResponse<RootChangedDTO>> {
  return apiRequest(
    {
      method: "PATCH",
      path: `/api/v1/library-roots/${id}`,
      body: { status: "disabled", expected_revision: expectedRevision },
      idempotencyKey,
    },
    decodeRootChanged,
  );
}

export function restoreRoot(id: string, expectedRevision: number, idempotencyKey: string): Promise<ApiResponse<RootChangedDTO>> {
  return apiRequest(
    { method: "POST", path: `/api/v1/library-roots/${id}/restore`, body: { expected_revision: expectedRevision }, idempotencyKey },
    decodeRootChanged,
  );
}

export function fetchActiveScan(signal?: AbortSignal): Promise<ApiResponse<{ scan: ScanDTO | null }>> {
  return apiRequest({ method: "GET", path: "/api/v1/scans/active", signal }, decodeActiveScan);
}

/** 开始扫描：202 表示新建，200 表示已有扫描在运行（返回的是那一个）。没有请求体与幂等键，重复开始是安全的。 */
export function startScan(): Promise<ApiResponse<ScanDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/scans" }, decodeScan);
}

/** 扫描记录（仅管理员）：按开始时间倒序分页，page 从 1 起。 */
export function fetchScanPage(page: number, pageSize: number, signal?: AbortSignal): Promise<ApiResponse<ScanPageDTO>> {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  return apiRequest({ method: "GET", path: `/api/v1/scans?${query}`, signal }, decodeScanPage);
}

export function fetchScan(scanId: string, signal?: AbortSignal): Promise<ApiResponse<ScanDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/scans/${scanId}`, signal }, decodeScan);
}

/** 请求取消：202 表示已请求，200 表示扫描已经是终态。终态以后续读取到的扫描状态为准。 */
export function cancelScan(scanId: string): Promise<ApiResponse<ScanDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/scans/${scanId}/cancel` }, decodeScan);
}

export function fetchScanDiagnostics(scanId: string, signal?: AbortSignal): Promise<ApiResponse<DiagnosticsPageDTO>> {
  return apiRequest({ method: "GET", path: `/api/v1/scans/${scanId}/diagnostics`, signal }, decodeDiagnosticsPage);
}

/**
 * 扫描报告（仅管理员）：这次扫描新增、更新、消失的专辑与曲目数，以及新增专辑的一页（page 从 1 起，每页最多 100）。
 * 只有 report_status 为 ready 或 partial 的扫描才请求；报告没能生成时返回 503 scan_report_unavailable。
 */
export function fetchScanReport(scanId: string, page: number, pageSize: number, signal?: AbortSignal): Promise<ApiResponse<ScanReportDTO>> {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  return apiRequest({ method: "GET", path: `/api/v1/scans/${scanId}/report?${query}`, signal }, decodeScanReport);
}
