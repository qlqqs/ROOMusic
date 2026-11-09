// 播放队列的类型与只用于显示的辅助函数。队列语义（四种播放模式、插入、移除、移动、前进与后退）在服务端（zones/queue），
// 浏览器只显示服务端给的队列。

import type { ArtistRefDTO } from "../../catalog";

export const playModes = ["sequential", "repeat_all", "repeat_one", "shuffle"] as const;
export type PlayMode = (typeof playModes)[number];

/** 队列容量上限，与服务端一致：整张发行版本都能一次放进队列。 */
export const MAX_QUEUE_ENTRIES = 10_000;

/** 源文件的事实（取自扫描观察，不是当前文件可播性的证明）。缺失的值是 null，显示为“未知”。 */
export type SourceFacts = {
  sourceKind: string;
  codec: string | null;
  bitDepth: number | null;
  sampleRate: number | null;
  channels: number | null;
  bitrate: number | null;
};

/** 队列里一首曲目需要的显示与来源信息。曲目身份是 trackId，同一首可以多次入队。 */
export type PlayerTrack = {
  trackId: string;
  releaseId: string;
  groupId: string;
  title: string;
  artist: string;
  /**
   * 艺人文字对应的艺人（M5 ⑤）：与 artist 同一个回退，名字与链接不会错配。所有构造入口都会给出；缺省（旧的测试数据）等于空数组，
   * 艺人名显示为纯文字。只用于显示，不进入刷新恢复的记录。
   */
  artistRefs?: readonly ArtistRefDTO[];
  albumTitle: string;
  albumArtist: string;
  /** 扫描得到的时长，只用于队列显示；播放进度与时长取自媒体事件。 */
  durationSeconds: number | null;
  /** 封面资源 ID（同源封面接口）；没有封面是 null。 */
  artworkId: string | null;
  source: SourceFacts;
};

/** 队列条目：entryId 表达同一首曲目多次入队的独立位置，排序与移除按它操作，不能与 trackId 混用。 */
export type QueueEntry = { entryId: string; track: PlayerTrack };

/** 正在显示的播放区队列：条目按列表顺序，当前条目与播放模式取自播放区状态。 */
export type QueueState = {
  entries: readonly QueueEntry[];
  currentEntryId: string | null;
  mode: PlayMode;
};

export const emptyQueue: QueueState = { entries: [], currentEntryId: null, mode: "sequential" };

/** 队列条目上只用于显示的字段（M5 ⑤）：刷新它们不影响身份、来源事实与播放。 */
export type TrackDisplayMetadata = Pick<PlayerTrack, "title" | "artist" | "artistRefs" | "albumTitle" | "albumArtist" | "artworkId">;

function sameRefs(a: PlayerTrack["artistRefs"], b: PlayerTrack["artistRefs"]): boolean {
  const left = a ?? [];
  const right = b ?? [];
  return left.length === right.length && left.every((ref, index) => ref.artist_id === right[index]?.artist_id && ref.name === right[index]?.name && ref.display_name === right[index]?.display_name);
}

function sameMetadata(track: PlayerTrack, patch: TrackDisplayMetadata): boolean {
  return (
    track.title === patch.title &&
    track.artist === patch.artist &&
    track.albumTitle === patch.albumTitle &&
    track.albumArtist === patch.albumArtist &&
    track.artworkId === patch.artworkId &&
    sameRefs(track.artistRefs, patch.artistRefs)
  );
}

/** 按曲目 ID 刷新一首曲目的显示元数据；没有补丁或没有变化时返回原对象（调用方据此判断是否变化）。 */
export function patchTrack(track: PlayerTrack, patches: ReadonlyMap<string, TrackDisplayMetadata>): PlayerTrack {
  const patch = patches.get(track.trackId);
  return patch === undefined || sameMetadata(track, patch) ? track : { ...track, ...patch };
}

/** 刷新条目列表里曲目的显示元数据（同一首的每个条目都改），顺序与条目 ID 不变；没有任何变化时返回原数组。 */
export function patchEntries(entries: readonly QueueEntry[], patches: ReadonlyMap<string, TrackDisplayMetadata>): readonly QueueEntry[] {
  let changed = false;
  const next = entries.map((entry) => {
    const track = patchTrack(entry.track, patches);
    if (track === entry.track) return entry;
    changed = true;
    return { ...entry, track };
  });
  return changed ? next : entries;
}

/**
 * “接下来”：当前条目之后的条目，最多 limit 个。随机模式下顺序由服务端决定，这里给出当前项以外的条目，
 * 并不代表真实的播放顺序；调用方据此写明“随机”。
 */
export function upNext(state: QueueState, limit: number): readonly QueueEntry[] {
  if (state.mode === "shuffle") return state.entries.filter((entry) => entry.entryId !== state.currentEntryId).slice(0, limit);
  const at = state.entries.findIndex((entry) => entry.entryId === state.currentEntryId);
  return state.entries.slice(at + 1, at + 1 + limit);
}
