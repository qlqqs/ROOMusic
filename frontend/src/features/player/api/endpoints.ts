import { decode as d } from "../../../shared/api";
import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodePlaybackFormats, decodeTrackAnalysis, decodeTrackLyrics, type PlaybackFormatsDTO, type TrackAnalysisDTO, type TrackLyricsDTO } from "./decoders";

/** 服务端能渲染的（来源类别、编码）组合：曲目能不能播放只看它。 */
export function fetchPlaybackFormats(signal?: AbortSignal): Promise<ApiResponse<PlaybackFormatsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/playback/formats", signal }, decodePlaybackFormats);
}

/**
 * 曲目的内嵌歌词（所有登录成员可读，trackId 必须是 UUID，由调用方保证）。没有可显示的歌词是 200 加 lyrics: null，不是错误；
 * 曲目不存在或已缺失是 404。响应必须是所请求的曲目，避免把别的曲目的歌词显示出来。
 */
export function fetchTrackLyrics(trackId: string, signal?: AbortSignal): Promise<ApiResponse<TrackLyricsDTO>> {
  const id = d.defineDecoder(d.uuid())(trackId);
  return apiRequest({ method: "GET", path: `/api/v1/tracks/${id}/lyrics`, signal }, (input): TrackLyricsDTO => {
    const lyrics = decodeTrackLyrics(input);
    if (lyrics.track_id.toLowerCase() !== id.toLowerCase()) throw new d.DecodeError("$.track_id", "歌词响应不是所请求的曲目");
    return lyrics;
  });
}

/** 曲目的音频分析结果（所有登录成员可读，trackId 必须是 UUID）：响度、增益、首尾静音与波形；还没分析或分析失败时只有状态。 */
export function fetchTrackAnalysis(trackId: string, signal?: AbortSignal): Promise<ApiResponse<TrackAnalysisDTO>> {
  const id = d.defineDecoder(d.uuid())(trackId);
  return apiRequest({ method: "GET", path: `/api/v1/tracks/${id}/analysis`, signal }, (input): TrackAnalysisDTO => {
    const analysis = decodeTrackAnalysis(input);
    if (analysis.trackId.toLowerCase() !== id.toLowerCase()) throw new d.DecodeError("$.track_id", "分析响应不是所请求的曲目");
    return analysis;
  });
}
