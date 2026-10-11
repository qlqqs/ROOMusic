import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../shared/api";
import type { PlayReport } from "./api/endpoints";
import { createPlayReporter, MAX_PENDING_REPORTS, RETRY_DELAYS_MS, type PlayReporterDeps } from "./play-reporter";

const report: PlayReport = { trackId: "00000000-0000-4000-8000-000000000003", playedSeconds: 120 };
const httpError = (status: number, code: string) => new ApiError({ kind: "http", status, code, message: "后端文案", requestId: "req-1" });
const networkError = () => new ApiError({ kind: "network", requestId: "req-1" });
const invalidResponse = () => new ApiError({ kind: "invalid_response", status: 200, requestId: "req-1" });

/** 让已排队的 Promise 回调执行完，不推进计时器。 */
async function settle(): Promise<void> {
  for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
}

function setup(send: PlayReporterDeps["send"]) {
  let counter = 0;
  const onRecorded = vi.fn<() => void>();
  const sendSpy = vi.fn(send);
  const reporter = createPlayReporter({ send: sendSpy, onRecorded, newKey: () => `key-${++counter}` });
  return { reporter, onRecorded, send: sendSpy, keysSent: () => sendSpy.mock.calls.map(([, key]) => key) };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("播放上报器", () => {
  it("上报带着曲目、秒数、幂等键与取消信号；成功后通知一次，不再有任何定时器", async () => {
    const { reporter, onRecorded, send } = setup(async () => ({}));
    reporter.report(report);
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(report, "key-1", expect.any(AbortSignal));
    await settle();
    expect(onRecorded).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("结果不确定的失败按 2、10、30 秒各重试一次，沿用同一个幂等键，用完后放弃且不通知", async () => {
    expect(RETRY_DELAYS_MS).toEqual([2_000, 10_000, 30_000]);
    const { reporter, onRecorded, send, keysSent } = setup(async () => {
      throw networkError();
    });
    reporter.report(report);
    await settle();
    expect(send).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1_999);
    expect(send).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(send).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(9_999);
    expect(send).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(send).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(29_999);
    expect(send).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(1);
    expect(send).toHaveBeenCalledTimes(4);
    await vi.advanceTimersByTimeAsync(600_000);
    expect(send).toHaveBeenCalledTimes(4);
    expect(keysSent()).toEqual(["key-1", "key-1", "key-1", "key-1"]);
    expect(onRecorded).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("重试到某一次成功就停：通知一次，之后不再发送", async () => {
    let failures = 2;
    const { reporter, onRecorded, send } = setup(async () => {
      if (failures > 0) {
        failures -= 1;
        throw networkError();
      }
      return {};
    });
    reporter.report(report);
    await vi.advanceTimersByTimeAsync(2_000);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(send).toHaveBeenCalledTimes(3);
    expect(onRecorded).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(600_000);
    expect(send).toHaveBeenCalledTimes(3);
    expect(onRecorded).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["网络失败", networkError()],
    ["服务端 500", httpError(500, "internal_error")],
    ["服务暂不可用 503", httpError(503, "database_unavailable")],
    ["不能信任的响应", invalidResponse()],
  ])("%s：结果不确定，重试", async (_name, error) => {
    const { reporter, send } = setup(async () => {
      throw error;
    });
    reporter.report(report);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["400 低于门槛", httpError(400, "play_too_short")],
    ["400 请求不合法", httpError(400, "invalid_input")],
    ["401 未登录（会话边界另行处理）", httpError(401, "unauthorized")],
    ["403 跨源", httpError(403, "forbidden")],
    ["404 曲目不存在", httpError(404, "not_found")],
    ["409 幂等键冲突", httpError(409, "idempotency_conflict")],
    ["429", httpError(429, "too_many_requests")],
    ["调用方取消", new ApiError({ kind: "aborted" })],
    ["不是 ApiError 的错误", new Error("意外")],
  ])("%s：明确的拒绝或不可重试的错误，只发一次，不重试、不通知", async (_name, error) => {
    const { reporter, onRecorded, send } = setup(async () => {
      throw error;
    });
    reporter.report(report);
    await settle();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(600_000);
    expect(send).toHaveBeenCalledTimes(1);
    expect(onRecorded).not.toHaveBeenCalled();
  });

  it("send 同步抛出也不让 report 抛出，按不可重试的错误放弃", async () => {
    const { reporter, onRecorded, send } = setup(() => {
      throw new Error("意外");
    });
    expect(() => reporter.report(report)).not.toThrow();
    await settle();
    expect(send).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
    expect(onRecorded).not.toHaveBeenCalled();
  });

  it("每次上报各有自己的幂等键，同一次上报的重试共用它", async () => {
    const { reporter, keysSent } = setup(async () => {
      throw networkError();
    });
    reporter.report(report);
    reporter.report({ ...report, playedSeconds: 130 });
    await vi.advanceTimersByTimeAsync(2_000);
    expect(keysSent()).toEqual(["key-1", "key-2", "key-1", "key-2"]);
  });

  describe("作废（会话结束）", () => {
    it("中止在途的请求：之后它即使成功也不通知", async () => {
      let finish: () => void = () => undefined;
      const { reporter, onRecorded, send } = setup(() => new Promise((resolve) => (finish = () => resolve({}))));
      reporter.report(report);
      const signal = send.mock.calls[0]?.[2];
      expect(signal?.aborted).toBe(false);
      reporter.dispose();
      expect(signal?.aborted).toBe(true);
      finish();
      await settle();
      expect(onRecorded).not.toHaveBeenCalled();
    });

    it("取消等待中的重试：定时器清零，之后不再发送", async () => {
      const { reporter, send } = setup(async () => {
        throw networkError();
      });
      reporter.report(report);
      reporter.report(report);
      await settle();
      expect(vi.getTimerCount()).toBe(2);
      reporter.dispose();
      expect(vi.getTimerCount()).toBe(0);
      await vi.advanceTimersByTimeAsync(600_000);
      expect(send).toHaveBeenCalledTimes(2);
    });

    it("作废之后才到达的失败不会再安排重试，哪怕它是结果不确定的网络失败", async () => {
      let abandon: (error: unknown) => void = () => undefined;
      const { reporter, send } = setup(() => new Promise((_resolve, reject) => (abandon = reject)));
      reporter.report(report);
      reporter.dispose();
      abandon(networkError());
      await settle();
      expect(vi.getTimerCount()).toBe(0);
      await vi.advanceTimersByTimeAsync(600_000);
      expect(send).toHaveBeenCalledTimes(1);
    });

    it("作废之后的上报被忽略，重复作废安全", async () => {
      const { reporter, send } = setup(async () => ({}));
      reporter.dispose();
      reporter.dispose();
      reporter.report(report);
      await settle();
      expect(send).not.toHaveBeenCalled();
    });
  });

  describe("上限", () => {
    it(`同时等待结果或重试的上报最多 ${MAX_PENDING_REPORTS} 个，超出的放弃；有空位后再接受`, async () => {
      const finishers: (() => void)[] = [];
      const { reporter, send } = setup(() => new Promise((resolve) => finishers.push(() => resolve({}))));
      for (let index = 0; index < MAX_PENDING_REPORTS + 5; index += 1) reporter.report(report);
      expect(send).toHaveBeenCalledTimes(MAX_PENDING_REPORTS);
      finishers[0]?.();
      await settle();
      reporter.report(report);
      expect(send).toHaveBeenCalledTimes(MAX_PENDING_REPORTS + 1);
    });

    it("等待重试的上报也占名额", async () => {
      const { reporter, send } = setup(async () => {
        throw networkError();
      });
      for (let index = 0; index < MAX_PENDING_REPORTS; index += 1) reporter.report(report);
      await settle();
      reporter.report(report);
      expect(send).toHaveBeenCalledTimes(MAX_PENDING_REPORTS);
    });
  });
});
