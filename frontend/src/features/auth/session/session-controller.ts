import type { ApiFailure } from "../../../shared/api";
import { failureOf, isHttpFailure } from "../../../shared/api";
import type { ViewerDTO } from "../api/decoders";
import {
  fetchCurrentViewer,
  fetchSetupStatus,
  postLogin,
  postLogout,
  postSetupAdmin,
  type Credentials,
} from "../api/endpoints";
import type { SessionChannel } from "./session-channel";

/** 会话在登录页上给出的说明：为什么回到了登录页。 */
export type SessionNotice = "signed_out" | "expired" | "setup_complete" | "setup_closed";

export type SessionState =
  | { status: "checking" }
  | { status: "unavailable"; failure: ApiFailure }
  | { status: "setup_required" }
  | { status: "anonymous"; notice: SessionNotice | null; username: string | null }
  | { status: "authenticated"; viewer: ViewerDTO; generation: number };

/** 已登录的会话结束的原因：主动退出、失效（401）、或检测到另一个标签页换了用户。 */
export type SessionEndReason = "signed_out" | "expired" | "switched";

export type SessionController = {
  getSnapshot: () => SessionState;
  subscribe: (listener: () => void) => () => void;
  /** 当前会话代次：进入与离开已登录状态时各加一。请求发出时读取，用来识别迟到的结果。 */
  currentGeneration: () => number;
  /** 启动：先查初始化状态，再用 /auth/me 确认会话。失败进入 unavailable，可用 retryStart 重试。 */
  start: () => Promise<void>;
  retryStart: () => Promise<void>;
  /** 登录失败时抛出 ApiError，状态保持不变。 */
  login: (credentials: Credentials) => Promise<void>;
  /** 创建管理员并用同一凭据登录；创建失败时抛出 ApiError，自动登录失败时回到登录页。 */
  setupAdmin: (credentials: Credentials) => Promise<void>;
  /** 后端确认后才结束会话；失败时抛出 ApiError，状态保持不变。 */
  logout: () => Promise<void>;
  /** 请求层在受保护请求得到 401 时调用；属于旧代次的 401 被忽略。 */
  handleUnauthorized: (generation: number) => void;
  /** 向后端核对会话是否仍是当前用户；瞬时失败时保持原状态。 */
  recheck: () => Promise<void>;
  /** 距上次核对已超过 minAgeMs 才核对：页面重新可见时用，避免频繁请求。 */
  recheckIfStale: (minAgeMs: number) => Promise<void>;
  /** 离开已登录状态（退出、失效、换用户）时调用，此时新状态已发布。应用在这里取消并清空查询缓存。 */
  onEnded: (handler: (event: { reason: SessionEndReason }) => void) => () => void;
  dispose: () => void;
};

type Resolved = { kind: "setup_required" } | { kind: "anonymous" } | { kind: "authenticated"; viewer: ViewerDTO };

function sameViewer(left: ViewerDTO, right: ViewerDTO): boolean {
  return left.username === right.username && left.role === right.role;
}

export function createSessionController(
  options: { now?: () => number; channel?: SessionChannel | null } = {},
): SessionController {
  const now = options.now ?? Date.now;
  const channel = options.channel ?? null;

  let state: SessionState = { status: "checking" };
  let generation = 0;
  let lastCheckedAt = now();
  let queue: Promise<void> = Promise.resolve();
  const listeners = new Set<() => void>();
  const endedHandlers = new Set<(event: { reason: SessionEndReason }) => void>();

  // 所有状态转换串行执行：前一个结束才处理下一个，登录与退出不会交错。
  function enqueue<T>(task: () => Promise<T>): Promise<T> {
    const run = queue.then(task, task);
    queue = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  /**
   * 发布新状态：进入与离开已登录状态时各递增一次代次；订阅者同步收到通知，
   * 随后（仍在同一个同步块里，React 重新渲染之前）通知“已结束”处理器取消并清空查询缓存。
   * 离开已登录状态一定会通知处理器，没有说明原因时按失效处理。
   */
  function commit(next: SessionState, meta: { reason?: SessionEndReason; broadcast: boolean }): void {
    const wasAuthenticated = state.status === "authenticated";
    if (wasAuthenticated) generation += 1;
    if (next.status === "authenticated") {
      generation += 1;
      next = { ...next, generation };
    }
    state = next;
    for (const listener of [...listeners]) listener();
    if (wasAuthenticated) {
      const reason = meta.reason ?? "expired";
      for (const handler of [...endedHandlers]) handler({ reason });
    }
    if (meta.broadcast) channel?.notifyChanged();
  }

  function becomeAuthenticated(viewer: ViewerDTO, broadcast: boolean): void {
    commit({ status: "authenticated", viewer, generation: 0 }, { reason: "switched", broadcast });
    lastCheckedAt = now();
  }

  function becomeAnonymous(meta: {
    notice: SessionNotice | null;
    username?: string;
    reason?: SessionEndReason;
    broadcast: boolean;
  }): void {
    const { notice, username = null, ...commitMeta } = meta;
    commit({ status: "anonymous", notice, username }, commitMeta);
  }

  async function resolveSession(signal?: AbortSignal): Promise<Resolved> {
    const { data: setup } = await fetchSetupStatus(signal);
    if (setup.setup_required) return { kind: "setup_required" };
    try {
      const { data: viewer } = await fetchCurrentViewer(signal);
      return { kind: "authenticated", viewer };
    } catch (error) {
      if (isHttpFailure(failureOf(error), 401, "unauthorized")) return { kind: "anonymous" };
      throw error;
    }
  }

  function startOnce(): Promise<void> {
    return enqueue(async () => {
      try {
        const resolved = await resolveSession();
        lastCheckedAt = now();
        if (resolved.kind === "setup_required") commit({ status: "setup_required" }, { broadcast: false });
        else if (resolved.kind === "anonymous") becomeAnonymous({ notice: null, broadcast: false });
        else becomeAuthenticated(resolved.viewer, false);
      } catch (error) {
        const failure = failureOf(error) ?? { kind: "invalid_response", status: 0, requestId: "" };
        commit({ status: "unavailable", failure }, { broadcast: false });
      }
    });
  }

  function applyResolved(resolved: Resolved): void {
    if (resolved.kind === "setup_required") {
      if (state.status !== "setup_required") commit({ status: "setup_required" }, { broadcast: false });
      return;
    }
    if (resolved.kind === "anonymous") {
      if (state.status === "authenticated") becomeAnonymous({ notice: "expired", reason: "expired", broadcast: false });
      else if (state.status !== "anonymous") becomeAnonymous({ notice: null, broadcast: false });
      return;
    }
    if (state.status === "authenticated" && sameViewer(state.viewer, resolved.viewer)) return;
    becomeAuthenticated(resolved.viewer, false);
  }

  const stopListeningToChannel = channel?.onChanged(() => void controller.recheck());

  const controller: SessionController = {
    getSnapshot: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    currentGeneration: () => generation,
    start: startOnce,
    retryStart() {
      return enqueue(async () => {
        commit({ status: "checking" }, { broadcast: false });
      }).then(startOnce);
    },
    login: (credentials) =>
      enqueue(async () => {
        const { data: viewer } = await postLogin(credentials);
        becomeAuthenticated(viewer, true);
      }),
    setupAdmin: (credentials) =>
      enqueue(async () => {
        try {
          await postSetupAdmin(credentials);
        } catch (error) {
          if (isHttpFailure(failureOf(error), 409, "setup_closed")) becomeAnonymous({ notice: "setup_closed", broadcast: false });
          throw error;
        }
        try {
          const { data: viewer } = await postLogin(credentials);
          becomeAuthenticated(viewer, true);
        } catch {
          becomeAnonymous({ notice: "setup_complete", username: credentials.username, broadcast: false });
        }
      }),
    logout: () =>
      enqueue(async () => {
        if (state.status !== "authenticated") return;
        await postLogout();
        becomeAnonymous({ notice: "signed_out", reason: "signed_out", broadcast: true });
      }),
    handleUnauthorized(requestGeneration) {
      if (requestGeneration !== generation || state.status !== "authenticated") return;
      void enqueue(async () => {
        // 排队期间会话可能已经换代（例如刚登录了另一个用户）：旧会话的 401 不能结束新会话。
        if (requestGeneration !== generation || state.status !== "authenticated") return;
        becomeAnonymous({ notice: "expired", reason: "expired", broadcast: true });
      });
    },
    recheck: () =>
      enqueue(async () => {
        if (state.status === "checking" || state.status === "unavailable") return;
        let resolved: Resolved;
        try {
          resolved = await resolveSession();
        } catch {
          return;
        }
        lastCheckedAt = now();
        applyResolved(resolved);
      }),
    recheckIfStale(minAgeMs) {
      return now() - lastCheckedAt >= minAgeMs ? controller.recheck() : Promise.resolve();
    },
    onEnded(handler) {
      endedHandlers.add(handler);
      return () => endedHandlers.delete(handler);
    },
    dispose() {
      stopListeningToChannel?.();
      channel?.close();
      listeners.clear();
      endedHandlers.clear();
    },
  };
  return controller;
}
