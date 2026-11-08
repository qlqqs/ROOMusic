import { decode as d } from "../../../shared/api";

// 格式表是后端的静态表（目前最多 11 项）；上限只是防止异常响应被整体接受。
const MAX_FORMATS = 32;

const mimeDecoder = d.string({ max: 100, pattern: /^audio\/[a-z0-9.+-]+$/i });
const codecsDecoder = d.nullable(d.string({ min: 1, max: 100, pattern: /^[a-z0-9., -]+$/i }));

/** 交付方式：original 直出原文件；derived 由服务端无损转换成 FLAC（ALAC、APE、WavPack、AIFF）后交付，mime_type 与 codecs 描述转换后的表示。 */
const deliveryDecoder = d.oneOf(["original", "derived"] as const);

const formatDecoder = d.object({
  source_kind: d.string({ min: 1, max: 64 }),
  codec: d.string({ min: 1, max: 64 }),
  delivery: deliveryDecoder,
  mime_type: mimeDecoder,
  // codecs= 参数的值；没有该参数时是 null。
  codecs: codecsDecoder,
});

const formatsDecoder = d.object({ items: d.array(formatDecoder, { max: MAX_FORMATS }) });

// 音频地址必须是本站的 /api/v1/tracks/<UUID>/audio：它会被交给媒体元素直接请求，所以不接受任何别的形状（绝对地址、查询串、其他路径）。
const audioUrlPattern = /^\/api\/v1\/tracks\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\/audio$/i;

const representationDecoder = d.object({
  // original：原文件；derived：服务端无损转换并核对过的 FLAC。两者的地址规则相同。
  kind: deliveryDecoder,
  url: d.string({ max: 200, pattern: audioUrlPattern }),
  mime_type: mimeDecoder,
  codecs: codecsDecoder,
  size_bytes: d.integer({ min: 0 }),
});

const trackPlaybackDecoder = d.object({
  track_id: d.uuid(),
  representation: representationDecoder,
});

// 202：派生仍在进行（服务端已等待过一轮），客户端立即再次预检。
const trackPreparingDecoder = d.object({
  track_id: d.uuid(),
  status: d.literal("preparing"),
});

export const decodePlaybackFormats = d.defineDecoder((input, path) => {
  const result = formatsDecoder(input, path);
  const keys = result.items.map((format) => `${format.source_kind}\u0000${format.codec}`);
  if (new Set(keys).size !== keys.length) throw new d.DecodeError(`${path}.items`, "格式组合重复");
  return result;
});
export const decodeTrackPreparing = d.defineDecoder(trackPreparingDecoder);
export const decodeTrackPlayback = d.defineDecoder((input, path) => {
  const result = trackPlaybackDecoder(input, path);
  if (result.representation.url.toLowerCase() !== `/api/v1/tracks/${result.track_id.toLowerCase()}/audio`) {
    throw new d.DecodeError(`${path}.representation.url`, "音频地址与曲目标识不一致");
  }
  return result;
});

export type PlaybackDelivery = d.Infer<typeof deliveryDecoder>;
export type PlaybackFormatDTO = d.Infer<typeof formatDecoder>;
export type PlaybackFormatsDTO = d.Infer<typeof formatsDecoder>;
export type TrackPlaybackDTO = d.Infer<typeof trackPlaybackDecoder>;
export type TrackPreparingDTO = d.Infer<typeof trackPreparingDecoder>;

// ---- 歌词 ----

// 上限与后端 catalog.ParseLyrics 一致：最多 2,000 行、每行最多 1,000 个字符（UTF-16 码元最多 2,000）、开始时间最多 24 小时。
// 异常响应不会被整体接受。
const MAX_LYRICS_LINES = 2000;
const MAX_LYRICS_TEXT_UNITS = 2000;
const MAX_LYRICS_START_MS = 24 * 60 * 60 * 1000;

const lyricsLineDecoder = d.object({
  start_ms: d.nullable(d.integer({ min: 0, max: MAX_LYRICS_START_MS })),
  text: d.string({ max: MAX_LYRICS_TEXT_UNITS }),
});

const rawLyricsDecoder = d.object({
  kind: d.oneOf(["synced", "plain"] as const),
  // 目前只有内嵌标签一种来源；新增来源时后端与这里一起扩展。
  source: d.literal("embedded"),
  lines: d.array(lyricsLineDecoder, { min: 1, max: MAX_LYRICS_LINES }),
});

const trackLyricsDecoder = d.object({
  track_id: d.uuid(),
  // 曲目存在但没有可显示的歌词是 null，不是错误。
  lyrics: d.nullable(rawLyricsDecoder),
});

/** 同步歌词的一行：开始时间是非负毫秒数。 */
export type SyncedLyricsLine = { start_ms: number; text: string };
/** 纯文本歌词的一行：没有开始时间；text 可以是空串（分段）。 */
export type PlainLyricsLine = { start_ms: null; text: string };
export type LyricsDTO =
  | { kind: "synced"; source: "embedded"; lines: SyncedLyricsLine[] }
  | { kind: "plain"; source: "embedded"; lines: PlainLyricsLine[] };
export type TrackLyricsDTO = { track_id: string; lyrics: LyricsDTO | null };

/**
 * 歌词响应。除字段形状外还检查两种形态的约束：synced 的每一行都有开始时间且非递减，plain 的每一行都没有；
 * 违反的响应整体拒绝，面板不会拿到顺序错乱或缺时间的“同步歌词”。
 */
export const decodeTrackLyrics = d.defineDecoder((input, path): TrackLyricsDTO => {
  const result = trackLyricsDecoder(input, path);
  const lyrics = result.lyrics;
  if (lyrics === null) return { track_id: result.track_id, lyrics: null };
  if (lyrics.kind === "plain") {
    const lines = lyrics.lines.map((line, index): PlainLyricsLine => {
      if (line.start_ms !== null) throw new d.DecodeError(`${path}.lyrics.lines[${index}].start_ms`, "纯文本歌词的行不应有开始时间");
      return { start_ms: null, text: line.text };
    });
    return { track_id: result.track_id, lyrics: { kind: "plain", source: lyrics.source, lines } };
  }
  let previous = 0;
  const lines = lyrics.lines.map((line, index): SyncedLyricsLine => {
    const linePath = `${path}.lyrics.lines[${index}].start_ms`;
    if (line.start_ms === null) throw new d.DecodeError(linePath, "同步歌词的每一行都应有开始时间");
    if (line.start_ms < previous) throw new d.DecodeError(linePath, "同步歌词的开始时间应非递减");
    previous = line.start_ms;
    return { start_ms: line.start_ms, text: line.text };
  });
  return { track_id: result.track_id, lyrics: { kind: "synced", source: lyrics.source, lines } };
});

// ---- 音频分析 ----

const MAX_WAVEFORM_POINTS = 1000;
const MAX_DURATION_MS = 24 * 60 * 60 * 1000;

const rawTrackAnalysisDecoder = d.object({
  track_id: d.uuid(),
  status: d.oneOf(["done", "pending", "failed"] as const),
  analysis: d.nullable(d.object({
    // 近乎静音或全静音时这几项为 null：响度未知，均衡按 −5 dB 处理；首尾点退回媒体时长。
    integrated_lufs: d.nullable(d.number({ min: -200, max: 50 })),
    true_peak_dbtp: d.nullable(d.number({ min: -200, max: 50 })),
    track_gain_db: d.nullable(d.number({ min: -100, max: 100 })),
    duration_ms: d.integer({ min: 0, max: MAX_DURATION_MS }),
    leading_silence_ms: d.nullable(d.integer({ min: 0, max: MAX_DURATION_MS })),
    trailing_silence_ms: d.nullable(d.integer({ min: 0, max: MAX_DURATION_MS })),
    waveform: d.object({ points: d.integer({ min: 1, max: MAX_WAVEFORM_POINTS }), peaks: d.string({ min: 1, max: 4 * MAX_WAVEFORM_POINTS }) }),
  })),
  album: d.nullable(d.object({ release_id: d.uuid(), gain_db: d.number({ min: -100, max: 100 }), peak_dbtp: d.number({ min: -200, max: 50 }) })),
});

export type TrackAnalysisDTO = {
  trackId: string;
  status: "done" | "pending" | "failed";
  analysis: {
    trackGainDb: number | null;
    durationMs: number;
    leadingSilenceMs: number | null;
    trailingSilenceMs: number | null;
    /** 波形峰值，每个字节 0–255，长度等于 points。 */
    peaks: Uint8Array;
  } | null;
  /** 整张发行都分析完时的专辑增益；还有曲目没分析完时是 null。 */
  album: { releaseId: string; gainDb: number } | null;
};

function decodeBase64(text: string, path: string): Uint8Array {
  let binary: string;
  try {
    binary = atob(text);
  } catch {
    throw new d.DecodeError(path, "不是合法的 base64");
  }
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

/** 分析结果：done 时 analysis 必有，否则两者都是 null；peaks 解出的字节数必须等于 points。 */
export const decodeTrackAnalysis = d.defineDecoder((input, path): TrackAnalysisDTO => {
  const result = rawTrackAnalysisDecoder(input, path);
  if (result.status !== "done" && (result.analysis !== null || result.album !== null)) throw new d.DecodeError(`${path}.analysis`, "未完成的分析不应带结果");
  if (result.status === "done" && result.analysis === null) throw new d.DecodeError(`${path}.analysis`, "完成的分析缺少结果");
  const analysis = result.analysis;
  let decoded: TrackAnalysisDTO["analysis"] = null;
  if (analysis !== null) {
    const peaks = decodeBase64(analysis.waveform.peaks, `${path}.analysis.waveform.peaks`);
    if (peaks.length !== analysis.waveform.points) throw new d.DecodeError(`${path}.analysis.waveform.peaks`, "峰值个数与 points 不符");
    decoded = { trackGainDb: analysis.track_gain_db, durationMs: analysis.duration_ms, leadingSilenceMs: analysis.leading_silence_ms, trailingSilenceMs: analysis.trailing_silence_ms, peaks };
  }
  return { trackId: result.track_id, status: result.status, analysis: decoded, album: result.album === null ? null : { releaseId: result.album.release_id, gainDb: result.album.gain_db } };
});
