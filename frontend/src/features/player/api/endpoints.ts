import { decode as d } from "../../../shared/api";
import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodePlaybackFormats, decodeTrackPlayback, decodeTrackPreparing, type PlaybackFormatsDTO, type TrackPlaybackDTO } from "./decoders";

/** 后端批准交付的（来源类别、编码）组合、它们的交付方式（直出或无损转换）与交给浏览器的媒体类型。 */
export function fetchPlaybackFormats(signal?: AbortSignal): Promise<ApiResponse<PlaybackFormatsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/playback/formats", signal }, decodePlaybackFormats);
}

/** 预检的结果：可以播放（200），或派生仍在进行（202，调用方应立即再次预检；服务端已替它等待过一轮）。 */
export type TrackPlaybackResult = { state: "ready"; playback: TrackPlaybackDTO } | { state: "preparing" };

/**
 * 播放前的核对（trackId 必须是 UUID，由调用方保证）。它走与音频接口相同的检查，所以失败原因（目录已停用、文件变化、
 * 不能直出……）在出声之前就能得到；媒体元素读不到错误正文，音频失败后也用它再次分类。音频地址只取自它的返回值。
 * 派生格式（ALAC）的预检是唯一会启动或加入转换的请求，最多等待约 20 秒：完成返回 200，仍在进行返回 202。
 * 状态码与响应体必须相符：200 带表示，202 带 preparing；其他组合按无效响应处理，避免把异常响应当作“再等一轮”而无休止地重试。
 */
export function fetchTrackPlayback(trackId: string, signal?: AbortSignal): Promise<ApiResponse<TrackPlaybackResult>> {
  const id = d.defineDecoder(d.uuid())(trackId);
  return apiRequest({ method: "GET", path: `/api/v1/tracks/${id}/playback`, signal }, (input, status): TrackPlaybackResult => {
    if (status === 202) {
      if (decodeTrackPreparing(input).track_id.toLowerCase() !== id.toLowerCase()) throw new d.DecodeError("$.track_id", "预检响应不是所请求的曲目");
      return { state: "preparing" };
    }
    if (status !== 200) throw new d.DecodeError("$", `预检的成功状态码应为 200 或 202，实际为 ${status}`);
    const playback = decodeTrackPlayback(input);
    if (playback.track_id.toLowerCase() !== id.toLowerCase()) throw new d.DecodeError("$.track_id", "预检响应不是所请求的曲目");
    return { state: "ready", playback };
  });
}
