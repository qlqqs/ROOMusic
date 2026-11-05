import type { HistoryItemDTO, HistoryQueueDTO, HistoryTracksDTO } from "../features/listening/api/decoders";
import { albumGroup } from "./catalog-data";

/** 历史测试专用的合成曲目，Vitest 与浏览器路由替身共用；不导入带样式的应用入口。 */
export function historyItem(index: number, overrides: Partial<HistoryItemDTO> = {}): HistoryItemDTO {
  const group = albumGroup(index, { title: `历史专辑 ${index}`, artist: `专辑艺人 ${index}`, artwork: false });
  return {
    track_id: `00000000-0000-4000-8000-c${String(index).padStart(11, "0")}`,
    group_id: group.id,
    release: group.representative,
    title: `历史曲目 ${index}`,
    artist: `曲目艺人 ${index}`,
    artist_refs: [],
    present: true,
    play_count: 3,
    last_played_at: "2026-11-04T08:30:00Z",
    duration_seconds: 120,
    source: { source_kind: "flac_vorbis_comment", codec: "flac", bit_depth: 16, sample_rate: 44100, channels: 2, bitrate: null },
    ...overrides,
  };
}

export function historyPage(items: readonly HistoryItemDTO[], page = 1, pageSize = 50): HistoryTracksDTO {
  return { items: items.slice((page - 1) * pageSize, page * pageSize), pagination: { page, page_size: pageSize, total: items.length }, has_history: items.length > 0 };
}

export function historyQueue(items: readonly HistoryItemDTO[]): HistoryQueueDTO {
  return { items: items.slice(0, 1000), total: items.length, limit: 1000, truncated: items.length > 1000, has_history: items.length > 0 };
}
