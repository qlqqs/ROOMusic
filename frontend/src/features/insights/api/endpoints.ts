import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeAbout, decodeInsightsStatus, type AboutDTO, type InsightsStatusDTO } from "./decoders";

const BASE = "/api/v1/admin/insights" as const;

export function fetchInsightsStatus(signal?: AbortSignal): Promise<ApiResponse<InsightsStatusDTO>> {
  return apiRequest({ method: "GET", path: BASE, signal }, decodeInsightsStatus);
}

/** 开关匿名统计；部署配置关闭时 409 insights_forced_off。返回改后的状态。 */
export function setInsightsEnabled(enabled: boolean): Promise<ApiResponse<InsightsStatusDTO>> {
  return apiRequest({ method: "PUT", path: BASE, body: { enabled } }, decodeInsightsStatus);
}

/** 管理员看过说明；幂等。 */
export function acknowledgeInsightsNotice(): Promise<ApiResponse<null>> {
  return apiRequest({ method: "POST", path: `${BASE}/notice-acknowledgement` }, () => null);
}

export function fetchAbout(signal?: AbortSignal): Promise<ApiResponse<AboutDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/system/about", signal }, decodeAbout);
}
