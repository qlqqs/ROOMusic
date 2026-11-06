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

const backupOverviewDecoder = d.object({
  status: backupStatusDecoder,
  backups: d.array(backupFileDecoder, { max: BACKUP_LIST_LIMIT }),
  total: d.integer({ min: 0 }),
});

export const decodeBackupOverview = d.defineDecoder(backupOverviewDecoder);
export type BackupOverviewDTO = d.Infer<typeof backupOverviewDecoder>;
export type BackupStatusDTO = d.Infer<typeof backupStatusDecoder>;
export type BackupFileDTO = d.Infer<typeof backupFileDecoder>;

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
