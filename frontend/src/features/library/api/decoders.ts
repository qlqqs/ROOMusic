import { decode as d } from "../../../shared/api";

export const rootStatuses = ["active", "disabled"] as const;
export type RootStatus = (typeof rootStatuses)[number];

export const scanStatuses = ["running", "succeeded", "failed", "canceled", "incomplete"] as const;
export type ScanStatus = (typeof scanStatuses)[number];

export const rootOperationKinds = ["create", "disable", "restore"] as const;
export const rootOperationStatuses = ["succeeded", "failed"] as const;

// 目录只返回目录名（路径的最后一段），不返回服务器上的绝对路径。
const rootDecoder = d.object({
  id: d.uuid(),
  path: d.string({ min: 1 }),
  name: d.string({ min: 1 }),
  status: d.oneOf(rootStatuses),
  revision: d.integer({ min: 1 }),
  created_at: d.timestamp(),
  updated_at: d.timestamp(),
});

// 后端不给目录列表设上限（后端缺口），所以不限条数，只限字段长度。
const rootListDecoder = d.object({ items: d.array(rootDecoder, { max: d.unbounded }) });

export const decodeRootList = d.defineDecoder(rootListDecoder);
export type RootDTO = d.Infer<typeof rootDecoder>;

/** POST /library-roots（201）：路径已登记时返回已有目录，停用的保持停用。 */
export const decodeRootCreated = d.defineDecoder(
  d.object({ id: d.uuid(), name: d.string({ min: 1 }), status: d.oneOf(rootStatuses), revision: d.integer({ min: 1 }) }),
);

/** 停用与恢复的结果。 */
const rootChangedDecoder = d.object({
  id: d.uuid(),
  path: d.string({ min: 1 }),
  status: d.oneOf(rootStatuses),
  revision: d.integer({ min: 1 }),
});

export const decodeRootChanged = d.defineDecoder(rootChangedDecoder);
export type RootChangedDTO = d.Infer<typeof rootChangedDecoder>;

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

const scanDecoder = d.object({
  id: d.uuid(),
  scan_run_id: d.uuid(),
  status: d.oneOf(scanStatuses),
  started_at: d.timestamp(),
  finished_at: d.nullable(d.timestamp()),
  cancel_requested_at: d.nullable(d.timestamp()),
  diagnostics: diagnosticsSummaryDecoder,
});

export const decodeScan = d.defineDecoder(scanDecoder);
export type ScanDTO = d.Infer<typeof scanDecoder>;
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
