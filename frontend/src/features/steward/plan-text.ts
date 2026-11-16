import type { PlanBasisDTO } from "./api/decoders";
import { releaseFieldLabels } from "./tool-labels";

// 方案卡片的文字：字段名、旧值与新值的显示、依据的来源。字段键与值来自后端的预览（各工具的 Preview），值来自曲库，是不可信文本，只按文本节点渲染。

const fieldLabels: Readonly<Record<string, string>> = {
  ...releaseFieldLabels,
  default_release_id: "默认版本",
  group: "所在专辑",
  grouping: "归组冲突",
  missing_tracks: "缺失曲目",
  source_type: "来源类型",
  media_type: "介质类型",
  name: "主名",
  artist: "艺人",
  correction: "人工修正",
  group_default: "默认版本",
  issue_review: "问题核对",
};

/** 字段键的中文名：认领的键是“类别.字段”（取字段部分），忽略候选的键以 ignored: 开头；不认识的键原样显示。 */
export function planFieldLabel(field: string): string {
  if (field.startsWith("ignored:")) return "重复艺人提示";
  const known = fieldLabels[field];
  if (known !== undefined) return known;
  const dot = field.lastIndexOf(".");
  return dot < 0 ? field : (fieldLabels[field.slice(dot + 1)] ?? field);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** 旧值或新值的显示：没有值写“（空）”，列表（流派）用“ · ”连接，对象取它的标题或名字，布尔值写“是/否”。 */
export function planValueText(value: unknown): string {
  if (value === null || value === undefined || value === "") return "（空）";
  if (typeof value === "string") return value;
  if (typeof value === "number") return String(value);
  if (typeof value === "boolean") return value ? "是" : "否";
  if (Array.isArray(value)) return value.length === 0 ? "（空）" : value.map(planValueText).join(" · ");
  if (isRecord(value)) {
    for (const key of ["title", "name"]) {
      const text = value[key];
      if (typeof text === "string" && text !== "") return text;
    }
  }
  return JSON.stringify(value);
}

const basisSources: Readonly<Record<PlanBasisDTO["source"], string>> = {
  local: "本地证据",
  external: "外部资料",
  mixed: "本地证据与外部资料",
};

/** 依据的来源：本地证据、外部资料或两者都有（ADR 0005 4.3）。 */
export function basisSourceLabel(source: PlanBasisDTO["source"]): string {
  return basisSources[source];
}
