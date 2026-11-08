import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeActiveScan,
  decodeAnalysisProgress,
  decodeDiagnosticsPage,
  decodeFolderListing,
  decodeRootChanged,
  decodeRootCreated,
  decodeRootExclusions,
  decodeRootList,
  decodeRootOperationList,
  decodeScan,
  decodeScanPage,
  decodeScanReport,
  type AnalysisProgressDTO,
  type DiagnosticsPageDTO,
  type FolderListingDTO,
  type RootChangedDTO,
  type RootCreatedDTO,
  type RootExclusionsDTO,
  type RootDTO,
  type RootOperationDTO,
  type ScanDTO,
  type ScanMode,
  type ScanPageDTO,
  type ScanReportDTO,
} from "./decoders";

export function fetchRoots(signal?: AbortSignal): Promise<ApiResponse<{ items: RootDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-roots", signal }, decodeRootList);
}

export function fetchRootOperations(signal?: AbortSignal): Promise<ApiResponse<{ items: RootOperationDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-root-operations", signal }, decodeRootOperationList);
}

/**
 * 服务器上的文件夹（管理员）：path 为 null 时从允许的音乐目录开始；只回传接口给出的路径。
 * 给了 rootId 时只在这个目录之内浏览，path 为 null 时从目录本身开始。
 */
export function fetchFolders(path: string | null, rootId: string | null, signal?: AbortSignal): Promise<ApiResponse<FolderListingDTO>> {
  const params = new URLSearchParams();
  if (rootId !== null) params.set("root_id", rootId);
  if (path !== null) params.set("path", path);
  const query = params.toString();
  return apiRequest({ method: "GET", path: `/api/v1/library-folders${query === "" ? "" : `?${query}`}`, signal }, decodeFolderListing);
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

/**
 * 改目录路径：path 是目录浏览器给出的绝对路径，必须带界面上看到的 revision。
 * 有扫描在运行时返回 409 scan_running，新路径已被别的目录登记时返回 409 path_already_registered。
 */
export function relocateRoot(id: string, path: string, expectedRevision: number, idempotencyKey: string): Promise<ApiResponse<RootChangedDTO>> {
  return apiRequest(
    { method: "POST", path: `/api/v1/library-roots/${id}/relocate`, body: { path, expected_revision: expectedRevision }, idempotencyKey },
    decodeRootChanged,
  );
}

/** 添加不扫描的子文件夹：path 是目录浏览器给出的绝对路径，必须在该目录之内；已排除的再加一次返回当前状态。 */
export function addRootExclusion(id: string, path: string, idempotencyKey: string): Promise<ApiResponse<RootExclusionsDTO>> {
  return apiRequest({ method: "POST", path: `/api/v1/library-roots/${id}/exclusions`, body: { path }, idempotencyKey }, decodeRootExclusions);
}

/** 移除不扫描的子文件夹：relativePath 取自目录的 excluded_paths；本来就没有的移除一次返回当前状态。 */
export function removeRootExclusion(id: string, relativePath: string, idempotencyKey: string): Promise<ApiResponse<RootExclusionsDTO>> {
  const query = new URLSearchParams({ path: relativePath });
  return apiRequest({ method: "DELETE", path: `/api/v1/library-roots/${id}/exclusions?${query}`, idempotencyKey }, decodeRootExclusions);
}

export function fetchActiveScan(signal?: AbortSignal): Promise<ApiResponse<{ scan: ScanDTO | null }>> {
  return apiRequest({ method: "GET", path: "/api/v1/scans/active", signal }, decodeActiveScan);
}

/**
 * 开始扫描：202 表示新建，200 表示已有扫描在运行（返回的是那一个，不论请求的模式）。没有幂等键，重复开始是安全的。
 * 增量扫描不发请求体；完整重扫发 {"mode":"full"}。
 */
export function startScan(mode: ScanMode = "incremental"): Promise<ApiResponse<ScanDTO>> {
  return apiRequest({ method: "POST", path: "/api/v1/scans", body: mode === "incremental" ? undefined : { mode } }, decodeScan);
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

/** 后台音频分析的进度（所有登录用户）。 */
export function fetchAnalysisProgress(signal?: AbortSignal): Promise<ApiResponse<AnalysisProgressDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/audio-analysis/progress", signal }, decodeAnalysisProgress);
}
