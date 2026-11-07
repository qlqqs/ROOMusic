import { describe, expect, it } from "vitest";
import { sampleBody } from "../../test/api-samples";
import { decodeReleaseComparison, type ComparedReleaseDTO } from "./api/decoders";
import { audioFormatText, comparisonRows, formatSampleRate, versionLabel } from "./comparison";

const sample = decodeReleaseComparison(sampleBody("release-groups/comparison"));

/** 以样本里的版本为底，造一个只改个别字段的版本；id 由序号决定。 */
function version(index: number, overrides: Partial<ComparedReleaseDTO> = {}): ComparedReleaseDTO {
  const base = sample.releases[1];
  if (base === undefined) throw new Error("样本里没有第二个版本");
  return { ...base, id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`, corrected_fields: [], ...overrides };
}

const rowOf = (rows: ReturnType<typeof comparisonRows>, key: string) => rows.find((row) => row.key === key);
const flac = (bitDepth: number | null, sampleRate: number | null, tracks = 1) => ({ codec: "flac", bit_depth: bitDepth, sample_rate: sampleRate, track_count: tracks });

describe("音频格式的显示", () => {
  it.each([
    [{ codec: "flac", bit_depth: 24, sample_rate: 96000 }, "FLAC · 24 bit / 96 kHz"],
    [{ codec: "flac", bit_depth: 16, sample_rate: 44100 }, "FLAC · 16 bit / 44.1 kHz"],
    [{ codec: "alac", bit_depth: 24, sample_rate: 48000 }, "ALAC · 24 bit / 48 kHz"],
    [{ codec: "mp3", bit_depth: null, sample_rate: 44100 }, "MP3 · 44.1 kHz"],
    [{ codec: "aac", bit_depth: null, sample_rate: null }, "AAC"],
    [{ codec: null, bit_depth: 16, sample_rate: 44100 }, "16 bit / 44.1 kHz"],
    [{ codec: null, bit_depth: null, sample_rate: 22050 }, "22.05 kHz"],
    [{ codec: null, bit_depth: null, sample_rate: null }, "参数未知"],
    [{ codec: "future_codec", bit_depth: 32, sample_rate: 192000 }, "future_codec · 32 bit / 192 kHz"],
  ])("%j → %s", (format, expected) => {
    expect(audioFormatText(format)).toBe(expected);
  });

  it("采样率去掉多余的小数", () => {
    expect([44100, 48000, 88200, 96000, 176400, 192000, 22050, 11025].map(formatSampleRate)).toEqual(["44.1 kHz", "48 kHz", "88.2 kHz", "96 kHz", "176.4 kHz", "192 kHz", "22.05 kHz", "11.025 kHz"]);
  });

  it("从不出现质量结论：任何参数组合的文字里都没有“最高”“更好”“无损”之类的评价", () => {
    for (const bitDepth of [null, 8, 16, 24, 32]) {
      for (const rate of [null, 44100, 96000, 192000, 2822400]) {
        expect(audioFormatText({ codec: "flac", bit_depth: bitDepth, sample_rate: rate })).not.toMatch(/最高|更好|最佳|高解析|无损|音质/);
      }
    }
  });
});

describe("版本名", () => {
  it("版本 N 与版本导航一致，从 1 起", () => {
    expect([0, 1, 9].map(versionLabel)).toEqual(["版本 1", "版本 2", "版本 10"]);
  });
});

describe("对比表的行", () => {
  it("样本：三个版本的行、基准列与差异", () => {
    const rows = comparisonRows(sample.releases);
    // 标题与专辑艺人各版本不同，所以出现；年份有一个版本没有值（人工修正为空）；介质类型、来源类型都没有值，整行隐藏。
    expect(rows.map((row) => row.key)).toEqual(["title", "album_artist", "year", "edition", "label", "catalog_number", "barcode", "genre", "content", "duration", "formats"]);
    const year = rowOf(rows, "year");
    expect(year?.cells.map((cell) => cell.lines)).toEqual([[], ["2001"], ["2005"]]);
    expect(year?.cells.map((cell) => cell.differs)).toEqual([false, true, true]);
    expect(year?.cells[0]?.corrected).toBe(true);
    expect(rowOf(rows, "title")?.cells.map((cell) => cell.corrected)).toEqual([true, false, false]);
    expect(rowOf(rows, "label")?.cells.map((cell) => [cell.lines, cell.corrected])).toEqual([[["Corrected Label"], true], [[], false], [[], false]]);
    expect(rowOf(rows, "content")?.cells.map((cell) => cell.lines[0])).toEqual(["2 张介质 · 3 首曲目", "1 张介质 · 1 首曲目", "1 张介质 · 2 首曲目"]);
    expect(rowOf(rows, "duration")?.cells.map((cell) => cell.lines[0])).toEqual(["12:00", "4:00", "4:00"]);
    expect(rowOf(rows, "formats")?.cells.map((cell) => cell.lines)).toEqual([["FLAC · 16 bit / 44.1 kHz"], ["FLAC · 16 bit / 44.1 kHz"], ["FLAC · 16 bit / 44.1 kHz"]]);
    expect(rowOf(rows, "formats")?.cells.map((cell) => cell.differs)).toEqual([false, false, false]);
  });

  it("第一列永远是基准：它自己不标差异，其余各列与它比较，不与相邻列比较", () => {
    const rows = comparisonRows([version(1, { year: 2000 }), version(2, { year: 2001 }), version(3, { year: 2000 })]);
    expect(rowOf(rows, "year")?.cells.map((cell) => cell.differs)).toEqual([false, true, false]);
  });

  it("所有版本都没有值的行不出现，个别版本没有值的格 lines 为空", () => {
    const empty = { edition: null, label: null, catalog_number: null, barcode: null, genres: [], source_type: null, media_type: null };
    const rows = comparisonRows([version(1, { ...empty, year: 2000 }), version(2, { ...empty, year: null }), version(3, { ...empty, year: 2000 })]);
    expect(rows.map((row) => row.key)).toEqual(["year", "content", "duration", "formats"]);
    expect(rowOf(rows, "year")?.cells.map((cell) => cell.lines)).toEqual([["2000"], [], ["2000"]]);
    // 没有值与有值也算不同；基准没有值时，有值的列才是不同的。
    expect(rowOf(rows, "year")?.cells.map((cell) => cell.differs)).toEqual([false, true, false]);
    const reversed = comparisonRows([version(1, { ...empty, year: null }), version(2, { ...empty, year: 2000 })]);
    expect(rowOf(reversed, "year")?.cells.map((cell) => cell.differs)).toEqual([false, true]);
  });

  it("只有空白的文字当作没有值；流派列表同序相同、顺序不同为不同", () => {
    const rows = comparisonRows([
      version(1, { label: "   ", genres: ["Classical", "Baroque"] }),
      version(2, { label: null, genres: ["Classical", "Baroque"] }),
      version(3, { label: null, genres: ["Baroque", "Classical"] }),
    ]);
    expect(rowOf(rows, "label")).toBeUndefined();
    expect(rowOf(rows, "genre")?.cells.map((cell) => cell.differs)).toEqual([false, false, true]);
  });

  it("标题与专辑艺人相同时不出现；有一个不同就出现，专辑艺人没有时用艺人", () => {
    const same = comparisonRows([version(1), version(2)]);
    expect(rowOf(same, "title")).toBeUndefined();
    expect(rowOf(same, "album_artist")).toBeUndefined();
    const different = comparisonRows([version(1), version(2, { title: "Another", album_artist: null, artist: "Soloist" })]);
    expect(rowOf(different, "title")?.cells.map((cell) => cell.lines[0])).toEqual([sample.releases[1]?.title, "Another"]);
    expect(rowOf(different, "album_artist")?.cells.map((cell) => cell.lines[0])).toEqual([sample.releases[1]?.album_artist, "Soloist"]);
  });

  it("总时长未知（null）整行隐藏或写成没有值，不会显示成 0:00", () => {
    const allUnknown = comparisonRows([version(1, { duration_seconds: null }), version(2, { duration_seconds: null })]);
    expect(rowOf(allUnknown, "duration")).toBeUndefined();
    const someUnknown = comparisonRows([version(1, { duration_seconds: 3661 }), version(2, { duration_seconds: null })]);
    expect(rowOf(someUnknown, "duration")?.cells.map((cell) => cell.lines)).toEqual([["1:01:01"], []]);
    expect(rowOf(someUnknown, "duration")?.cells.map((cell) => cell.differs)).toEqual([false, true]);
  });

  it("音频格式：多种格式逐项列出并注明曲目数；只有一种时不重复数量；格式不同才标差异", () => {
    const rows = comparisonRows([
      version(1, { formats: [flac(24, 96000, 10), flac(16, 44100, 2)] }),
      version(2, { formats: [flac(24, 96000, 12)] }),
      version(3, { formats: [flac(24, 96000, 10), flac(16, 44100, 2)] }),
      version(4, { formats: [] }),
    ]);
    const formats = rowOf(rows, "formats");
    expect(formats?.cells.map((cell) => cell.lines)).toEqual([
      ["FLAC · 24 bit / 96 kHz（10 首）", "FLAC · 16 bit / 44.1 kHz（2 首）"],
      ["FLAC · 24 bit / 96 kHz"],
      ["FLAC · 24 bit / 96 kHz（10 首）", "FLAC · 16 bit / 44.1 kHz（2 首）"],
      [],
    ]);
    expect(formats?.cells.map((cell) => cell.differs)).toEqual([false, true, false, true]);
  });

  it("介质与曲目的数量按千位分隔；没有人工修正的字段不标记", () => {
    const rows = comparisonRows([version(1, { medium_count: 1, track_count: 1234 }), version(2, { medium_count: 12, track_count: 5 })]);
    expect(rowOf(rows, "content")?.cells.map((cell) => cell.lines[0])).toEqual(["1 张介质 · 1,234 首曲目", "12 张介质 · 5 首曲目"]);
    expect(rows.flatMap((row) => row.cells).some((cell) => cell.corrected)).toBe(false);
  });

  it("人工修正：专辑艺人的修正标在专辑艺人行；来源类型与介质类型没有可修正字段，从不标记", () => {
    const rows = comparisonRows([
      version(1, { album_artist: "A", corrected_fields: ["album_artist"], source_type: "physical", media_type: "cd" }),
      version(2, { album_artist: "B", corrected_fields: ["source_type", "media_type"], source_type: "digital", media_type: "web" }),
    ]);
    expect(rowOf(rows, "album_artist")?.cells.map((cell) => cell.corrected)).toEqual([true, false]);
    expect(rowOf(rows, "source_type")?.cells.map((cell) => cell.corrected)).toEqual([false, false]);
    expect(rowOf(rows, "media_type")?.cells.map((cell) => cell.corrected)).toEqual([false, false]);
  });

  it("只有一个版本时没有差异", () => {
    const rows = comparisonRows([version(1)]);
    expect(rows.flatMap((row) => row.cells).some((cell) => cell.differs)).toBe(false);
  });
});
