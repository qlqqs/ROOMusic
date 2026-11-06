import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { formatDateTime } from "../shared/lib/time";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installSystemApi } from "../test/fake-system";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { respondToSystem, systemDataOf, type SystemData } from "../test/system-data";

const path = "/api/v1/system/backups";
function setup(overrides: Partial<SystemData> = {}, role: "admin" | "user" = "admin") {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  const data = installSystemApi(api, session, overrides);
  return { api, data };
}
const render = () => renderApp({ path: "/settings/backups" });
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

const deletionPath = `${path}/deletions`;
const retentionPath = `${path}/retention`;
const fixedNow = () => "2026-11-06T12:00:00Z";
// 样本时间按秒规范化，而界面显示到分钟；通过下载直链定位行，避免同一分钟的备份名称相同。
const fileRow = (file: { name: string }) => {
  const link = screen.getAllByRole("link", { name: /^下载/ }).find((candidate) => candidate.getAttribute("href") === `${path}/${encodeURIComponent(file.name)}`);
  const row = link?.closest("tr");
  if (row === undefined || row === null) throw new Error(`缺少备份行：${file.name}`);
  return within(row);
};
const fileButton = (file: { name: string; created_at: string }) => fileRow(file).getByRole("button", { name: new RegExp(`^删除\\s*${formatDateTime(file.created_at)} 的备份$`) });
const fileCheck = (file: { name: string; created_at: string }) => fileRow(file).getByRole<HTMLInputElement>("checkbox", { name: `选择 ${formatDateTime(file.created_at)} 的备份` });
const hasFileLink = (file: { name: string }) => screen.queryAllByRole("link", { name: /^下载/ }).some((candidate) => candidate.getAttribute("href") === `${path}/${encodeURIComponent(file.name)}`);
function systemAnswer(data: SystemData, method: string, requestPath: string, body?: unknown): Response {
  const result = respondToSystem(data, method, requestPath, fixedNow, body);
  if (result === null) throw new Error(`未处理的测试接口：${method} ${requestPath}`);
  return jsonResponse(result.status, result.body);
}

// 布局的真实像素与375宽可见性由Playwright验证；这里验证表格外工具行仍有独立可访问的全选。
describe("设置 › 备份：单份删除", () => {
  it("确认框含时间与后果，初始聚焦取消；取消不发DELETE", async () => {
    const { api, data } = setup();
    const file = data.backups[0];
    if (file === undefined) throw new Error("缺少种子备份");
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(fileButton(file));
    const dialog = screen.getByRole("dialog", { name: `删除 ${formatDateTime(file.created_at)} 的备份？` });
    expect(within(dialog).getByText("删除后不能恢复。")).toBeTruthy();
    expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("DELETE", `${path}/${file.name}`)).toHaveLength(0);
    expect(data.backups).toContain(file);
  });

  it("确认后忙碌期间不能重复提交或取消，成功移除行并聚焦标题", async () => {
    const { api, data } = setup();
    const file = data.backups[0];
    if (file === undefined) throw new Error("缺少种子备份");
    let complete: (() => void) | undefined;
    api.on("DELETE", `${path}/${file.name}`, () => new Promise<Response>((resolve) => {
      complete = () => resolve(systemAnswer(data, "DELETE", `${path}/${file.name}`));
    }));
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(fileCheck(file));
    await user.click(fileButton(file));
    const dialog = screen.getByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "删除" });
    await user.click(confirm);
    expect(confirm.getAttribute("aria-busy")).toBe("true");
    expect(within(dialog).getByRole("button", { name: "取消" }).hasAttribute("disabled")).toBe(true);
    await user.click(confirm);
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect(api.requestsTo("DELETE", `${path}/${file.name}`)).toHaveLength(1);
    await act(async () => complete?.());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(hasFileLink(file)).toBe(false);
    expect(screen.queryByRole("button", { name: /删除所选/ })).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "备份文件" }));
    expect(screen.getByText("已删除。")).toBeTruthy();
    expect(data.status.last_success_at).toBe(data.backups[0]?.created_at);
  });

  it("404已不存在算成功，刷新列表、关闭确认框且无错误", async () => {
    const { api, data } = setup();
    const file = data.backups[0];
    if (file === undefined) throw new Error("缺少种子备份");
    api.on("DELETE", `${path}/${file.name}`, () => {
      data.backups = data.backups.filter((candidate) => candidate.name !== file.name);
      return jsonResponse(404, { error: { code: "backup_not_found", message: "备份不存在" }, request_id: "already-missing" });
    });
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(fileButton(file));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(screen.queryByRole("button", { name: `删除 ${formatDateTime(file.created_at)} 的备份` })).toBeNull());
    expect(api.requestsTo("GET", path).length).toBeGreaterThan(1);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByText(/already-missing/)).toBeNull();
  });

  it.each(["重试", "取消"])("503留在确认框中，可以%s", async (action) => {
    const { data } = setup();
    const file = data.backups[0];
    if (file === undefined) throw new Error("缺少种子备份");
    data.undeletable = new Set([file.name]);
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(fileButton(file));
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(await within(dialog).findByText("备份目录不能写入，没有删除。")).toBeTruthy();
    expect(data.backups).toContain(file);
    data.undeletable = new Set();
    await user.click(within(dialog).getByRole("button", { name: action === "重试" ? "删除" : "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(data.backups.includes(file)).toBe(action === "取消");
  });
});

describe("设置 › 备份：批量删除", () => {
  it("表格外全选支持半选、全选和取消全选", async () => {
    const { data } = setup();
    const user = userEvent.setup();
    render();
    const table = await ready();
    const all = screen.getByRole<HTMLInputElement>("checkbox", { name: "全选" });
    expect(table.contains(all)).toBe(false);
    const first = data.backups[0];
    if (first === undefined) throw new Error("缺少种子备份");
    await user.click(fileCheck(first));
    expect(all.indeterminate).toBe(true);
    expect(all.checked).toBe(false);
    await user.click(all);
    expect(all.indeterminate).toBe(false);
    expect(all.checked).toBe(true);
    expect(screen.getByRole("button", { name: "删除所选（4）" })).toBeTruthy();
    for (const file of data.backups) expect(fileCheck(file).checked).toBe(true);
    await user.click(all);
    expect(all.checked).toBe(false);
    expect(screen.queryByRole("button", { name: /删除所选/ })).toBeNull();
  });

  it("删除两份只发一个POST，清空勾选、聚焦标题并宣告结果", async () => {
    const { api, data } = setup();
    const chosen = data.backups.slice(0, 2);
    const user = userEvent.setup();
    render();
    await ready();
    for (const file of chosen) await user.click(fileCheck(file));
    await user.click(screen.getByRole("button", { name: "删除所选（2）" }));
    const dialog = screen.getByRole("dialog", { name: "删除 2 份备份？" });
    expect(within(dialog).getByText("删除后不能恢复。")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const requests = api.requestsTo("POST", deletionPath);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toEqual({ names: chosen.map((file) => file.name) });
    expect(api.requestsTo("DELETE", /^\/api\/v1\/system\/backups\//)).toHaveLength(0);
    for (const file of chosen) expect(hasFileLink(file)).toBe(false);
    expect(screen.queryByRole("button", { name: /删除所选/ })).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "备份文件" }));
    expect(screen.getByText("已删除 2 份。")).toBeTruthy();
  });

  it("批删提交忙碌期间不能重复提交、取消或Esc，完成后移除全部", async () => {
    const { api, data } = setup();
    let complete: (() => void) | undefined;
    api.on("POST", deletionPath, (request) => new Promise<Response>((resolve) => {
      complete = () => resolve(systemAnswer(data, "POST", deletionPath, request.body));
    }));
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(screen.getByRole("checkbox", { name: "全选" }));
    await user.click(screen.getByRole("button", { name: "删除所选（4）" }));
    const dialog = screen.getByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "删除" });
    await user.click(confirm);
    expect(confirm.getAttribute("aria-busy")).toBe("true");
    expect(within(dialog).getByRole("button", { name: "取消" }).hasAttribute("disabled")).toBe(true);
    await user.click(confirm);
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect(api.requestsTo("POST", deletionPath)).toHaveLength(1);
    await act(async () => complete?.());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByText("还没有备份。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "备份文件" }));
  });

  it("取消批量删除不发请求、保留勾选", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(screen.getByRole("checkbox", { name: "全选" }));
    await user.click(screen.getByRole("button", { name: "删除所选（4）" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("POST", deletionPath)).toHaveLength(0);
    expect(screen.getByRole("button", { name: "删除所选（4）" })).toBeTruthy();
  });

  it("部分失败只列剩下时间，再点删除只重试失败名字", async () => {
    const { api, data } = setup();
    const chosen = data.backups.slice(0, 2);
    const failed = chosen[1];
    if (failed === undefined) throw new Error("缺少种子备份");
    data.undeletable = new Set([failed.name]);
    const user = userEvent.setup();
    render();
    await ready();
    for (const file of chosen) await user.click(fileCheck(file));
    await user.click(screen.getByRole("button", { name: "删除所选（2）" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "删除" }));
    const dialog = await screen.findByRole("dialog", { name: "删除 1 份备份？" });
    expect(within(dialog).getByText("有 1 份没能删除")).toBeTruthy();
    expect(within(dialog).getByRole("list").textContent).toBe(formatDateTime(failed.created_at));
    expect(within(await ready()).getAllByRole("checkbox")).toHaveLength(3);
    data.undeletable = new Set();
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const requests = api.requestsTo("POST", deletionPath);
    expect(requests).toHaveLength(2);
    expect(requests[0]?.body).toEqual({ names: chosen.map((file) => file.name) });
    expect(requests[1]?.body).toEqual({ names: [failed.name] });
    expect(data.backups).toHaveLength(2);
  });

  it("missing算成功，不列为失败也不重试", async () => {
    const { api, data } = setup();
    const chosen = data.backups.slice(0, 2);
    const missing = chosen[0];
    if (missing === undefined) throw new Error("缺少种子备份");
    api.on("POST", deletionPath, (request) => {
      data.backups = data.backups.filter((file) => file.name !== missing.name);
      return systemAnswer(data, "POST", deletionPath, request.body);
    });
    const user = userEvent.setup();
    render();
    await ready();
    for (const file of chosen) await user.click(fileCheck(file));
    await user.click(screen.getByRole("button", { name: "删除所选（2）" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText(/没能删除/)).toBeNull();
    expect(data.backups).toHaveLength(2);
    expect(api.requestsTo("POST", deletionPath)).toHaveLength(1);
  });

  it("轮询后不在列表的勾选自动去掉", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const status = { ...systemDataOf().status, running: { source: "scheduled" as const, started_at: fixedNow() } };
    const { data } = setup({ status, backupFinishesAfterReads: null });
    render();
    await ready();
    const chosen = data.backups.slice(0, 2);
    for (const file of chosen) fireEvent.click(fileCheck(file));
    expect(screen.getByRole("button", { name: "删除所选（2）" })).toBeTruthy();
    data.backups = data.backups.slice(1);
    await tick(2100);
    expect(screen.getByRole("button", { name: "删除所选（1）" })).toBeTruthy();
  });
});

const retentionFields = () => ({
  days: screen.getByRole<HTMLInputElement>("spinbutton", { name: "保留天数" }),
  count: screen.getByRole<HTMLInputElement>("spinbutton", { name: "保留份数" }),
  daysUnlimited: within(screen.getByRole("group", { name: "保留天数" })).getByRole<HTMLInputElement>("checkbox", { name: "不限" }),
  countUnlimited: within(screen.getByRole("group", { name: "保留份数" })).getByRole<HTMLInputElement>("checkbox", { name: "不限" }),
});
function setCountRule(count: string) {
  const fields = retentionFields();
  if (!fields.daysUnlimited.checked) fireEvent.click(fields.daysUnlimited);
  if (fields.countUnlimited.checked) fireEvent.click(fields.countUnlimited);
  fireEvent.change(fields.count, { target: { value: count } });
}

describe("设置 › 备份：自动清理", () => {
  it("初值取规则，取消不限会聚焦数字框，勾回不限禁用且保留值", async () => {
    setup({ retention: { keep_days: null, keep_count: 3 } });
    const user = userEvent.setup();
    render();
    await ready();
    const fields = retentionFields();
    expect(fields.days.disabled).toBe(true);
    expect(fields.daysUnlimited.checked).toBe(true);
    expect(fields.count.value).toBe("3");
    expect(fields.countUnlimited.checked).toBe(false);
    await user.click(fields.daysUnlimited);
    expect(fields.days.disabled).toBe(false);
    expect(document.activeElement).toBe(fields.days);
    fireEvent.change(fields.days, { target: { value: "30" } });
    await user.click(fields.daysUnlimited);
    expect(fields.days.disabled).toBe(true);
    expect(fields.days.value).toBe("30");
  });

  it("两项不限只发一次未确认PUT，保存成功不弹框不删文件", async () => {
    const { api, data } = setup();
    const before = [...data.backups];
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(retentionFields().daysUnlimited);
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("已保存。")).toBeTruthy();
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(1);
    expect(api.requestsTo("PUT", retentionPath)[0]?.body).toEqual({ keep_days: null, keep_count: null, confirm: false });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(data.backups).toEqual(before);
    expect(data.retention).toEqual({ keep_days: null, keep_count: null });
  });

  it.each([
    ["保留天数", "0", "填 1 到 3650 之间的整数。"],
    ["保留天数", "3651", "填 1 到 3650 之间的整数。"],
    ["保留天数", "1.5", "填 1 到 3650 之间的整数。"],
    ["保留天数", "", "填 1 到 3650 之间的整数。"],
    ["保留份数", "0", "填 1 到 1000 之间的整数。"],
    ["保留份数", "1001", "填 1 到 1000 之间的整数。"],
    ["保留份数", "1.5", "填 1 到 1000 之间的整数。"],
  ])("%s=%s显示字段错误且不发请求", async (legend, value, error) => {
    const { api } = setup();
    render();
    await ready();
    const fields = retentionFields();
    if (legend === "保留份数") fireEvent.click(fields.countUnlimited);
    const field = screen.getByRole("spinbutton", { name: legend });
    fireEvent.change(field, { target: { value } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(screen.getByText(error)).toBeTruthy();
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(field);
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(0);
  });

  it("会删先确认，取消不改规则或文件，保持草稿", async () => {
    const { api, data } = setup();
    const before = [...data.backups];
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("1");
    await user.click(screen.getByRole("button", { name: "保存" }));
    const dialog = await screen.findByRole("dialog", { name: "保存清理设置？" });
    expect(within(dialog).getByText("将删除 3 份旧备份。删除后不能恢复。")).toBeTruthy();
    expect(data.retention).toEqual({ keep_days: 14, keep_count: null });
    expect(data.backups).toEqual(before);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(1);
    expect(retentionFields().count.value).toBe("1");
    expect(screen.queryByText(/^已保存/)).toBeNull();
  });

  it("保留3份确认后带confirm=true，刷新到最新3份并显示实际结果", async () => {
    const { api, data } = setup();
    const expected = data.backups.slice(0, 3);
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("3");
    await user.click(screen.getByRole("button", { name: "保存" }));
    const dialog = await screen.findByRole("dialog", { name: "保存清理设置？" });
    expect(within(dialog).getByText("将删除 1 份旧备份。删除后不能恢复。")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    expect(await screen.findByText("已保存，删除了 1 份旧备份。")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("PUT", retentionPath).map((request) => request.body)).toEqual([
      { keep_days: null, keep_count: 3, confirm: false },
      { keep_days: null, keep_count: 3, confirm: true },
    ]);
    expect(data.backups).toEqual(expected);
    const table = await ready();
    expect(within(table).getAllByRole("checkbox")).toHaveLength(3);
    expect(within(table).getAllByRole("link", { name: /^下载/ }).map((link) => link.getAttribute("href"))).toEqual(expected.map((file) => `${path}/${encodeURIComponent(file.name)}`));
  });

  it("确认期间列表变化以实际结果为准，最新一份保留", async () => {
    const { data } = setup();
    const latest = data.backups[0];
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("1");
    await user.click(screen.getByRole("button", { name: "保存" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("将删除 3 份旧备份。删除后不能恢复。")).toBeTruthy();
    data.backups = data.backups.slice(0, 3);
    await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    expect(await screen.findByText("已保存，删除了 2 份旧备份。")).toBeTruthy();
    expect(data.backups).toEqual([latest]);
  });

  it.each([false, true])("目录不可写提示留在%s并可重试", async (confirmation) => {
    const { api, data } = setup({ retentionNotWritable: true });
    const user = userEvent.setup();
    render();
    await ready();
    if (confirmation) setCountRule("1");
    else await user.click(retentionFields().daysUnlimited);
    await user.click(screen.getByRole("button", { name: "保存" }));
    if (confirmation) {
      const dialog = await screen.findByRole("dialog");
      await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
      expect(await within(dialog).findByText("备份目录不能写入，没有保存。")).toBeTruthy();
      data.retentionNotWritable = false;
      await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    } else {
      expect(await screen.findByText("备份目录不能写入，没有保存。")).toBeTruthy();
      expect(screen.queryByRole("dialog")).toBeNull();
      data.retentionNotWritable = false;
      await user.click(screen.getByRole("button", { name: "保存" }));
    }
    expect(await screen.findByText(confirmation ? "已保存，删除了 3 份旧备份。" : "已保存。")).toBeTruthy();
    expect(api.requestsTo("GET", path).length).toBeGreaterThan(1);
  });

  it("规则已保存但个别文件没删掉，显示失败份数且保留文件", async () => {
    const { data } = setup();
    const failed = data.backups[1];
    if (failed === undefined) throw new Error("缺少种子备份");
    data.undeletable = new Set([failed.name]);
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("1");
    await user.click(screen.getByRole("button", { name: "保存" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    expect(await screen.findByText("已保存，删除了 2 份旧备份。")).toBeTruthy();
    expect(screen.getByText("有 1 份旧备份没能删除。")).toBeTruthy();
    expect(data.backups).toContain(failed);
    expect(data.retention).toEqual({ keep_days: null, keep_count: 1 });
  });

  it("读不到规则时提示并用默认值，保存可修复", async () => {
    const { data } = setup({ retention: null });
    const user = userEvent.setup();
    render();
    await ready();
    expect(screen.getByText("没能读取当前设置。")).toBeTruthy();
    expect(retentionFields().days.value).toBe("14");
    expect(retentionFields().countUnlimited.checked).toBe(true);
    await user.click(retentionFields().daysUnlimited);
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("已保存。")).toBeTruthy();
    expect(screen.queryByText("没能读取当前设置。")).toBeNull();
    expect(data.retention).toEqual({ keep_days: null, keep_count: null });
  });
});

describe("设置 › 备份：异步状态与规则同步", () => {
  it.each([false, true])("轮询更新规则%s覆盖草稿", async (edited) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const status = { ...systemDataOf().status, running: { source: "scheduled" as const, started_at: fixedNow() } };
    const { data } = setup({ status, backupFinishesAfterReads: null });
    render();
    await ready();
    if (edited) fireEvent.change(retentionFields().days, { target: { value: "30" } });
    data.retention = { keep_days: 7, keep_count: 3 };
    await tick(2100);
    expect(retentionFields().days.value).toBe(edited ? "30" : "7");
    expect(retentionFields().countUnlimited.checked).toBe(edited);
  });

  it("批量请求503不丢勾选，错误留在确认框，重试仍发相同名单", async () => {
    const { api, data } = setup();
    let fail = true;
    api.on("POST", deletionPath, (request) => fail
      ? jsonResponse(503, { error: { code: "backup_directory_not_writable", message: "备份目录不能写入" }, request_id: "batch-failed" })
      : systemAnswer(data, "POST", deletionPath, request.body));
    const names = data.backups.map((file) => file.name);
    const user = userEvent.setup();
    render();
    await ready();
    await user.click(screen.getByRole("checkbox", { name: "全选" }));
    await user.click(screen.getByRole("button", { name: "删除所选（4）" }));
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(await within(dialog).findByText("备份目录不能写入，没有删除。")).toBeTruthy();
    expect(data.backups).toHaveLength(4);
    fail = false;
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(api.requestsTo("POST", deletionPath).map((request) => request.body)).toEqual([{ names }, { names }]);
    expect(data.backups).toHaveLength(0);
  });
});
