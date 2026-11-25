import { act, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { ApiError } from "../../shared/api";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";
import { reportRenderError, resetClientErrorReporting } from "./client-errors";

const endpoint = "/api/v1/diagnostics/client-errors";
const route = "/settings/about";

function dispatchError(error: Error) {
  window.dispatchEvent(new ErrorEvent("error", { error, message: error.message }));
}

function dispatchRejection(reason: unknown) {
  window.dispatchEvent(Object.assign(new Event("unhandledrejection"), { reason }));
}

async function startReporting() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const { runtime, view } = renderApp({ path: `${route}?source=feedback#details` });
  await screen.findByRole("heading", { level: 1, name: "关于" });
  return { requests: () => api.requestsTo("POST", endpoint), router: runtime.router, unmount: view.unmount };
}

describe("页面报错上报", () => {
  beforeEach(() => resetClientErrorReporting());

  it("已登录时上报 window error、Promise 拒绝和 React 渲染错误，发送路由模板", async () => {
    const { requests } = await startReporting();
    dispatchError(new TypeError("读取失败"));
    dispatchRejection(new RangeError("范围错误"));
    reportRenderError(new Error("渲染失败"));

    expect(requests().map(({ body }) => body)).toEqual([
      { kind: "error", name: "TypeError", message: "读取失败", route },
      { kind: "unhandledrejection", name: "RangeError", message: "范围错误", route },
      { kind: "render", name: "Error", message: "渲染失败", route },
    ]);
    expect(requests().map(({ init }) => init.keepalive)).toEqual([true, true, true]);
  });

  it("忽略 API 错误、取消请求和浏览器噪声", async () => {
    const { requests } = await startReporting();
    dispatchRejection(new ApiError({ kind: "http", status: 503, code: "unavailable", message: "暂时不可用", requestId: "web-1" }));
    dispatchRejection(Object.assign(new Error("请求取消"), { name: "AbortError" }));
    window.dispatchEvent(new ErrorEvent("error", { message: "Script error." }));
    dispatchError(new Error("ResizeObserver loop completed with undelivered notifications."));
    expect(requests()).toHaveLength(0);
  });

  it("导航后仅上报参数路由的模板", async () => {
    const { requests, router } = await startReporting();
    await act(() => router.navigate({ to: "/albums/$groupId", params: { groupId: "synthetic-group" } }));
    reportRenderError(new Error("详情渲染失败"));
    expect(requests().map(({ body }) => body)).toEqual([
      { kind: "render", name: "Error", message: "详情渲染失败", route: "/albums/$groupId" },
    ]);
  });

  it("同一错误只上报一次，不同错误分别发送", async () => {
    const { requests } = await startReporting();
    dispatchError(new Error("重复错误"));
    dispatchError(new Error("重复错误"));
    dispatchError(new Error("另一个错误"));
    expect(requests().map(({ body }) => body)).toEqual([
      { kind: "error", name: "Error", message: "重复错误", route },
      { kind: "error", name: "Error", message: "另一个错误", route },
    ]);
  });

  it("一页最多上报前 10 条不同的错误", async () => {
    const { requests } = await startReporting();
    for (let index = 0; index < 12; index += 1) dispatchError(new Error(`页面错误 ${index}`));
    expect(requests().map(({ body }) => body)).toEqual(
      Array.from({ length: 10 }, (_, index) => ({ kind: "error", name: "Error", message: `页面错误 ${index}`, route })),
    );
  });

  it("将过长的消息截到 500 字", async () => {
    const { requests } = await startReporting();
    const message = "错".repeat(500);
    reportRenderError(new Error(`${message}这段超出限制`));
    expect(requests().map(({ body }) => body)).toEqual([{ kind: "render", name: "Error", message, route }]);
  });

  it("按码点截断并保留第 500 个码点上的完整 emoji", async () => {
    const { requests } = await startReporting();
    const message = `${"错".repeat(499)}🎵`;
    reportRenderError(new Error(`${message}这段超出限制`));
    expect(requests().map(({ body }) => body)).toEqual([{ kind: "render", name: "Error", message, route }]);
  });

  it("已登录布局卸载后停止上报所有错误", async () => {
    const { requests, unmount } = await startReporting();
    dispatchError(new Error("卸载前"));
    expect(requests()).toHaveLength(1);
    unmount();
    window.dispatchEvent(new ErrorEvent("error", { message: "卸载后的 window error" }));
    dispatchRejection(new Error("卸载后的 Promise 拒绝"));
    reportRenderError(new Error("卸载后的渲染错误"));
    expect(requests()).toHaveLength(1);
  });
});
