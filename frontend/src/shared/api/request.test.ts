import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, stubFetch } from "../../test/http";
import { decode as d } from "./index";
import { ApiError, failureOf, type ApiFailure } from "./errors";
import { apiRequest, setSessionBoundary } from "./request";

const decodeOk = d.defineDecoder(d.object({ ok: d.boolean() }));

async function failureFrom(promise: Promise<unknown>): Promise<ApiFailure> {
  try {
    await promise;
  } catch (error) {
    const failure = failureOf(error);
    if (failure !== null) return failure;
    throw error;
  }
  throw new Error("预期请求失败");
}

afterEach(() => {
  setSessionBoundary(null);
});

describe("apiRequest 请求形态", () => {
  it("GET 同源、携带 Cookie、不用缓存，并带客户端请求 ID", async () => {
    const requests = stubFetch(() => jsonResponse(200, { ok: true }));
    const response = await apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeOk);

    expect(response).toEqual({ status: 200, data: { ok: true } });
    expect(requests).toHaveLength(1);
    const [request] = requests;
    expect(request?.path).toBe("/api/v1/auth/me");
    expect(request?.method).toBe("GET");
    expect(request?.headers.get("X-Request-ID")).toMatch(/^web-[0-9a-f]{16}$/);
    expect(request?.headers.get("Accept")).toBe("application/json");
    expect(request?.headers.has("Content-Type")).toBe(false);
    expect(request?.body).toBeUndefined();
    expect(request?.init.credentials).toBe("same-origin");
    expect(request?.init.cache).toBe("no-store");
  });

  it("POST 携带 JSON 请求体与幂等键", async () => {
    const requests = stubFetch(() => jsonResponse(201, { ok: true }));
    await apiRequest(
      { method: "POST", path: "/api/v1/library-roots", body: { path: "/music/a" }, idempotencyKey: "key-1" },
      decodeOk,
    );

    const [request] = requests;
    expect(request?.method).toBe("POST");
    expect(request?.headers.get("Content-Type")).toBe("application/json");
    expect(request?.headers.get("Idempotency-Key")).toBe("key-1");
    expect(request?.body).toEqual({ path: "/music/a" });
  });

  it("收藏 PUT 同源携带 Cookie，无请求体时不发送 JSON null 或 Content-Type", async () => {
    const requests = stubFetch(() => jsonResponse(200, { ok: true }));
    await apiRequest({ method: "PUT", path: "/api/v1/me/favorites/tracks/00000000-0000-4000-8000-000000000001" }, decodeOk);

    const [request] = requests;
    expect(request?.method).toBe("PUT");
    expect(request?.headers.has("Content-Type")).toBe(false);
    expect(request?.headers.has("Idempotency-Key")).toBe(false);
    expect(request?.body).toBeUndefined();
    expect(request?.init.body).toBeUndefined();
    expect(request?.init.credentials).toBe("same-origin");
    expect(request?.init.cache).toBe("no-store");
  });

  it("DELETE 同样同源携带 Cookie，不带请求体与 Content-Type，也不带幂等键", async () => {
    const requests = stubFetch(() => jsonResponse(200, { ok: true }));
    await apiRequest({ method: "DELETE", path: "/api/v1/me/plays" }, decodeOk);

    const [request] = requests;
    expect(request?.method).toBe("DELETE");
    expect(request?.path).toBe("/api/v1/me/plays");
    expect(request?.headers.has("Content-Type")).toBe(false);
    expect(request?.headers.has("Idempotency-Key")).toBe(false);
    expect(request?.body).toBeUndefined();
    expect(request?.init.credentials).toBe("same-origin");
    expect(request?.init.cache).toBe("no-store");
  });

  it("把成功响应的状态码交给解码器，状态码与响应体不符时按无效响应处理", async () => {
    const decodeByStatus = (input: unknown, status: number) => {
      const body = decodeOk(input);
      if (status !== 200) throw new d.DecodeError("$", `状态码 ${status} 不符`);
      return body;
    };
    stubFetch(() => jsonResponse(200, { ok: true }));
    expect((await apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeByStatus)).data).toEqual({ ok: true });
    stubFetch(() => jsonResponse(202, { ok: true }));
    expect(await failureFrom(apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeByStatus))).toMatchObject({ kind: "invalid_response", status: 202 });
  });

  it("返回状态码，让 201、202 与 200 的业务含义可区分", async () => {
    stubFetch(() => jsonResponse(202, { ok: true }));
    const response = await apiRequest({ method: "POST", path: "/api/v1/scans" }, decodeOk);
    expect(response.status).toBe(202);
  });

  it("把取消信号交给 fetch", async () => {
    const requests = stubFetch(() => jsonResponse(200, { ok: true }));
    const controller = new AbortController();
    await apiRequest({ method: "GET", path: "/api/v1/scans/active", signal: controller.signal }, decodeOk);
    expect(requests[0]?.signal).toBe(controller.signal);
  });
});

describe("apiRequest 后端错误", () => {
  it("把错误信封解码为 http 失败，保留状态、错误码、文案与请求 ID", async () => {
    stubFetch(() =>
      jsonResponse(409, { error: { code: "revision_conflict", message: "目录已变化" }, request_id: "web-server-echo" }),
    );
    const failure = await failureFrom(apiRequest({ method: "PATCH", path: "/api/v1/library-roots/x" }, decodeOk));
    expect(failure).toEqual({
      kind: "http",
      status: 409,
      code: "revision_conflict",
      message: "目录已变化",
      requestId: "web-server-echo",
    });
  });

  it("信封的 request_id 为空时回退到客户端请求 ID", async () => {
    stubFetch(() => jsonResponse(503, { error: { code: "database_unavailable", message: "服务暂不可用" }, request_id: "" }));
    const failure = await failureFrom(apiRequest({ method: "GET", path: "/api/v1/setup/status" }, decodeOk));
    expect(failure.kind === "http" && failure.requestId).toMatch(/^web-[0-9a-f]{16}$/);
  });
});

describe("apiRequest 无效响应", () => {
  const invalidCases: [string, () => Response][] = [
    ["HTML 回落页", () => new Response("<!doctype html>", { status: 200, headers: { "Content-Type": "text/html" } })],
    ["缺少 Content-Type 的响应", () => new Response('{"ok":true}', { status: 200 })],
    ["损坏的 JSON", () => new Response("{not json", { status: 200, headers: { "Content-Type": "application/json" } })],
    ["非 JSON 的错误响应", () => new Response("Bad Gateway", { status: 502, headers: { "Content-Type": "text/plain" } })],
    ["不合法的错误信封", () => jsonResponse(400, { error: "bad" })],
    ["decoder 拒绝的成功体", () => jsonResponse(200, { ok: "yes" })],
  ];

  it.each(invalidCases)("%s 归为 invalid_response 且不进入解码结果", async (_name, respond) => {
    stubFetch(respond);
    const failure = await failureFrom(apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeOk));
    expect(failure.kind).toBe("invalid_response");
    expect(failure.kind === "invalid_response" && failure.requestId).toMatch(/^web-[0-9a-f]{16}$/);
  });

  it("invalid_response 记录收到的状态码", async () => {
    stubFetch(() => new Response("<html>", { status: 502, headers: { "Content-Type": "text/html" } }));
    const failure = await failureFrom(apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeOk));
    expect(failure.kind === "invalid_response" && failure.status).toBe(502);
  });

  it("接受带 charset 的 application/json", async () => {
    stubFetch(() => jsonResponse(200, { ok: true }, { "Content-Type": "application/json; charset=utf-8" }));
    await expect(apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeOk)).resolves.toMatchObject({ status: 200 });
  });
});

describe("apiRequest 网络失败与取消", () => {
  it("fetch 拒绝时归为 network，并带客户端请求 ID（写请求结果未知）", async () => {
    stubFetch(() => {
      throw new TypeError("Failed to fetch");
    });
    const failure = await failureFrom(apiRequest({ method: "POST", path: "/api/v1/users", body: {} }, decodeOk));
    expect(failure).toEqual({ kind: "network", requestId: expect.stringMatching(/^web-[0-9a-f]{16}$/) });
  });

  it("读取响应体中断时同样归为 network", async () => {
    stubFetch(() => {
      const response = jsonResponse(200, { ok: true });
      vi.spyOn(response, "text").mockRejectedValue(new TypeError("terminated"));
      return response;
    });
    const failure = await failureFrom(apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeOk));
    expect(failure.kind).toBe("network");
  });

  it("信号已取消时归为 aborted 而不是 network", async () => {
    const controller = new AbortController();
    stubFetch(() => {
      controller.abort();
      throw new DOMException("The operation was aborted.", "AbortError");
    });
    const failure = await failureFrom(
      apiRequest({ method: "GET", path: "/api/v1/auth/me", signal: controller.signal }, decodeOk),
    );
    expect(failure).toEqual({ kind: "aborted" });
  });

  it("抛出的是 ApiError，原始原因保存在 cause 中", async () => {
    const cause = new TypeError("Failed to fetch");
    stubFetch(() => {
      throw cause;
    });
    const error = await apiRequest({ method: "GET", path: "/api/v1/auth/me" }, decodeOk).catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).cause).toBe(cause);
  });
});

describe("apiRequest 与会话边界", () => {
  const unauthorized = () =>
    jsonResponse(401, { error: { code: "unauthorized", message: "需要登录" }, request_id: "r" });

  it("受保护请求得到 401 unauthorized 时，带上请求发出时的代次通知会话边界", async () => {
    let generation = 3;
    const onUnauthorized = vi.fn();
    setSessionBoundary({ currentGeneration: () => generation, onUnauthorized });
    stubFetch(() => {
      generation = 4; // 响应到达前会话已换代
      return unauthorized();
    });

    const failure = await failureFrom(apiRequest({ method: "GET", path: "/api/v1/users" }, decodeOk));
    expect(failure.kind === "http" && failure.status).toBe(401);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).toHaveBeenCalledWith(3);
  });

  it('session: "none" 的请求不通知会话边界', async () => {
    const onUnauthorized = vi.fn();
    setSessionBoundary({ currentGeneration: () => 1, onUnauthorized });
    stubFetch(unauthorized);
    await failureFrom(apiRequest({ method: "GET", path: "/api/v1/auth/me", session: "none" }, decodeOk));
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("登录失败的 401 invalid_credentials 不算会话失效", async () => {
    const onUnauthorized = vi.fn();
    setSessionBoundary({ currentGeneration: () => 1, onUnauthorized });
    stubFetch(() =>
      jsonResponse(401, { error: { code: "invalid_credentials", message: "用户名或密码错误" }, request_id: "r" }),
    );
    await failureFrom(apiRequest({ method: "POST", path: "/api/v1/auth/login", body: {} }, decodeOk));
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("403 与其他状态不通知会话边界", async () => {
    const onUnauthorized = vi.fn();
    setSessionBoundary({ currentGeneration: () => 1, onUnauthorized });
    stubFetch(() => jsonResponse(403, { error: { code: "forbidden", message: "需要管理员权限" }, request_id: "r" }));
    await failureFrom(apiRequest({ method: "GET", path: "/api/v1/users" }, decodeOk));
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("没有注册会话边界时，401 仍作为 http 失败抛出", async () => {
    stubFetch(unauthorized);
    const failure = await failureFrom(apiRequest({ method: "GET", path: "/api/v1/users" }, decodeOk));
    expect(failure.kind).toBe("http");
  });
});
