import type { CorrectableField, ReviewableIssue } from "./api/decoders";

/** 可修正字段的中文名；与发行详情、证据里的同名字段一一对应。 */
export const correctionFieldLabels: Readonly<Record<CorrectableField, string>> = {
  title: "标题",
  album_artist: "专辑艺人",
  year: "年份",
  genre: "流派",
  label: "厂牌",
  catalog_number: "编号",
  edition: "版本说明",
  barcode: "条码",
};

/** 可以标记为已核对的问题的中文名：问题页、操作记录与“修改记录”共用。 */
export const reviewableIssueLabels: Readonly<Record<ReviewableIssue, string>> = {
  grouping: "归组冲突",
  missing_tracks: "缺失曲目",
  source_type: "来源类型",
  media_type: "介质类型",
};

/** 标题与专辑艺人不能留空；其余字段可以明确设为空。 */
export function canBeEmpty(field: CorrectableField): boolean {
  return field !== "title" && field !== "album_artist";
}

/** 扫描值、修正值或有效值的显示：没有值写“（空）”，年份显示为数字。 */
export function displayValue(value: string | number | null): string {
  return value === null || value === "" ? "（空）" : String(value);
}
