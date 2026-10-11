import { decode as d } from "../../../shared/api";
import { releaseSummaryDecoder } from "../../catalog";

// 上限与后端一致（backend/internal/listening 的 RecentReleaseLimit）：超界的响应整体拒绝，而不是截断后显示。
const MAX_RECENT_RELEASES = 8;

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

export const decodeRecordedPlay = d.defineDecoder(recordedPlayDecoder);
export const decodeRecentReleases = d.defineDecoder(recentReleasesDecoder);
export const decodePlaysCleared = d.defineDecoder(playsClearedDecoder);

export type RecordedPlayDTO = d.Infer<typeof recordedPlayDecoder>;
export type RecentReleaseDTO = d.Infer<typeof recentReleaseDecoder>;
export type RecentReleasesDTO = d.Infer<typeof recentReleasesDecoder>;
export type PlaysClearedDTO = d.Infer<typeof playsClearedDecoder>;
