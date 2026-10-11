import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodePlaysCleared,
  decodeRecentReleases,
  decodeRecordedPlay,
  type PlaysClearedDTO,
  type RecentReleasesDTO,
  type RecordedPlayDTO,
} from "./decoders";

/** 一次达到计次规则的播放：曲目 ID 与达到规则时累计的实际出声秒数。 */
export type PlayReport = { trackId: string; playedSeconds: number };

/**
 * 记录一次播放。幂等键标识“这一次播放”：同一次播放的重试沿用同一个键，后端据此只写一行；
 * 成功是 201（重放也是 201 与同一个响应）。
 */
export function postPlay(report: PlayReport, idempotencyKey: string, signal?: AbortSignal): Promise<ApiResponse<RecordedPlayDTO>> {
  return apiRequest(
    {
      method: "POST",
      path: "/api/v1/me/plays",
      body: { track_id: report.trackId, played_seconds: report.playedSeconds },
      idempotencyKey,
      signal,
    },
    decodeRecordedPlay,
  );
}

/** 当前用户最近播放的发行（最多 8 个，每个发行一次，最近的在前）与是否有任何历史。 */
export function fetchRecentReleases(signal?: AbortSignal): Promise<ApiResponse<RecentReleasesDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/me/plays/recent-releases", signal }, decodeRecentReleases);
}

/** 清除当前用户的全部播放历史；没有记录时 deleted 为 0，重复调用安全。 */
export function deletePlayHistory(signal?: AbortSignal): Promise<ApiResponse<PlaysClearedDTO>> {
  return apiRequest({ method: "DELETE", path: "/api/v1/me/plays", signal }, decodePlaysCleared);
}
