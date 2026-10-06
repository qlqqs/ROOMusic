// 能力判定：一首曲目能不能在当前浏览器里直接播放。
// 服务端愿不愿意直出由后端的格式表决定（GET /playback/formats）；浏览器能不能解码用 canPlayType 与可选的 decodingInfo 询问。
// 两者都满足才可播放。canPlayType 与 decodingInfo 是能力提示，不是任何具体文件的播放保证：真正的证据是实际加载、播放与拖动。
import type { PlaybackFormatDTO } from "../api/decoders";

/** 浏览器的能力探针；测试可以注入替身。 */
export type BrowserProbe = {
  /** HTMLMediaElement.canPlayType：空字符串、"maybe" 或 "probably"。 */
  canPlayType: (type: string) => string;
  /** navigator.mediaCapabilities.decodingInfo 的音频形态；浏览器没有该 API 时缺失。抛错或拒绝按“没有该信息”处理。 */
  decodingSupported?: ((type: string) => Promise<boolean>) | undefined;
};

/** 为 canPlayType 与 decodingInfo 拼出完整的类型串：`audio/mp4; codecs="alac"`。 */
export function typeString(format: Pick<PlaybackFormatDTO, "mime_type" | "codecs">): string {
  return format.codecs === null ? format.mime_type : `${format.mime_type}; codecs="${format.codecs}"`;
}

export type TrackVerdict =
  | { playable: true }
  | {
      playable: false;
      /**
       * cue_virtual：需要切分的 CUE 曲目（M2b 才能播放）；
       * format_not_direct：（来源类别、编码）不在后端的直出格式表里；
       * browser_cannot_decode：格式表里有，但当前浏览器不能解码。
       */
      reason: "cue_virtual" | "format_not_direct" | "browser_cannot_decode";
    };

/** 判定一首曲目所需的来源事实（来自曲目详情）。 */
export type VerdictInput = { sourceKind: string; codec: string | null };

export type CapabilityTable = {
  verdictOf: (input: VerdictInput) => TrackVerdict;
};

const formatKey = (sourceKind: string, codec: string) => `${sourceKind}\u0000${codec}`;

async function decodable(format: PlaybackFormatDTO, probe: BrowserProbe): Promise<boolean> {
  const type = typeString(format);
  if (probe.canPlayType(type) === "") return false;
  if (probe.decodingSupported === undefined) return true;
  try {
    // supported 为 false 才算不能解码；API 缺失、拒绝或抛错时只看 canPlayType。
    return (await probe.decodingSupported(type)) !== false;
  } catch {
    return true;
  }
}

/** 对格式表里的每个组合询问浏览器一次，得到可以同步查询的判定表（结果在一个会话内不变）。 */
export async function buildCapabilityTable(formats: readonly PlaybackFormatDTO[], probe: BrowserProbe): Promise<CapabilityTable> {
  const browserCanDecode = new Map<string, boolean>();
  const types = new Map<string, Promise<boolean>>();
  await Promise.all(
    formats.map(async (format) => {
      const type = typeString(format);
      const result = types.get(type) ?? decodable(format, probe);
      types.set(type, result);
      browserCanDecode.set(formatKey(format.source_kind, format.codec), await result);
    }),
  );
  return {
    verdictOf: ({ sourceKind, codec }) => {
      if (sourceKind === "cue_virtual") return { playable: false, reason: "cue_virtual" };
      const supported = codec === null ? undefined : browserCanDecode.get(formatKey(sourceKind, codec));
      if (supported === undefined) return { playable: false, reason: "format_not_direct" };
      return supported ? { playable: true } : { playable: false, reason: "browser_cannot_decode" };
    },
  };
}

/** 不能播放的原因（给曲目行、专辑头部与队列用）。编码名取自曲目的事实，不是浏览器名单。 */
export function unplayableText(verdict: Extract<TrackVerdict, { playable: false }>, codec: string | null): string {
  switch (verdict.reason) {
    case "cue_virtual":
      return "CUE 分轨曲目需要切分，目前不能播放";
    case "format_not_direct":
      return "这种格式目前不能直接播放";
    case "browser_cannot_decode":
      return codec === null || codec.trim() === "" ? "当前浏览器不能解码这种编码" : `当前浏览器不能解码 ${codec.toUpperCase()}`;
  }
}

/** 真实浏览器的探针：canPlayType 用一个不挂进页面的 audio 元素，decodingInfo 来自 navigator.mediaCapabilities（有才用）。 */
export function browserProbe(): BrowserProbe {
  const element = document.createElement("audio");
  const capabilities = typeof navigator === "undefined" ? undefined : navigator.mediaCapabilities;
  return {
    canPlayType: (type) => element.canPlayType(type),
    decodingSupported:
      capabilities === undefined || typeof capabilities.decodingInfo !== "function"
        ? undefined
        : async (type) => (await capabilities.decodingInfo({ type: "file", audio: { contentType: type } })).supported,
  };
}
