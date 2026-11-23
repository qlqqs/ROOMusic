import type { ClaimSummaryDTO, CorrectionIntentDTO, DefaultVersionDTO, FieldValue, OperationChangesDTO, OperationItemDTO, OperationOriginDTO } from "./api/decoders";
import { correctionFieldLabels, displayValue, reviewableIssueLabels, trackFieldLabels } from "./labels";

/**
 * 操作的来源标签：审查批准后自动执行的写“管家（审查通过）”（C7），管家确认方案后执行的写“管家（确认）”，管家直接执行的写“管家（直接执行）”，
 * MCP 令牌写“MCP”；后台自动整理写“自动整理 · 来源标识”（标识缺失时只写“自动整理”）；没有来源（页面上的操作）是 null。
 * 审查转给用户、由用户确认执行的项仍是“管家（确认）”。
 */
export function operationOriginLabel(origin: OperationOriginDTO | null | undefined): string | null {
  if (origin === null || origin === undefined) return null;
  if (origin.source === "mcp") return "MCP";
  if (origin.source === "curation") return origin.provider === null || origin.provider === undefined || origin.provider === "" ? "自动整理" : `自动整理 · ${sourceName(origin.provider)}`;
  const reviewed = origin.review_id !== null && origin.review_id !== undefined && (origin.review_decision ?? "approved") === "approved";
  if (reviewed) return "管家（审查通过）";
  return origin.plan_id === null || origin.plan_id === undefined ? "管家（直接执行）" : "管家（确认）";
}

// 外部源的显示名：已知的三个内置源写名字，其他的写标识本身（标识来自后端的声明，不是用户输入）。
const sourceNames: Record<string, string> = { musicbrainz: "MusicBrainz", coverartarchive: "Cover Art Archive", lrclib: "LRCLIB" };

/** 外部源的显示名。 */
export function sourceName(source: string): string {
  return sourceNames[source] ?? source;
}

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
    case "release_merge":
      return "合并发行";
    case "release_auto_merge":
      return "自动合并发行";
    case "release_auto_correction":
      return "自动补全发行信息";
    case "track_correction":
      return "修改曲目信息";
    case "track_auto_correction":
      return "自动补全曲目信息";
    case "release_external_artwork":
      return "自动补封面";
    case "track_external_lyrics":
      return "自动补歌词";
    case "external_link":
      return "对上外部资料";
    case "artist_merge":
      return "合并艺人";
    case "artist_split":
      return "拆开艺人";
    case "artist_rename":
      return "修改艺人主名";
    case "artist_candidate_ignore":
      return item.changes?.kind === "artist_candidate_ignore" && !item.changes.ignored ? "恢复重复艺人提示" : "忽略重复艺人提示";
    case "revert":
      if (item.changes?.kind === "release_correction") return "撤销修改";
      if (item.changes?.kind === "release_group_change") return "撤销归组调整";
      if (item.changes?.kind === "release_group_default") return "撤销默认版本调整";
      if (item.changes?.kind === "release_issue_review") return "撤销核对";
      if (item.changes?.kind === "release_claim") return "撤销认领";
      if (item.changes?.kind === "release_merge" || item.changes?.kind === "release_auto_merge") return "撤销合并";
      if (item.changes?.kind === "release_auto_correction") return "撤销自动补全";
      if (item.changes?.kind === "track_correction") return "撤销曲目修改";
      if (item.changes?.kind === "track_auto_correction") return "撤销曲目的自动补全";
      if (item.changes?.kind === "release_external_artwork") return "撤销自动补的封面";
      if (item.changes?.kind === "track_external_lyrics") return "撤销自动补的歌词";
      if (item.changes?.kind === "external_link") return "解除外部对应";
      if (item.changes?.kind === "artist_merge") return "撤销合并艺人";
      if (item.changes?.kind === "artist_rename") return "撤销修改主名";
      if (item.changes?.kind === "artist_candidate_ignore") return item.changes.ignored ? "撤销忽略提示" : "撤销恢复提示";
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
/** 一次艺人治理影响的范围：“1 个名字、12 个版本、150 首曲目”，为 0 的不写。 */
function artistScopeText(scope: { name_count: number; release_count: number; track_count: number }): string {
  return [
    scope.name_count === 0 ? null : `${scope.name_count} 个名字`,
    scope.release_count === 0 ? null : `${scope.release_count} 个版本`,
    scope.track_count === 0 ? null : `${scope.track_count} 首曲目`,
  ]
    .filter((part) => part !== null)
    .join("、");
}

/** 自动层的一个值：没有自动值写“无”，明确留空写“留空”，其余写值本身。 */
function autoText(auto: CorrectionIntentDTO | null): string {
  if (auto === null) return "无";
  return auto.mode === "null" || auto.value === undefined ? "留空" : displayValue(auto.value);
}

// 匹配依据的中文名：程序层的六种依据；以后新增的（如模型判断）按标识原样显示。
const linkMethodLabels: Record<string, string> = { tag_id: "标签里的编号", barcode: "条码", catalog_number: "厂牌编号", isrc: "ISRC", disc_id: "光盘目录", acoustid: "声学指纹", model: "模型判断" };

/** 外部发行的一行说明：《标题》— 艺人（年份 · 地区），空的部分不写。 */
function externalReleaseText(external: { title: string; artist: string; date: string; country: string }): string {
  const detail = [external.date.slice(0, 4), external.country].filter((part) => part !== "").join(" · ");
  const title = external.title === "" ? "外部发行" : `《${external.title}》`;
  return `${title}${external.artist === "" ? "" : `— ${external.artist}`}${detail === "" ? "" : `（${detail}）`}`;
}

function sameValue(a: FieldValue | null, b: FieldValue | null): boolean {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((item, index) => item === b[index]);
  return a === b;
}

function linesOf(changes: OperationChangesDTO | null, reverse: boolean): string[] {
  if (changes === null) return ["无法显示改动内容"];
  // 艺人的治理：合并写“‘甲’并入‘乙’”，拆开写拆出到谁，改名写“主名：前 → 后”（改回自动时注明）；撤销（M7 G）时反过来写。
  if (changes.kind === "artist_merge") {
    const scope = artistScopeText(changes);
    if (reverse) return [`“${changes.source.name}”从“${changes.target.name}”分回`, ...(scope === "" ? [] : [`涉及 ${scope}`])];
    return [`“${changes.source.name}”并入“${changes.target.name}”`, ...(scope === "" ? [] : [`涉及 ${scope}`])];
  }
  if (changes.kind === "artist_split") {
    const scope = artistScopeText(changes);
    const how = changes.mode === "aliases" ? "把别名拆成" : "按专辑把署名分给";
    return [`从“${changes.source.name}”${how}“${changes.target.name}”`, ...(scope === "" ? [] : [`涉及 ${scope}`])];
  }
  if (changes.kind === "artist_rename") {
    if (reverse) return [`主名：${changes.to.name} → ${changes.from.name}`];
    const note = changes.to.source === "auto" ? "（按最常见的写法）" : "";
    return [`主名：${changes.from.name} → ${changes.to.name}${note}`];
  }
  if (changes.kind === "artist_candidate_ignore") {
    const ignored = reverse ? !changes.ignored : changes.ignored;
    return [ignored ? `不再提示与“${changes.other_artist.name}”重复` : `恢复与“${changes.other_artist.name}”重复的提示`];
  }
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
  // 发行整张并入（M7 H）：来源的碟接在这个版本后面；撤销时把碟还给来源，来源恢复为单独的发行。
  if (changes.kind === "release_merge" || changes.kind === "release_auto_merge") {
    const discs = changes.medium_count === 0 ? "" : `（${changes.medium_count} 碟）`;
    return reverse ? [`《${changes.source.title}》恢复为单独的发行${discs}`] : [`把《${changes.source.title}》整张并入${discs}`];
  }
  if (changes.kind === "release_issue_review") {
    const [from, to] = reverse ? [changes.to, changes.from] : [changes.from, changes.to];
    const trackCount = (to.reviewed ? to : from).track_count;
    const label = trackCount === null ? reviewableIssueLabels[changes.issue] : `${reviewableIssueLabels[changes.issue]}（${trackCount} 首）`;
    return [`${label}：${to.reviewed ? "标记为已核对" : "取消核对"}`];
  }
  // 外部封面与外部歌词只在本地没有时补上，撤销就是去掉；本地的封面与歌词不受影响。
  if (changes.kind === "release_external_artwork") {
    const what = `${changes.artwork.width}×${changes.artwork.height} 的封面，来自 ${sourceName(changes.source)}`;
    return [reverse ? `去掉自动补的封面（${what}）` : `补了 ${what}`];
  }
  if (changes.kind === "track_external_lyrics") {
    const what = `${changes.lyrics_kind === "synced" ? "同步歌词" : "歌词"}（${changes.lines} 行），来自 ${sourceName(changes.source)}`;
    return [reverse ? `去掉给《${changes.track.title}》自动补的${what}` : `给《${changes.track.title}》补了${what}`];
  }
  // 外部对应：撤销就是去掉这些对应（专辑与曲目的对应一并去掉）。
  if (changes.kind === "external_link") {
    const source = sourceName(changes.source);
    const tracks = changes.links.filter((link) => link.object_type === "track").length;
    const artists = changes.links.filter((link) => link.object_type === "artist").length;
    const album = changes.links.some((link) => link.object_type === "release_group");
    const extra = [album ? "专辑" : null, tracks === 0 ? null : `${tracks} 首曲目`, artists === 0 ? null : `${artists} 位艺人`].filter((part) => part !== null).join("、");
    const what = `${source}：${externalReleaseText(changes.external)}`;
    if (reverse) return [`解除与 ${what} 的对应`, ...(extra === "" ? [] : [`同时去掉${extra}的对应`])];
    return [`对上 ${what}`, `依据${linkMethodLabels[changes.method] ?? changes.method}${extra === "" ? "" : `，同时对上${extra}`}`];
  }
  // 曲目字段（M7 I）：每行带曲目名，字段名是“曲目标题”“曲目艺人”。
  if (changes.kind === "track_correction") {
    return changes.fields.map((field) => {
      const [from, to] = reverse ? [field.after, field.before] : [field.before, field.after];
      const note = to.correction === null ? "（恢复为扫描值或自动补全的值）" : "";
      return `《${changes.track.title}》${trackFieldLabels[field.field]}：${displayValue(from.effective)} → ${displayValue(to.effective)}${note}`;
    });
  }
  if (changes.kind === "track_auto_correction") {
    return changes.fields.map((field) => {
      const [from, to] = reverse ? [field.after, field.before] : [field.before, field.after];
      const label = `《${changes.track.title}》${trackFieldLabels[field.field]}`;
      if (sameValue(from.effective, to.effective)) return `${label}：自动值 ${from.auto?.value ?? "（无）"} → ${to.auto?.value ?? "（无）"}（人工修正优先，显示不变）`;
      return `${label}：${displayValue(from.effective)} → ${displayValue(to.effective)}${to.auto === null ? "（取消自动补全）" : ""}`;
    });
  }
  if (changes.kind === "release_auto_correction") {
    return changes.fields.map((field) => {
      const [from, to] = reverse ? [field.after, field.before] : [field.before, field.after];
      const label = correctionFieldLabels[field.field];
      // 有人工修正时自动值不生效，显示的值没有变：这时写自动值本身的变化，并说明人工修正仍然优先。
      if (sameValue(from.effective, to.effective)) return `${label}：自动值 ${autoText(from.auto)} → ${autoText(to.auto)}（人工修正优先，显示不变）`;
      const note = to.auto === null ? "（取消自动补全）" : to.auto.mode === "null" ? "（留空）" : "";
      return `${label}：${displayValue(from.effective)} → ${displayValue(to.effective)}${note}`;
    });
  }
  return changes.fields.map((field) => {
    const [from, to] = reverse ? [field.after, field.before] : [field.before, field.after];
    const note = to.correction === null ? "（恢复为扫描值）" : to.correction.mode === "null" ? "（留空）" : "";
    return `${correctionFieldLabels[field.field]}：${displayValue(from.effective)} → ${displayValue(to.effective)}${note}`;
  });
}

/**
 * 这条记录改了什么。撤销记录的前后状态与它撤销的操作相反，所以同样的写法读起来就是“恢复成了什么”。
 * 自动补的封面、歌词、外部对应与整张并入例外：它们的撤销记录带的是被撤销的那一份（不分前后），所以按记录的类型写成“去掉”或“恢复”。
 */
export function changeLines(item: Pick<OperationItemDTO, "changes"> & Partial<Pick<OperationItemDTO, "type">>): string[] {
  const kind = item.changes?.kind;
  const removal =
    item.type === "revert" && (kind === "release_external_artwork" || kind === "track_external_lyrics" || kind === "external_link" || kind === "release_merge" || kind === "release_auto_merge");
  return linesOf(item.changes, removal);
}

/** 撤销这条记录会把什么改回去：撤销前的确认对话框用它说明影响。 */
export function revertLines(item: Pick<OperationItemDTO, "changes">): string[] {
  return linesOf(item.changes, true);
}

/** 这条记录现在为什么不能撤销（可以撤销时是 null）：列表在“撤销”按钮的位置写出原因，不让用户猜。 */
export function notRevertibleReason(item: Pick<OperationItemDTO, "type" | "revertible" | "reverted_by" | "release" | "album">): string | null {
  if (item.revertible) return null;
  if (item.type === "revert") return "已完成";
  // 拆分不可撤销（M7 G）：它就是纠正错误合并的手段，需要时再合并。
  if (item.type === "artist_split") return "需要时可以再合并";
  if (item.reverted_by !== null) return "已撤销";
  // 合并、改名、忽略候选可以撤销（M7 G）：撤不了是这位艺人之后又有修改（倒序撤销更晚的那些之后就能撤）。
  if (item.type === "artist_merge" || item.type === "artist_rename" || item.type === "artist_candidate_ignore") return "之后又有修改";
  // 自动补全的撤销不看专辑是否可见，也不受之后的人工修改影响：撤不了只可能是同一个字段又被更晚的自动整理改写了。
  if (item.type === "release_auto_correction" || item.type === "track_auto_correction") return "之后又被自动整理改写";
  // 曲目的人工修改：撤不了是这首曲目之后又被修改过，或已经不在库里。
  if (item.type === "track_correction") return "这首曲目之后又被修改过";
  // 外部封面与外部歌词同理：撤不了只可能是它已经不在了（恢复备份后图片文件丢失、被新的一张替换）。
  if (item.type === "release_external_artwork" || item.type === "track_external_lyrics") return "已被替换";
  // 外部对应从不被替换：撤不了只可能是其中某一项已经不在（被别的撤销去掉了）。
  if (item.type === "external_link") return "对应已被改动";
  // 认领的撤销不要求新版本可见（它的目的就是把修改还给旧版本），所以不写“专辑已不可见”：能不能撤销只看之后有没有修改。
  if (item.type === "release_claim") return "之后又有修改";
  // 整张并入：撤不了是被并入的版本之后又被改动，或（自动合并）之后又有人工的归组、认领或合并。
  if (item.type === "release_merge" || item.type === "release_auto_merge") return "之后又有改动";
  // 目标是发行的记录看发行，目标是专辑的记录看专辑：专辑没有可见版本了，就没有可以撤销的东西。
  const target = item.release ?? item.album;
  if (target === null || !target.visible) return "专辑已不可见";
  return "之后又有修改";
}
