import { decode as d } from "../../../shared/api";
import { artistRefsDecoder, releaseSummaryDecoder } from "../../catalog/api/decoders";

// 上限与后端一致（backend/internal/listening 的 RecentReleaseLimit）：超界的响应整体拒绝，而不是截断后显示。
const MAX_RECENT_RELEASES = 8;
/** 历史页每页条数的上限（后端 ParsePagination 的上限）。 */
export const MAX_HISTORY_PAGE_SIZE = 100;
/** “播放全部”一次取回的历史曲目上限（后端固定值，与 catalog 一次批量读取的上限一致）。 */
export const HISTORY_QUEUE_LIMIT = 1000;

// 记录一次播放的响应（201，重放同样是 201 与同一个响应）。
const recordedPlayDecoder = d.object({
  id: d.uuid(),
  track_id: d.uuid(),
  played_at: d.timestamp(),
});

// 最近播放的一个发行：当前所在的组与发行摘要（与专辑列表同一个摘要形状，含封面）、最后播放的时间与曲目。
const recentReleaseDecoder = d.object({
  group_id: d.uuid(),
  release: releaseSummaryDecoder,
  last_played_at: d.timestamp(),
  last_track: d.object({
    id: d.uuid(),
    title: d.string(),
  }),
});

// has_history 表示是否有任何记录（含当前不显示的，例如曲目文件已缺失）：items 为空而 has_history 为真时仍可以清除。
const recentReleasesDecoder = d.object({
  items: d.array(recentReleaseDecoder, { max: MAX_RECENT_RELEASES }),
  has_history: d.boolean(),
});

const playsClearedDecoder = d.object({
  deleted: d.integer({ min: 0 }),
});

// 曲目的来源事实（扫描观察，不是文件名或路径）：未知的编码与音频参数是 null，与发行详情里曲目的同名字段同一套规则。
const historySourceDecoder = d.object({
  source_kind: d.string({ min: 1 }),
  codec: d.nullable(d.string()),
  bit_depth: d.nullable(d.integer({ min: 0 })),
  sample_rate: d.nullable(d.integer({ min: 0 })),
  channels: d.nullable(d.integer({ min: 0 })),
  bitrate: d.nullable(d.integer({ min: 0 })),
});

// 历史里的一首曲目：最后一次播放的时间与次数（记录条数），以及曲目当前所在的组与发行。
// 文件缺失时 present 为 false，标题、艺人、发行与来源事实照常给出；标题与艺人可以是空字符串。
const historyItemDecoder = d.object({
  track_id: d.uuid(),
  last_played_at: d.timestamp(),
  play_count: d.integer({ min: 1 }),
  present: d.boolean(),
  title: d.string(),
  artist: d.string(),
  artist_refs: artistRefsDecoder,
  duration_seconds: d.nullable(d.number({ min: 0 })),
  group_id: d.uuid(),
  release: releaseSummaryDecoder,
  source: historySourceDecoder,
});

// 历史的一页：total 是本人播放过的不同曲目数（含文件缺失的）；页码超出末页时 items 为空、total 照实给出。
const historyTracksDecoder = d.object({
  items: d.array(historyItemDecoder, { max: MAX_HISTORY_PAGE_SIZE }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_HISTORY_PAGE_SIZE }),
    total: d.integer({ min: 0 }),
  }),
  has_history: d.boolean(),
});

// “播放全部”的候选：与历史页同一顺序的最近最多 1,000 首（不按能否播放筛选）；truncated 表示更早的历史没有取回。
const historyQueueDecoder = d.object({
  items: d.array(historyItemDecoder, { max: HISTORY_QUEUE_LIMIT }),
  total: d.integer({ min: 0 }),
  limit: d.literal(HISTORY_QUEUE_LIMIT),
  truncated: d.boolean(),
  has_history: d.boolean(),
});

export const decodeRecordedPlay = d.defineDecoder(recordedPlayDecoder);
export const decodeRecentReleases = d.defineDecoder(recentReleasesDecoder);
export const decodePlaysCleared = d.defineDecoder(playsClearedDecoder);
export const decodeHistoryTracks = d.defineDecoder(historyTracksDecoder);
export const decodeHistoryQueue = d.defineDecoder(historyQueueDecoder);
/** 从历史中移除一首曲目的响应：删掉的播放记录数，与清除全部同一形状（没有记录时为 0）。 */
export const decodeTrackPlaysDeleted = d.defineDecoder(playsClearedDecoder);

export type RecordedPlayDTO = d.Infer<typeof recordedPlayDecoder>;
export type RecentReleaseDTO = d.Infer<typeof recentReleaseDecoder>;
export type RecentReleasesDTO = d.Infer<typeof recentReleasesDecoder>;
export type PlaysClearedDTO = d.Infer<typeof playsClearedDecoder>;
export type HistoryItemDTO = d.Infer<typeof historyItemDecoder>;
export type HistoryTracksDTO = d.Infer<typeof historyTracksDecoder>;
export type HistoryQueueDTO = d.Infer<typeof historyQueueDecoder>;
