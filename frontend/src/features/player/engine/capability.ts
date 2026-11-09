// 能力判定：一首曲目能不能播放。所有播放都由服务端渲染成流交付，所以只看服务端的格式表（GET /playback/formats，
// 渲染会话能解码的来源类别与编码组合），与这台设备的浏览器无关（design.md §5.10）。
import type { PlaybackFormatDTO } from "../api/decoders";

export type TrackVerdict =
  | { playable: true }
  | {
      playable: false;
      /** format_unsupported：（来源类别、编码）不在服务端的格式表里。 */
      reason: "format_unsupported";
    };

/** 判定一首曲目所需的来源事实（来自曲目详情）。 */
export type VerdictInput = { sourceKind: string; codec: string | null };

export type CapabilityTable = { verdictOf: (input: VerdictInput) => TrackVerdict };

const formatKey = (sourceKind: string, codec: string) => `${sourceKind}\u0000${codec}`;

export function buildCapabilityTable(formats: readonly Pick<PlaybackFormatDTO, "source_kind" | "codec">[]): CapabilityTable {
  const supported = new Set(formats.map((format) => formatKey(format.source_kind, format.codec)));
  return {
    verdictOf: ({ sourceKind, codec }) =>
      codec !== null && supported.has(formatKey(sourceKind, codec)) ? { playable: true } : { playable: false, reason: "format_unsupported" },
  };
}

/** 不能播放的原因（给曲目行、专辑头部与队列用）。 */
export function unplayableText(verdict: Extract<TrackVerdict, { playable: false }>): string {
  switch (verdict.reason) {
    case "format_unsupported":
      return "这种格式目前不能播放";
  }
}
