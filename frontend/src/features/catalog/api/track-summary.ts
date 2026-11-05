import { decode as d } from "../../../shared/api";
import { albumArtistOf } from "../format";
import { artistRefsDecoder, releaseSummaryDecoder, type ArtistRefDTO } from "./decoders";

// 曲目的安全摘要：收藏、歌单（以及后续的搜索与艺人页）共用的公开合同，与历史条目的同名字段同一套规则。
// 纯类型与解码器，不连带组件与样式：接口样本测试与真实服务用例可以直接导入这个模块。

// 曲目的来源事实（扫描观察，不是文件名或路径）：未知的编码与音频参数是 null；取值范围与历史条目、发行详情的同名字段一致。
const trackSourceDecoder = d.object({
  source_kind: d.string({ min: 1 }),
  codec: d.nullable(d.string()),
  bit_depth: d.nullable(d.integer({ min: 0 })),
  sample_rate: d.nullable(d.integer({ min: 0 })),
  channels: d.nullable(d.integer({ min: 0 })),
  bitrate: d.nullable(d.integer({ min: 0 })),
});

// 一首曲目当前所在的组与发行；文件缺失时 present 为 false，标题、艺人、发行与来源事实照常给出。时长未知是 null。
export const trackSummaryDecoder = d.object({
  track_id: d.uuid(),
  title: d.string(),
  artist: d.string(),
  artist_refs: artistRefsDecoder,
  group_id: d.uuid(),
  present: d.boolean(),
  duration_seconds: d.nullable(d.number({ min: 0 })),
  release: releaseSummaryDecoder,
  source: trackSourceDecoder,
});

export type TrackSummaryDTO = d.Infer<typeof trackSummaryDecoder>;

/** 批量读取曲目摘要一次最多的曲目（去重后）。 */
export const MAX_TRACK_SUMMARY_BATCH = 1_000;

// 批量曲目摘要（POST /tracks/summaries，M5 ⑤ 的 I4）：按首次输入的顺序，未知的 ID 不出现，文件缺失的照常给出（present 为 false）。
const trackSummariesDecoder: d.Decoder<{ items: TrackSummaryDTO[] }> = (input, path) => {
  const value = d.object({ items: d.array(trackSummaryDecoder, { max: MAX_TRACK_SUMMARY_BATCH }) })(input, path);
  const ids = value.items.map((item) => item.track_id.toLowerCase());
  if (new Set(ids).size !== ids.length) throw new d.DecodeError(`${path}.items`, "有重复的曲目");
  return value;
};
export const decodeTrackSummaries = d.defineDecoder(trackSummariesDecoder);

/** 曲目的显示标题：标题为空白时写“（无标题）”，与放进队列时的规则相同。 */
export function trackTitleOf(track: Pick<TrackSummaryDTO, "title">): string {
  return track.title.trim() === "" ? "（无标题）" : track.title;
}

/** 曲目的显示艺人：曲目艺人为空白时退回到专辑艺人（没有专辑艺人时用发行艺人）。 */
export function trackArtistOf(track: Pick<TrackSummaryDTO, "artist" | "release">): string {
  return track.artist.trim() === "" ? albumArtistOf(track.release) : track.artist;
}

/** 专辑艺人对应的艺人：有专辑艺人时用它的引用，没有时用发行艺人的（与 albumArtistOf 的文字同一个回退）。 */
export function albumArtistRefsOf(release: { album_artist: string | null; artist_refs: readonly ArtistRefDTO[]; album_artist_refs: readonly ArtistRefDTO[] }): readonly ArtistRefDTO[] {
  return release.album_artist === null ? release.artist_refs : release.album_artist_refs;
}

/** 曲目艺人对应的艺人：与 trackArtistOf 同一个回退（曲目艺人为空白时退回专辑艺人），名字与链接不会错配。 */
export function trackArtistRefsOf(track: Pick<TrackSummaryDTO, "artist" | "artist_refs" | "release">): readonly ArtistRefDTO[] {
  return track.artist.trim() === "" ? albumArtistRefsOf(track.release) : track.artist_refs;
}
