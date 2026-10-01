import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeActiveScan,
  decodeRootList,
  type RootDTO,
  type ScanDTO,
} from "./decoders";

export function fetchRoots(signal?: AbortSignal): Promise<ApiResponse<{ items: RootDTO[] }>> {
  return apiRequest({ method: "GET", path: "/api/v1/library-roots", signal }, decodeRootList);
}

export function fetchActiveScan(signal?: AbortSignal): Promise<ApiResponse<{ scan: ScanDTO | null }>> {
  return apiRequest({ method: "GET", path: "/api/v1/scans/active", signal }, decodeActiveScan);
}
