import { decode as d } from "../../../shared/api";

export const backupSources = ["scheduled", "manual", "pre_migration", "pre_restore"] as const;
export type BackupSource = (typeof backupSources)[number];

export const backupDirectoryStates = ["ready", "missing", "not_writable"] as const;
export const backupFailureStages = ["directory", "prepare", "pg_dump", "verify", "publish"] as const;

/** 列表接口最多返回最近 500 份；total 是目录里的全部份数。 */
export const BACKUP_LIST_LIMIT = 500;

// 文件名由后端的命名规则限定（只有 ASCII 字母、数字、连字符与点），下载路径直接用它。
const backupFileDecoder = d.object({
  name: d.string({ pattern: /^roomusic-[0-9]{8}-[0-9]{6}(-manual|-pre-migration-[1-9][0-9]{0,9}|-pre-restore)?\.dump$/ }),
  source: d.oneOf(backupSources),
  created_at: d.timestamp(),
  size_bytes: d.integer({ min: 0 }),
});

// last_failure 只在它晚于最近一次成功时出现；next_scheduled_at 在调度没有启动时为 null。
const backupStatusDecoder = d.object({
  running: d.nullable(d.object({ source: d.oneOf(backupSources), started_at: d.timestamp() })),
  directory: d.oneOf(backupDirectoryStates),
  last_success_at: d.nullable(d.timestamp()),
  last_failure: d.nullable(d.object({ at: d.timestamp(), stage: d.oneOf(backupFailureStages), source: d.oneOf(backupSources) })),
  next_scheduled_at: d.nullable(d.timestamp()),
});

/** 保留天数与保留份数的取值范围；null 表示不限。 */
export const RETENTION_DAYS_RANGE = { min: 1, max: 3650 } as const;
export const RETENTION_COUNT_RANGE = { min: 1, max: 1000 } as const;

// 自动清理的规则：两项都是上限，null 表示不限。
const backupRetentionDecoder = d.object({
  keep_days: d.nullable(d.integer(RETENTION_DAYS_RANGE)),
  keep_count: d.nullable(d.integer(RETENTION_COUNT_RANGE)),
});

// retention 为 null：服务器读不到保存的规则（规则文件损坏），重新保存即可覆盖。
const backupOverviewDecoder = d.object({
  status: backupStatusDecoder,
  retention: d.nullable(backupRetentionDecoder),
  backups: d.array(backupFileDecoder, { max: BACKUP_LIST_LIMIT }),
  total: d.integer({ min: 0 }),
});

export const decodeBackupOverview = d.defineDecoder(backupOverviewDecoder);
export type BackupOverviewDTO = d.Infer<typeof backupOverviewDecoder>;
export type BackupStatusDTO = d.Infer<typeof backupStatusDecoder>;
export type BackupFileDTO = d.Infer<typeof backupFileDecoder>;
export type BackupRetentionDTO = d.Infer<typeof backupRetentionDecoder>;

/** 一次最多删除的份数，与列表上限相同（全选正好不超过）。 */
export const BACKUP_DELETE_LIMIT = BACKUP_LIST_LIMIT;

// POST /system/backups/deletions：每个名字恰好在一个名单里。missing（名字不合规或已经不在）按成功处理，failed 是删除时目录报错的。
const backupNames = d.array(d.string({ max: 255 }), { max: BACKUP_DELETE_LIMIT });
const backupDeletionsDecoder = d.object({
  deleted: backupNames,
  missing: backupNames,
  failed: backupNames,
  overview: backupOverviewDecoder,
});

export const decodeBackupDeletions = d.defineDecoder(backupDeletionsDecoder);
export type BackupDeletionsDTO = d.Infer<typeof backupDeletionsDecoder>;

// PUT /system/backups/retention：不带确认而会删除文件时，服务器什么都不改，只回答将删除几份；否则已保存并按新规则清理，回答实际删除与没能删除的份数。
const retentionConfirmationDecoder = d.object({ confirmation_required: d.literal(true), deletions: d.integer({ min: 1 }) });
const retentionSavedDecoder = d.object({
  confirmation_required: d.literal(false),
  deleted: d.integer({ min: 0 }),
  failed: d.integer({ min: 0 }),
  overview: backupOverviewDecoder,
});
const retentionSaveTag = d.object({ confirmation_required: d.boolean() });

export type RetentionSaveDTO = d.Infer<typeof retentionConfirmationDecoder> | d.Infer<typeof retentionSavedDecoder>;

export const decodeRetentionSave = d.defineDecoder((input, path): RetentionSaveDTO =>
  retentionSaveTag(input, path).confirmation_required ? retentionConfirmationDecoder(input, path) : retentionSavedDecoder(input, path),
);

/** POST /system/backups：202 开始了一份手动备份，200 已有备份在进行（返回的是那一份的状态）。 */
const backupCommandDecoder = d.object({ status: backupStatusDecoder });

export const decodeBackupCommand = d.defineDecoder(backupCommandDecoder);
export type BackupCommandDTO = d.Infer<typeof backupCommandDecoder>;

// 开发构建没有注入版本号，两个字段都是 null。
const buildInfoDecoder = d.object({
  version: d.nullable(d.string({ pattern: /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/ })),
  commit: d.nullable(d.string({ pattern: /^[0-9a-f]{40}$/ })),
});

export const decodeBuildInfo = d.defineDecoder(buildInfoDecoder);
export type BuildInfoDTO = d.Infer<typeof buildInfoDecoder>;

// 诊断报告（仅管理员）：整份原样交给管理员粘贴进 issue，界面不读其中字段。
// 这里只核对外形（生成时间与三个分区），结果是排版好的 JSON 文本；后端加字段不影响前端。
export type DiagnosticsReport = { generatedAt: string; text: string };

const diagnosticsShape = d.object({
  generated_at: d.timestamp(),
  status: (input: unknown, path: string) => {
    if (typeof input !== "object" || input === null || Array.isArray(input)) throw new d.DecodeError(path, "应为对象");
    return input;
  },
  disks: (input: unknown, path: string) => {
    if (!Array.isArray(input)) throw new d.DecodeError(path, "应为数组");
    return input;
  },
  logs: (input: unknown, path: string) => {
    if (!Array.isArray(input)) throw new d.DecodeError(path, "应为数组");
    return input;
  },
});

export const decodeDiagnosticsReport = d.defineDecoder((input, path): DiagnosticsReport => {
  const report = diagnosticsShape(input, path);
  return { generatedAt: report.generated_at, text: JSON.stringify(input, null, 2) };
});

// 页面报错上报：成功是 204，没有响应体。
export const decodeNoContent = d.defineDecoder((): null => null);
