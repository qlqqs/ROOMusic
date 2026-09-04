import { describe, expect, it } from "vitest";
import type { ReleaseDetailDTO, TrackDTO } from "../../../api";
import {
  artworkURL,
  clearDemoQueue,
  coverFallbackLabel,
  currentDemoItem,
  emptyDemoQueue,
  flattenReleaseTracks,
  formatAudioFacts,
  formatDuration,
  formatReleaseLabel,
  maxDemoQueueItems,
  nextDemoTrack,
  playDemoItems,
  playDemoTrack,
  previousDemoTrack,
  removeCurrentDemoItem,
  setDemoPlaying,
  toReleaseCardModel,
  toTrackRowModel,
  type DemoQueueItem,
} from "./display";

function makeTrack(id: string, overrides: Partial<TrackDTO> = {}): TrackDTO {
  return {
    id,
    title: `曲目 ${id}`,
    artist: "艺术家",
    position: 1,
    source: `${id}.flac`,
    source_kind: "flac_vorbis_comment",
    duration_seconds: 240.5,
    codec: "flac",
    bit_depth: 24,
    sample_rate: 96_000,
    channels: 2,
    bitrate: 2_400,
    cue: null,
    credits: [],
    ...overrides,
  };
}

function makeItem(id: string): DemoQueueItem {
  return { releaseId: "release-1", releaseTitle: "专辑", releaseArtist: "艺术家", releaseArtwork: null, track: makeTrack(id) };
}

function makeDetail(trackIDs: string[][]): ReleaseDetailDTO {
  return {
    id: "release-1",
    title: "专辑",
    artist: "艺术家",
    album_artist: null,
    year: 2026,
    source_type: null,
    media_type: "CD",
    medium_count: trackIDs.length,
    track_count: trackIDs.flat().length,
    attention_count: 0,
    artwork: null,
    candidate_kind: "ordinary_directory",
    genre: null,
    catalog_number: null,
    edition: null,
    label: null,
    barcode: null,
    credits: [],
    evidence: [],
    media: trackIDs.map((ids, index) => ({
      id: `medium-${index + 1}`,
      position: index + 1,
      title: "",
      tracks: ids.map((id) => makeTrack(id)),
    })),
  };
}

describe("formatDuration", () => {
  it("formats valid durations and falls back on invalid values", () => {
    expect(formatDuration(240.5)).toBe("4:01");
    expect(formatDuration(0)).toBe("0:00");
    expect(formatDuration(59.4)).toBe("0:59");
    expect(formatDuration(3_661)).toBe("61:01");
    expect(formatDuration(null)).toBe("未记录");
    expect(formatDuration(Number.NaN)).toBe("未记录");
    expect(formatDuration(Number.POSITIVE_INFINITY)).toBe("未记录");
    expect(formatDuration(-1)).toBe("未记录");
  });
});

describe("formatAudioFacts", () => {
  it("joins only the facts that exist", () => {
    expect(formatAudioFacts(makeTrack("t1"))).toBe("flac · 24 bit · 96 kHz · 2 声道 · 2400 kbps");
    expect(formatAudioFacts(makeTrack("t2", { codec: null, bit_depth: null, sample_rate: null, channels: null, bitrate: null }))).toBe("");
    expect(formatAudioFacts(makeTrack("t3", {
      cue: { index_frames: null, end_frames: null, start_seconds: 12.345, end_seconds: null, isrc: null },
    }))).toContain("CUE 12.35s");
    expect(formatAudioFacts(makeTrack("t4", {
      cue: { index_frames: null, end_frames: null, start_seconds: null, end_seconds: null, isrc: null },
    }))).not.toContain("CUE");
  });

  it("never emits undefined or raw paths", () => {
    expect(formatAudioFacts(makeTrack("t5", { codec: null }))).not.toContain("undefined");
  });
});

describe("formatReleaseLabel", () => {
  it("prefers album artist, then artist, then the unknown label", () => {
    expect(formatReleaseLabel({ album_artist: "专辑艺术家", artist: "艺术家" })).toBe("专辑艺术家");
    expect(formatReleaseLabel({ album_artist: null, artist: "艺术家" })).toBe("艺术家");
    expect(formatReleaseLabel({ album_artist: "  ", artist: "艺术家" })).toBe("艺术家");
    expect(formatReleaseLabel({ album_artist: null, artist: "" })).toBe("艺术家未知");
    expect(formatReleaseLabel({ album_artist: null, artist: "   " })).toBe("艺术家未知");
  });
});

describe("coverFallbackLabel", () => {
  it("uses the first displayable character or the music note", () => {
    expect(coverFallbackLabel("  爵士之夜")).toBe("爵");
    expect(coverFallbackLabel("")).toBe("♫");
    expect(coverFallbackLabel("   ")).toBe("♫");
    expect(coverFallbackLabel("Étude")).toBe("É");
  });
});

describe("artworkURL", () => {
  it("builds the authorized same-origin URL only from resource_id", () => {
    expect(artworkURL(null)).toBeNull();
    expect(artworkURL({ resource_id: "cover abc", mime: "image/png", width: 1, height: 1 })).toBe("/api/v1/artworks/cover%20abc");
  });
});

describe("toReleaseCardModel / toTrackRowModel", () => {
  it("projects empty values to safe display labels", () => {
    const model = toReleaseCardModel({
      id: "release-1",
      title: "",
      artist: "",
      album_artist: null,
      year: null,
      source_type: null,
      media_type: null,
      medium_count: 1,
      track_count: 3,
      attention_count: 2,
      artwork: null,
    });
    expect(model.title).toBe("未知专辑");
    expect(model.artistLabel).toBe("艺术家未知");
    expect(model.yearLabel).toBe("年份未知");
    expect(model.sourceLabel).toBeNull();
    expect(model.fallbackLabel).toBe("♫");

    const row = toTrackRowModel(makeTrack("t1", { title: "", artist: "", duration_seconds: null, credits: [{ role: "composer", name: "作曲者" }] }));
    expect(row.title).toBe("未命名曲目");
    expect(row.artistLabel).toBe("艺术家未知");
    expect(row.durationLabel).toBe("未记录");
    expect(row.creditsLabel).toBe("composer：作曲者");
  });
});

describe("flattenReleaseTracks", () => {
  it("keeps medium and track order with release context", () => {
    const items = flattenReleaseTracks(makeDetail([["a1", "a2"], ["b1"]]));
    expect(items.map((item) => item.track.id)).toEqual(["a1", "a2", "b1"]);
    expect(items[0]?.releaseId).toBe("release-1");
    expect(items[0]?.releaseTitle).toBe("专辑");
    expect(items[0]?.releaseArtist).toBe("艺术家");
    expect(flattenReleaseTracks(makeDetail([]))).toEqual([]);
  });
});

describe("demo queue", () => {
  it("plays a track set and steps within bounds", () => {
    let queue = playDemoItems(emptyDemoQueue, [makeItem("a"), makeItem("b"), makeItem("c")]);
    expect(queue.currentIndex).toBe(0);
    expect(queue.isPlaying).toBe(true);

    queue = nextDemoTrack(queue);
    expect(queue.currentIndex).toBe(1);
    expect(previousDemoTrack(queue).currentIndex).toBe(0);
    expect(nextDemoTrack(nextDemoTrack(nextDemoTrack(queue))).currentIndex).toBe(2);
    expect(previousDemoTrack({ ...queue, currentIndex: 0 }).currentIndex).toBe(0);
  });

  it("appends without stealing the current position and dedupes single-track play", () => {
    let queue = playDemoItems(emptyDemoQueue, [makeItem("a"), makeItem("b")]);
    queue = nextDemoTrack(queue);
    queue = playDemoItems(queue, [makeItem("c")]);
    expect(queue.items).toHaveLength(3);
    expect(queue.currentIndex).toBe(1);

    queue = playDemoTrack(queue, makeItem("d"));
    expect(queue.currentIndex).toBe(3);
    queue = playDemoTrack(queue, makeItem("a"));
    expect(queue.items).toHaveLength(4);
    expect(queue.currentIndex).toBe(0);
  });

  it("removes the current item with a deterministic index fix", () => {
    let queue = playDemoItems(emptyDemoQueue, [makeItem("a"), makeItem("b"), makeItem("c")]);
    queue = { ...queue, currentIndex: 2, isPlaying: true };
    queue = removeCurrentDemoItem(queue);
    expect(queue.items.map((item) => item.track.id)).toEqual(["a", "b"]);
    expect(queue.currentIndex).toBe(1);
    expect(queue.isPlaying).toBe(true);

    queue = removeCurrentDemoItem(removeCurrentDemoItem(queue));
    expect(queue).toEqual(emptyDemoQueue);
    expect(removeCurrentDemoItem(emptyDemoQueue)).toEqual(emptyDemoQueue);
  });

  it("clears the queue, pauses, and enforces the queue bound", () => {
    let queue = playDemoItems(emptyDemoQueue, [makeItem("a")]);
    queue = setDemoPlaying(queue, false);
    expect(queue.isPlaying).toBe(false);
    expect(setDemoPlaying(emptyDemoQueue, true)).toEqual(emptyDemoQueue);
    expect(clearDemoQueue(queue)).toEqual(emptyDemoQueue);
    expect(currentDemoItem(queue)?.track.id).toBe("a");

    const full = playDemoItems(emptyDemoQueue, Array.from({ length: maxDemoQueueItems }, (_, index) => makeItem(`t${index}`)));
    const overflow = playDemoTrack(full, makeItem("overflow"));
    expect(overflow.items).toHaveLength(maxDemoQueueItems);
    expect(overflow.currentIndex).toBe(0);
  });
});
