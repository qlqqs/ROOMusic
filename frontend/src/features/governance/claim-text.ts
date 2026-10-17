import type { ClaimDetailDTO, ClaimEntryDTO, ClaimItemDTO, ClaimResultDTO, ClaimSuggestionDTO, CorrectionIntentDTO } from "./api/decoders";
import { correctionFieldLabels, displayValue, reviewableIssueLabels } from "./labels";

/** 列表行与详情里一句话说明旧版本留着什么，如“修正 3 项 · 专辑 · 默认版本 · 核对 1 项”；没有的类别不写。 */
export function holdsText(holds: ClaimEntryDTO["holds"]): string {
  const parts: string[] = [];
  if (holds.corrections.length > 0) parts.push(`修正 ${holds.corrections.length} 项`);
  if (holds.album) parts.push("专辑");
  if (holds.default) parts.push("默认版本");
  if (holds.reviews.length > 0) parts.push(`核对 ${holds.reviews.length} 项`);
  return parts.join(" · ");
}

/** 最后所在的位置：“扫描目录 / 相对目录”；相对目录为空（版本在扫描目录的根上，或目录不安全不返回）时只写扫描目录。 */
export function locationText(location: ClaimEntryDTO["location"]): string {
  return location.directory === "" ? location.root_name : `${location.root_name} / ${location.directory}`;
}

/** 修正字段与核对类别的中文名列表，如“标题、流派”。 */
export function correctionNames(fields: readonly (keyof typeof correctionFieldLabels)[]): string {
  return fields.map((field) => correctionFieldLabels[field]).join("、");
}

export function reviewNames(issues: readonly (keyof typeof reviewableIssueLabels)[]): string {
  return issues.map((issue) => reviewableIssueLabels[issue]).join("、");
}

/**
 * 推荐的理由：同样的文件写命中的文件数（“同样的文件（12 个）”），同一目录就写“同一目录”，两者都有用顿号连起来。
 * 理由码按字符串解码，后端新增理由时原样显示，不让页面失败。
 */
export function suggestionReasonText(suggestion: Pick<ClaimSuggestionDTO, "reasons" | "same_files">): string {
  return suggestion.reasons
    .map((reason) => {
      switch (reason) {
        case "same_files":
          return `同样的文件（${suggestion.same_files} 个）`;
        case "same_directory":
          return "同一目录";
        default:
          return reason;
      }
    })
    .join("、");
}

/**
 * 认领成功的提示：迁了什么、保留了什么（新版本已有的）、有几张专辑的默认版本改回了自动选择。什么都没迁（新版本已有的都保留了）时说明
 * 新版本已经接替了旧版本。
 */
export function claimResultText(sourceTitle: string, targetTitle: string, result: ClaimResultDTO): string {
  const part = (summary: ClaimResultDTO["moved"]): string | null => {
    const parts = [
      summary.corrections.length === 0 ? null : `修正 ${correctionNames(summary.corrections)}`,
      summary.album ? "专辑" : null,
      summary.reviews.length === 0 ? null : `核对 ${reviewNames(summary.reviews)}`,
    ].filter((item) => item !== null);
    return parts.length === 0 ? null : parts.join("；");
  };
  const moved = [part(result.moved), result.defaults.moved.length === 0 ? null : "默认版本"].filter((item) => item !== null).join("；");
  const kept = part(result.skipped);
  const sentences = [
    moved === ""
      ? `《${targetTitle}》已经接替了《${sourceTitle}》：没有迁移任何修改，它已有的都保留了。`
      : `已把《${sourceTitle}》上的修改认领到《${targetTitle}》：${moved}。重新扫描不会冲掉它们。`,
    kept === null || moved === "" ? null : `保留了这个版本已有的：${kept}。`,
    result.defaults.reset.length === 0 ? null : `有 ${result.defaults.reset.length} 张专辑的默认版本改回了自动选择（旧版本不可见，显示不变）。`,
  ];
  return sentences.filter((item) => item !== null).join("");
}

/** 修正意图的说明：改成某个值，或明确设为空。 */
export function intentText(intent: CorrectionIntentDTO): string {
  return intent.mode === "null" ? "设为空" : `改成“${displayValue(intent.value ?? null)}”`;
}

/** 预览里一项的去向的短标签：会迁移、保留，还是改回自动选择。用文字表达，不只靠颜色。 */
export function claimActionLabel(item: Pick<ClaimItemDTO, "action">): string {
  switch (item.action) {
    case "move":
      return "迁移";
    case "skip":
      return "保留";
    case "reset":
      return "改回自动选择";
  }
}

/** 一张专辑的标题：先在旧版本详情里找（原来所在的专辑、作为默认版本的专辑），找不到就泛称。 */
function albumTitleIn(detail: Pick<ClaimDetailDTO, "album" | "default_of">, groupId: string | undefined): string {
  if (groupId !== undefined) {
    if (detail.album?.id === groupId) return `《${detail.album.title}》`;
    const named = detail.default_of.find((album) => album.id === groupId);
    if (named !== undefined) return `《${named.title}》`;
  }
  return "那张专辑";
}

/** 迁移的核对在新版本上的效果。 */
function reviewEffectText(item: ClaimItemDTO): string {
  if (item.effect === "valid") return "这个版本现在有同样的问题，依据相同，核对立即生效。";
  if (item.effect === "stale") return "这个版本现在有同类问题，但依据不同，核对不生效，问题会重新提醒。";
  if (item.effect === "no_issue") return "这个版本现在没有这类问题，核对暂时无事可压；将来出现依据相同的问题时才生效。";
  return "";
}

/** 跳过的原因。 */
function skipReasonText(item: ClaimItemDTO, albumTitle: string): string {
  switch (item.reason) {
    case "target_has_correction":
      return `这个版本已经有自己的修正（${item.to === undefined ? "" : intentText(item.to)}），以它为准；旧版本的不迁移，撤销认领可以恢复。`;
    case "target_has_review":
      return "这个版本已经核对过这一类问题，以它为准；旧版本的不迁移，撤销认领可以恢复。";
    case "target_in_album":
      return `这个版本已经在另一张有多个版本的专辑里，不再归入${albumTitle}；之后可以在专辑页手动归入。`;
    case "already_in_album":
      return `这个版本已经在${albumTitle}里。`;
    case "not_transferable":
      return "缺失曲目的核对针对旧版本自己的那几首曲目，不迁移。";
    default:
      return item.reason ?? "";
  }
}

/** 预览里一项的说明：标题（哪一类）与一句话（会怎样）。detail 用来给专辑项补上专辑的名字。 */
export function claimItemText(item: ClaimItemDTO, detail: Pick<ClaimDetailDTO, "album" | "default_of">): { heading: string; body: string } {
  const albumTitle = albumTitleIn(detail, item.group);
  switch (item.kind) {
    case "correction": {
      const name = item.field === undefined ? "字段" : correctionFieldLabels[item.field];
      const old = item.from === undefined ? "" : intentText(item.from);
      return item.action === "move"
        ? { heading: `修正 · ${name}`, body: `旧版本的“${name}”${old}，迁到这个版本。` }
        : { heading: `修正 · ${name}`, body: `${skipReasonText(item, albumTitle)}旧版本的“${name}”${old}。` };
    }
    case "album":
      return item.action === "move"
        ? { heading: "所在的专辑", body: `这个版本会归入旧版本原来的专辑${albumTitle}，旧版本离开这张专辑。` }
        : { heading: "所在的专辑", body: `${skipReasonText(item, albumTitle)}旧版本会离开${albumTitle}。` };
    case "default":
      return item.action === "move"
        ? { heading: "默认版本", body: `这个版本接任${albumTitle}的默认版本。` }
        : { heading: "默认版本", body: `这个版本不会进入${albumTitle}，所以它的默认版本改回自动选择（旧版本已不可见，页面上的显示不变）。` };
    case "review": {
      const name = item.issue === undefined ? "问题" : reviewableIssueLabels[item.issue];
      return item.action === "move"
        ? { heading: `核对 · ${name}`, body: `旧版本上的核对（连同核对人与时间）迁到这个版本。${reviewEffectText(item)}` }
        : { heading: `核对 · ${name}`, body: skipReasonText(item, albumTitle) };
    }
  }
}
