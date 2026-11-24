import { correctionFieldLabels, displayValue, sourceName, trackFieldLabels, type CorrectableField } from "../governance";
import { formatCount } from "../../shared/lib/number";
import type {
  CurationExceptionDTO,
  CurationSourceDTO,
  JsonValue,
  ModelState,
  ModelUnavailableReason,
  ProgramState,
  SourceCapability,
  SourceInactiveReason,
  SuppressionDTO,
} from "./api/decoders";

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
  fingerprint_lookup: "声学指纹",
  artist_image: "艺人图片",
  artist_bio: "艺人简介",
};

/** 一个源能提供什么，如“发行、录音、艺人”。 */
export function capabilitiesText(source: CurationSourceDTO): string {
  return source.capabilities.map((capability) => capabilityText[capability]).join("、");
}

/**
 * 各行自己的不生效原因（总开关关着、开启者不可用写在分组层面，这里返回 null）。
 * 指纹工具、内置应用密钥与密钥几条只对 AcoustID 出现。
 */
export function sourceInactiveText(reason: SourceInactiveReason | null): string | null {
  switch (reason) {
    case "source_disabled":
      return "已关闭，后台不使用它。";
    case "fingerprint_unavailable":
      return "这台服务器缺少指纹工具（fpcalc），暂时不能使用。";
    case "bundled_key_rejected":
      return "ROOMusic 内置的应用密钥被 AcoustID 拒绝了，升级到新版本后恢复。";
    case "key_missing":
      return "填入密钥后可用。";
    case "key_invalid":
      return "密钥无效，请重新填写。";
    case "search_unconfigured":
      return "打开自动整理的管理员还没有在管家设置里选搜索来源，暂时不能使用。";
    default:
      return null;
  }
}

/**
 * 只用来识别、自己不写入任何内容的源（AcoustID 的指纹、Wikidata 的艺人条目）：经它对上的结果记在别的源名下，
 * 没有“来自它的改动”可看或可撤。
 */
export function writesOwnChanges(source: CurationSourceDTO): boolean {
  return source.capabilities.some((capability) => capability !== "fingerprint_lookup" && capability !== "artist_lookup");
}

/** 插件源（标识 plugin:<id>，M8）。 */
export function isPluginSource(source: Pick<CurationSourceDTO, "id">): boolean {
  return source.id.startsWith("plugin:");
}

/** 一个源会收到什么：内置源按下表；插件源写出它会访问的地址（来自它的清单），查询内容由插件决定。 */
export function sentToSourceText(source: Pick<CurationSourceDTO, "id" | "upstreams">): string {
  if (isPluginSource(source)) return `插件决定的查询内容，只会发往 ${(source.upstreams ?? []).join("、") || "它声明的地址"}`;
  return sentToSource[source.id] ?? "查询需要的信息";
}

/** 打开总开关前列出：每个开着的源会收到什么（ADR 0006 第 8 节）。 */
export const sentToSource: Record<string, string> = {
  web_search: "没对上外部条目的艺人的名字、别名与若干专辑名、曲目名（经管家设置的对话模型与搜索来源）",
  musicbrainz: "外部编号、条码、ISRC、厂牌与编号、标题与艺人的查询词、由曲目时长推出的光盘目录",
  coverartarchive: "外部编号",
  lrclib: "曲目标题、艺人、专辑名、时长",
  acoustid: "由音频算出的声学指纹（不能还原成音频）与时长，以及应用密钥与你填入的用户密钥",
  wikidata: "艺人的 MusicBrainz 编号与 Wikidata 条目编号",
  commons: "Wikidata 给出的图片文件名",
  wikipedia: "维基百科的条目名",
  fanarttv: "艺人的 MusicBrainz 编号，以及项目密钥与你填入的个人密钥",
  theaudiodb: "艺人的 MusicBrainz 编号与你填入的密钥",
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

const targetText: Record<SuppressionDTO["target_type"], string> = { release: "发行", track: "曲目", release_group: "专辑", artist: "艺人" };

/** 标记的对象类型。 */
export function suppressionTargetText(item: SuppressionDTO): string {
  return targetText[item.target_type];
}

const subjectText: Record<string, string> = {
  artwork: "封面",
  lyrics: "歌词",
  external_link: "外部对应",
  group_join: "归组",
  // 自动合并的两位艺人（M7 G）：撤销之后不再自动合并。
  artist_merge: "自动合并艺人",
  // 自动合并的两个发行（M7 H）：撤销或不采纳之后不再自动提出合并它们。
  release_merge: "自动合并发行",
};

/** 标记的类别：字段名，或封面、歌词、外部对应、归组；曲目上的标题与艺人（M7 I）写“曲目标题”“曲目艺人”。 */
export function suppressionSubjectText(item: SuppressionDTO): string {
  if (item.target_type === "track" && (item.subject === "title" || item.subject === "artist")) return trackFieldLabels[item.subject];
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
  if (item.subject === "artist_merge") return typeof value["name"] === "string" ? `与“${value["name"]}”合并` : "与另一位艺人合并";
  if (item.subject === "release_merge") return typeof value["title"] === "string" ? `与《${value["title"]}》合并` : "与另一个发行合并";
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

// ---- 模型判断（M7 D）----

/** 模型层状态的一句话。 */
export const modelStateText: Record<ModelState, string> = {
  idle: "空闲，等程序拿不准的项",
  running: "正在判断",
  waiting_review: "等审查恢复后重新审查",
  paused: "已暂停",
  disabled: "未打开",
  waiting_for_scan: "等扫描结束后继续",
  limit_reached: "今天的用量到了上限，明天继续",
  unavailable: "暂时不能判断",
};

/** 模型层不可用的原因；mine 表示当前管理员就是开启者（原因说的是他自己的设置）。 */
export function modelUnavailableText(reason: ModelUnavailableReason, mine: boolean): string {
  switch (reason) {
    case "not_configured":
      return mine ? "你还没有配置管家的模型接口。" : "开启者还没有配置管家的模型接口。";
    case "key_needs_reentry":
      return mine ? "模型接口的密钥需要重新填写。" : "开启者的模型接口密钥需要重新填写。";
    case "tool_unsupported":
      return mine ? "当前模型不支持工具调用，换一个支持的模型。" : "开启者配置的模型不支持工具调用。";
    case "enabler_unavailable":
      return "开启者已经不是启用的管理员，重新打开总开关后由你接手。";
    case "source_disabled":
      return "MusicBrainz 已关闭，模型判断要用它查资料。";
    case "provider_error":
      return "模型接口最近出错了，过一会儿自动再试。";
  }
}

/** 需要开启者去 Agent 设置里处理的原因。 */
export function needsAgentSettings(reason: ModelUnavailableReason | null): boolean {
  return reason === "not_configured" || reason === "key_needs_reentry" || reason === "tool_unsupported";
}

/** token 数：整万写“n 万”，否则写原数。 */
export function tokenText(tokens: number): string {
  return tokens % 10_000 === 0 && tokens >= 10_000 ? `${formatCount(tokens / 10_000)} 万` : formatCount(tokens);
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const WIKIDATA_ID = /^Q[1-9][0-9]{0,11}$/;

/**
 * 例外里匹配的外部条目：来源与种类的说明（“MusicBrainz 发行”“Wikidata 条目”）、编号与它的地址（编号格式不对时没有地址）；外部编号是不可信文本。
 */
export function exceptionExternal(exception: CurationExceptionDTO): { label: string; id: string; url: string | null } | null {
  const raw = exception.input["external_id"];
  if (typeof raw !== "string") return null;
  if (exception.tool === "resolve_artist_identity") {
    if (exception.input["source"] === "wikidata") return { label: "Wikidata 条目", id: raw, url: WIKIDATA_ID.test(raw) ? `https://www.wikidata.org/wiki/${raw}` : null };
    return { label: "MusicBrainz 艺人", id: raw, url: UUID.test(raw) ? `https://musicbrainz.org/artist/${raw.toLowerCase()}` : null };
  }
  if (exception.tool !== "resolve_release_match") return null;
  const kind = exception.kind === "recording_match" ? "recording" : "release";
  return { label: exception.kind === "recording_match" ? "MusicBrainz 录音" : "MusicBrainz 发行", id: raw, url: UUID.test(raw) ? `https://musicbrainz.org/${kind}/${raw.toLowerCase()}` : null };
}

function fieldValueText(value: JsonValue): string {
  if (value === null || typeof value === "string" || typeof value === "number") return displayValue(value);
  if (Array.isArray(value) && value.every((part) => typeof part === "string")) return displayValue(value as string[]);
  return "—";
}

function countOf(value: JsonValue): number {
  return typeof value === "number" ? value : 0;
}

/** 预览里发行的快照 {id,title,group_id} 的标题；读不出来时是 null。 */
function releaseTitleOf(value: JsonValue): string | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const title = (value as { readonly [key: string]: JsonValue })["title"];
  return typeof title === "string" ? title : null;
}

/** 并入时合并期间不生效的来源内容（hidden 的各项计数）是否有任何一项。 */
function hasHidden(value: JsonValue): boolean {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  return Object.values(value).some((part) => part === true || (typeof part === "number" && part > 0));
}

/**
 * 例外提出的修改，逐行：字段写“标题：A → B”；匹配另有外部对应（新建或替换）、曲目对应数、专辑对应与归组。
 * 外部对应那一行由调用方带链接显示，这里只给其余各行。值都是不可信文本。
 */
export function exceptionChangeLines(exception: CurationExceptionDTO): string[] {
  const lines: string[] = [];
  for (const change of exception.preview.changes) {
    switch (change.field) {
      case "external_link":
        break;
      case "track_links":
        lines.push(`曲目对应：${formatCount(countOf(change.before))} → ${formatCount(countOf(change.after))} 首`);
        break;
      case "group_external_link":
        lines.push("建立专辑的外部对应");
        break;
      case "group":
        lines.push("归入已有的专辑");
        break;
      // 整张并入（M7 H）：before 是来源，after 是目标；令牌不显示。
      case "release_merge":
        lines.push(`把《${releaseTitleOf(change.before) ?? "另一个发行"}》整张并入《${releaseTitleOf(change.after) ?? "这个发行"}》`);
        break;
      case "merge_token":
        break;
      case "favorites_moved":
        if (countOf(change.after) > 0) lines.push(`${formatCount(countOf(change.after))} 个专辑收藏转到合并后的专辑`);
        break;
      case "hidden":
        if (hasHidden(change.after)) lines.push("被并入的发行上的修正等内容在合并期间不生效，撤销之后恢复");
        break;
      default: {
        const label = exception.tool === "correct_track" ? trackFieldLabel(change.field, exception.track?.title) : change.field in correctionFieldLabels ? correctionFieldLabels[change.field as CorrectableField] : change.field;
        lines.push(`${label}：${fieldValueText(change.before)} → ${fieldValueText(change.after)}`);
      }
    }
  }
  return lines;
}

/** 曲目字段的名字，带曲目名：“《Intro》曲目标题”。 */
function trackFieldLabel(field: string, title: string | undefined): string {
  const label = field === "title" || field === "artist" ? trackFieldLabels[field] : field;
  return title === undefined ? label : `《${title}》${label}`;
}

/** 匹配的外部对应是新建还是替换现有的。 */
export function exceptionLinkVerb(exception: CurationExceptionDTO): string {
  const change = exception.preview.changes.find((item) => item.field === "external_link");
  return change !== undefined && change.before !== null && change.before !== "" ? "改为对应" : "对应到";
}

/** 审查的结论：没通过；转给你确认（按规则转的另写明）。 */
export function exceptionReviewText(exception: CurationExceptionDTO): string {
  if (exception.review.decision === "rejected") return "审查没通过";
  return exception.review.source === "policy" ? "按规则转给你确认" : "审查转给你确认";
}

export const riskText: Record<"low" | "medium" | "high", string> = { low: "低", medium: "中", high: "高" };

/** 采纳与不采纳的错误文案（其余按通用文案）。 */
export const EXCEPTION_MESSAGES: Readonly<Record<string, string>> = {
  exception_stale: "提出之后内容变了，这一项没有执行。可以查看过程，或等模型重新判断。",
  exception_decided: "这一项已经被处理了。",
  not_found: "这一项已经被处理了。",
  curation_disabled: "自动整理关着、暂停中或正在扫描，现在不能采纳匹配。",
  source_disabled: "MusicBrainz 已关闭，现在不能采纳匹配。",
  nothing_to_apply: "要写的内容都已经在了，或被标记为不再提出。",
  source_unavailable: "外部资料暂时取不到，没有完成。可以稍后在这里重试，已写入的部分会接着做完。",
};
