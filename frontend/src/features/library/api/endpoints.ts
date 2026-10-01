import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeActiveScan,
  decodeDiagnosticsPage,
  decodeRootChanged,
  decodeRootCreated,
  decodeRootList,
  decodeRootOperationList,
  decodeScan,
  type DiagnosticsPageDTO,
  type RootChangedDTO,
  type RootCreatedDTO,
  type RootDTO,
  type RootOperationDTO,
  type ScanDTO,
} from "./decoders";

export function fetchRoots(signal?: AbortSignal): Promise<ApiResponse<{ items: RootDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-roots", signal }, decodeRootList);
}

export function fetchRootOperations(signal?: AbortSignal): Promise<ApiResponse<{ items: RootOperationDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-root-operations", signal }, decodeRootOperationList);
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
