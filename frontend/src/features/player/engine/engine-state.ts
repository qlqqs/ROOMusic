// 播放引擎的显式状态：挂载生命周期、恢复阶段与加载管线。每个联合只表达可达的组合，status 与管线不再各自变化。

/**
 * 挂载生命周期。fresh：还没挂载过（第一次 attach 时恢复刷新前的队列）；attached：监听媒体事件与系统媒体键；
 * detached：卸载过，可以再次 attach（开发模式下 React 会模拟卸载再挂载），不再恢复；ended：会话已结束，不再接受命令，也不再写存储。
 */
export type Lifecycle = "fresh" | "attached" | "detached" | "ended";

/**
 * 刷新后恢复队列的阶段。running：正在按发行读取详情，队列还是空的；retryable：读取遇到暂时性失败，重试会重新恢复。
 * 两者都挡住保存（此时保存会删掉刷新前保存的队列）。用户操作队列即回到 none，恢复的结果不再覆盖用户的队列。
 */
export type RestorePhase = { kind: "none" } | { kind: "running" } | { kind: "retryable" };

/** 等待预检结果的方式：请求在途（可中止），或收到“正在准备”之后等待发出下一次预检的定时器。 */
export type PreflightWait = { kind: "inflight"; controller: AbortController } | { kind: "timer"; handle: ReturnType<typeof setTimeout> };

/**
 * 加载管线与播放状态。status 收在各变体里，只能取该变体可达的值：
 * - stopped：既没有预检也没有媒体，空闲（没有当前项）或暂停；
 * - requesting：预检（含等待转换）进行中，此时一定没有媒体；loading 是普通预检，preparing 是服务端正在转换。sentAt 是最近一次预检发出的时刻；
 * - media：媒体元素已设置来源，entryId 与 src 标识这份媒体（迟到的旧媒体事件据此忽略）；pendingSeek 是等时长与可定位范围都已知后才定位的开始位置。
 */
export type Pipeline =
  | { kind: "stopped"; status: "idle" | "paused" }
  | { kind: "requesting"; status: "loading" | "preparing"; sentAt: number; wait: PreflightWait }
  | { kind: "media"; status: "loading" | "playing" | "paused" | "buffering"; entryId: string; src: string; pendingSeek: number | null };

export type StoppedPipeline = Extract<Pipeline, { kind: "stopped" }>;
export type MediaPipeline = Extract<Pipeline, { kind: "media" }>;

/** 发出预检时的管线：请求在途，之后用 controller 中止。 */
export function requesting(status: "loading" | "preparing", sentAt: number, controller: AbortController): Pipeline {
  return { kind: "requesting", status, sentAt, wait: { kind: "inflight", controller } };
}

/** 停下的管线：有当前项时暂停，没有时空闲。 */
export function stopped(currentEntryId: string | null): StoppedPipeline {
  return { kind: "stopped", status: currentEntryId === null ? "idle" : "paused" };
}

/** 穷尽检查：所有分支都处理过时，这里的参数类型是 never。 */
export function unreachable(value: never): never {
  throw new Error(`未处理的状态：${String(value)}`);
}
