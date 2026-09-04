import type {
  ReleaseArtworkDTO,
  ReleaseDetailDTO,
  ReleaseSummaryDTO,
  TrackDTO,
} from "../../../api";

// 本文件只包含无副作用的 DTO -> 视图模型投影与格式化函数；
// 不读取 DOM、不发起请求、不输出绝对路径或原始 JSON。

export const unknownArtistLabel = "艺术家未知";
export const untitledReleaseLabel = "未知专辑";
export const untitledTrackLabel = "未命名曲目";
export const notRecordedLabel = "未记录";

export function formatDuration(seconds: number | null): string {
  if (seconds === null || typeof seconds !== "number" || !Number.isFinite(seconds) || seconds < 0) {
    return notRecordedLabel;
  }
  const wholeSeconds = Math.round(seconds);
  const minutes = Math.floor(wholeSeconds / 60);
  const restSeconds = wholeSeconds % 60;
  return `${minutes}:${String(restSeconds).padStart(2, "0")}`;
}

export function formatAudioFacts(track: TrackDTO): string {
  const facts: string[] = [];
  if (track.codec) facts.push(track.codec);
  if (track.bit_depth) facts.push(`${track.bit_depth} bit`);
  if (track.sample_rate) facts.push(`${Math.round(track.sample_rate / 1000)} kHz`);
  if (track.channels) facts.push(`${track.channels} 声道`);
  if (track.bitrate) facts.push(`${track.bitrate} kbps`);
  if (track.cue && track.cue.start_seconds !== null) facts.push(`CUE ${track.cue.start_seconds.toFixed(2)}s`);
  return facts.join(" · ");
}

export function formatReleaseLabel(release: { album_artist: string | null; artist: string }): string {
  const albumArtist = release.album_artist?.trim() ?? "";
  if (albumArtist) return release.album_artist ?? unknownArtistLabel;
  const artist = release.artist.trim();
  return artist === "" ? unknownArtistLabel : release.artist;
}

export function formatSourceMediaLabel(
  release: Pick<ReleaseSummaryDTO, "source_type" | "media_type">,
  separator = " / ",
): string | null {
  const values: string[] = [];
  const seen = new Set<string>();
  for (const rawValue of [release.source_type, release.media_type]) {
    const value = rawValue?.trim();
    if (!value) continue;
    const normalized = value.toLowerCase();
    if (seen.has(normalized)) continue;
    seen.add(normalized);
    values.push(value);
  }
  return values.length > 0 ? values.join(separator) : null;
}

export function coverFallbackLabel(title: string): string {
  const trimmed = title.trim();
  if (trimmed === "") return "♫";
  const first = Array.from(trimmed)[0];
  return first ?? "♫";
}

export function artworkURL(artwork: ReleaseArtworkDTO | null): string | null {
  if (!artwork) return null;
  return `/api/v1/artworks/${encodeURIComponent(artwork.resource_id)}`;
}

export type ReleaseCardModel = {
  id: string;
  title: string;
  artistLabel: string;
  yearLabel: string;
  sizeLabel: string;
  sourceLabel: string | null;
  attentionCount: number;
  artwork: ReleaseArtworkDTO | null;
  fallbackLabel: string;
};

export function toReleaseCardModel(release: ReleaseSummaryDTO): ReleaseCardModel {
  const title = release.title.trim() === "" ? untitledReleaseLabel : release.title;
  return {
    id: release.id,
    title,
    artistLabel: formatReleaseLabel(release),
    yearLabel: release.year === null ? "年份未知" : String(release.year),
    sizeLabel: `${release.medium_count} 碟 / ${release.track_count} 首`,
    sourceLabel: formatSourceMediaLabel(release, " · "),
    attentionCount: release.attention_count,
    artwork: release.artwork,
    fallbackLabel: coverFallbackLabel(release.title),
  };
}

export type TrackRowModel = {
  id: string;
  positionLabel: string;
  title: string;
  artistLabel: string;
  durationLabel: string;
  factsLabel: string;
  creditsLabel: string | null;
};

export function toTrackRowModel(track: TrackDTO): TrackRowModel {
  const creditsLabel = track.credits.length > 0
    ? track.credits.map((credit) => `${credit.role}：${credit.name}`).join(" / ")
    : null;
  return {
    id: track.id,
    positionLabel: String(track.position).padStart(2, "0"),
    title: track.title.trim() === "" ? untitledTrackLabel : track.title,
    artistLabel: track.artist.trim() === "" ? unknownArtistLabel : track.artist,
    durationLabel: formatDuration(track.duration_seconds),
    factsLabel: formatAudioFacts(track),
    creditsLabel,
  };
}

export type DemoQueueItem = {
  releaseId: string;
  releaseTitle: string;
  releaseArtist: string;
  releaseArtwork: ReleaseArtworkDTO | null;
  track: TrackDTO;
};

export type DemoQueueState = {
  items: DemoQueueItem[];
  currentIndex: number | null;
  isPlaying: boolean;
};

export const maxDemoQueueItems = 500;

export const emptyDemoQueue: DemoQueueState = { items: [], currentIndex: null, isPlaying: false };

// 保留服务端 Medium/Track 顺序展开为演示队列上下文，不改变任何身份信息。
export function flattenReleaseTracks(detail: ReleaseDetailDTO): DemoQueueItem[] {
  const releaseTitle = detail.title.trim() === "" ? untitledReleaseLabel : detail.title;
  const releaseArtist = formatReleaseLabel(detail);
  const items: DemoQueueItem[] = [];
  for (const medium of detail.media) {
    for (const track of medium.tracks) {
      items.push({ releaseId: detail.id, releaseTitle, releaseArtist, releaseArtwork: detail.artwork, track });
    }
  }
  return items;
}

function boundedAppend(items: DemoQueueItem[], incoming: DemoQueueItem[]): DemoQueueItem[] {
  const remaining = maxDemoQueueItems - items.length;
  if (remaining <= 0) return items;
  return [...items, ...incoming.slice(0, remaining)];
}

// “播放全部 / 播放首曲”：追加到队尾；队列空闲时从新增项开始演示。
export function playDemoItems(queue: DemoQueueState, items: DemoQueueItem[]): DemoQueueState {
  if (items.length === 0) return queue;
  const nextItems = boundedAppend(queue.items, items);
  if (nextItems === queue.items) return queue;
  const currentIndex = queue.currentIndex ?? queue.items.length;
  return { items: nextItems, currentIndex, isPlaying: true };
}

// “逐曲播放”：已在队列中则跳到该曲目，否则追加并跳到队尾。
export function playDemoTrack(queue: DemoQueueState, item: DemoQueueItem): DemoQueueState {
  const existingIndex = queue.items.findIndex((queued) => queued.track.id === item.track.id);
  if (existingIndex >= 0) {
    return { ...queue, currentIndex: existingIndex, isPlaying: true };
  }
  const nextItems = boundedAppend(queue.items, [item]);
  if (nextItems === queue.items) return queue;
  return { items: nextItems, currentIndex: nextItems.length - 1, isPlaying: true };
}

export function nextDemoTrack(queue: DemoQueueState): DemoQueueState {
  if (queue.currentIndex === null || queue.currentIndex >= queue.items.length - 1) return queue;
  return { ...queue, currentIndex: queue.currentIndex + 1 };
}

export function previousDemoTrack(queue: DemoQueueState): DemoQueueState {
  if (queue.currentIndex === null || queue.currentIndex <= 0) return queue;
  return { ...queue, currentIndex: queue.currentIndex - 1 };
}

// 移除当前项后确定性修正索引：指向原下一首；移除末尾则指向新的末首。
export function removeCurrentDemoItem(queue: DemoQueueState): DemoQueueState {
  if (queue.currentIndex === null) return queue;
  const nextItems = queue.items.filter((_, index) => index !== queue.currentIndex);
  if (nextItems.length === 0) return emptyDemoQueue;
  const currentIndex = Math.min(queue.currentIndex, nextItems.length - 1);
  return { items: nextItems, currentIndex, isPlaying: queue.isPlaying };
}

export function clearDemoQueue(_queue: DemoQueueState): DemoQueueState {
  return emptyDemoQueue;
}

export function setDemoPlaying(queue: DemoQueueState, isPlaying: boolean): DemoQueueState {
  if (queue.currentIndex === null) return queue;
  return { ...queue, isPlaying };
}

export function currentDemoItem(queue: DemoQueueState): DemoQueueItem | null {
  if (queue.currentIndex === null) return null;
  return queue.items[queue.currentIndex] ?? null;
}
