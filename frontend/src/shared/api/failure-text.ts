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
            ? "连接中断，无法确认操作是否已经完成。请先刷新核对结果，再决定是否重试。"
            : "无法连接到服务器，请检查网络后重试。",
        requestId: failure.requestId,
      };
    case "invalid_response":
      return {
        message: "服务器返回了无法识别的响应，请稍后重试。若问题持续，请把请求 ID 提供给管理员。",
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
