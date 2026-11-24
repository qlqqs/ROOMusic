import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { decode as d } from "../shared/api";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const statusPath = "/api/v1/admin/insights";
const ackPath = "/api/v1/admin/insights/notice-acknowledgement";
const aboutPath = "/api/v1/system/about";
const enabledBody = d.defineDecoder(d.object({ enabled: d.boolean() }));
type Status = Record<string, unknown> & { enabled: boolean; forced_off: boolean; notice_acknowledged: boolean };

function setup({ current = "curator", status = {} }: { current?: "curator" | "listener"; status?: Partial<Status> } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current });
  let state = { ...(sampleBody("insights/status") as Status), ...status };
  api.on("GET", statusPath, () => jsonResponse(200, state));
  api.on("PUT", statusPath, ({ body }) => {
    if (state.forced_off) return jsonResponse(409, { error: { code: "insights_forced_off", message: "部署配置已关闭匿名统计" }, request_id: "r" });
    state = { ...state, enabled: enabledBody(body).enabled };
    return jsonResponse(200, state);
  });
  api.on("POST", ackPath, () => {
    state = { ...state, notice_acknowledged: true };
    return new Response(null, { status: 204 });
  });
  api.on("GET", aboutPath, () => jsonResponse(200, sampleBody("insights/about")));
  return api;
}

describe("匿名统计", () => {
  it("概览说明：管理员“知道了”后记为已确认、不再显示", async () => {
    const user = userEvent.setup();
    const api = setup();
    renderApp({ path: "/" });
    expect(await screen.findByText(/每天向项目发送一次匿名统计/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "查看发送内容" }).getAttribute("href")).toBe("/settings/insights");
    await user.click(screen.getByRole("button", { name: "知道了" }));
    await waitFor(() => expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull());
    expect(api.requestsTo("POST", ackPath)).toHaveLength(1);
    expect(api.requestsTo("PUT", statusPath)).toHaveLength(0);
  });

  it("概览说明：“关闭统计”先关开关再确认", async () => {
    const user = userEvent.setup();
    const api = setup();
    renderApp({ path: "/" });
    await user.click(await screen.findByRole("button", { name: "关闭统计" }));
    await waitFor(() => expect(api.requestsTo("POST", ackPath)).toHaveLength(1));
    expect(api.requestsTo("PUT", statusPath).map(({ body }) => body)).toEqual([{ enabled: false }]);
    await waitFor(() => expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull());
  });

  it("已确认后不显示说明", async () => {
    setup({ status: { notice_acknowledged: true } });
    renderApp({ path: "/" });
    expect(await screen.findByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull();
  });

  it("成员看不到说明，也不请求管理接口", async () => {
    const api = setup({ current: "listener" });
    renderApp({ path: "/" });
    expect(await screen.findByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull();
    expect(api.requestsTo("GET", statusPath)).toHaveLength(0);
  });

  it("设置页：开关、上次发送与完整载荷", async () => {
    const user = userEvent.setup();
    const api = setup();
    renderApp({ path: "/settings/insights" });
    const toggle = await screen.findByRole<HTMLInputElement>("switch", { name: "发送匿名统计" });
    expect(toggle.checked).toBe(true);
    expect(screen.getByText(/还没有发送过；最早在/)).toBeTruthy();
    const payload = screen.getByLabelText("将要发送的内容");
    expect(payload.textContent).toContain('"instance_id": "00000000-0000-4000-8000-000000000001"');
    expect(payload.textContent).toContain('"tracks": "0"');
    await user.click(toggle);
    await waitFor(() => expect(toggle.checked).toBe(false));
    expect(api.requestsTo("PUT", statusPath).map(({ body }) => body)).toEqual([{ enabled: false }]);
  });

  it("部署层关闭时开关不可改，说明原因", async () => {
    setup({ status: { forced_off: true, enabled: true } });
    renderApp({ path: "/settings/insights" });
    const toggle = await screen.findByRole<HTMLInputElement>("switch", { name: "发送匿名统计" });
    expect(toggle.disabled).toBe(true);
    expect(toggle.checked).toBe(false);
    expect(screen.getByText(/ROOMUSIC_INSIGHTS_DISABLED/)).toBeTruthy();
  });

  it("关于：所有人都能看到许可与源代码地址", async () => {
    setup({ current: "listener" });
    renderApp({ path: "/settings/about" });
    const link = await screen.findByRole("link", { name: "github.com/qlqqs/ROOMusic" });
    expect(link.getAttribute("href")).toBe("https://github.com/qlqqs/ROOMusic");
    expect(screen.getByText(/AGPL-3.0/)).toBeTruthy();
    expect(screen.queryByText("版本")).toBeNull();
  });
});
