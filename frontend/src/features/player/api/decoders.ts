import { decode as d } from "../../../shared/api";

// 格式表是后端的静态表（目前最多 7 项）；上限只是防止异常响应被整体接受。
const MAX_FORMATS = 32;

const mimeDecoder = d.string({ max: 100, pattern: /^audio\/[a-z0-9.+-]+$/i });
const codecsDecoder = d.nullable(d.string({ min: 1, max: 100, pattern: /^[a-z0-9., -]+$/i }));

/** 交付方式：original 直出原文件；derived 由服务端无损转换（ALAC → FLAC）后交付，mime_type 与 codecs 描述转换后的表示。 */
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
