import { describe, expect, it } from "vitest";
import { albumArtistOf, albumMeta } from "./format";

describe("albumMeta", () => {
  it("年份、介质数（多于一张才写）与曲目数，用间隔号连接", () => {
    expect(albumMeta({ year: 1998, medium_count: 2, track_count: 3 })).toBe("1998 · 2 张介质 · 3 首曲目");
    expect(albumMeta({ year: 2001, medium_count: 1, track_count: 1 })).toBe("2001 · 1 首曲目");
  });

  it("年份未知就省略，不写占位", () => {
    expect(albumMeta({ year: null, medium_count: 1, track_count: 12 })).toBe("12 首曲目");
  });

  it("数量按中文习惯加千位分隔符", () => {
    expect(albumMeta({ year: null, medium_count: 12, track_count: 1234 })).toBe("12 张介质 · 1,234 首曲目");
  });
});

describe("albumArtistOf", () => {
  it("优先专辑艺人，没有时用艺人", () => {
    expect(albumArtistOf({ album_artist: "柏林爱乐", artist: "卡拉扬" })).toBe("柏林爱乐");
    expect(albumArtistOf({ album_artist: null, artist: "Glenn Gould" })).toBe("Glenn Gould");
  });
});
