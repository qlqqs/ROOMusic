import type { ApiFailure } from "./errors";

export type FailureContext = "read" | "write";

export type FailureTextOptions = {
  /** 写请求遇到网络失败时结果未知，措辞不同于读请求。 */
  context?: FailureContext | undefined;
  /** 按错误码覆盖后端错误的文案；只对 http 失败生效。 */
  messages?: Readonly<Record<string, string>> | undefined;
};

export type FailureDescription = { message: string; requestId: string | null };

const httpMessages: Readonly<Record<string, string>> = {
  unauthorized: "登录已失效，请重新登录。",
  forbidden: "你没有权限执行此操作。",
  not_found: "请求的内容不存在或已被移除。",
  method_not_allowed: "服务器不支持此操作。",
  database_unavailable: "服务暂时不可用，请稍后重试。",
  // 写请求的 Origin 与服务器信任的访问地址不一致（例如经 IP 访问而部署配置了公网地址）。
  origin_forbidden: "请用正确的网址打开页面后重试。",
  origin_required: "请用正确的网址打开页面后重试。",
};

/** 把请求失败转成用户可读的简体中文；界面按错误码选文案，不比较后端 message 的字符串。 */
export function describeFailure(failure: ApiFailure, options: FailureTextOptions = {}): FailureDescription {
  switch (failure.kind) {
    case "aborted":
      return { message: "请求已取消。", requestId: null };
    case "network":
      return {
        message:
          options.context === "write"
            ? "连接断了，不确定操作有没有完成。请先刷新看看，再决定要不要重试。"
            : "无法连接到服务器，请检查网络后重试。",
        requestId: failure.requestId,
      };
    case "invalid_response":
      return {
        message: "服务器返回的内容无法识别，请稍后重试。",
        requestId: failure.requestId,
      };
    case "http": {
      const requestId = failure.requestId === "" ? null : failure.requestId;
      const override = options.messages?.[failure.code];
      if (override !== undefined) return { message: override, requestId };
      const known = httpMessages[failure.code];
      if (known !== undefined) return { message: known, requestId };
      if (failure.status >= 500) return { message: "服务器出错了，请稍后重试。", requestId };
      return { message: failure.message === "" ? "请求未能完成。" : failure.message, requestId };
    }
  }
}
