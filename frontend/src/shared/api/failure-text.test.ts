import { describe, expect, it } from "vitest";
import { describeFailure } from "./failure-text";

describe("describeFailure", () => {
  it("网络失败：读请求提示重试，写请求说明结果未知", () => {
    const failure = { kind: "network", requestId: "web-1" } as const;
    expect(describeFailure(failure).message).toContain("无法连接到服务器");
    const write = describeFailure(failure, { context: "write" });
    expect(write.message).toContain("不确定操作有没有完成");
    expect(write.requestId).toBe("web-1");
  });

  it("无效响应带请求 ID", () => {
    const description = describeFailure({ kind: "invalid_response", status: 200, requestId: "web-2" });
    expect(description.message).toContain("无法识别");
    expect(description.requestId).toBe("web-2");
  });

  it("按错误码选文案，已知通用错误码有固定文案", () => {
    const http = (code: string, status: number, message = "后端文案") =>
      ({ kind: "http", status, code, message, requestId: "web-3" }) as const;
    expect(describeFailure(http("forbidden", 403)).message).toBe("你没有权限执行此操作。");
    expect(describeFailure(http("database_unavailable", 503)).message).toBe("服务暂时不可用，请稍后重试。");
    expect(describeFailure(http("unauthorized", 401)).message).toBe("登录已失效，请重新登录。");
    expect(describeFailure(http("origin_forbidden", 403)).message).toBe("请用正确的网址打开页面后重试。");
    expect(describeFailure(http("origin_required", 403)).message).toBe("请用正确的网址打开页面后重试。");
  });

  it("feature 提供的错误码文案优先于通用文案", () => {
    const failure = { kind: "http", status: 409, code: "revision_conflict", message: "后端文案", requestId: "web-4" } as const;
    const description = describeFailure(failure, { messages: { revision_conflict: "目录已变化，已刷新。" } });
    expect(description).toEqual({ message: "目录已变化，已刷新。", requestId: "web-4" });
  });

  it("未知错误码：4xx 显示后端的 message，5xx 用通用文案", () => {
    const unknown4xx = { kind: "http", status: 400, code: "new_code", message: "后端给出的安全文案", requestId: "web-5" } as const;
    expect(describeFailure(unknown4xx).message).toBe("后端给出的安全文案");
    const unknown5xx = { kind: "http", status: 500, code: "new_code", message: "内部细节", requestId: "web-6" } as const;
    expect(describeFailure(unknown5xx).message).toBe("服务器出错了，请稍后重试。");
  });

  it("空 message 与空请求 ID 有安全回退", () => {
    const failure = { kind: "http", status: 400, code: "new_code", message: "", requestId: "" } as const;
    expect(describeFailure(failure)).toEqual({ message: "请求未能完成。", requestId: null });
  });

  it("取消没有请求 ID", () => {
    expect(describeFailure({ kind: "aborted" }).requestId).toBeNull();
  });
});
