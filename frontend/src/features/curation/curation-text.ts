import { correctionFieldLabels, displayValue, sourceName, type CorrectableField } from "../governance";
import type { CurationSourceDTO, ProgramState, SourceCapability, SuppressionDTO } from "./api/decoders";

/** 后台状态的一句话。 */
export const programStateText: Record<ProgramState, string> = {
  running: "正在整理",
  waiting_for_scan: "等扫描结束后继续",
  idle: "已处理完，等新的内容",
  paused: "已暂停",
  disabled: "未打开",
  unavailable: "后台整理没有运行",
};

const capabilityText: Record<SourceCapability, string> = {
  release_lookup: "发行",
  recording_lookup: "录音",
  artist_lookup: "艺人",
  cover_art: "封面",
  lyrics: "歌词",
};

/** 一个源能提供什么，如“发行、录音、艺人”。 */
export function capabilitiesText(source: CurationSourceDTO): string {
  return source.capabilities.map((capability) => capabilityText[capability]).join("、");
}

/** 打开总开关前列出：每个开着的源会收到什么（ADR 0006 第 8 节）。 */
export const sentToSource: Record<string, string> = {
  musicbrainz: "外部编号、条码、ISRC、厂牌与编号、标题与艺人的查询词、由曲目时长推出的光盘目录",
  coverartarchive: "外部编号",
  lrclib: "曲目标题、艺人、专辑名、时长",
};

/** 范围撤销里跳过的原因；未知的码给兜底文案，临时性失败提示可以再试。 */
export function skippedReason(code: string): string {
  switch (code) {
    case "revert_conflict":
      return "之后又被改动";
    case "already_reverted":
      return "已经撤销";
    case "not_revertible":
      return "不能撤销";
    case "not_found":
      return "目标已不存在";
    case "database_unavailable":
    case "operation_failed":
      return "暂时没能撤销，可以再试一次";
    default:
      return "没能撤销";
  }
}

/** 范围撤销的跳过项按原因归类计数，如“之后又被改动 2 条”。 */
export function skippedSummary(skipped: readonly { code: string }[]): string[] {
  const counts = new Map<string, number>();
  for (const item of skipped) {
    const reason = skippedReason(item.code);
    counts.set(reason, (counts.get(reason) ?? 0) + 1);
  }
  return [...counts].map(([reason, n]) => `${reason} ${n} 条`);
}

const targetText: Record<SuppressionDTO["target_type"], string> = { release: "发行", track: "曲目", release_group: "专辑" };

/** 标记的对象类型。 */
export function suppressionTargetText(item: SuppressionDTO): string {
  return targetText[item.target_type];
}

const subjectText: Record<string, string> = {
  artwork: "封面",
  lyrics: "歌词",
  external_link: "外部对应",
  group_join: "归组",
};

/** 标记的类别：字段名，或封面、歌词、外部对应、归组。 */
export function suppressionSubjectText(item: SuppressionDTO): string {
  if (item.subject in correctionFieldLabels) return correctionFieldLabels[item.subject as CorrectableField];
  return subjectText[item.subject] ?? item.subject;
}

/** 列表里隐藏成对标记里按外部编号的那一半（封面、歌词各写两条，清除时一起清除）。 */
export function isPairedIdentityMarker(item: SuppressionDTO): boolean {
  return item.subject === "artwork_source" || item.subject === "lyrics_source";
}

/** 标记的值（不可信文本）：字段的意图写值或“留空”，归组写“归入另一张专辑”，其余写来源与外部编号。 */
export function suppressionValueText(item: SuppressionDTO): string {
  const value = item.value;
  if (item.subject === "group_join") return "归入另一张专辑";
  if (item.subject in correctionFieldLabels) {
    if (value["mode"] === "null") return "留空";
    const raw = value["value"];
    if (typeof raw === "string" || typeof raw === "number") return displayValue(raw);
    if (Array.isArray(raw) && raw.every((part) => typeof part === "string")) return displayValue(raw as string[]);
    return "—";
  }
  const source = typeof value["source"] === "string" ? sourceName(value["source"]) : null;
  const external = typeof value["external_id"] === "string" ? value["external_id"] : null;
  return [source, external].filter((part) => part !== null).join(" · ") || "—";
}
