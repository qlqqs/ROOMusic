// 播放失败的分类：预检与音频请求的失败 → 曲目不能播放的原因，以及媒体元素错误码、DOMException 名字这类与引擎状态无关的判断。
import type { ApiFailure } from "../../../shared/api";

/** 曲目不能播放的原因；界面按它选文案（不比较后端 message）。 */
export type PlaybackFailureReason =
  | "not_found"
  | "root_disabled"
  | "not_directly_playable"
  | "source_unavailable"
  | "source_changed"
  | "decode_failed"
  | "derivation_failed";

export const MEDIA_ERR_ABORTED = 1;
export const MEDIA_ERR_NETWORK = 2;

/** DOMException 等错误的名字（NotAllowedError、AbortError……）；不依赖 instanceof，不同运行环境里的 DOMException 不一定继承同一个 Error。 */
export function errorName(error: unknown): string {
  return typeof error === "object" && error !== null && "name" in error ? String(error.name) : "";
}

/** 后端对预检与音频的 404/409 分类 → 曲目不能播放的原因；不认识的 4xx 按“文件无法读取”处理，5xx 与其他失败是暂时性的。 */
export function failureReasonOf(failure: ApiFailure): PlaybackFailureReason | null {
  if (failure.kind !== "http") return null;
  if (failure.status === 404) return "not_found";
  if (failure.status === 409) {
    switch (failure.code) {
      case "root_disabled":
        return "root_disabled";
      case "not_directly_playable":
        return "not_directly_playable";
      case "source_changed":
        return "source_changed";
      case "derivation_failed":
        return "derivation_failed";
      default:
        return "source_unavailable";
    }
  }
  return failure.status >= 400 && failure.status < 500 ? "source_unavailable" : null;
}

/** 服务器的转换并发、排队或磁盘空间已满（503 derivation_busy）：不是曲目的问题，稍后重试即可。 */
export function isDerivationBusy(failure: ApiFailure | null): boolean {
  return failure?.kind === "http" && failure.status === 503 && failure.code === "derivation_busy";
}
