import { sampleBody } from "./api-samples";
import { decodeBackupOverview, decodeBuildInfo, BACKUP_LIST_LIMIT, type BackupFileDTO, type BackupSource, type BackupStatusDTO, type BuildInfoDTO } from "../features/system/api/decoders";
import { failure, type ApiResult } from "./catalog-data";

// “系统 › 备份”接口（版本、备份状态与列表、立即备份）的共享纯替身，依据 builtin-backup 的 design.md §3.1。
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
};

/** 四种来源各一份，与后端样本 system/backups.json 的安排相同：旧命名的定时与手动、迁移前、恢复前。 */
export function seedBackups(): BackupFileDTO[] {
  return decodeBackupOverview(sampleBody("system/backups")).backups;
}

export function systemDataOf(overrides: Partial<SystemData> = {}): SystemData {
  return {
    version: decodeBuildInfo(sampleBody("system/version-dev")),
    status: {
      ...decodeBackupOverview(sampleBody("system/backups")).status,
      last_failure: null,
      next_scheduled_at: "2026-11-06T19:00:00Z",
    },
    backups: seedBackups(),
    total: null,
    backupFinishesAfterReads: 2,
    busy: false,
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
export function respondToSystem(data: SystemData, method: string, path: string, now: () => string): ApiResult | null {
  if (method === "GET" && path === "/api/v1/system/version") return { status: 200, body: data.version };
  if (method === "GET" && path === "/api/v1/system/backups") {
    advance(data, now);
    return {
      status: 200,
      body: { status: data.status, backups: data.backups.slice(0, BACKUP_LIST_LIMIT), total: data.total ?? data.backups.length },
    };
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
