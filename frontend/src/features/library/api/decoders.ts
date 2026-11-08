import { decode as d } from "../../../shared/api";
import { releaseGroupSummaryWithAddedAtDecoder } from "../../catalog/api/decoders";

export const rootStatuses = ["active", "disabled"] as const;
export type RootStatus = (typeof rootStatuses)[number];

export const scanStatuses = ["running", "succeeded", "failed", "canceled", "incomplete"] as const;
export type ScanStatus = (typeof scanStatuses)[number];

export const rootOperationKinds = ["create", "disable", "restore", "exclude", "unexclude"] as const;
export const rootOperationStatuses = ["succeeded", "failed"] as const;

/** 每个目录最多 100 个不扫描的子文件夹。 */
export const MAX_ROOT_EXCLUSIONS = 100;

// 不扫描的子文件夹：相对该目录的路径（`/` 分隔），按字节升序，没有时为空数组。
const excludedPathsDecoder = d.array(d.string({ min: 1 }), { max: MAX_ROOT_EXCLUSIONS });

// 目录只返回目录名（路径的最后一段），不返回服务器上的绝对路径。
const rootDecoder = d.object({
  id: d.uuid(),
  path: d.string({ min: 1 }),
  name: d.string({ min: 1 }),
  status: d.oneOf(rootStatuses),
  revision: d.integer({ min: 1 }),
  created_at: d.timestamp(),
  updated_at: d.timestamp(),
  excluded_paths: excludedPathsDecoder,
});

// 后端不给目录列表设上限（后端缺口），所以不限条数，只限字段长度。
const rootListDecoder = d.object({ items: d.array(rootDecoder, { max: d.unbounded }) });

export const decodeRootList = d.defineDecoder(rootListDecoder);
export type RootDTO = d.Infer<typeof rootDecoder>;

/** POST /library-roots（201）：路径已登记时返回已有目录，停用的保持停用。 */
const rootCreatedDecoder = d.object({
  id: d.uuid(),
  name: d.string({ min: 1 }),
  status: d.oneOf(rootStatuses),
  revision: d.integer({ min: 1 }),
});

export const decodeRootCreated = d.defineDecoder(rootCreatedDecoder);
export type RootCreatedDTO = d.Infer<typeof rootCreatedDecoder>;

/** 停用与恢复的结果。 */
const rootChangedDecoder = d.object({
  id: d.uuid(),
  path: d.string({ min: 1 }),
  status: d.oneOf(rootStatuses),
  revision: d.integer({ min: 1 }),
});

export const decodeRootChanged = d.defineDecoder(rootChangedDecoder);
export type RootChangedDTO = d.Infer<typeof rootChangedDecoder>;

/** 添加与移除不扫描的子文件夹的结果：目录的当前状态与全部排除项（不改变 revision）。 */
const rootExclusionsDecoder = d.object({
  id: d.uuid(),
  name: d.string({ min: 1 }),
  path: d.string({ min: 1 }),
  status: d.oneOf(rootStatuses),
  revision: d.integer({ min: 1 }),
  excluded_paths: excludedPathsDecoder,
});

export const decodeRootExclusions = d.defineDecoder(rootExclusionsDecoder);
export type RootExclusionsDTO = d.Infer<typeof rootExclusionsDecoder>;

// root_id、revision 与 error_code 在记录没有对应值时被省略（目录被删除、失败操作等）。
const rootOperationDecoder = d.object({
  id: d.uuid(),
  operation: d.oneOf(rootOperationKinds),
  status: d.oneOf(rootOperationStatuses),
  created_at: d.timestamp(),
  root_id: d.optional(d.uuid()),
  revision: d.optional(d.integer({ min: 1 })),
  error_code: d.optional(d.string({ min: 1, max: 64 })),
});

// 目录操作记录最多 100 条。
const rootOperationListDecoder = d.object({ items: d.array(rootOperationDecoder, { max: 100 }) });

export const decodeRootOperationList = d.defineDecoder(rootOperationListDecoder);
export type RootOperationDTO = d.Infer<typeof rootOperationDecoder>;

// 诊断类别由后端写入，已知类别有中文名，未知类别原样显示；后端保证类别只含小写字母、数字与下划线。
const diagnosticKindDecoder = d.string({ min: 1, max: 64, pattern: /^[a-z0-9_]+$/ });

const diagnosticsSummaryDecoder = d.object({
  total: d.integer({ min: 0 }),
  counts: d.array(d.object({ kind: diagnosticKindDecoder, count: d.integer({ min: 0 }) }), { max: 100 }),
});

/** 在已解码的值上再检查字段之间的关系（单个字段的类型与范围由原语负责）。check 返回违反时的说明，没有问题返回 null。 */
function refine<T>(inner: d.Decoder<T>, check: (value: T) => string | null): d.Decoder<T> {
  return (input, path) => {
    const value = inner(input, path);
    const problem = check(value);
    if (problem !== null) throw new d.DecodeError(path, problem);
    return value;
  };
}

/**
 * 扫描的四个工作阶段（成功不是第五个阶段）：先给所有目录数文件，然后逐个目录读取标签、整理专辑、保存。
 * 每个阶段的 completed / total 用固定的单位：数文件按目录、读取标签按文件、整理专辑按目录（0 或 1）、保存按专辑范围。
 */
export const scanProgressPhases = ["counting", "reading_tags", "organizing", "saving"] as const;
export type ScanProgressPhase = (typeof scanProgressPhases)[number];
const scanProgressUnits = ["files", "roots", "scopes"] as const;
const phaseUnits: Record<ScanProgressPhase, (typeof scanProgressUnits)[number]> = {
  counting: "roots",
  reading_tags: "files",
  organizing: "roots",
  saving: "scopes",
};

/** 进度以基点（万分之一）表示；只有成功的扫描才到 10,000。 */
export const MAX_PERCENT_BP = 10_000;

const count = () => d.integer({ min: 0 });

// 服务端持久化的扫描进度。percent_bp 是加权后的整体工作进度（不是耗时，也不能用来估剩余时间）；files_* 是全局累计，
// completed / total 是当前目录当前阶段的分子分母，换阶段或换目录时可以重置。计数没结束（或被取消）时 files_total 为 null。
// files_processed 可以超过 files_total：计数之后目录里多了文件，分母不变。
const scanProgressDecoder = refine(
  d.object({
    version: d.literal(1),
    phase: d.oneOf(scanProgressPhases),
    percent_bp: d.integer({ min: 0, max: MAX_PERCENT_BP }),
    completed: count(),
    total: count(),
    unit: d.oneOf(scanProgressUnits),
    files_counted: count(),
    files_total: d.nullable(count()),
    files_processed: count(),
    count_complete: d.boolean(),
    files_changed: d.boolean(),
    updated_at: d.timestamp(),
  }),
  (progress) => (progress.unit === phaseUnits[progress.phase] ? null : `阶段 ${progress.phase} 的单位应为 ${phaseUnits[progress.phase]}`),
);

// 当前目录的序号（1 起）与本次扫描的目录总数，所有登录用户可见；id 与 name 只有管理员的响应才有（普通成员省略这两个键）。
// 全部目录处理完、正在保存扫描结果时没有当前目录，index 为 null（管理员的 id、name 也为 null）。
const rootProgressDecoder = refine(
  d.object({
    index: d.nullable(d.integer({ min: 1 })),
    total: count(),
    id: d.optional(d.nullable(d.uuid())),
    name: d.optional(d.nullable(d.string({ min: 1, max: 1024 }))),
  }),
  (root) => (root.index !== null && root.index > root.total ? "目录序号不应超过目录总数" : null),
);

/**
 * 报告的状态：legacy 是本功能上线前的扫描（没有报告）；pending 只出现在运行中；ready 是成功扫描的完整报告；
 * partial 是没有成功结束的扫描已经保存的那部分变化；unavailable 是报告没能生成（扫描结果本身不受影响）。
 */
export const scanReportStatuses = ["legacy", "pending", "ready", "partial", "unavailable"] as const;
export type ScanReportStatus = (typeof scanReportStatuses)[number];

// progress、root_progress 与 report_status 是后来加的字段：旧服务没有它们，按“缺失 = 没有进度与报告”处理，不伪造进度。
// 本功能上线前的扫描 progress 与 root_progress 为 null、report_status 为 legacy。
// skipped_files 是这次扫描跳过的文件数（读不了的文件、文件夹与 CUE 各计 1），之前的扫描为 null。
const scanDecoder = d.object({
  id: d.uuid(),
  scan_run_id: d.uuid(),
  status: d.oneOf(scanStatuses),
  started_at: d.timestamp(),
  finished_at: d.nullable(d.timestamp()),
  cancel_requested_at: d.nullable(d.timestamp()),
  diagnostics: diagnosticsSummaryDecoder,
  progress: d.optional(d.nullable(scanProgressDecoder)),
  root_progress: d.optional(d.nullable(rootProgressDecoder)),
  report_status: d.optional(d.oneOf(scanReportStatuses)),
  skipped_files: d.nullable(count()),
});

export const decodeScan = d.defineDecoder(scanDecoder);
export type ScanDTO = d.Infer<typeof scanDecoder>;
export type ScanProgressDTO = d.Infer<typeof scanProgressDecoder>;
export type RootProgressDTO = d.Infer<typeof rootProgressDecoder>;

// 扫描记录一页（仅管理员）：条目与 GET /scans/{id} 相同；每页最多 100 条，total 是全部扫描数。
const scanPageDecoder = d.object({
  items: d.array(scanDecoder, { max: 100 }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: 100 }),
    total: d.integer({ min: 0 }),
  }),
});

export const decodeScanPage = d.defineDecoder(scanPageDecoder);
export type ScanPageDTO = d.Infer<typeof scanPageDecoder>;
export type DiagnosticsSummaryDTO = d.Infer<typeof diagnosticsSummaryDecoder>;

export const decodeActiveScan = d.defineDecoder(d.object({ scan: d.nullable(scanDecoder) }));

// 诊断明细最多 100 条；path 只在是安全相对路径时返回。
const diagnosticDecoder = d.object({
  id: d.string({ min: 1, max: 64 }),
  kind: diagnosticKindDecoder,
  path: d.nullable(d.string({ min: 1, max: 1024 })),
  message: d.string({ max: 1000 }),
});

const diagnosticsPageDecoder = d.object({
  items: d.array(diagnosticDecoder, { max: 100 }),
  summary: diagnosticsSummaryDecoder,
});

export const decodeDiagnosticsPage = d.defineDecoder(diagnosticsPageDecoder);
export type DiagnosticDTO = d.Infer<typeof diagnosticDecoder>;
export type DiagnosticsPageDTO = d.Infer<typeof diagnosticsPageDecoder>;

// 扫描报告（仅管理员，GET /scans/{id}/report）：计数与新增专辑的标题、艺人、时间在扫描结束时冻结；
// album（当前的专辑卡片，当前不可见时为 null）、current_group_id 与 claim_hints 随资料库的现状变化。
const changeCountsDecoder = d.object({ added: count(), updated: count(), missing: count() });

/** 新增专辑可能接替的已不可见旧版本：每项最多 3 个。 */
const MAX_CLAIM_HINTS = 3;
const MAX_REPORT_PAGE_ITEMS = 100;

const addedAlbumDecoder = d.object({
  group_id_at_scan: d.uuid(),
  added_at: d.timestamp(),
  title_at_scan: d.string(),
  artist_at_scan: d.string(),
  release_id: d.uuid(),
  current_group_id: d.nullable(d.uuid()),
  album: d.nullable(releaseGroupSummaryWithAddedAtDecoder),
  claim_hints: d.array(d.object({ id: d.uuid(), title: d.string() }), { max: MAX_CLAIM_HINTS }),
});

const scanReportDecoder = d.object({
  scan_run_id: d.uuid(),
  schema_version: d.literal(1),
  status: d.oneOf(["ready", "partial"] as const),
  scan_status: d.oneOf(scanStatuses),
  finalized_at: d.timestamp(),
  missing_reconciled: d.boolean(),
  files: d.object({
    counted: count(),
    total: d.nullable(count()),
    processed: count(),
    count_complete: d.boolean(),
    changed: d.boolean(),
  }),
  albums: changeCountsDecoder,
  tracks: changeCountsDecoder,
  // recorded 是这次扫描记下的诊断条数；诊断最多保留 100 条，may_be_truncated 为 true 时实际问题至少这么多。
  issues: d.object({ recorded: count(), may_be_truncated: d.boolean() }),
  added_albums: d.object({
    items: d.array(addedAlbumDecoder, { max: MAX_REPORT_PAGE_ITEMS }),
    pagination: d.object({
      page: d.integer({ min: 1 }),
      page_size: d.integer({ min: 1, max: MAX_REPORT_PAGE_ITEMS }),
      total: count(),
    }),
  }),
});

export const decodeScanReport = d.defineDecoder(scanReportDecoder);
export type ScanReportDTO = d.Infer<typeof scanReportDecoder>;
export type AddedAlbumDTO = d.Infer<typeof addedAlbumDecoder>;
export type ChangeCountsDTO = d.Infer<typeof changeCountsDecoder>;

// 添加目录时的文件夹浏览（GET /library-folders）：从 `/` 开始，parent 只在 `/` 为 null。
// selectable 为假的是允许目录的上层路径，只能进入；registered 含停用的目录。最多 2000 条，多了 truncated 为真。
// 带 root_id 时只在该目录之内浏览：从目录本身开始，那里 parent 为 null、can_go_up 为假，目录本身不可选。
const folderListingDecoder = d.object({
  path: d.string({ min: 1 }),
  parent: d.nullable(d.string({ min: 1 })),
  can_go_up: d.boolean(),
  registered: d.boolean(),
  selectable: d.boolean(),
  truncated: d.boolean(),
  items: d.array(
    d.object({ name: d.string({ min: 1 }), path: d.string({ min: 1 }), registered: d.boolean(), selectable: d.boolean() }),
    { max: 2000 },
  ),
});
export const decodeFolderListing = d.defineDecoder(folderListingDecoder);
export type FolderListingDTO = d.Infer<typeof folderListingDecoder>;

// 后台音频分析的进度（所有登录用户）：analyzed、failed 都是 total 口径里当前有效的计数。
// unavailable 是分析没有运行（ffmpeg 不可用或开关关闭），计数照常返回。
export const analysisStates = ["running", "waiting_for_scan", "idle", "unavailable"] as const;
const analysisProgressDecoder = d.object({
  total: d.integer({ min: 0 }),
  analyzed: d.integer({ min: 0 }),
  failed: d.integer({ min: 0 }),
  state: d.oneOf(analysisStates),
});
export const decodeAnalysisProgress = d.defineDecoder(analysisProgressDecoder);
export type AnalysisProgressDTO = d.Infer<typeof analysisProgressDecoder>;
