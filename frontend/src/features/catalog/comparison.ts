import { formatDuration } from "../../shared/lib/duration";
import { formatCount } from "../../shared/lib/number";
import type { AudioFormatDTO, ComparedReleaseDTO } from "./api/decoders";
import { albumArtistOf, isFieldCorrected } from "./format";
import { codecLabel } from "./labels";

// 版本对比表的数据模型：把接口给的各版本字段整理成“字段 × 版本”的行。只做显示规则（哪些行出现、哪些格与基准不同、
// 哪些值有人工修正），不做任何质量判断：音频格式只列出编码与参数，不排名，也不称“最高音质”。

/** 采样率按千赫兹显示，去掉多余的小数：44100 写 44.1 kHz，48000 写 48 kHz，22050 写 22.05 kHz。 */
export function formatSampleRate(hertz: number): string {
  return `${Number((hertz / 1000).toFixed(3))} kHz`;
}

/** 一种音频格式的显示：“FLAC · 24 bit / 96 kHz”。没有的参数省略，编码与参数都未知时写“参数未知”。 */
export function audioFormatText(format: Pick<AudioFormatDTO, "codec" | "bit_depth" | "sample_rate">): string {
  const parameters = [format.bit_depth === null ? null : `${format.bit_depth} bit`, format.sample_rate === null ? null : formatSampleRate(format.sample_rate)]
    .filter((part) => part !== null)
    .join(" / ");
  const parts = [format.codec === null ? null : codecLabel(format.codec), parameters === "" ? null : parameters].filter((part) => part !== null);
  return parts.length === 0 ? "参数未知" : parts.join(" · ");
}

/** 一个版本的音频格式逐项列出；有多种格式时每项注明曲目数，只有一种时它就是全部曲目，不重复数量。 */
function formatLines(formats: readonly AudioFormatDTO[]): string[] {
  return formats.map((format) => (formats.length > 1 ? `${audioFormatText(format)}（${formatCount(format.track_count)} 首）` : audioFormatText(format)));
}

/** 列头与差异说明里的版本名，与版本导航一致（按接口给出的顺序，第一个是默认版本或自动选出的代表版本）。 */
export function versionLabel(index: number): string {
  return `版本 ${index + 1}`;
}

export type ComparisonCell = {
  /** 显示的内容；空数组表示这个版本没有值（界面写“—”）。多项（音频格式）逐项列出。 */
  lines: readonly string[];
  /** 与第一列（基准）的值不同；第一列自己永远是 false。 */
  differs: boolean;
  /** 这个字段有人工修正：显示的是管理员改过的值。 */
  corrected: boolean;
};

export type ComparisonRow = {
  key: string;
  label: string;
  /** 与版本一一对应，顺序相同。 */
  cells: readonly ComparisonCell[];
};

type RowSpec = {
  key: string;
  label: string;
  /** 对应的可修正字段名（与发行详情的 corrected_fields 相同），用来标出“人工修正”。 */
  field?: string;
  /** 只在各版本不同时才出现：相同时它已经是页面的标题与说明，不需要再列一行。 */
  onlyWhenDifferent?: boolean;
  lines: (release: ComparedReleaseDTO) => string[];
};

const text = (value: string | null): string[] => {
  const trimmed = value?.trim() ?? "";
  return trimmed === "" ? [] : [trimmed];
};

const ROWS: readonly RowSpec[] = [
  { key: "title", label: "标题", field: "title", onlyWhenDifferent: true, lines: (release) => [release.title] },
  { key: "album_artist", label: "专辑艺人", field: "album_artist", onlyWhenDifferent: true, lines: (release) => [albumArtistOf(release)] },
  { key: "year", label: "年份", field: "year", lines: (release) => (release.year === null ? [] : [String(release.year)]) },
  { key: "edition", label: "版本说明", field: "edition", lines: (release) => text(release.edition) },
  { key: "label", label: "厂牌", field: "label", lines: (release) => text(release.label) },
  { key: "catalog_number", label: "编号", field: "catalog_number", lines: (release) => text(release.catalog_number) },
  { key: "barcode", label: "条码", field: "barcode", lines: (release) => text(release.barcode) },
  { key: "genre", label: "流派", field: "genre", lines: (release) => text(release.genre) },
  { key: "source_type", label: "来源类型", lines: (release) => text(release.source_type) },
  { key: "media_type", label: "介质类型", lines: (release) => text(release.media_type) },
  {
    key: "content",
    label: "介质与曲目",
    lines: (release) => [`${formatCount(release.medium_count)} 张介质 · ${formatCount(release.track_count)} 首曲目`],
  },
  // 总时长只在每一首曲目的时长都已知时才有值（接口保证），所以“—”表示至少一首曲目的时长未知，而不是时长为零。
  { key: "duration", label: "总时长", lines: (release) => (release.duration_seconds === null ? [] : [formatDuration(release.duration_seconds)]) },
  { key: "formats", label: "音频格式", lines: (release) => formatLines(release.formats) },
];

/**
 * 对比表的行。所有版本都没有值的行不出现；标题与专辑艺人只在各版本不同时才出现；个别版本没有值的格 lines 为空。
 * 以第一个版本为基准：与它不同的格 differs 为真（没有值与有值也算不同）。
 */
export function comparisonRows(releases: readonly ComparedReleaseDTO[]): ComparisonRow[] {
  return ROWS.flatMap((spec) => {
    const lines = releases.map((release) => spec.lines(release));
    const keys = lines.map((entry) => entry.join("\n"));
    const baseline = keys[0];
    if (lines.every((entry) => entry.length === 0)) return [];
    if (spec.onlyWhenDifferent === true && keys.every((key) => key === baseline)) return [];
    return [
      {
        key: spec.key,
        label: spec.label,
        cells: releases.map(
          (release, index): ComparisonCell => ({
            lines: lines[index] ?? [],
            differs: index > 0 && keys[index] !== baseline,
            corrected: spec.field !== undefined && isFieldCorrected(release.corrected_fields, spec.field),
          }),
        ),
      },
    ];
  });
}
