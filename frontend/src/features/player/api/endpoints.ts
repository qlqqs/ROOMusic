import { decode as d } from "../../../shared/api";
import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodePlaybackFormats, decodeTrackPlayback, type PlaybackFormatsDTO, type TrackPlaybackDTO } from "./decoders";

/** 后端批准直出的（来源类别、编码）组合与它们的媒体类型。 */
export function fetchPlaybackFormats(signal?: AbortSignal): Promise<ApiResponse<PlaybackFormatsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/playback/formats", signal }, decodePlaybackFormats);
}

/**
 * 播放前的核对（trackId 必须是 UUID，由调用方保证）。它走与音频接口完全相同的检查，所以失败原因（目录已停用、文件变化、
 * 不能直出……）在出声之前就能得到；媒体元素读不到错误正文，音频失败后也用它再次分类。音频地址只取自它的返回值。
 */
export function fetchTrackPlayback(trackId: string, signal?: AbortSignal): Promise<ApiResponse<TrackPlaybackDTO>> {
  const id = d.defineDecoder(d.uuid())(trackId);
  return apiRequest({ method: "GET", path: `/api/v1/tracks/${id}/playback`, signal }, (input) => {
    const result = decodeTrackPlayback(input);
    if (result.track_id.toLowerCase() !== id.toLowerCase()) throw new d.DecodeError("$.track_id", "预检响应不是所请求的曲目");
    return result;
  });
}
