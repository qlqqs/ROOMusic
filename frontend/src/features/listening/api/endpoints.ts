import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import {
  decodeHistoryQueue,
  decodeHistoryTracks,
  decodePlaysCleared,
  decodeRecentReleases,
  decodeRecordedPlay,
  decodeTrackPlaysDeleted,
  type HistoryQueueDTO,
  type HistoryTracksDTO,
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

/** 当前用户的历史，一首曲目一行：按最后一次播放倒序，时间相同按曲目 ID；页码超出末页时返回空的 items 与真实的总数。 */
export function fetchHistoryTracks(params: { page: number; pageSize: number; signal?: AbortSignal | undefined }): Promise<ApiResponse<HistoryTracksDTO>> {
  const query = new URLSearchParams({ page: String(params.page), page_size: String(params.pageSize) });
  return apiRequest({ method: "GET", path: `/api/v1/me/plays/tracks?${query}`, signal: params.signal }, decodeHistoryTracks);
}

/** “播放全部”的候选：与历史页同一顺序的最近最多 1,000 首，不受当前页影响；能否播放由调用方判断。 */
export function fetchHistoryQueue(signal?: AbortSignal): Promise<ApiResponse<HistoryQueueDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/me/plays/queue", signal }, decodeHistoryQueue);
}

/** 从当前用户的历史中移除一首曲目的全部记录；没有记录时 deleted 为 0，重复调用安全。 */
export function deleteTrackPlays(trackId: string, signal?: AbortSignal): Promise<ApiResponse<PlaysClearedDTO>> {
  return apiRequest({ method: "DELETE", path: `/api/v1/me/plays/tracks/${encodeURIComponent(trackId)}`, signal }, decodeTrackPlaysDeleted);
}
