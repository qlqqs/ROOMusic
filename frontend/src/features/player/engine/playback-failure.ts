// DOMException 等错误的名字（NotAllowedError、AbortError……）：输出端据此区分“浏览器拒绝自动播放”与被新的加载打断。

/** 不依赖 instanceof：不同运行环境里的 DOMException 不一定继承同一个 Error。 */
export function errorName(error: unknown): string {
  return typeof error === "object" && error !== null && "name" in error ? String(error.name) : "";
}
