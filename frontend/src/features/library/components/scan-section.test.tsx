import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession } from "../../../test/fake-api";
import { installLibraryApi, seedScan } from "../../../test/fake-library";
import { jsonResponse, type RecordedRequest } from "../../../test/http";
import { renderApp } from "../../../test/render-app";

const scanModes = [
  { mode: "incremental", button: "开始扫描", body: undefined },
  { mode: "full", button: "完整重扫", body: { mode: "full" } },
] as const;

function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  const library = installLibraryApi(api);
  return { api, library };
}

function expectScanRequest(request: RecordedRequest | undefined, body: { mode: "full" } | undefined) {
  if (request === undefined) throw new Error("没有发出开始扫描请求");
  expect(request.method).toBe("POST");
  expect(request.path).toBe("/api/v1/scans");
  expect(request.body).toEqual(body);
  expect(request.init.body).toBe(body === undefined ? undefined : JSON.stringify(body));
  expect(request.headers.get("Content-Type")).toBe(body === undefined ? null : "application/json");
}

describe.each(scanModes)("扫描区段：$button", ({ mode, button, body }) => {
  it("202 新建扫描后进入详情，按钮发出约定的真实请求", async () => {
    const { api, library } = setup();
    const { history } = renderApp({ path: "/settings/library" });
    await userEvent.click(await screen.findByRole("button", { name: button }));

    expect(await screen.findByRole("heading", { level: 1, name: "扫描详情" })).toBeTruthy();
    expect(history.location.pathname).toBe(`/settings/library/scans/${library.activeScanId}`);
    const requests = api.requestsTo("POST", "/api/v1/scans");
    expect(requests).toHaveLength(1);
    expectScanRequest(requests[0], body);
    expect(library.scans.get(library.activeScanId ?? "")?.mode).toBe(mode);
    const status = await screen.findByRole("region", { name: "状态" });
    expect(within(status).getByText("运行中")).toBeTruthy();
    expect(within(status).queryByText("完整重扫") !== null).toBe(mode === "full");
  });

  it("200 返回已有扫描时留在本页，不覆盖服务端实际模式", async () => {
    const { api, library } = setup();
    const { history } = renderApp({ path: "/settings/library" });
    const trigger = await screen.findByRole("button", { name: button });
    // 按钮仍显示空闲时，另一个客户端已经以不同模式开始扫描。
    const existing = seedScan(library, { mode: mode === "full" ? "incremental" : "full" }, true);
    await userEvent.click(trigger);

    expect(await screen.findByText(/已经有扫描在运行。/)).toBeTruthy();
    expect(history.location.pathname).toBe("/settings/library");
    expect(screen.getByRole("link", { name: "查看这个扫描" }).getAttribute("href")).toBe(`/settings/library/scans/${existing.id}`);
    expect(library.scans.size).toBe(1);
    const requests = api.requestsTo("POST", "/api/v1/scans");
    expect(requests).toHaveLength(1);
    expectScanRequest(requests[0], body);

    await userEvent.click(screen.getByRole("link", { name: "查看这个扫描" }));
    expect(await screen.findByRole("heading", { level: 1, name: "扫描详情" })).toBeTruthy();
    const status = await screen.findByRole("region", { name: "状态" });
    expect(within(status).queryByText("完整重扫") !== null).toBe(existing.mode === "full");
  });

  it("失败后的重试沿用失败模式，两次实际请求的请求体一致", async () => {
    const { api, library } = setup();
    library.startScanFailure = jsonResponse(503, {
      error: { code: "scan_coordination_unavailable", message: "暂时无法协调扫描任务" },
      request_id: "scan-mode-retry",
    });
    const { history } = renderApp({ path: "/settings/library" });
    await userEvent.click(await screen.findByRole("button", { name: button }));

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("暂时无法协调扫描任务，请稍后重试。")).toBeTruthy();
    expect(history.location.pathname).toBe("/settings/library");
    expect(library.activeScanId).toBeNull();
    expect(api.requestsTo("POST", "/api/v1/scans")).toHaveLength(1);
    expectScanRequest(api.requestsTo("POST", "/api/v1/scans")[0], body);

    library.startScanFailure = null;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("heading", { level: 1, name: "扫描详情" })).toBeTruthy();
    expect(history.location.pathname).toBe(`/settings/library/scans/${library.activeScanId}`);
    const requests = api.requestsTo("POST", "/api/v1/scans");
    expect(requests).toHaveLength(2);
    for (const request of requests) expectScanRequest(request, body);
    expect(library.scans.get(library.activeScanId ?? "")?.mode).toBe(mode);
  });
});

describe("扫描记录与详情的模式标签", () => {
  it.each(["incremental", "full"] as const)("%s 只在完整重扫时显示标签", async (mode) => {
    const { library } = setup();
    const scan = seedScan(library, { mode, status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    const { history } = renderApp({ path: "/settings/library" });

    const table = await screen.findByRole("table", { name: "扫描记录" });
    expect(within(table).queryByText("完整重扫") !== null).toBe(mode === "full");
    await userEvent.click(within(table).getByRole("link", { name: /查看详情/ }));
    expect(await screen.findByRole("heading", { level: 1, name: "扫描详情" })).toBeTruthy();
    await waitFor(() => expect(history.location.pathname).toBe(`/settings/library/scans/${scan.id}`));
    const status = await screen.findByRole("region", { name: "状态" });
    expect(within(status).getByText("成功")).toBeTruthy();
    expect(within(status).queryByText("完整重扫") !== null).toBe(mode === "full");
  });
});
