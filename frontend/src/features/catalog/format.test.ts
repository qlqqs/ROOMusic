import { describe, expect, it } from "vitest";
import type { ReleaseMediumDTO } from "./api/decoders";
import { albumArtistOf, albumMeta, creditsText, groupCredits, isFieldCorrected, mediumHeading, releaseFacts, totalDuration } from "./format";

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

function medium(durations: (number | null)[]): ReleaseMediumDTO {
  return {
    id: "00000000-0000-4000-8000-000000000001",
    position: 1,
    title: "Medium",
    tracks: durations.map((duration, index) => ({
      id: `00000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      title: `Track ${index + 1}`,
      artist: "Artist",
      position: index + 1,
      source: "a.flac",
      source_kind: "flac_vorbis_comment",
      duration_seconds: duration,
      codec: null,
      bit_depth: null,
      sample_rate: null,
      channels: null,
      bitrate: null,
      cue: null,
      credits: [],
    })),
  };
}

describe("totalDuration", () => {
  it("跨介质求和", () => {
    expect(totalDuration([medium([100, 200]), medium([50.5])])).toBe(350.5);
  });

  it("有一首曲目时长未知，或者没有曲目，总时长就是未知", () => {
    expect(totalDuration([medium([100, null])])).toBeNull();
    expect(totalDuration([medium([100]), medium([null])])).toBeNull();
    expect(totalDuration([medium([])])).toBeNull();
    expect(totalDuration([])).toBeNull();
  });
});

describe("mediumHeading", () => {
  it("默认的介质标题不重复写，自己的标题附在碟号后面", () => {
    expect(mediumHeading({ position: 2, title: "Medium" }, 1)).toBe("第 2 张");
    expect(mediumHeading({ position: 1, title: "" }, 0)).toBe("第 1 张");
    expect(mediumHeading({ position: 1, title: "  " }, 0)).toBe("第 1 张");
    expect(mediumHeading({ position: 3, title: "Bonus Disc" }, 2)).toBe("第 3 张 · Bonus Disc");
  });

  it("碟号缺失（0）时用顺序", () => {
    expect(mediumHeading({ position: 0, title: "Medium" }, 4)).toBe("第 5 张");
  });
});

describe("署名", () => {
  const credits = [
    { role: "composer", name: "甲" },
    { role: "conductor", name: "丙" },
    { role: "composer", name: "乙" },
    { role: "mixer", name: "丁" },
  ];

  it("按角色归并，保持出现顺序", () => {
    expect(groupCredits(credits)).toEqual([
      { role: "composer", names: ["甲", "乙"] },
      { role: "conductor", names: ["丙"] },
      { role: "mixer", names: ["丁"] },
    ]);
  });

  it("文字形式：已知角色给中文名，未知角色原样，分号分隔", () => {
    expect(creditsText(credits)).toBe("作曲：甲、乙；指挥：丙；mixer：丁");
    expect(creditsText([])).toBe("");
  });
});

describe("releaseFacts", () => {
  const empty = { label: null, catalog_number: null, barcode: null, edition: null, genre: null, source_type: null, media_type: null };

  it("没有值（或只有空白）的条目不列出", () => {
    expect(releaseFacts(empty)).toEqual([]);
    expect(releaseFacts({ ...empty, label: "  ", genre: "" })).toEqual([]);
  });

  it("按固定顺序列出有值的条目", () => {
    expect(
      releaseFacts({ label: "L", catalog_number: "C-1", barcode: "012", edition: "Remastered", genre: "Classical", source_type: "CD", media_type: "CD" }),
    ).toEqual([
      { label: "厂牌", value: "L", field: "label" },
      { label: "编号", value: "C-1", field: "catalog_number" },
      { label: "条码", value: "012", field: "barcode" },
      { label: "版本说明", value: "Remastered", field: "edition" },
      { label: "流派", value: "Classical", field: "genre" },
      { label: "来源类型", value: "CD", field: "source_type" },
      { label: "介质类型", value: "CD", field: "media_type" },
    ]);
  });
});

describe("isFieldCorrected", () => {
  it("字段名在人工修正的清单里就是已修正", () => {
    expect(isFieldCorrected(["title", "genre"], "genre")).toBe(true);
    expect(isFieldCorrected(["title"], "genre")).toBe(false);
    expect(isFieldCorrected([], "title")).toBe(false);
  });

  it("专辑艺人的修正同时覆盖证据里的“艺人”，反过来不成立", () => {
    expect(isFieldCorrected(["album_artist"], "artist")).toBe(true);
    expect(isFieldCorrected(["album_artist"], "album_artist")).toBe(true);
    expect(isFieldCorrected(["title"], "artist")).toBe(false);
  });
});
