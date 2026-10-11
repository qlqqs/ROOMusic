import { failureOf, isOutcomeUncertain } from "../../shared/api";
import type { PlayReport } from "./api/endpoints";

/** 失败后的重试间隔：2、10、30 秒，各一次，共 3 次，沿用同一个幂等键（后端按键只写一行）。 */
export const RETRY_DELAYS_MS = [2_000, 10_000, 30_000] as const;

/** 同时等待结果或重试的上报上限：只有服务长时间不可用、又连续播放极短的曲目时才会触及，超出的直接放弃。 */
export const MAX_PENDING_REPORTS = 50;

export type PlayReporterDeps = {
  /** 发出一次上报（POST /me/plays）；带幂等键，signal 被取消时应当拒绝。 */
  send: (report: PlayReport, idempotencyKey: string, signal: AbortSignal) => Promise<unknown>;
  /** 一次上报成功之后：让“最近播放”的查询失效，概览下次显示时重新读取。 */
  onRecorded: () => void;
  /** 为“这一次播放”生成幂等键；同一次播放的所有重试共用它。 */
  newKey: () => string;
};

export type PlayReporter = {
  /** 上报一次达到计次规则的播放。不抛出、不等待结果，失败也不通知用户（记录只是信息性的）。 */
  report: (report: PlayReport) => void;
  /** 作废所有未完成的上报：中止在途请求、取消等待中的重试，之后的 report 被忽略。会话结束时调用，上一位用户的记录不会在新会话下发出。 */
  dispose: () => void;
};

type Job = { controller: AbortController; timer: ReturnType<typeof setTimeout> | null };

/**
 * 播放上报器：每次 report 生成新的幂等键并上报。结果不确定的失败（网络失败、无效响应、5xx，见 isOutcomeUncertain）按 RETRY_DELAYS_MS 重试，
 * 同一个键，所以后端不会重复计数；4xx（含 400 低于门槛、404、409）是明确的拒绝，401 已由请求层通知会话边界，这些都直接放弃。
 * 页面关闭时还没成功的记录随页面消失，不写浏览器存储（P5）。
 */
export function createPlayReporter(deps: PlayReporterDeps): PlayReporter {
  let disposed = false;
  const pending = new Set<Job>();

  const finish = (job: Job): void => {
    if (job.timer !== null) clearTimeout(job.timer);
    pending.delete(job);
  };

  const attempt = (job: Job, report: PlayReport, key: string, retriesDone: number): void => {
    const controller = new AbortController();
    job.controller = controller;
    job.timer = null;
    let sending: Promise<unknown>;
    try {
      sending = deps.send(report, key, controller.signal);
    } catch (error) {
      sending = Promise.reject(error); // report 承诺不抛出：同步抛出的错误与异步失败一样处理
    }
    sending.then(
      () => {
        if (disposed || controller.signal.aborted) return;
        finish(job);
        deps.onRecorded();
      },
      (error: unknown) => {
        if (disposed || controller.signal.aborted) return;
        const delay = RETRY_DELAYS_MS[retriesDone];
        if (delay === undefined || !isOutcomeUncertain(failureOf(error))) {
          finish(job);
          return;
        }
        job.timer = setTimeout(() => {
          job.timer = null;
          if (!disposed) attempt(job, report, key, retriesDone + 1);
        }, delay);
      },
    );
  };

  return {
    report: (report) => {
      if (disposed || pending.size >= MAX_PENDING_REPORTS) return;
      const job: Job = { controller: new AbortController(), timer: null };
      pending.add(job);
      attempt(job, report, deps.newKey(), 0);
    },
    dispose: () => {
      disposed = true;
      for (const job of pending) {
        job.controller.abort();
        if (job.timer !== null) clearTimeout(job.timer);
      }
      pending.clear();
    },
  };
}
