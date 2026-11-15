import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { formatDateTime } from "../shared/lib/time";
import { backupSourceLabels } from "../features/system/components/backup-labels";
import { deferred } from "../test/deferred";
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
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

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

  it("下载在行尾菜单里：交给浏览器的同源附件直链，菜单的名称带上时间", async () => {
    const { data } = setup();
    const clicked: { href: string | null; download: boolean }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push({ href: this.getAttribute("href"), download: this.hasAttribute("download") });
    });
    const user = userEvent.setup();
    render();
    await ready();
    for (const file of data.backups) await chooseFileAction(user, file, "下载");
    expect(clicked).toEqual(data.backups.map((file) => ({ href: `${path}/${encodeURIComponent(file.name)}`, download: true })));
    // 临时的链接点过就移除，不留在页面里。
    expect(document.querySelector(`a[href^="${path}/"]`)).toBeNull();
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

  it("其他写失败显示错误与重试（不显示请求ID），重试仍用POST并刷新列表", async () => {
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
    expect((await screen.findByRole("alert")).textContent).not.toContain("backup-write-id");
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
// 样本时间按秒规范化，而界面显示到分钟（四份同一分钟）；种子里四份的来源各不相同，按来源定位行。
type File = { name: string; created_at: string; source: keyof typeof backupSourceLabels };
const findFileRow = (file: File) =>
  screen.queryAllByRole("row").find((row) => within(row).queryAllByRole("cell").some((cell) => cell.textContent === backupSourceLabels[file.source])) ?? null;
const fileRow = (file: File) => {
  const row = findFileRow(file);
  if (row === null) throw new Error(`缺少备份行：${file.name}`);
  return within(row);
};
const fileCheck = (file: File) => fileRow(file).getByRole<HTMLInputElement>("checkbox", { name: `选择 ${formatDateTime(file.created_at)} 的备份` });
const hasFileRow = (file: File) => findFileRow(file) !== null;
/** 打开这一行的“更多”菜单并选一项（“下载”“删除…”）。 */
async function chooseFileAction(user: ReturnType<typeof userEvent.setup>, file: File, action: string) {
  const label = `${formatDateTime(file.created_at)} 的备份的更多操作`;
  await user.click(fileRow(file).getByRole("button", { name: label }));
  await user.click(within(screen.getByRole("menu", { name: label })).getByRole("menuitem", { name: action }));
}
const removeOne = (user: ReturnType<typeof userEvent.setup>, file: File) => chooseFileAction(user, file, "删除…");
/** 表头里的全选（jsdom 不应用样式，操作条里给窄容器用的那一个也在，这里取表头的）。 */
const headSelectAll = () => {
  const all = within(screen.getByRole("table", { name: "备份文件" })).getByRole<HTMLInputElement>("checkbox", { name: "全选" });
  return all;
};
const bulkDelete = (count: number) => screen.getByRole("button", { name: `删除所选的 ${count} 份备份…` });
const queryBulkDelete = () => screen.queryByRole("button", { name: /^删除所选的/ });
function systemAnswer(data: SystemData, method: string, requestPath: string, body?: unknown): Response {
  const result = respondToSystem(data, method, requestPath, fixedNow, body);
  if (result === null) throw new Error(`未处理的测试接口：${method} ${requestPath}`);
  return jsonResponse(result.status, result.body);
}

// 布局的真实像素与375宽可见性由Playwright验证；这里验证表头全选、操作条与行菜单的行为。
describe("设置 › 备份：单份删除", () => {
  it("确认框含时间与后果，初始聚焦取消；取消不发DELETE", async () => {
    const { api, data } = setup();
    const file = data.backups[0];
    if (file === undefined) throw new Error("缺少种子备份");
    const user = userEvent.setup();
    render();
    await ready();
    await removeOne(user, file);
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
    await removeOne(user, file);
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
    expect(hasFileRow(file)).toBe(false);
    expect(queryBulkDelete()).toBeNull();
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
    await removeOne(user, file);
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(hasFileRow(file)).toBe(false));
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
    await removeOne(user, file);
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
  it("表头全选支持半选、全选和取消全选；有勾选时操作条显示份数", async () => {
    const { data } = setup();
    const user = userEvent.setup();
    render();
    const table = await ready();
    const all = headSelectAll();
    expect(within(table).getAllByRole("columnheader")[0]?.contains(all)).toBe(true);
    expect(screen.queryByText(/^已选/)).toBeNull();
    const first = data.backups[0];
    if (first === undefined) throw new Error("缺少种子备份");
    await user.click(fileCheck(first));
    expect(all.indeterminate).toBe(true);
    expect(all.checked).toBe(false);
    expect(screen.getByText("已选 1 个")).toBeTruthy();
    await user.click(all);
    expect(all.indeterminate).toBe(false);
    expect(all.checked).toBe(true);
    expect(screen.getByText("已选 4 个")).toBeTruthy();
    expect(bulkDelete(4)).toBeTruthy();
    expect(screen.getByRole("button", { name: "下载所选的 4 份备份" })).toBeTruthy();
    for (const file of data.backups) expect(fileCheck(file).checked).toBe(true);
    await user.click(all);
    expect(all.checked).toBe(false);
    expect(queryBulkDelete()).toBeNull();
    expect(screen.queryByText(/^已选/)).toBeNull();
  });

  it("窄容器用的操作条全选与表头全选是同一份勾选", async () => {
    const { data } = setup();
    const user = userEvent.setup();
    render();
    const table = await ready();
    const bar = screen.getAllByRole<HTMLInputElement>("checkbox", { name: "全选" }).find((candidate) => !table.contains(candidate));
    if (bar === undefined) throw new Error("缺少操作条里的全选");
    await user.click(bar);
    expect(headSelectAll().checked).toBe(true);
    for (const file of data.backups) expect(fileCheck(file).checked).toBe(true);
    await user.click(headSelectAll());
    expect(bar.checked).toBe(false);
  });

  it("下载所选逐个交给浏览器下载，不发 JSON 请求，勾选保留", async () => {
    const { api, data } = setup();
    const chosen = data.backups.slice(1, 3);
    const clicked: (string | null)[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.getAttribute("href"));
    });
    const user = userEvent.setup();
    render();
    await ready();
    const reads = api.requestsTo("GET", /^\/api\/v1\/system\/backups\//).length;
    for (const file of chosen) await user.click(fileCheck(file));
    await user.click(screen.getByRole("button", { name: "下载所选的 2 份备份" }));
    expect(clicked).toEqual(chosen.map((file) => `${path}/${encodeURIComponent(file.name)}`));
    expect(api.requestsTo("GET", /^\/api\/v1\/system\/backups\//)).toHaveLength(reads);
    for (const file of chosen) expect(fileCheck(file).checked).toBe(true);
  });

  it("删除两份只发一个POST，清空勾选、聚焦标题并宣告结果", async () => {
    const { api, data } = setup();
    const chosen = data.backups.slice(0, 2);
    const user = userEvent.setup();
    render();
    await ready();
    for (const file of chosen) await user.click(fileCheck(file));
    await user.click(bulkDelete(2));
    const dialog = screen.getByRole("dialog", { name: "删除 2 份备份？" });
    expect(within(dialog).getByText("删除后不能恢复。")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const requests = api.requestsTo("POST", deletionPath);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toEqual({ names: chosen.map((file) => file.name) });
    expect(api.requestsTo("DELETE", /^\/api\/v1\/system\/backups\//)).toHaveLength(0);
    for (const file of chosen) expect(hasFileRow(file)).toBe(false);
    expect(queryBulkDelete()).toBeNull();
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
    await user.click(headSelectAll());
    await user.click(bulkDelete(4));
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
    await user.click(headSelectAll());
    await user.click(bulkDelete(4));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("POST", deletionPath)).toHaveLength(0);
    expect(bulkDelete(4)).toBeTruthy();
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
    await user.click(bulkDelete(2));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "删除" }));
    const dialog = await screen.findByRole("dialog", { name: "删除 1 份备份？" });
    expect(within(dialog).getByText("有 1 份没能删除")).toBeTruthy();
    expect(within(dialog).getByRole("list").textContent).toBe(formatDateTime(failed.created_at));
    expect(within(await ready()).getAllByRole("checkbox", { name: /^选择 / })).toHaveLength(3);
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
    await user.click(bulkDelete(2));
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
    expect(bulkDelete(2)).toBeTruthy();
    data.backups = data.backups.slice(1);
    await tick(2100);
    expect(bulkDelete(1)).toBeTruthy();
  });
});

const retentionFields = () => ({
  days: screen.getByRole<HTMLInputElement>("spinbutton", { name: "保留天数" }),
  count: screen.getByRole<HTMLInputElement>("spinbutton", { name: "保留份数" }),
  daysUnlimited: within(screen.getByRole("group", { name: "保留天数" })).getByRole<HTMLInputElement>("checkbox", { name: "不限" }),
  countUnlimited: within(screen.getByRole("group", { name: "保留份数" })).getByRole<HTMLInputElement>("checkbox", { name: "不限" }),
});
/** 只改保留份数（保留天数要已经是“不限”）：取消“不限”，填数字，按回车保存。 */
function setCountRule(count: string) {
  const fields = retentionFields();
  if (fields.countUnlimited.checked) fireEvent.click(fields.countUnlimited);
  fireEvent.change(fields.count, { target: { value: count } });
  fireEvent.keyDown(fields.count, { key: "Enter" });
}
const unlimitedBoth = { retention: { keep_days: null, keep_count: null } };
const savedStatus = () => within(screen.getByRole("region", { name: "自动清理" })).getByRole("status");

describe("设置 › 备份：自动清理", () => {
  it("初值取规则，取消不限会聚焦数字框且不保存，勾回不限禁用且保留值", async () => {
    const { api } = setup({ retention: { keep_days: null, keep_count: 3 } });
    const user = userEvent.setup();
    render();
    await ready();
    const fields = retentionFields();
    expect(fields.days.disabled).toBe(true);
    expect(fields.daysUnlimited.checked).toBe(true);
    expect(fields.count.value).toBe("3");
    expect(fields.countUnlimited.checked).toBe(false);
    expect(screen.queryByRole("button", { name: "保存" })).toBeNull();
    await user.click(fields.daysUnlimited);
    expect(fields.days.disabled).toBe(false);
    expect(document.activeElement).toBe(fields.days);
    // 取消“不限”时框里是默认值，不立即保存；等离开或回车。
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(0);
    fireEvent.change(fields.days, { target: { value: "30" } });
    // fireEvent 不移动焦点，数字框不触发离开：只验证勾回“不限”本身。
    fireEvent.click(fields.daysUnlimited);
    expect(fields.days.disabled).toBe(true);
    expect(fields.days.value).toBe("30");
    // 勾回之后的规则与服务器相同，不发请求。
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(0);
  });

  it("勾上不限立即保存：两项不限只发一次未确认PUT，不弹框不删文件，标题旁显示已保存", async () => {
    const { api, data } = setup();
    const before = [...data.backups];
    const user = userEvent.setup();
    render();
    await ready();
    expect(savedStatus().textContent).toBe("");
    await user.click(retentionFields().daysUnlimited);
    await waitFor(() => expect(savedStatus().textContent).toBe("已保存。"));
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(1);
    expect(api.requestsTo("PUT", retentionPath)[0]?.body).toEqual({ keep_days: null, keep_count: null, confirm: false });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(data.backups).toEqual(before);
    expect(data.retention).toEqual({ keep_days: null, keep_count: null });
  });

  it("数字框离开时保存，回车与随后的离开只发一次", async () => {
    const { api, data } = setup();
    const user = userEvent.setup();
    render();
    await ready();
    const { days } = retentionFields();
    await user.clear(days);
    await user.type(days, "3650");
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(0);
    await user.tab();
    await waitFor(() => expect(savedStatus().textContent).toBe("已保存。"));
    expect(api.requestsTo("PUT", retentionPath).map((request) => request.body)).toEqual([{ keep_days: 3650, keep_count: null, confirm: false }]);
    expect(data.retention).toEqual({ keep_days: 3650, keep_count: null });

    await user.clear(days);
    await user.type(days, "3000{Enter}");
    await user.tab();
    await waitFor(() => expect(data.retention).toEqual({ keep_days: 3000, keep_count: null }));
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(2);
    // 没改过就离开：不发请求。
    days.focus();
    await user.tab();
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(2);
  });

  it("保存期间又改的值排在后面接着保存，不被前一次的结果冲掉", async () => {
    const { api, data } = setup();
    const gate = deferred<void>();
    let first = true;
    api.on("PUT", retentionPath, async (request) => {
      if (first) {
        first = false;
        await gate.promise;
      }
      return systemAnswer(data, "PUT", retentionPath, request.body);
    });
    render();
    await ready();
    const fields = retentionFields();
    fireEvent.change(fields.days, { target: { value: "3650" } });
    fireEvent.keyDown(fields.days, { key: "Enter" });
    fireEvent.change(fields.days, { target: { value: "3000" } });
    fireEvent.keyDown(fields.days, { key: "Enter" });
    await waitFor(() => expect(api.requestsTo("PUT", retentionPath)).toHaveLength(1));
    await act(async () => gate.resolve());
    await waitFor(() => expect(data.retention).toEqual({ keep_days: 3000, keep_count: null }));
    expect(api.requestsTo("PUT", retentionPath).map((request) => request.body)).toEqual([
      { keep_days: 3650, keep_count: null, confirm: false },
      { keep_days: 3000, keep_count: null, confirm: false },
    ]);
    expect(retentionFields().days.value).toBe("3000");
  });

  it.each([
    ["保留天数", "0", "填 1 到 3650 之间的整数。"],
    ["保留天数", "3651", "填 1 到 3650 之间的整数。"],
    ["保留天数", "1.5", "填 1 到 3650 之间的整数。"],
    ["保留天数", "", "填 1 到 3650 之间的整数。"],
    ["保留份数", "0", "填 1 到 1000 之间的整数。"],
    ["保留份数", "1001", "填 1 到 1000 之间的整数。"],
    ["保留份数", "1.5", "填 1 到 1000 之间的整数。"],
  ])("%s=%s回车时显示字段错误且不发请求", async (legend, value, error) => {
    const { api } = setup();
    render();
    await ready();
    const fields = retentionFields();
    if (legend === "保留份数") fireEvent.click(fields.countUnlimited);
    const field = screen.getByRole("spinbutton", { name: legend });
    field.focus();
    fireEvent.change(field, { target: { value } });
    fireEvent.keyDown(field, { key: "Enter" });
    expect(screen.getByText(error)).toBeTruthy();
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(field);
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(0);
  });

  it("离开时数字不合法也显示字段错误、不发请求", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    render();
    await ready();
    const { days } = retentionFields();
    await user.clear(days);
    await user.type(days, "0");
    await user.tab();
    expect(screen.getByText("填 1 到 3650 之间的整数。")).toBeTruthy();
    expect(days.getAttribute("aria-invalid")).toBe("true");
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(0);
  });

  it("会删先确认，取消不改规则或文件，恢复原值", async () => {
    const { api, data } = setup(unlimitedBoth);
    const before = [...data.backups];
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("1");
    const dialog = await screen.findByRole("dialog", { name: "保存清理设置？" });
    expect(within(dialog).getByText("将删除 3 份旧备份。删除后不能恢复。")).toBeTruthy();
    expect(data.retention).toEqual({ keep_days: null, keep_count: null });
    expect(data.backups).toEqual(before);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("PUT", retentionPath)).toHaveLength(1);
    expect(retentionFields().countUnlimited.checked).toBe(true);
    expect(retentionFields().count.disabled).toBe(true);
    expect(savedStatus().textContent).toBe("");
  });

  it("保留3份确认后带confirm=true，刷新到最新3份并显示实际结果", async () => {
    const { api, data } = setup(unlimitedBoth);
    const expected = data.backups.slice(0, 3);
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("3");
    const dialog = await screen.findByRole("dialog", { name: "保存清理设置？" });
    expect(within(dialog).getByText("将删除 1 份旧备份。删除后不能恢复。")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    await waitFor(() => expect(savedStatus().textContent).toBe("已保存，删除了 1 份旧备份。"));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("PUT", retentionPath).map((request) => request.body)).toEqual([
      { keep_days: null, keep_count: 3, confirm: false },
      { keep_days: null, keep_count: 3, confirm: true },
    ]);
    expect(data.backups).toEqual(expected);
    const table = await ready();
    expect(within(table).getAllByRole("checkbox", { name: /^选择 / })).toHaveLength(3);
    for (const file of expected) expect(hasFileRow(file)).toBe(true);
    expect(retentionFields().count.value).toBe("3");
  });

  it("确认期间列表变化以实际结果为准，最新一份保留", async () => {
    const { data } = setup(unlimitedBoth);
    const latest = data.backups[0];
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("1");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("将删除 3 份旧备份。删除后不能恢复。")).toBeTruthy();
    data.backups = data.backups.slice(0, 3);
    await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    expect(await screen.findByText("已保存，删除了 2 份旧备份。")).toBeTruthy();
    expect(data.backups).toEqual([latest]);
  });

  it.each([false, true])("目录不可写提示留在%s并可重试", async (confirmation) => {
    const { api, data } = setup(confirmation ? { ...unlimitedBoth, retentionNotWritable: true } : { retentionNotWritable: true });
    const user = userEvent.setup();
    render();
    await ready();
    if (confirmation) setCountRule("1");
    else await user.click(retentionFields().daysUnlimited);
    if (confirmation) {
      const dialog = await screen.findByRole("dialog");
      await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
      expect(await within(dialog).findByText("备份目录不能写入，没有保存。")).toBeTruthy();
      data.retentionNotWritable = false;
      await user.click(within(dialog).getByRole("button", { name: "保存并删除" }));
    } else {
      expect(await screen.findByText("备份目录不能写入，没有保存。")).toBeTruthy();
      expect(screen.queryByRole("dialog")).toBeNull();
      // 失败后恢复原值：“不限”又是没勾上的，再勾一次就是重试。
      expect(retentionFields().daysUnlimited.checked).toBe(false);
      data.retentionNotWritable = false;
      await user.click(retentionFields().daysUnlimited);
    }
    expect(await screen.findByText(confirmation ? "已保存，删除了 3 份旧备份。" : "已保存。")).toBeTruthy();
    await waitFor(() => expect(screen.queryByText("备份目录不能写入，没有保存。")).toBeNull());
    expect(api.requestsTo("GET", path).length).toBeGreaterThan(1);
  });

  it("规则已保存但个别文件没删掉，显示失败份数且保留文件", async () => {
    const { data } = setup(unlimitedBoth);
    const failed = data.backups[1];
    if (failed === undefined) throw new Error("缺少种子备份");
    data.undeletable = new Set([failed.name]);
    const user = userEvent.setup();
    render();
    await ready();
    setCountRule("1");
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
    await user.click(headSelectAll());
    await user.click(bulkDelete(4));
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
