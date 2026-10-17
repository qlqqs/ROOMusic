import type { ClaimSummaryDTO, DefaultVersionDTO, OperationChangesDTO, OperationItemDTO } from "./api/decoders";
import { correctionFieldLabels, displayValue, reviewableIssueLabels } from "./labels";

/** 记录自己的类型：修正、归组、默认版本、核对与认领按它改的东西命名；撤销记录写“撤销”，并说明撤销的是哪一类（记录坏了读不出改动时只写“撤销”）。 */
export function operationTypeLabel(item: Pick<OperationItemDTO, "type" | "changes">): string {
  switch (item.type) {
    case "release_correction":
      return "修改发行信息";
    case "release_group_change":
      return "调整归组";
    case "release_group_default":
      return "设定默认版本";
    case "release_issue_review":
      return "核对问题";
    case "release_claim":
      return "认领";
    case "revert":
      if (item.changes?.kind === "release_correction") return "撤销修改";
      if (item.changes?.kind === "release_group_change") return "撤销归组调整";
      if (item.changes?.kind === "release_group_default") return "撤销默认版本调整";
      if (item.changes?.kind === "release_issue_review") return "撤销核对";
      if (item.changes?.kind === "release_claim") return "撤销认领";
      return "撤销";
  }
}

/** 默认版本的显示：没有指定是“自动选择”；指定的版本写《标题》，年份与版本说明有才写，如“《Sample》（2015 · Remaster）”。 */
export function defaultVersionText(version: DefaultVersionDTO | null): string {
  if (version === null) return "自动选择";
  const detail = [version.year === null ? null : String(version.year), version.edition].filter((part) => part !== null).join(" · ");
  return detail === "" ? `《${version.title}》` : `《${version.title}》（${detail}）`;
}

/** 认领迁移了（或保留了）哪几类：修正写字段名，专辑、默认版本与核对写类别，如“修正 标题、流派；专辑；默认版本；核对 来源类型”。没有内容时是 null。 */
function claimPartsText(summary: ClaimSummaryDTO): string | null {
  const parts = [
    summary.corrections.length === 0 ? null : `修正 ${summary.corrections.map((field) => correctionFieldLabels[field]).join("、")}`,
    summary.album ? "专辑" : null,
    summary.defaults === 0 ? null : summary.defaults === 1 ? "默认版本" : `默认版本（${summary.defaults} 张专辑）`,
    summary.reviews.length === 0 ? null : `核对 ${summary.reviews.map((issue) => reviewableIssueLabels[issue]).join("、")}`,
  ].filter((part) => part !== null);
  return parts.length === 0 ? null : parts.join("；");
}

/**
 * 改动逐条一行。字段修正写“字段：前 → 后”（取当时的有效值），恢复扫描值与留空另加说明；归组写专辑的移动（标题是当时用户看到的专辑名）；
 * 默认版本写“默认版本：自动选择 → 《标题》（年份 · 版本说明）”（版本取当时的快照）；核对写“归组冲突：标记为已核对”或“…：取消核对”，
 * 缺失曲目带上核对时的曲目数，如“缺失曲目（3 首）：标记为已核对”。
 * reverse 为 true 时前后互换，写出“撤销之后会变成什么”：撤销前的预览用它，读起来是“改回去”。
 * 改动内容不可读（记录里的状态损坏）时只说明这一点，不让列表失败。
 */
function linesOf(changes: OperationChangesDTO | null, reverse: boolean): string[] {
  if (changes === null) return ["无法显示改动内容"];
  if (changes.kind === "release_group_change") {
    return reverse ? [`从《${changes.to.group_title}》移回《${changes.from.group_title}》`] : [`从《${changes.from.group_title}》移到《${changes.to.group_title}》`];
  }
  if (changes.kind === "release_group_default") {
    const [from, to] = reverse ? [changes.to, changes.from] : [changes.from, changes.to];
    return [`默认版本：${defaultVersionText(from.default)} → ${defaultVersionText(to.default)}`];
  }
  if (changes.kind === "release_claim") {
    const source = `《${changes.source.title}》${changes.source.visible ? "" : "（已不可见）"}`;
    // 撤销记录（没有 moved 与 skipped），以及撤销认领之前的确认（reverse），都只能说修改回到了旧版本。
    if (reverse || changes.moved === undefined || changes.skipped === undefined) return [`把迁走的修改还给${source}`];
    const moved = claimPartsText(changes.moved);
    const kept = claimPartsText(changes.skipped);
    return [
      moved === null ? `从${source}认领：没有迁移任何修改` : `从${source}认领：${moved}`,
      ...(kept === null ? [] : [`保留这个版本已有的：${kept}`]),
    ];
  }
  if (changes.kind === "release_issue_review") {
    const [from, to] = reverse ? [changes.to, changes.from] : [changes.from, changes.to];
    const trackCount = (to.reviewed ? to : from).track_count;
    const label = trackCount === null ? reviewableIssueLabels[changes.issue] : `${reviewableIssueLabels[changes.issue]}（${trackCount} 首）`;
    return [`${label}：${to.reviewed ? "标记为已核对" : "取消核对"}`];
  }
  return changes.fields.map((field) => {
    const [from, to] = reverse ? [field.after, field.before] : [field.before, field.after];
    const note = to.correction === null ? "（恢复为扫描值）" : to.correction.mode === "null" ? "（留空）" : "";
    return `${correctionFieldLabels[field.field]}：${displayValue(from.effective)} → ${displayValue(to.effective)}${note}`;
  });
}

/** 这条记录改了什么。撤销记录的前后状态与它撤销的操作相反，所以同样的写法读起来就是“恢复成了什么”。 */
export function changeLines(item: Pick<OperationItemDTO, "changes">): string[] {
  return linesOf(item.changes, false);
}

/** 撤销这条记录会把什么改回去：撤销前的确认对话框用它说明影响。 */
export function revertLines(item: Pick<OperationItemDTO, "changes">): string[] {
  return linesOf(item.changes, true);
}

/** 这条记录现在为什么不能撤销（可以撤销时是 null）：列表在“撤销”按钮的位置写出原因，不让用户猜。 */
export function notRevertibleReason(item: Pick<OperationItemDTO, "type" | "revertible" | "reverted_by" | "release" | "album">): string | null {
  if (item.revertible) return null;
  if (item.type === "revert") return "撤销不能再撤销";
  if (item.reverted_by !== null) return "已撤销";
  // 认领的撤销不要求新版本可见（它的目的就是把修改还给旧版本），所以不写“专辑已不可见”：能不能撤销只看之后有没有修改。
  if (item.type === "release_claim") return "之后又有修改";
  // 目标是发行的记录看发行，目标是专辑的记录看专辑：专辑没有可见版本了，就没有可以撤销的东西。
  const target = item.release ?? item.album;
  if (target === null || !target.visible) return "专辑已不可见";
  return "之后又有修改";
}
