import { decode as d } from "../../../shared/api";
// 直接取 catalog 的解码器模块而不是公开入口：公开入口连带导入组件与 CSS，Playwright（Node）里的受控后端导入这个文件时加载不了。
import { releaseSummaryDecoder } from "../../catalog/api/decoders";
import { artistRoles, artistSummaryDecoder } from "../../catalog/api/artist-summary";

// 发行字段修正（M3）的响应解码。可修正的字段与后端 backend/internal/catalog/corrections.go 的 correctableFields 一致，
// 顺序也是接口返回的顺序。后端新增可修正字段时，样本契约测试先失败，在这里补上才算确认前端已经知道。
export const CORRECTABLE_FIELDS = ["title", "album_artist", "year", "genre", "label", "catalog_number", "edition", "barcode"] as const;
export type CorrectableField = (typeof CORRECTABLE_FIELDS)[number];

const MAX_FIELD_TEXT = 4096; // 修正值的字节上限（后端 maxCorrectionTextBytes）；扫描值同样有界

// 年份是 1 到 9999 的整数，其余字段是字符串；按值的运行时类型选择解码器，字段与类型的对应由后端保证。
const fieldValueDecoder: d.Decoder<string | number> = (input, path) =>
  typeof input === "number" ? d.integer({ min: 1, max: 9999 })(input, path) : d.string({ max: MAX_FIELD_TEXT })(input, path);

// 当前的修正意图：set 带值，null 表示明确设为空（没有 value）。没有修正时整个 correction 为 null。
const correctionDecoder = d.object({
  mode: d.oneOf(["set", "null"] as const),
  value: d.optional(fieldValueDecoder),
});

const correctionFieldDecoder = d.object({
  field: d.oneOf(CORRECTABLE_FIELDS),
  scan_value: d.nullable(fieldValueDecoder),
  correction: d.nullable(correctionDecoder),
  effective_value: d.nullable(fieldValueDecoder),
  uncertain: d.boolean(),
});

const correctionStateShape = {
  release_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  fields: d.array(correctionFieldDecoder, { max: CORRECTABLE_FIELDS.length }),
};

const correctionStateDecoder = d.object(correctionStateShape);
// 修正成功的响应：与读取相同的状态，加上这次操作的 ID（撤销用）。
const correctionResultDecoder = d.object({ ...correctionStateShape, operation_id: d.uuid() });

// 人工归组成功的响应：发行现在所在的组、原来的组、新的人工修改版本号与这次操作的 ID（撤销用）。
const groupChangeResultDecoder = d.object({
  release_id: d.uuid(),
  group_id: d.uuid(),
  previous_group_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  operation_id: d.uuid(),
});

// 设定默认版本成功的响应：专辑、设定之后的默认版本（改回自动选择时为 null）、专辑新的人工修改版本号与这次操作的 ID（撤销用）。
const groupDefaultResultDecoder = d.object({
  group_id: d.uuid(),
  default_release_id: d.nullable(d.uuid()),
  revision: d.integer({ min: 1 }),
  operation_id: d.uuid(),
});

// 核对问题（M4 第二项）：可以标记为已核对的四类问题，与后端 backend/internal/catalog/issue_review.go 的取值一致。
export const REVIEWABLE_ISSUES = ["grouping", "missing_tracks", "source_type", "media_type"] as const;
export type ReviewableIssue = (typeof REVIEWABLE_ISSUES)[number];

// 核对或取消核对成功的响应：发行、问题、现在是否有核对、新的人工修改版本号与这次操作的 ID（撤销用）。
const issueReviewResultDecoder = d.object({
  release_id: d.uuid(),
  issue: d.oneOf(REVIEWABLE_ISSUES),
  reviewed: d.boolean(),
  revision: d.integer({ min: 1 }),
  operation_id: d.uuid(),
});

// 问题列表与版本的问题（管理员）。上限与后端一致（backend/internal/catalog/issue_read.go）：超界的响应整体拒绝。
const MAX_ISSUE_PAGE_ITEMS = 100;
const MAX_FIELD_ISSUES = 100; // 字段决定的上限（maxReleaseEvidence）
const MAX_ISSUE_CANDIDATES = 20;
const MAX_ISSUE_SOURCE_REFS = 100;
const MAX_ISSUE_SIBLINGS = 20;
const MAX_ISSUE_MISSING_TRACKS = 1000;
const MAX_ISSUE_TEXT = 500; // 值、候选、原因码与来源路径，后端按 500 字节截断

const ISSUE_STATUSES = ["open", "reviewed"] as const;
export type IssueStatus = (typeof ISSUE_STATUSES)[number];

const issueCountsDecoder = d.object({
  fields: d.integer({ min: 0 }),
  grouping: d.integer({ min: 0, max: 1 }),
  missing_tracks: d.integer({ min: 0 }),
});
const issueSummaryCountsDecoder = d.object({
  all: d.integer({ min: 0 }),
  fields: d.integer({ min: 0 }),
  grouping: d.integer({ min: 0 }),
  missing_tracks: d.integer({ min: 0 }),
});

// 列表的一行：版本摘要（attention_count 等于 open 三项之和）、所在的组、版本说明，以及待处理与已核对的数目；
// summary 是筛选项上的数字，不随类别筛选与分页变化。
const issueListItemDecoder = d.object({
  release: releaseSummaryDecoder,
  group_id: d.uuid(),
  edition: d.nullable(d.string()),
  open: issueCountsDecoder,
  reviewed: issueCountsDecoder,
});
const issuePageDecoder = d.object({
  items: d.array(issueListItemDecoder, { max: MAX_ISSUE_PAGE_ITEMS }),
  summary: d.object({ open: issueSummaryCountsDecoder, reviewed: issueSummaryCountsDecoder }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_ISSUE_PAGE_ITEMS }),
    total: d.integer({ min: 0 }),
  }),
});

// 核对状态：设下它的操作、时间与核对人（用户已不存在时用户名为空串）。
const issueReviewDecoder = d.object({
  operation_id: d.uuid(),
  reviewed_at: d.timestamp(),
  reviewer: d.object({ id: d.uuid(), username: d.string({ max: 200 }) }),
});
// 依据令牌：问题依据的 SHA-256，核对时原样带回。
const basisDecoder = d.string({ min: 64, max: 64 });
const issueTextDecoder = d.string({ max: MAX_ISSUE_TEXT });

// 字段名按字符串解码（与证据一致）；只有来源类型与介质类型 reviewable 为真，并带依据令牌。
const fieldIssueDecoder = d.object({
  field: d.string({ min: 1, max: 64 }),
  value: d.nullable(issueTextDecoder),
  reason_code: d.nullable(issueTextDecoder),
  candidates: d.array(issueTextDecoder, { max: MAX_ISSUE_CANDIDATES }),
  reviewable: d.boolean(),
  basis: d.nullable(basisDecoder),
  status: d.oneOf(ISSUE_STATUSES),
  review: d.nullable(issueReviewDecoder),
});
const groupingIssueDecoder = d.object({
  reason_codes: d.array(issueTextDecoder, { max: MAX_ISSUE_CANDIDATES }),
  source_refs: d.array(issueTextDecoder, { max: MAX_ISSUE_SOURCE_REFS }),
  truncated: d.boolean(),
  siblings: d.array(d.object({ release_id: d.uuid(), group_id: d.uuid(), title: d.string() }), { max: MAX_ISSUE_SIBLINGS }),
  basis: basisDecoder,
  status: d.oneOf(ISSUE_STATUSES),
  review: d.nullable(issueReviewDecoder),
});
const missingTrackDecoder = d.object({
  id: d.uuid(),
  title: d.string(),
  medium_position: d.integer({ min: 0 }),
  position: d.integer({ min: 0 }),
  source: issueTextDecoder,
  last_seen_at: d.timestamp(),
  reviewed: d.boolean(),
});
const missingTracksIssueDecoder = d.object({
  count: d.integer({ min: 1 }),
  tracks: d.array(missingTrackDecoder, { min: 1, max: MAX_ISSUE_MISSING_TRACKS }),
  truncated: d.boolean(),
  basis: basisDecoder,
  status: d.oneOf(ISSUE_STATUSES),
  review: d.nullable(issueReviewDecoder),
});

// 一个版本的全部问题：release 是版本摘要（页面的标题与“需要关注”），edition 是版本说明；
// status 为 open 而 review 不为 null，就是“核对之后情况变了”。
const releaseIssuesDecoder = d.object({
  release_id: d.uuid(),
  group_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  release: releaseSummaryDecoder,
  edition: d.nullable(d.string()),
  fields: d.array(fieldIssueDecoder, { max: MAX_FIELD_ISSUES }),
  grouping: d.nullable(groupingIssueDecoder),
  missing_tracks: d.nullable(missingTracksIssueDecoder),
});

// 操作记录（管理员）。type 是记录自己的类型；changes.kind 是前后状态按哪种类型解释（撤销记录与它所撤销的操作同类型）。
// 目标是发行的记录给 release、album 为 null，目标是专辑的记录反过来；目标已不存在时对应的一项也是 null；
// 撤销记录与被撤销的记录互相指向（reverts、reverted_by）。
const MAX_OPERATION_PAGE_ITEMS = 100;
const OPERATION_TYPES = [
  "release_correction",
  "release_group_change",
  "release_group_default",
  "release_issue_review",
  "release_claim",
  "revert",
  "artist_merge",
  "artist_split",
  "artist_rename",
  "artist_candidate_ignore",
] as const;
export type OperationType = (typeof OPERATION_TYPES)[number];

const operationActorDecoder = d.object({ id: d.uuid(), username: d.string({ max: 200 }) });
const operationReleaseDecoder = d.object({ id: d.uuid(), title: d.string(), group_id: d.uuid(), visible: d.boolean() });
const operationAlbumDecoder = d.object({ id: d.uuid(), title: d.string(), visible: d.boolean() });
// 目标是艺人的记录（M5 ⑤）：艺人现在的名字；已经合并进别的艺人时 redirect_artist_id 是现在的艺人，没有可显示的曲目时 visible 为 false。
const operationArtistDecoder = d.object({ id: d.uuid(), name: d.string(), visible: d.boolean(), redirect_artist_id: d.nullable(d.uuid()) });

const fieldStateDecoder = d.object({ correction: d.nullable(correctionDecoder), effective: d.nullable(fieldValueDecoder) });
const correctionChangesDecoder = d.object({
  kind: d.literal("release_correction"),
  fields: d.array(
    d.object({ field: d.oneOf(CORRECTABLE_FIELDS), before: fieldStateDecoder, after: fieldStateDecoder }),
    { max: CORRECTABLE_FIELDS.length },
  ),
});
const groupSnapshotDecoder = d.object({ group_id: d.uuid(), group_title: d.string() });
const groupChangesDecoder = d.object({ kind: d.literal("release_group_change"), from: groupSnapshotDecoder, to: groupSnapshotDecoder });

// 设定默认版本：前后的专辑状态是当时的专辑标题与已存的默认版本（null 是自动选择；快照取当时的标题、年份与版本说明，
// 版本之后改名或变得不可见，记录仍能读）。
const defaultVersionDecoder = d.object({
  release_id: d.uuid(),
  title: d.string(),
  year: d.nullable(d.integer({ min: 1, max: 9999 })),
  edition: d.nullable(d.string()),
});
const defaultStateDecoder = d.object({ group_title: d.string(), default: d.nullable(defaultVersionDecoder) });
const defaultChangesDecoder = d.object({ kind: d.literal("release_group_default"), from: defaultStateDecoder, to: defaultStateDecoder });

// 核对问题：前后是否有核对；缺失曲目的核对另给依据里的曲目数（其余为 null）。
const issueReviewStateDecoder = d.object({ reviewed: d.boolean(), track_count: d.nullable(d.integer({ min: 0 })) });
const issueReviewChangesDecoder = d.object({
  kind: d.literal("release_issue_review"),
  issue: d.oneOf(REVIEWABLE_ISSUES),
  from: issueReviewStateDecoder,
  to: issueReviewStateDecoder,
});

// 认领（M4 第三项）：改动摘要写明来源（已不可见的旧版本）、迁移了什么与保留了什么；撤销记录只带来源，没有 moved 与 skipped。
// defaults 是接任默认版本的专辑数（改回自动选择的不算）。
const claimSummaryDecoder = d.object({
  corrections: d.array(d.oneOf(CORRECTABLE_FIELDS), { max: CORRECTABLE_FIELDS.length }),
  album: d.boolean(),
  defaults: d.integer({ min: 0 }),
  reviews: d.array(d.oneOf(REVIEWABLE_ISSUES), { max: REVIEWABLE_ISSUES.length }),
});
const claimChangesDecoder = d.object({
  kind: d.literal("release_claim"),
  source: d.object({ id: d.uuid(), title: d.string(), visible: d.boolean() }),
  // 认领之前新版本所在的组：从记录撤销认领时页面跟着新版本回到那里；撤销记录没有它。
  previous_group_id: d.optional(d.uuid()),
  moved: d.optional(claimSummaryDecoder),
  skipped: d.optional(claimSummaryDecoder),
});

// 艺人的治理（M5 ⑤，都不能撤销：合并的反向是拆开，改名的反向是再改名）。名字是当时的快照。
const artistSnapshotDecoder = d.object({ id: d.uuid(), name: d.string() });
const artistScopeCounts = { name_count: d.integer({ min: 0 }), release_count: d.integer({ min: 0 }), track_count: d.integer({ min: 0 }) };
const artistMergeChangesDecoder = d.object({ kind: d.literal("artist_merge"), source: artistSnapshotDecoder, target: artistSnapshotDecoder, ...artistScopeCounts });
const artistSplitChangesDecoder = d.object({
  kind: d.literal("artist_split"),
  mode: d.oneOf(["aliases", "albums"]),
  source: artistSnapshotDecoder,
  target: artistSnapshotDecoder,
  ...artistScopeCounts,
});
const artistNameStateDecoder = d.object({ name: d.string(), source: d.oneOf(["auto", "manual"]) });
const artistRenameChangesDecoder = d.object({ kind: d.literal("artist_rename"), from: artistNameStateDecoder, to: artistNameStateDecoder });
const artistIgnoreChangesDecoder = d.object({ kind: d.literal("artist_candidate_ignore"), other_artist: artistSnapshotDecoder, ignored: d.boolean() });

type OperationChanges =
  | d.Infer<typeof artistMergeChangesDecoder>
  | d.Infer<typeof artistSplitChangesDecoder>
  | d.Infer<typeof artistRenameChangesDecoder>
  | d.Infer<typeof artistIgnoreChangesDecoder>
  | d.Infer<typeof correctionChangesDecoder>
  | d.Infer<typeof groupChangesDecoder>
  | d.Infer<typeof defaultChangesDecoder>
  | d.Infer<typeof issueReviewChangesDecoder>
  | d.Infer<typeof claimChangesDecoder>;
// 按 kind 选择解码器；未知的 kind 整体拒绝（后端新增操作类型时，样本契约测试先失败，在这里补上才算确认前端已经知道）。
const operationChangesDecoder: d.Decoder<OperationChanges> = (input, path) => {
  const kind = typeof input === "object" && input !== null && "kind" in input ? input.kind : undefined;
  if (kind === "release_correction") return correctionChangesDecoder(input, path);
  if (kind === "release_group_change") return groupChangesDecoder(input, path);
  if (kind === "release_group_default") return defaultChangesDecoder(input, path);
  if (kind === "release_issue_review") return issueReviewChangesDecoder(input, path);
  if (kind === "release_claim") return claimChangesDecoder(input, path);
  if (kind === "artist_merge") return artistMergeChangesDecoder(input, path);
  if (kind === "artist_split") return artistSplitChangesDecoder(input, path);
  if (kind === "artist_rename") return artistRenameChangesDecoder(input, path);
  if (kind === "artist_candidate_ignore") return artistIgnoreChangesDecoder(input, path);
  throw new d.DecodeError(`${path}.kind`, "不是已知的操作类型");
};

const operationItemDecoder = d.object({
  id: d.uuid(),
  type: d.oneOf(OPERATION_TYPES),
  created_at: d.timestamp(),
  actor: operationActorDecoder,
  release: d.nullable(operationReleaseDecoder),
  album: d.nullable(operationAlbumDecoder),
  artist: d.nullable(operationArtistDecoder),
  changes: d.nullable(operationChangesDecoder),
  reverts: d.nullable(d.uuid()),
  reverted_by: d.nullable(d.uuid()),
  revertible: d.boolean(),
});

const operationPageDecoder = d.object({
  items: d.array(operationItemDecoder, { max: MAX_OPERATION_PAGE_ITEMS }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_OPERATION_PAGE_ITEMS }),
    total: d.integer({ min: 0 }),
  }),
});

// 撤销成功的响应：新的撤销记录、被撤销的记录、目标（发行或专辑）与目标新的人工修改版本号。
const revertResultDecoder = d.object({
  operation_id: d.uuid(),
  reverts_operation_id: d.uuid(),
  target_type: d.oneOf(["release", "release_group", "artist"] as const),
  target_id: d.uuid(),
  revision: d.integer({ min: 1 }),
});

// 待认领（M4 第三项，管理员）：已不可见、但留有人工修改的旧版本。上限与后端一致（backend/internal/catalog/claim_read.go）。
const MAX_CLAIM_PAGE_ITEMS = 100;
const MAX_CLAIM_SIBLINGS = 20;
const MAX_CLAIM_SUGGESTIONS = 5;
const MAX_CLAIM_HINTS = 3;
const MAX_CLAIM_PATH = 500; // 目录与名称，后端按 500 字节截断

const claimReleaseDecoder = d.object({
  id: d.uuid(),
  title: d.string(),
  artist: d.string(),
  year: d.nullable(d.integer({ min: 1, max: 9999 })),
  edition: d.nullable(d.string()),
});
const claimEntryShape = {
  release: claimReleaseDecoder,
  // 旧版本和别的发行同在一张专辑里时才有；title 是别的成员里按代表版本规则排第一的有效标题。
  album: d.nullable(d.object({ id: d.uuid(), title: d.string(), other_release_count: d.integer({ min: 1 }) })),
  // 扫描目录的名称（路径末段）与相对目录；不安全的目录是空串。不含绝对路径。
  location: d.object({ root_name: d.string({ max: MAX_CLAIM_PATH }), directory: d.string({ max: MAX_CLAIM_PATH }) }),
  last_seen_at: d.timestamp(),
  holds: d.object({
    corrections: d.array(d.oneOf(CORRECTABLE_FIELDS), { max: CORRECTABLE_FIELDS.length }),
    album: d.boolean(),
    default: d.boolean(),
    reviews: d.array(d.oneOf(REVIEWABLE_ISSUES), { max: REVIEWABLE_ISSUES.length }),
  }),
};
const claimEntryDecoder = d.object(claimEntryShape);
const claimPageDecoder = d.object({
  items: d.array(claimEntryDecoder, { max: MAX_CLAIM_PAGE_ITEMS }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_CLAIM_PAGE_ITEMS }),
    total: d.integer({ min: 0 }),
  }),
});

// 推荐的新版本：可见版本的摘要、版本说明、理由码（同样的文件、同一目录）与命中的路径数。理由码按字符串解码，后端新增理由时原样显示。
const claimSuggestionDecoder = d.object({
  release: releaseSummaryDecoder,
  group_id: d.uuid(),
  edition: d.nullable(d.string()),
  reasons: d.array(d.string({ min: 1, max: 64 }), { max: 4 }),
  same_files: d.integer({ min: 0 }),
});

// 详情：列表项的全部字段在顶层，再加旧版本的版本号、修正明细（意图与扫描值）、同专辑的别的发行、作为默认版本的专辑、核对与推荐。
const claimDetailDecoder = d.object({
  ...claimEntryShape,
  revision: d.integer({ min: 1 }),
  corrections: d.array(d.object({ field: d.oneOf(CORRECTABLE_FIELDS), correction: correctionDecoder, scan_value: d.nullable(fieldValueDecoder) }), {
    max: CORRECTABLE_FIELDS.length,
  }),
  siblings: d.array(d.object({ id: d.uuid(), title: d.string(), visible: d.boolean() }), { max: MAX_CLAIM_SIBLINGS }),
  default_of: d.array(d.object({ id: d.uuid(), title: d.string() }), { max: MAX_CLAIM_SIBLINGS }),
  reviews: d.array(
    d.object({ issue: d.oneOf(REVIEWABLE_ISSUES), reviewed_at: d.timestamp(), reviewer: d.object({ id: d.uuid(), username: d.string({ max: 200 }) }) }),
    { max: REVIEWABLE_ISSUES.length },
  ),
  suggestions: d.array(claimSuggestionDecoder, { max: MAX_CLAIM_SUGGESTIONS }),
});

// 专辑详情用的提示：把这个可见版本列为推荐的待认领旧版本。
const claimHintsDecoder = d.object({ items: d.array(d.object({ id: d.uuid(), title: d.string() }), { max: MAX_CLAIM_HINTS }) });

// 预览里一项的去向：迁移、保留（新版本已有的或无法迁移的）、改回自动选择（只用于默认版本）。
export const CLAIM_ACTIONS = ["move", "skip", "reset"] as const;
export const CLAIM_ITEM_KINDS = ["correction", "album", "default", "review"] as const;
// 迁移的核对在新版本上的效果：立即生效、依据不同会重新提醒、新版本上没有这类问题暂时无事可压。
export const CLAIM_EFFECTS = ["valid", "stale", "no_issue"] as const;
const claimItemDecoder = d.object({
  kind: d.oneOf(CLAIM_ITEM_KINDS),
  field: d.optional(d.oneOf(CORRECTABLE_FIELDS)),
  issue: d.optional(d.oneOf(REVIEWABLE_ISSUES)),
  group: d.optional(d.uuid()),
  action: d.oneOf(CLAIM_ACTIONS),
  // 跳过的原因按字符串解码（target_has_correction、target_has_review、target_in_album、already_in_album、not_transferable）。
  reason: d.optional(d.string({ min: 1, max: 64 })),
  effect: d.optional(d.oneOf(CLAIM_EFFECTS)),
  from: d.optional(correctionDecoder),
  to: d.optional(correctionDecoder),
});
const claimPreviewReleaseDecoder = d.object({ id: d.uuid(), title: d.string(), group_id: d.uuid(), visible: d.boolean() });
const MAX_CLAIM_ITEMS = 32; // 修正 8 项、专辑 1 项、默认版本若干、核对 4 项
const claimPreviewDecoder = d.object({
  release: claimPreviewReleaseDecoder,
  source: claimPreviewReleaseDecoder,
  revision: d.integer({ min: 1 }),
  source_revision: d.integer({ min: 1 }),
  items: d.array(claimItemDecoder, { max: MAX_CLAIM_ITEMS }),
  token: basisDecoder,
});

// 认领成功的响应：新版本认领之后与之前所在的组、两个版本新的版本号、各类的去向与这次操作的 ID（撤销用）。
const claimResultSummaryDecoder = d.object({
  corrections: d.array(d.oneOf(CORRECTABLE_FIELDS), { max: CORRECTABLE_FIELDS.length }),
  album: d.boolean(),
  reviews: d.array(d.oneOf(REVIEWABLE_ISSUES), { max: REVIEWABLE_ISSUES.length }),
});
const claimResultDecoder = d.object({
  release_id: d.uuid(),
  source_release_id: d.uuid(),
  group_id: d.uuid(),
  previous_group_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  source_revision: d.integer({ min: 1 }),
  operation_id: d.uuid(),
  moved: claimResultSummaryDecoder,
  skipped: claimResultSummaryDecoder,
  defaults: d.object({ moved: d.array(d.uuid(), { max: MAX_CLAIM_ITEMS }), reset: d.array(d.uuid(), { max: MAX_CLAIM_ITEMS }) }),
});

// ---- 艺人的治理（M5 ⑤，管理员）----

/** “可能是同一位”每页的上限、每条候选给出的理由样例与名字键对的上限。 */
const MAX_CANDIDATE_PAGE_ITEMS = 100;
const MAX_CANDIDATE_REASONS = 3;
const MAX_CANDIDATE_KEY_PAIRS = 100;
/** 拆开的预览一次最多的名字键与专辑。 */
export const MAX_SPLIT_NAME_KEYS = 100;
export const MAX_SPLIT_GROUPS = 100;

const artistRevisionDecoder = d.integer({ min: 1 });

// 合并：target（路径里的艺人）的新版本号，被并入的 source 的新版本号与它现在指向的艺人。
const artistMergeResultDecoder = d.object({
  artist_id: d.uuid(),
  operation_id: d.uuid(),
  revision: artistRevisionDecoder,
  source: d.object({ id: d.uuid(), revision: artistRevisionDecoder, redirect_artist_id: d.uuid() }),
});

// 拆开的范围：别名整体拆回（aliases）或按专辑拆同名的人（albums）。groups 是选中的专辑展开成的版本快照；
// excluded_scopes 是已经分给别人、不在这次范围里的部分（预览里列明，不抢）。它的条目形状由后端样本确认后再细化，这里只核对是数组。
const splitScopeDecoder = d.object({
  mode: d.oneOf(["aliases", "albums"]),
  name_key_ids: d.array(d.uuid(), { max: MAX_SPLIT_NAME_KEYS }),
  name_count: d.integer({ min: 0 }),
  groups: d.array(d.object({ group_id: d.uuid(), release_ids: d.array(d.uuid(), { max: 1000 }), revision: artistRevisionDecoder }), { max: MAX_SPLIT_GROUPS }),
  release_ids: d.array(d.uuid(), { max: 1000 }),
  release_count: d.integer({ min: 0 }),
  track_count: d.integer({ min: 0 }),
  roles: d.array(d.oneOf(artistRoles), { max: artistRoles.length }),
  excluded_scope_count: d.integer({ min: 0 }),
  excluded_scopes: d.array((input: unknown) => input, { max: 1000 }),
});

// 拆开的预览（只读）：basis 是服务端对当时状态算出的依据，提交时原样带回；状态变了返回 409 artist_scope_changed，需要重新预览。
const splitPreviewDecoder = d.object({
  artist_id: d.uuid(),
  revision: artistRevisionDecoder,
  basis: d.string({ min: 1, max: 128 }),
  scope: splitScopeDecoder,
  limits: d.object({ groups: d.integer({ min: 0 }), name_keys: d.integer({ min: 0 }), overrides: d.integer({ min: 0 }), releases: d.integer({ min: 0 }), tracks: d.integer({ min: 0 }) }),
});

const splitResultDecoder = d.object({
  artist_id: d.uuid(),
  operation_id: d.uuid(),
  revision: artistRevisionDecoder,
  scope_summary: splitScopeDecoder,
  target: d.object({ id: d.uuid(), revision: artistRevisionDecoder }),
});

const renameResultDecoder = d.object({
  artist_id: d.uuid(),
  operation_id: d.uuid(),
  revision: artistRevisionDecoder,
  display_name: d.string(),
  name_source: d.oneOf(["auto", "manual"]),
});

const ignoreResultDecoder = d.object({
  artist_id: d.uuid(),
  operation_id: d.uuid(),
  revision: artistRevisionDecoder,
  other_revision: artistRevisionDecoder,
  ignored: d.boolean(),
});

// “可能是同一位”的候选：两位艺人、实际看到的名字键对（忽略时原样提交）、最多 3 个理由样例与证据数。只是提示，不是同一人的结论。
// 理由的 kind 按字符串解码（显示时只给已知的 kind 写中文说明，未知的不显示）；括号别名的理由带哪一边是带括号的写法、另一边等于括号里面还是外面。
const candidateReasonDecoder = d.object({
  kind: d.string({ min: 1, max: 64 }),
  release_id: d.uuid(),
  left_name: d.string(),
  right_name: d.string(),
  bracket_side: d.optional(d.oneOf(["left", "right"])),
  alias_part: d.optional(d.oneOf(["inner", "outer"])),
});
const artistCandidateDecoder = d.object({
  left: artistSummaryDecoder,
  right: artistSummaryDecoder,
  key_pairs: d.array(d.object({ left_key_id: d.uuid(), right_key_id: d.uuid() }), { min: 1, max: MAX_CANDIDATE_KEY_PAIRS }),
  reasons: d.array(candidateReasonDecoder, { max: MAX_CANDIDATE_REASONS }),
  evidence_count: d.integer({ min: 1 }),
});
const artistCandidatePageDecoder = d.object({
  items: d.array(artistCandidateDecoder, { max: MAX_CANDIDATE_PAGE_ITEMS }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_CANDIDATE_PAGE_ITEMS }),
    total: d.integer({ min: 0 }),
  }),
});

// 之前并入这位艺人、现在可以拆回原身份的艺人（管理员只读，I1）：name_key_ids 是那次合并带过来、现在仍归这位艺人的名字键（可能为空）；
// revision 是旧身份现在的版本号（恢复时的预期版本）。
const MAX_MERGED_SOURCES = 100;
const mergedSourcesDecoder = d.object({
  items: d.array(
    d.object({
      artist_id: d.uuid(),
      name: d.string(),
      revision: artistRevisionDecoder,
      name_key_ids: d.array(d.uuid(), { max: MAX_SPLIT_NAME_KEYS }),
      operation_id: d.uuid(),
      merged_at: d.timestamp(),
    }),
    { max: MAX_MERGED_SOURCES },
  ),
});

export const decodeArtistMergedSources = d.defineDecoder(mergedSourcesDecoder);
export type ArtistMergedSourceDTO = d.Infer<typeof mergedSourcesDecoder>["items"][number];

export const decodeArtistMergeResult = d.defineDecoder(artistMergeResultDecoder);
export const decodeArtistSplitPreview = d.defineDecoder(splitPreviewDecoder);
export const decodeArtistSplitResult = d.defineDecoder(splitResultDecoder);
export const decodeArtistRenameResult = d.defineDecoder(renameResultDecoder);
export const decodeArtistIgnoreResult = d.defineDecoder(ignoreResultDecoder);
export const decodeArtistCandidatePage = d.defineDecoder(artistCandidatePageDecoder);

export type ArtistMergeResultDTO = d.Infer<typeof artistMergeResultDecoder>;
export type ArtistSplitScopeDTO = d.Infer<typeof splitScopeDecoder>;
export type ArtistSplitPreviewDTO = d.Infer<typeof splitPreviewDecoder>;
export type ArtistSplitResultDTO = d.Infer<typeof splitResultDecoder>;
export type ArtistRenameResultDTO = d.Infer<typeof renameResultDecoder>;
export type ArtistIgnoreResultDTO = d.Infer<typeof ignoreResultDecoder>;
export type ArtistCandidateDTO = d.Infer<typeof artistCandidateDecoder>;
export type ArtistCandidatePageDTO = d.Infer<typeof artistCandidatePageDecoder>;

export const decodeCorrectionState = d.defineDecoder(correctionStateDecoder);
export const decodeCorrectionResult = d.defineDecoder(correctionResultDecoder);
export const decodeGroupChangeResult = d.defineDecoder(groupChangeResultDecoder);
export const decodeGroupDefaultResult = d.defineDecoder(groupDefaultResultDecoder);
export const decodeIssueReviewResult = d.defineDecoder(issueReviewResultDecoder);
export const decodeIssuePage = d.defineDecoder(issuePageDecoder);
export const decodeReleaseIssues = d.defineDecoder(releaseIssuesDecoder);
export const decodeOperationPage = d.defineDecoder(operationPageDecoder);
export const decodeRevertResult = d.defineDecoder(revertResultDecoder);
export const decodeClaimPage = d.defineDecoder(claimPageDecoder);
export const decodeClaimDetail = d.defineDecoder(claimDetailDecoder);
export const decodeClaimHints = d.defineDecoder(claimHintsDecoder);
export const decodeClaimPreview = d.defineDecoder(claimPreviewDecoder);
export const decodeClaimResult = d.defineDecoder(claimResultDecoder);

export type CorrectionIntentDTO = d.Infer<typeof correctionDecoder>;
export type CorrectionFieldDTO = d.Infer<typeof correctionFieldDecoder>;
export type CorrectionStateDTO = d.Infer<typeof correctionStateDecoder>;
export type CorrectionResultDTO = d.Infer<typeof correctionResultDecoder>;
export type GroupChangeResultDTO = d.Infer<typeof groupChangeResultDecoder>;
export type GroupDefaultResultDTO = d.Infer<typeof groupDefaultResultDecoder>;
export type IssueReviewResultDTO = d.Infer<typeof issueReviewResultDecoder>;
export type IssueCountsDTO = d.Infer<typeof issueCountsDecoder>;
export type IssueListItemDTO = d.Infer<typeof issueListItemDecoder>;
export type IssuePageDTO = d.Infer<typeof issuePageDecoder>;
export type IssueReviewDTO = d.Infer<typeof issueReviewDecoder>;
export type FieldIssueDTO = d.Infer<typeof fieldIssueDecoder>;
export type GroupingIssueDTO = d.Infer<typeof groupingIssueDecoder>;
export type MissingTrackDTO = d.Infer<typeof missingTrackDecoder>;
export type MissingTracksIssueDTO = d.Infer<typeof missingTracksIssueDecoder>;
export type ReleaseIssuesDTO = d.Infer<typeof releaseIssuesDecoder>;
export type DefaultVersionDTO = d.Infer<typeof defaultVersionDecoder>;
export type OperationItemDTO = d.Infer<typeof operationItemDecoder>;
export type OperationPageDTO = d.Infer<typeof operationPageDecoder>;
export type OperationChangesDTO = OperationChanges;
export type RevertResultDTO = d.Infer<typeof revertResultDecoder>;
export type ClaimEntryDTO = d.Infer<typeof claimEntryDecoder>;
export type ClaimPageDTO = d.Infer<typeof claimPageDecoder>;
export type ClaimDetailDTO = d.Infer<typeof claimDetailDecoder>;
export type ClaimSuggestionDTO = d.Infer<typeof claimSuggestionDecoder>;
export type ClaimHintsDTO = d.Infer<typeof claimHintsDecoder>;
export type ClaimItemDTO = d.Infer<typeof claimItemDecoder>;
export type ClaimPreviewDTO = d.Infer<typeof claimPreviewDecoder>;
export type ClaimResultDTO = d.Infer<typeof claimResultDecoder>;
export type ClaimSummaryDTO = d.Infer<typeof claimSummaryDecoder>;
export type ClaimAction = (typeof CLAIM_ACTIONS)[number];
export type ClaimEffect = (typeof CLAIM_EFFECTS)[number];
