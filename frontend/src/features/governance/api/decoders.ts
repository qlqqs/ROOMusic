import { decode as d } from "../../../shared/api";

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

// 操作记录（管理员）。type 是记录自己的类型；changes.kind 是前后状态按哪种类型解释（撤销记录与它所撤销的操作同类型）。
// 目标是发行的记录给 release、album 为 null，目标是专辑的记录反过来；目标已不存在时对应的一项也是 null；
// 撤销记录与被撤销的记录互相指向（reverts、reverted_by）。
const MAX_OPERATION_PAGE_ITEMS = 100;
const OPERATION_TYPES = ["release_correction", "release_group_change", "release_group_default", "release_issue_review", "revert"] as const;

const operationActorDecoder = d.object({ id: d.uuid(), username: d.string({ max: 200 }) });
const operationReleaseDecoder = d.object({ id: d.uuid(), title: d.string(), group_id: d.uuid(), visible: d.boolean() });
const operationAlbumDecoder = d.object({ id: d.uuid(), title: d.string(), visible: d.boolean() });

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

type OperationChanges =
  | d.Infer<typeof correctionChangesDecoder>
  | d.Infer<typeof groupChangesDecoder>
  | d.Infer<typeof defaultChangesDecoder>
  | d.Infer<typeof issueReviewChangesDecoder>;
// 按 kind 选择解码器；未知的 kind 整体拒绝（后端新增操作类型时，样本契约测试先失败，在这里补上才算确认前端已经知道）。
const operationChangesDecoder: d.Decoder<OperationChanges> = (input, path) => {
  const kind = typeof input === "object" && input !== null && "kind" in input ? input.kind : undefined;
  if (kind === "release_correction") return correctionChangesDecoder(input, path);
  if (kind === "release_group_change") return groupChangesDecoder(input, path);
  if (kind === "release_group_default") return defaultChangesDecoder(input, path);
  if (kind === "release_issue_review") return issueReviewChangesDecoder(input, path);
  throw new d.DecodeError(`${path}.kind`, "不是已知的操作类型");
};

const operationItemDecoder = d.object({
  id: d.uuid(),
  type: d.oneOf(OPERATION_TYPES),
  created_at: d.timestamp(),
  actor: operationActorDecoder,
  release: d.nullable(operationReleaseDecoder),
  album: d.nullable(operationAlbumDecoder),
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
  target_type: d.oneOf(["release", "release_group"] as const),
  target_id: d.uuid(),
  revision: d.integer({ min: 1 }),
});

export const decodeCorrectionState = d.defineDecoder(correctionStateDecoder);
export const decodeCorrectionResult = d.defineDecoder(correctionResultDecoder);
export const decodeGroupChangeResult = d.defineDecoder(groupChangeResultDecoder);
export const decodeGroupDefaultResult = d.defineDecoder(groupDefaultResultDecoder);
export const decodeIssueReviewResult = d.defineDecoder(issueReviewResultDecoder);
export const decodeOperationPage = d.defineDecoder(operationPageDecoder);
export const decodeRevertResult = d.defineDecoder(revertResultDecoder);

export type CorrectionIntentDTO = d.Infer<typeof correctionDecoder>;
export type CorrectionFieldDTO = d.Infer<typeof correctionFieldDecoder>;
export type CorrectionStateDTO = d.Infer<typeof correctionStateDecoder>;
export type CorrectionResultDTO = d.Infer<typeof correctionResultDecoder>;
export type GroupChangeResultDTO = d.Infer<typeof groupChangeResultDecoder>;
export type GroupDefaultResultDTO = d.Infer<typeof groupDefaultResultDecoder>;
export type IssueReviewResultDTO = d.Infer<typeof issueReviewResultDecoder>;
export type DefaultVersionDTO = d.Infer<typeof defaultVersionDecoder>;
export type OperationItemDTO = d.Infer<typeof operationItemDecoder>;
export type OperationPageDTO = d.Infer<typeof operationPageDecoder>;
export type OperationChangesDTO = OperationChanges;
export type RevertResultDTO = d.Infer<typeof revertResultDecoder>;
