import type { ClaimEntryDTO, ClaimSuggestionDTO } from "./api/decoders";
import { correctionFieldLabels, reviewableIssueLabels } from "./labels";

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
