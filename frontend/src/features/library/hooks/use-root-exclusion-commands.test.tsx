import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { isUuid } from "../../../shared/lib/ids";
import { deferred } from "../../../test/deferred";
import { jsonResponse, stubFetch, type RecordedRequest } from "../../../test/http";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../auth";
import { libraryKeys } from "../query-keys";
import { useRootCommands, type RootIntent } from "./use-root-commands";

const root = { id: "00000000-0000-4000-8000-000000000001", name: "测试音乐", revision: 3 };
const relativePath = "演出 & 附录/现场 #1";
const absolutePath = `/music/${relativePath}`;
const endpoint = `/api/v1/library-roots/${root.id}/exclusions`;
const intents = [
  { kind: "exclude", root, path: absolutePath },
  { kind: "unexclude", root, relativePath },
] as const satisfies readonly RootIntent[];

function responseFor(kind: "exclude" | "unexclude") {
  return { ...root, path: "测试音乐", status: "active", excluded_paths: kind === "exclude" ? [relativePath] : [] };
}

const disposers: (() => void)[] = [];
afterEach(() => disposers.splice(0).forEach((dispose) => dispose()));

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const session = createSessionController({ channel: null });
  const generation = session.currentGeneration();
  const keys = [
    libraryKeys.roots(generation),
    libraryKeys.rootOperations(generation),
    libraryKeys.folders(generation, null, root.id),
  ];
  for (const key of keys) client.setQueryData(key, { marker: "操作前" });
  disposers.push(() => { client.clear(); session.dispose(); });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <SessionProvider controller={session}>
        <ViewerScopeProvider generation={generation} viewer={{ id: "00000000-0000-4000-8000-000000000002", username: "curator", role: "admin" }}>
          {children}
        </ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>
  );
  return { ...renderHook(() => useRootCommands(), { wrapper }), client, keys };
}

function expectRequest(request: RecordedRequest | undefined, kind: "exclude" | "unexclude") {
  if (request === undefined) throw new Error("没有发出排除项请求");
  const url = new URL(request.path, "http://localhost");
  expect(url.pathname).toBe(endpoint);
  expect(isUuid(request.headers.get("Idempotency-Key"))).toBe(true);
  if (kind === "exclude") {
    expect(request.method).toBe("POST");
    expect(url.search).toBe("");
    expect(request.body).toEqual({ path: absolutePath });
  } else {
    expect(request.method).toBe("DELETE");
    expect(Object.fromEntries(url.searchParams)).toEqual({ path: relativePath });
    expect(request.body).toBeUndefined();
  }
}

describe("不扫描的子文件夹命令", () => {
  it.each(intents)("$kind 发送约定的路径和幂等键，确认后返回排除项并刷新列表", async (intent) => {
    const pending = deferred<Response>();
    const requests = stubFetch(() => pending.promise);
    const { result, client, keys } = setup();
    const onSucceeded = vi.fn();
    act(() => result.current.submit(intent, { onSucceeded }));
    await waitFor(() => expect(result.current.state.status).toBe("pending"));
    expectRequest(requests[0], intent.kind);
    expect(onSucceeded).not.toHaveBeenCalled();
    for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(false);

    const response = responseFor(intent.kind);
    await act(async () => pending.resolve(jsonResponse(200, response)));
    await waitFor(() => expect(result.current.state.status).toBe("succeeded"));
    expect(result.current.state).toMatchObject({ result: { kind: intent.kind, root: response } });
    expect(onSucceeded).toHaveBeenCalledOnce();
    for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    expect(requests).toHaveLength(1);
  });

  it.each(intents)("$kind 网络失败不自动重发，手动重试沿用同一请求和幂等键", async (intent) => {
    let attempts = 0;
    const requests = stubFetch(() => {
      attempts += 1;
      if (attempts === 1) throw new TypeError("连接中断");
      return jsonResponse(200, responseFor(intent.kind));
    });
    const { result } = setup();
    const onFailed = vi.fn();
    act(() => result.current.submit(intent, { onFailed }));
    await waitFor(() => expect(result.current.state.status).toBe("failed"));
    expect(result.current.state).toMatchObject({ uncertain: true, failure: { kind: "network" } });
    expect(onFailed).toHaveBeenCalledWith(expect.objectContaining({ kind: "network" }), true);
    expect(requests).toHaveLength(1);
    expectRequest(requests[0], intent.kind);

    act(() => result.current.retry());
    await waitFor(() => expect(result.current.state.status).toBe("succeeded"));
    expect(requests).toHaveLength(2);
    expectRequest(requests[1], intent.kind);
    expect(requests[1]?.path).toBe(requests[0]?.path);
    expect(requests[1]?.body).toEqual(requests[0]?.body);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
  });

  it("添加与移除是新意图，分别生成幂等键", async () => {
    const requests = stubFetch((request) => jsonResponse(200, responseFor(request.method === "POST" ? "exclude" : "unexclude")));
    const { result } = setup();
    act(() => result.current.submit(intents[0]));
    await waitFor(() => expect(result.current.state.status).toBe("succeeded"));
    act(() => result.current.submit(intents[1]));
    await waitFor(() => expect(result.current.state).toMatchObject({ status: "succeeded", result: { kind: "unexclude" } }));
    expect(requests).toHaveLength(2);
    expectRequest(requests[0], "exclude");
    expectRequest(requests[1], "unexclude");
    expect(requests[1]?.headers.get("Idempotency-Key")).not.toBe(requests[0]?.headers.get("Idempotency-Key"));
  });

  it.each([
    { intent: intents[0], code: "exclusion_limit" },
    { intent: intents[1], code: "idempotency_conflict" },
  ] as const)("$intent.kind 遇到 $code 保留明确拒绝并刷新列表", async ({ intent, code }) => {
    const requests = stubFetch(() => jsonResponse(409, { error: { code, message: "操作未完成" }, request_id: "req-exclusion" }));
    const { result, client, keys } = setup();
    const onFailed = vi.fn();
    act(() => result.current.submit(intent, { onFailed }));
    await waitFor(() => expect(result.current.state.status).toBe("failed"));
    expect(result.current.state).toMatchObject({ uncertain: false, command: intent, failure: { kind: "http", status: 409, code } });
    expect(onFailed).toHaveBeenCalledWith(expect.objectContaining({ code }), false);
    for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    expect(requests).toHaveLength(1);
  });
});
