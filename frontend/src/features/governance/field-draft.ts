import { MAX_GENRES, type CorrectableField, type CorrectionFieldDTO, type CorrectionStateDTO, type FieldValue } from "./api/decoders";
import type { CorrectionChangeBody, CorrectionChanges } from "./api/endpoints";
import { canBeEmpty, correctionFieldLabels } from "./labels";

// 编辑发行信息的草稿模型。每个字段三选一：使用扫描值（没有修正）、自定义（改成某个值）、留空（明确设为空）。
// 草稿只记录用户动过的字段（DraftEdits）：没动过的字段始终显示最新的基线，所以别人在此期间改了某个字段、基线刷新之后，
// 提交不会把我没碰过的字段静默改回去；动过的字段保留我的输入，并与最新的基线比较。

export type FieldMode = "scan" | "custom" | "empty";
/** text 是输入框里的内容；list 只有流派用：已经加成标签的流派（text 是还没加成标签的那一项）。 */
export type FieldDraft = { mode: FieldMode; text: string; list: string[] };
export type DraftEdits = Partial<Record<CorrectableField, FieldDraft>>;

/** 与后端 corrections.go 一致的文本上限（字节）。 */
const MAX_TEXT_BYTES = 4096;

/** 与后端 unicode.IsControl 相同的范围：C0 与 C1 控制字符（制表符、换行等空白类已在规范化时折叠成空格）。 */
function hasControlCharacter(text: string): boolean {
  for (const character of text) {
    const code = character.codePointAt(0) ?? 0;
    if (code <= 0x1f || (code >= 0x7f && code <= 0x9f)) return true;
  }
  return false;
}

/** 与后端一致：去掉首尾空白并把内部连续空白折叠成一个空格。 */
export function normalizeText(text: string): string {
  return text.split(/\s+/).filter((part) => part !== "").join(" ");
}

/** 值对应的输入：流派是标签列表，其余字段是输入框里的文字。 */
function inputOf(value: FieldValue | null): Pick<FieldDraft, "text" | "list"> {
  if (Array.isArray(value)) return { text: "", list: [...value] };
  return { text: value === null ? "" : String(value), list: [] };
}

/** 流派之间的分隔符：分号（半角、全角）与顿号；斜杠和逗号常是流派名的一部分（“R&B/Soul”），不拆。 */
const GENRE_SEPARATORS = /[;；、]/;

export function hasGenreSeparator(text: string): boolean {
  return GENRE_SEPARATORS.test(text);
}

/** 两个流派是不是同一个：不分大小写与全半角（与后端的合并键一致）。 */
function genreKey(genre: string): string {
  return genre.normalize("NFKC").toLowerCase();
}

/** 把 text 按分隔符拆开、去掉空项，接到 list 后面；重复的不再添加。 */
export function addGenres(list: readonly string[], text: string): string[] {
  const next = [...list];
  const keys = new Set(next.map(genreKey));
  for (const part of text.split(GENRE_SEPARATORS)) {
    const genre = normalizeText(part);
    if (genre === "" || keys.has(genreKey(genre))) continue;
    keys.add(genreKey(genre));
    next.push(genre);
  }
  return next;
}

/** 流派草稿要提交的列表：输入框里还没加成标签的内容也算。 */
function genresOf(draft: FieldDraft): string[] {
  return addGenres(draft.list, draft.text);
}

/** 字段现在的状态对应的草稿：没有修正是“使用扫描值”，设值是“自定义”，设空是“留空”。输入预填扫描值，切到“自定义”时可以直接修改。 */
export function baselineDraft(field: CorrectionFieldDTO): FieldDraft {
  const correction = field.correction;
  if (correction === null) return { mode: "scan", ...inputOf(field.scan_value) };
  if (correction.mode === "null") return { mode: "empty", ...inputOf(field.scan_value) };
  return { mode: "custom", ...inputOf(correction.value ?? null) };
}

/** 显示用的草稿：动过的字段取用户的输入，没动过的取最新的基线。 */
export function draftOf(field: CorrectionFieldDTO, edits: DraftEdits): FieldDraft {
  return edits[field.field] ?? baselineDraft(field);
}

/** 字段输入的校验错误；通过时是 null。只有“自定义”需要检查内容，“留空”只检查这个字段能不能留空。 */
export function fieldError(field: CorrectableField, draft: FieldDraft): string | null {
  const label = correctionFieldLabels[field];
  if (draft.mode === "scan") return null;
  if (draft.mode === "empty") return canBeEmpty(field) ? null : `${label}不能留空。`;
  if (field === "genre") {
    // 一个都没有时按“留空”提交，不算错误。
    const genres = genresOf(draft);
    if (genres.length > MAX_GENRES) return `最多 ${MAX_GENRES} 个。`;
    if (genres.some(hasControlCharacter)) return "含有不允许的字符。";
    if (genres.some((genre) => new TextEncoder().encode(genre).length > MAX_TEXT_BYTES)) return "内容太长了。";
    return null;
  }
  const text = normalizeText(draft.text);
  if (text === "") return canBeEmpty(field) ? "请输入内容；要清空这个字段，请选择“留空”。" : `${label}不能为空。`;
  if (field === "year") {
    return /^\d{1,4}$/.test(text) && Number(text) >= 1 ? null : "请填写有效的年份。";
  }
  if (hasControlCharacter(text)) return "含有不允许的字符。";
  if (new TextEncoder().encode(text).length > MAX_TEXT_BYTES) return "内容太长了。";
  return null;
}

/** 草稿对应的修改；相对基线没有变化时是 null（不提交）。 */
function changeOf(field: CorrectionFieldDTO, draft: FieldDraft): CorrectionChangeBody | null {
  const baseline = field.correction;
  switch (draft.mode) {
    case "scan":
      return baseline === null ? null : { mode: "clear" };
    case "empty":
      return baseline?.mode === "null" ? null : { mode: "null" };
    case "custom": {
      if (field.field === "genre") {
        const genres = genresOf(draft);
        if (genres.length === 0) return baseline?.mode === "null" ? null : { mode: "null" };
        const current = baseline?.mode === "set" && Array.isArray(baseline.value) ? baseline.value : null;
        // 按整个列表比较：顺序不同也是修改（管理员可以把主流派放在第一个）。
        return current !== null && current.length === genres.length && current.every((genre, index) => genre === genres[index]) ? null : { mode: "set", value: genres };
      }
      const text = normalizeText(draft.text);
      const value = field.field === "year" ? Number(text) : text;
      return baseline?.mode === "set" && baseline.value === value ? null : { mode: "set", value };
    }
  }
}

export type PlannedChanges = {
  /** 要提交的修改：只含动过且确有变化的字段。 */
  changes: CorrectionChanges;
  /** 要提交的字段，按接口里的固定顺序。 */
  fields: CorrectableField[];
  /** 动过的字段里输入有问题的，字段名到说明；有错误时不能提交。 */
  errors: Partial<Record<CorrectableField, string>>;
};

/** 把用户的输入整理成要提交的修改与校验错误；没动过的字段既不校验也不提交。 */
export function planChanges(state: CorrectionStateDTO, edits: DraftEdits): PlannedChanges {
  const planned: PlannedChanges = { changes: {}, fields: [], errors: {} };
  for (const field of state.fields) {
    const edit = edits[field.field];
    if (edit === undefined) continue;
    const error = fieldError(field.field, edit);
    if (error !== null) {
      planned.errors[field.field] = error;
      continue;
    }
    const change = changeOf(field, edit);
    if (change !== null) {
      planned.changes[field.field] = change;
      planned.fields.push(field.field);
    }
  }
  return planned;
}

/** “确认扫描值”：把当前扫描值存为修正（钉住它，该字段不再计入需要关注）；扫描值为空时确认的是“空”。流派的扫描值是列表，确认的就是这个列表。 */
export function confirmScanDraft(field: CorrectionFieldDTO): FieldDraft {
  return field.scan_value === null ? { mode: "empty", text: "", list: [] } : { mode: "custom", ...inputOf(field.scan_value) };
}
