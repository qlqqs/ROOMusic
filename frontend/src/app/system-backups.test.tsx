import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { formatDateTime } from "../shared/lib/time";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installSystemApi } from "../test/fake-system";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { systemDataOf, type SystemData } from "../test/system-data";

const path = "/api/v1/system/backups";
function setup(overrides: Partial<SystemData> = {}, role: "admin" | "user" = "admin") {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  const data = installSystemApi(api, session, overrides);
  return { api, data };
}
const render = () => renderApp({ path: "/system/backups" });
const ready = () => screen.findByRole("table", { name: "备份文件" });
const tick = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
afterEach(() => vi.useRealTimers());

describe("系统备份：状态、文件与权限", () => {
  it("展示版本、最近成功、下次自动备份、四种来源；两个区段共用一次查询", async () => {
    const { api, data } = setup({ version: { version: "0.1.0", commit: null } });
    render();
    const table = await ready();
    expect(await screen.findByText("0.1.0")).toBeTruthy();
    expect(screen.getAllByText(formatDateTime(data.status.last_success_at ?? "")).length).toBeGreaterThan(0);
    expect(screen.getByText(formatDateTime(data.status.next_scheduled_at ?? ""))).toBeTruthy();
    for (const source of ["自动", "手动", "版本切换前", "恢复前"]) expect(within(table).getByText(source)).toBeTruthy();
    expect(api.requestsTo("GET", path)).toHaveLength(1);
    expect(api.requestsTo("GET", "/api/v1/system/version")).toHaveLength(1);
    expect(screen.queryByText(/只显示最近/)).toBeNull();
  });

  it("空列表与开发版不虚构时间，不提供下载链接", async () => {
    const status = { ...systemDataOf().status, last_success_at: null, next_scheduled_at: null };
    setup({ version: { version: null, commit: null }, status, backups: [] });
    render();
    expect(await screen.findByText("还没有备份。")).toBeTruthy();
    expect(await screen.findByText("开发版")).toBeTruthy();
    expect(screen.getByText("还没有")).toBeTruthy();
    expect(screen.getByText("—")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("link", { name: /^下载/ })).toBeNull();
  });

  it("下载为同源附件直链，href与可访问名称包含对应文件和时间", async () => {
    const { data } = setup();
    render();
    const table = await ready();
    for (const file of data.backups) {
      const link = within(table).getAllByRole("link", { name: new RegExp(`^下载\\s*${formatDateTime(file.created_at)} 的备份$`) }).find((candidate) => candidate.getAttribute("href") === `${path}/${encodeURIComponent(file.name)}`);
      if (link === undefined) throw new Error(`缺少对应的下载链接：${file.name}`);
      expect(link.getAttribute("href")).toBe(`${path}/${encodeURIComponent(file.name)}`);
      expect(link.hasAttribute("download")).toBe(true);
    }
  });

  it.each([4, 5])("只有 total > 实际条数才提示截断（total=%d）", async (total) => {
    setup({ total });
    render();
    await ready();
    expect(screen.queryByText("只显示最近 500 份。") !== null).toBe(total > 4);
  });

  it("普通成员没有入口，直接打开不发出任何系统请求", async () => {
    const { api } = setup({}, "user");
    render();
    expect(await screen.findByRole("heading", { name: "需要管理员权限" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "备份" })).toBeNull();
    expect(api.requestsTo("GET", /^\/api\/v1\/system\//)).toHaveLength(0);
  });
});

describe("系统备份：进行中与轮询", () => {
  it("有备份时每2秒轮询，按钮保持可聚焦且点击不发POST；结束后停止轮询", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const status = { ...systemDataOf().status, running: { source: "scheduled" as const, started_at: "2026-11-06T12:00:00Z" } };
    const { api, data } = setup({ status, backupFinishesAfterReads: null });
    const { view } = render();
    await ready();
    const button = screen.getByRole("button", { name: "正在备份…" });
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(button.hasAttribute("disabled")).toBe(false);
    button.focus();
    fireEvent.click(button);
    expect(document.activeElement).toBe(button);
    expect(api.requestsTo("POST", path)).toHaveLength(0);
    const initial = api.requestsTo("GET", path).length;
    await tick(1800);
    expect(api.requestsTo("GET", path)).toHaveLength(initial);
    await tick(250);
    expect(api.requestsTo("GET", path)).toHaveLength(initial + 1);
    data.status = { ...data.status, running: null };
    await tick(2000);
    expect(screen.getByRole("button", { name: "立即备份" }).getAttribute("aria-busy")).toBeNull();
    const finished = api.requestsTo("GET", path).length;
    await tick(6000);
    expect(api.requestsTo("GET", path)).toHaveLength(finished);
    view.unmount();
  });

  it("立即备份发送空POST，刷新完成前保持忙碌，服务器running继续接管按钮状态", async () => {
    const { api, data } = setup({ backupFinishesAfterReads: null });
    const user = userEvent.setup();
    render();
    await ready();
    let finish!: () => void;
    api.on("POST", path, () => new Promise<Response>((resolve) => {
      finish = () => {
        data.status = { ...data.status, running: { source: "manual", started_at: "2026-11-06T12:00:00Z" } };
        resolve(jsonResponse(202, { status: data.status }));
      };
    }));
    const button = screen.getByRole("button", { name: "立即备份" });
    await user.click(button);
    expect(screen.getByRole("button", { name: "正在备份…" }).getAttribute("aria-busy")).toBe("true");
    await user.click(button);
    expect(api.requestsTo("POST", path)).toHaveLength(1);
    expect(api.requestsTo("POST", path)[0]?.body).toBeUndefined();
    await act(async () => finish());
    await waitFor(() => expect(api.requestsTo("GET", path).length).toBe(2));
    expect(screen.getByRole("button", { name: "正在备份…" }).getAttribute("aria-busy")).toBe("true");
  });
});

describe("系统备份：提示与写入失败", () => {
  it.each(["not_writable", "missing"] as const)("目录%s只提示部署设置，不重复展示最近失败", async (directory) => {
    const status = { ...systemDataOf().status, directory, last_failure: { at: "2026-11-06T11:00:00Z", stage: "directory" as const, source: "scheduled" as const } };
    setup({ status });
    render();
    expect(await screen.findByText("备份目录无法写入，请检查部署设置。")).toBeTruthy();
    expect(screen.queryByText(/的备份没有成功/)).toBeNull();
  });

  it("最近失败展示时间，不泄漏阶段、连接串或原始错误", async () => {
    const at = "2026-11-06T11:00:00Z";
    setup({ status: { ...systemDataOf().status, last_failure: { at, stage: "pg_dump", source: "scheduled" } } });
    render();
    expect(await screen.findByText(`${formatDateTime(at)} 的备份没有成功。`)).toBeTruthy();
    expect(screen.queryByText(/pg_dump|postgres:\/\//)).toBeNull();
  });

  it("409 busy是信息提示，没有请求ID与重试", async () => {
    const { api } = setup({ busy: true });
    const user = userEvent.setup();
    render();
    await ready();
    api.on("POST", path, () => jsonResponse(409, { error: { code: "backup_busy", message: "另一份备份正在进行，稍后再试" }, request_id: "busy-request-id" }));
    await user.click(screen.getByRole("button", { name: "立即备份" }));
    const message = await screen.findByText("另一份备份正在进行，稍后再试。");
    expect(message.closest('[role="status"]')).not.toBeNull();
    expect(message.closest("[data-tone]")?.getAttribute("data-tone")).toBe("info");
    expect(message.closest('[role="alert"]')).toBeNull();
    expect(screen.queryByText(/busy-request-id|请求 ID/)).toBeNull();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("其他写失败显示请求ID与重试，重试仍用POST并刷新列表", async () => {
    const { api, data } = setup();
    const user = userEvent.setup();
    let fail = true;
    api.on("POST", path, () => {
      if (fail) return jsonResponse(500, { error: { code: "internal_error", message: "服务出错了" }, request_id: "backup-write-id" });
      return jsonResponse(202, { status: data.status });
    });
    render();
    await ready();
    await user.click(screen.getByRole("button", { name: "立即备份" }));
    expect(await screen.findByText(/backup-write-id/)).toBeTruthy();
    fail = false;
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "重试" })).toBeNull());
    expect(api.requestsTo("POST", path)).toHaveLength(2);
    expect(api.requestsTo("GET", path).length).toBeGreaterThan(1);
  });
});
