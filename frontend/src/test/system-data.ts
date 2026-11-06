import { sampleBody } from "./api-samples";
import {
  decodeBackupOverview,
  decodeBuildInfo,
  BACKUP_DELETE_LIMIT,
  BACKUP_LIST_LIMIT,
  RETENTION_COUNT_RANGE,
  RETENTION_DAYS_RANGE,
  type BackupFileDTO,
  type BackupOverviewDTO,
  type BackupRetentionDTO,
  type BackupSource,
  type BackupStatusDTO,
  type BuildInfoDTO,
} from "../features/system/api/decoders";
import { failure, type ApiResult } from "./catalog-data";

// “设置 › 备份”接口（版本、备份状态与列表、立即备份、删除与批量删除、自动清理规则）的共享纯替身，
// 依据 builtin-backup 的 design.md §3.1 与 settings-page-and-user-menu 的 design.md §1、§1b、§1c。
// 只依赖解码器、样本读取与纯替身，不依赖 vitest 或界面代码：Vitest 的替身（fake-system.ts）与 Playwright 的受控后端共用。
// 文件、状态与开发版种子取自后端集成测试生成的真实API样本，并通过正式解码器校验。

export type SystemData = {
  version: BuildInfoDTO;
  status: BackupStatusDTO;
  /** 最新的在前，与后端一致；接口只返回前 500 份。 */
  backups: BackupFileDTO[];
  /** 目录里的备份总数；null 表示等于 backups 的份数。 */
  total: number | null;
  /** 进行中的备份被读取这么多次之后完成（不依赖真实时间）；null 表示一直进行。 */
  backupFinishesAfterReads: number | null;
  /** 另一个进程持有备份锁：立即备份返回 409 backup_busy。 */
  busy: boolean;
  /** 当前的自动清理规则；null 表示服务器读不到规则文件。 */
  retention: BackupRetentionDTO | null;
  /** 删除时目录报错的文件名：单删 503 backup_directory_not_writable，批量删除列入 failed，保存规则后的清理计入 failed。 */
  undeletable: ReadonlySet<string>;
  /** 规则文件写不进：保存规则返回 503 backup_directory_not_writable，什么都不删。 */
  retentionNotWritable: boolean;
};

/**
 * 读备份列表样本。样本录制早于 Overview 加 retention 字段时没有这个字段；替身只用样本里的状态与文件，缺的 retention 按 null 补上。
 * 样本本身是否符合当前合同，由样本解码测试检查。
 */
function overviewSample(name: string): BackupOverviewDTO {
  const body = sampleBody(name);
  return decodeBackupOverview(typeof body === "object" && body !== null ? { retention: null, ...body } : body);
}

/** 四种来源各一份，与后端样本 system/backups.json 的安排相同：旧命名的定时与手动、迁移前、恢复前。 */
export function seedBackups(): BackupFileDTO[] {
  return overviewSample("system/backups").backups;
}

export function systemDataOf(overrides: Partial<SystemData> = {}): SystemData {
  return {
    version: decodeBuildInfo(sampleBody("system/version-dev")),
    status: {
      ...overviewSample("system/backups").status,
      last_failure: null,
      next_scheduled_at: "2026-11-06T19:00:00Z",
    },
    backups: seedBackups(),
    total: null,
    backupFinishesAfterReads: 2,
    busy: false,
    retention: { keep_days: 14, keep_count: null },
    undeletable: new Set(),
    retentionNotWritable: false,
    ...overrides,
  };
}

/** 进行中的备份已被读取的次数；按替身对象记，测试之间互不影响。 */
const runningReads = new WeakMap<SystemData, number>();

const suffixes: Record<BackupSource, string> = { scheduled: "", manual: "-manual", pre_migration: "-pre-migration-1", pre_restore: "-pre-restore" };

/** 备份文件名：与后端一样按 UTC+8（默认时区 Asia/Shanghai）的本地时间命名，后缀区分来源。 */
const backupName = (at: string, source: BackupSource) => {
  const local = new Date(Date.parse(at) + 8 * 3_600_000).toISOString();
  return `roomusic-${local.slice(0, 10).replaceAll("-", "")}-${local.slice(11, 19).replaceAll(":", "")}${suffixes[source]}.dump`;
};

/**
 * 一次接口调用。返回 null 表示不是这里的接口。鉴权（未登录 401、非管理员 403、写请求同源）由调用方的门处理。
 * now 是替身的时钟：开始与完成一次备份时取一次。
 */
export function respondToSystem(data: SystemData, method: string, path: string, now: () => string, body: unknown = null): ApiResult | null {
  if (method === "GET" && path === "/api/v1/system/version") return { status: 200, body: data.version };
  if (method === "GET" && path === "/api/v1/system/backups") {
    advance(data, now);
    return { status: 200, body: overviewOf(data) };
  }
  if (method === "POST" && path === "/api/v1/system/backups/deletions") return deleteMany(data, body);
  if (method === "PUT" && path === "/api/v1/system/backups/retention") return saveRetention(data, body, now);
  if (method === "DELETE") {
    const file = backupFileAt(data, path);
    if (file === null) return /^\/api\/v1\/system\/backups\/[^/]+$/.test(path) ? backupNotFound() : null;
    if (data.undeletable.has(file.name)) return notWritable();
    removeFiles(data, [file.name]);
    return { status: 200, body: overviewOf(data) };
  }
  if (method === "POST" && path === "/api/v1/system/backups") {
    if (data.status.running !== null) return { status: 200, body: { status: data.status } };
    if (data.busy) return failure(409, "backup_busy", "另一份备份正在进行，稍后再试");
    data.status = { ...data.status, running: { source: "manual", started_at: now() } };
    runningReads.set(data, 0);
    return { status: 202, body: { status: data.status } };
  }
  return null;
}

/** 下载接口要找的文件：路径段解码后必须与某份备份的名字完全相同，否则是 404 backup_not_found（不是 JSON 的成功响应由调用方构造）。 */
export function backupFileAt(data: SystemData, path: string): BackupFileDTO | null {
  const match = /^\/api\/v1\/system\/backups\/([^/]+)$/.exec(path);
  if (match === null) return null;
  let name: string;
  try {
    name = decodeURIComponent(match[1] ?? "");
  } catch {
    return null;
  }
  return data.backups.find((file) => file.name === name) ?? null;
}

export const backupNotFound = (): ApiResult => failure(404, "backup_not_found", "备份不存在");

const notWritable = (): ApiResult => failure(503, "backup_directory_not_writable", "备份目录不能写入");

/** 读取、删除与保存规则的响应共用的 Overview：状态、规则、最近 500 份与总数。 */
function overviewOf(data: SystemData): BackupOverviewDTO {
  return { status: data.status, retention: data.retention, backups: data.backups.slice(0, BACKUP_LIST_LIMIT), total: data.total ?? data.backups.length };
}

function removeFiles(data: SystemData, names: readonly string[]): void {
  const removed = new Set(names);
  const before = data.backups.length;
  data.backups = data.backups.filter((file) => !removed.has(file.name));
  if (data.total !== null) data.total -= before - data.backups.length;
  // 与后端相同：“最近一次成功”按剩下最新的那份算。
  data.status = { ...data.status, last_success_at: data.backups[0]?.created_at ?? null };
}

/** 批量删除：1–500 个字符串，去重后逐个归入 deleted / missing / failed，名单内保持请求顺序。 */
function deleteMany(data: SystemData, body: unknown): ApiResult {
  const names = typeof body === "object" && body !== null && "names" in body ? body.names : undefined;
  if (!Array.isArray(names) || names.length === 0 || names.length > BACKUP_DELETE_LIMIT || !names.every((name) => typeof name === "string")) {
    return failure(400, "invalid_input", "要删除的备份无效");
  }
  const result = { deleted: [] as string[], missing: [] as string[], failed: [] as string[] };
  for (const name of new Set(names as string[])) {
    if (!data.backups.some((file) => file.name === name)) result.missing.push(name);
    else if (data.undeletable.has(name)) result.failed.push(name);
    else result.deleted.push(name);
  }
  removeFiles(data, result.deleted);
  return { status: 200, body: { ...result, overview: overviewOf(data) } };
}

function limitOf(input: Record<string, unknown>, key: string, range: { min: number; max: number }): { ok: true; value: number | null } | { ok: false } {
  if (!Object.hasOwn(input, key)) return { ok: false };
  const value = input[key];
  if (value === null) return { ok: true, value: null };
  return typeof value === "number" && Number.isInteger(value) && value >= range.min && value <= range.max ? { ok: true, value } : { ok: false };
}

/**
 * 与后端 retentionPlan 相同的算法：files 新到旧，第一份（最新）永远保留；其余只要早于 now 减保留天数，或排在第 keep_count 份之后，就删除。
 */
export function retentionPlan(files: readonly BackupFileDTO[], rule: BackupRetentionDTO, now: string): BackupFileDTO[] {
  const cutoff = rule.keep_days === null ? null : Date.parse(now) - rule.keep_days * 86_400_000;
  return files.filter(
    (file, index) => index > 0 && ((cutoff !== null && Date.parse(file.created_at) < cutoff) || (rule.keep_count !== null && index >= rule.keep_count)),
  );
}

/** 保存规则：不带确认而会删除文件时什么都不改，只回答份数；否则保存并按新规则清理（删不掉的计入 failed）。 */
function saveRetention(data: SystemData, body: unknown, now: () => string): ApiResult {
  const invalid = failure(400, "invalid_input", "清理设置无效");
  if (typeof body !== "object" || body === null || Array.isArray(body)) return invalid;
  const input = body as Record<string, unknown>;
  const days = limitOf(input, "keep_days", RETENTION_DAYS_RANGE);
  const count = limitOf(input, "keep_count", RETENTION_COUNT_RANGE);
  const confirm = input["confirm"] ?? false;
  if (!days.ok || !count.ok || typeof confirm !== "boolean") return invalid;
  const rule = { keep_days: days.value, keep_count: count.value };
  const plan = retentionPlan(data.backups, rule, now());
  if (plan.length > 0 && !confirm) return { status: 200, body: { confirmation_required: true, deletions: plan.length } };
  if (data.retentionNotWritable) return notWritable();
  data.retention = rule;
  const failed = plan.filter((file) => data.undeletable.has(file.name));
  removeFiles(data, plan.filter((file) => !data.undeletable.has(file.name)).map((file) => file.name));
  return { status: 200, body: { confirmation_required: false, deleted: plan.length - failed.length, failed: failed.length, overview: overviewOf(data) } };
}

/** 进行中的备份读到第 N 次时完成：新文件排在最前，最近一次成功更新，之前的失败被覆盖。 */
function advance(data: SystemData, now: () => string): void {
  const running = data.status.running;
  if (running === null || data.backupFinishesAfterReads === null) return;
  const reads = (runningReads.get(data) ?? 0) + 1;
  runningReads.set(data, reads);
  if (reads < data.backupFinishesAfterReads) return;
  runningReads.set(data, 0);
  const at = now();
  data.backups = [{ name: backupName(running.started_at, running.source), source: running.source, created_at: at, size_bytes: 1_600_000 }, ...data.backups];
  if (data.total !== null) data.total += 1;
  data.status = { ...data.status, running: null, last_success_at: at, last_failure: null };
}
