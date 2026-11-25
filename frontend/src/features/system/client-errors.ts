import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { isApiError } from "../../shared/api";
import { postClientError, type ClientErrorKind, type ClientErrorReport } from "./api/endpoints";

// 页面报错上报：已登录时把未处理的异常、未处理的 Promise 拒绝与渲染错误发给后端，记进服务器日志，管理员导出诊断时一并带上。
// 请求失败（ApiError）不上报：后端已按请求 ID 记了日志。取消、跨域脚本的“Script error.”与 ResizeObserver 的循环提示是噪声，也不报。

const MAX_REPORTS = 10;
const MAX_MESSAGE = 500;
const NAME = /^[A-Za-z][A-Za-z0-9_$]{0,63}$/;
const ROUTE = /^\/[A-Za-z0-9_$./-]{0,199}$/;
const NOISE = [/^Script error\.?$/, /^ResizeObserver loop/];

type Sender = (kind: ClientErrorKind, error: unknown) => void;

let sender: Sender | null = null;
let sent = 0;
const seen = new Set<string>();

function describe(error: unknown): { name: string; message: string } | null {
  if (isApiError(error)) return null;
  if (error instanceof Error) {
    if (error.name === "AbortError") return null;
    return { name: NAME.test(error.name) ? error.name : "Error", message: error.message };
  }
  return { name: "NonError", message: typeof error === "string" ? error : "" };
}

/** 从 React 根的 onCaughtError / onUncaughtError 调用：渲染错误由错误边界接住，不会冒到 window。 */
export function reportRenderError(error: unknown): void {
  sender?.("render", error);
}

function send(kind: ClientErrorKind, error: unknown, route: string): void {
  const described = describe(error);
  if (described === null || sent >= MAX_REPORTS) return;
  // 按码点截断（接口按 Unicode 码点计数），不把代理对切成半个。
  const message = Array.from(described.message).slice(0, MAX_MESSAGE).join("");
  if (NOISE.some((pattern) => pattern.test(message))) return;
  const report: ClientErrorReport = { kind, name: described.name, message, route: ROUTE.test(route) ? route : "/" };
  const key = `${report.kind}\n${report.name}\n${report.message}\n${report.route}`;
  if (seen.has(key)) return;
  seen.add(key);
  sent += 1;
  // 上报本身失败（离线、限流、会话过期）就算了，不再上报，避免循环。
  postClientError(report).catch(() => undefined);
}

/** 在已登录布局里挂一次：登录期间收集，退出后停止。路由报的是模板（如 /albums/$groupId），不含具体编号与查询串。 */
export function useClientErrorReporting(): void {
  const router = useRouter();
  useEffect(() => {
    const route = () => router.state.matches.at(-1)?.fullPath ?? "/";
    sender = (kind, error) => send(kind, error, route());
    const onError = (event: ErrorEvent) => sender?.("error", event.error ?? new Error(event.message));
    const onRejection = (event: PromiseRejectionEvent) => sender?.("unhandledrejection", event.reason);
    window.addEventListener("error", onError);
    window.addEventListener("unhandledrejection", onRejection);
    return () => {
      sender = null;
      window.removeEventListener("error", onError);
      window.removeEventListener("unhandledrejection", onRejection);
    };
  }, [router]);
}

/** 测试用：清掉本页已发出的计数与去重记录。 */
export function resetClientErrorReporting(): void {
  sent = 0;
  seen.clear();
}
