import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { decodeDiagnosticsPage, type ScanDTO } from "../features/library/api/decoders";
import { formatDateTime } from "../shared/lib/time";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installLibraryApi, seedScan, type FakeLibrary } from "../test/fake-library";
import { jsonResponse, sampleResponse } from "../test/http";
import { withValue } from "../test/mutate";
import { renderApp } from "../test/render-app";

function setup(options: { role?: "admin" | "user"; library?: Partial<FakeLibrary> } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const library = installLibraryApi(api, options.library);
  return { api, library };
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const statusPanel = () => screen.findByRole("region", { name: "状态" });
const scanPath = (scan: ScanDTO) => `/api/v1/scans/${scan.id}`;
const tick = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

/** 模拟页面在后台或回到前台：visibilitychange 会冒泡到 window，TanStack Query 据此判断是否在前台。 */
function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange", { bubbles: true }));
}

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(document, "visibilityState");
});

describe("扫描目录页：扫描区段", () => {
  it("没有活动扫描：说明扫描范围，提供“开始扫描”", async () => {
    setup();
    renderApp({ path: "/library" });
    expect(await screen.findByText(/没有正在运行的扫描。扫描会读取所有启用的目录/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "开始扫描" })).toBeTruthy();
  });

  it("开始扫描（202）后进入该扫描的详情页", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: "/library" });
    await userEvent.click(await screen.findByRole("button", { name: "开始扫描" }));

    expect(await heading("扫描详情")).toBeTruthy();
    expect(history.location.pathname).toMatch(/^\/library\/scans\/[0-9a-f-]{36}$/);
    expect(within(await statusPanel()).getByText("运行中")).toBeTruthy();
    const [request] = api.requestsTo("POST", "/api/v1/scans");
    expect(request?.body).toBeUndefined();
  });

  it("已有扫描在运行（200）：不新建，留在本页提示并给出进入该扫描的链接", async () => {
    const { library } = setup();
    const { history } = renderApp({ path: "/library" });
    const button = await screen.findByRole("button", { name: "开始扫描" });
    // 界面还显示“没有活动扫描”时，别处已经开始了扫描。
    const running = seedScan(library, {}, true);
    await userEvent.click(button);

    expect(await screen.findByText(/已有扫描在运行，没有新建扫描/)).toBeTruthy();
    expect(history.location.pathname).toBe("/library");
    const links = screen.getAllByRole("link", { name: /查看/ });
    expect(links.some((link) => link.getAttribute("href") === `/library/scans/${running.id}`)).toBe(true);
    // 活动扫描摘要随之出现，不再提供“开始扫描”。
    await waitFor(() => expect(screen.queryByRole("button", { name: "开始扫描" })).toBeNull());
  });

  it("503 scan_coordination_unavailable：说明无法协调扫描任务并提供重试，重试成功后进入详情", async () => {
    const { library } = setup();
    library.startScanFailure = jsonResponse(
      503,
      withValue(sampleBody("errors/database-unavailable"), ["error", "code"], "scan_coordination_unavailable"),
    );
    const { history } = renderApp({ path: "/library" });
    await userEvent.click(await screen.findByRole("button", { name: "开始扫描" }));

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("暂时无法协调扫描任务，请稍后重试。")).toBeTruthy();
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();

    library.startScanFailure = null;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await heading("扫描详情")).toBeTruthy();
    expect(history.location.pathname).toMatch(/^\/library\/scans\//);
  });

  it("有活动扫描：显示阶段、开始时间、诊断数与详情链接，不提供“开始扫描”", async () => {
    const { library } = setup();
    const scan = seedScan(library, { diagnostics: { total: 2, counts: [{ kind: "parse_failure", count: 2 }] } }, true);
    renderApp({ path: "/library" });

    expect(await screen.findByText(/已发现 2 条诊断/)).toBeTruthy();
    const section = screen.getByRole("heading", { level: 2, name: "扫描" }).closest("section");
    if (section === null) throw new Error("没有找到扫描区段");
    expect(within(section).getByText("运行中")).toBeTruthy();
    expect(within(section).getByRole("link", { name: "查看扫描详情" }).getAttribute("href")).toBe(`/library/scans/${scan.id}`);
    expect(screen.queryByRole("button", { name: "开始扫描" })).toBeNull();
  });

  it("取消请求中的活动扫描显示“取消请求中”，不显示成已取消", async () => {
    const { library } = setup();
    seedScan(library, { cancel_requested_at: "2026-01-02T00:00:05Z" }, true);
    renderApp({ path: "/library" });
    expect(await screen.findByText("取消请求中")).toBeTruthy();
    expect(screen.queryByText("已取消")).toBeNull();
  });

  it("活动扫描结束后，卡片在下一次轮询（5 秒）里自己更新", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library } = setup();
    const scan = seedScan(library, {}, true);
    renderApp({ path: "/library" });
    // 扫描记录里同一个扫描也显示“运行中”：这里只看“扫描”区段里的卡片。
    expect(await within(await screen.findByRole("region", { name: "扫描" })).findByText("运行中")).toBeTruthy();
    const reads = () => api.requestsTo("GET", "/api/v1/scans/active").length;
    const first = reads();

    library.scans.set(scan.id, { ...scan, status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    library.activeScanId = null;
    await tick(5000);
    expect(await screen.findByText(/没有正在运行的扫描。扫描会读取/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "开始扫描" })).toBeTruthy();
    expect(reads()).toBeGreaterThan(first);

    // 没有活动扫描后不再轮询。
    const settled = reads();
    await tick(30_000);
    expect(reads()).toBe(settled);
  });
});

/** 预置 count 个已成功的扫描：开始时间每个晚一分钟，所以最后预置的在列表（最新在前）里排第一。 */
function seedHistory(library: FakeLibrary, count: number, overrides: (index: number) => Partial<ScanDTO> = () => ({})): ScanDTO[] {
  return Array.from({ length: count }, (_, index) =>
    seedScan(library, {
      status: "succeeded",
      started_at: new Date(Date.UTC(2026, 0, 1, 0, index)).toISOString(),
      finished_at: new Date(Date.UTC(2026, 0, 1, 0, index, 30)).toISOString(),
      ...overrides(index),
    }),
  );
}

const historyRegion = () => screen.findByRole("region", { name: "扫描记录" });
const historyTable = () => screen.findByRole("table", { name: "扫描记录" });

function historyRows(table: HTMLElement): HTMLElement[] {
  return within(table).getAllByRole("row").slice(1);
}

function rowAt(table: HTMLElement, index: number): HTMLElement {
  const row = historyRows(table)[index];
  if (row === undefined) throw new Error(`扫描记录没有第 ${index + 1} 行`);
  return row;
}

const lastScanListRequest = (api: ReturnType<typeof setup>["api"]) => api.requestsTo("GET", "/api/v1/scans").at(-1)?.path;

describe("扫描目录页：扫描记录", () => {
  it("没有扫描：说明还没有记录，不显示表格与分页", async () => {
    setup();
    renderApp({ path: "/library" });
    const region = await historyRegion();
    expect(await within(region).findByText("还没有扫描记录。开始一次扫描后，它会出现在这里。")).toBeTruthy();
    expect(within(region).queryByRole("table")).toBeNull();
    expect(within(region).queryByRole("navigation")).toBeNull();
  });

  it("列出扫描：最新的在前；开始时间、状态、结束时间与诊断数；运行中显示“进行中”；每行有进入详情的链接", async () => {
    const { library } = setup();
    const failed = seedScan(library, {
      status: "failed",
      started_at: "2026-01-01T00:00:00Z",
      finished_at: "2026-01-01T00:01:00Z",
      diagnostics: { total: 1234, counts: [{ kind: "parse_failure", count: 1234 }] },
    });
    const succeeded = seedScan(library, { status: "succeeded", started_at: "2026-01-02T00:00:00Z", finished_at: "2026-01-02T00:02:00Z" });
    const canceling = seedScan(library, { started_at: "2026-01-03T00:00:00Z", cancel_requested_at: "2026-01-03T00:00:10Z" }, true);
    renderApp({ path: "/library" });

    const table = await historyTable();
    expect(historyRows(table)).toHaveLength(3);
    const [first, second, third] = [rowAt(table, 0), rowAt(table, 1), rowAt(table, 2)];
    expect(within(first).getByRole("rowheader").textContent).toBe(formatDateTime(canceling.started_at));
    expect(within(first).getByText("取消请求中")).toBeTruthy();
    expect(within(first).getByText("进行中")).toBeTruthy();
    expect(within(second).getByRole("rowheader").textContent).toBe(formatDateTime(succeeded.started_at));
    expect(within(second).getByText("成功")).toBeTruthy();
    expect(within(second).getByText(formatDateTime("2026-01-02T00:02:00Z"))).toBeTruthy();
    expect(within(third).getByText("失败")).toBeTruthy();
    expect(within(third).getByText("1,234")).toBeTruthy();
    expect(within(third).getByText(formatDateTime(failed.finished_at ?? ""))).toBeTruthy();

    // 每行的链接文字相同，开始时间在可访问名称里，读屏的链接列表里分得清。
    const link = within(third).getByRole("link", { name: `查看详情（${formatDateTime(failed.started_at)} 开始的扫描）` });
    expect(link.getAttribute("href")).toBe(`/library/scans/${failed.id}`);
    expect(within(await historyRegion()).getByText(/共 3 次扫描，最新的在前，每页 20 条。/)).toBeTruthy();
  });

  it("从记录进入详情：显示该次扫描的状态", async () => {
    const { library } = setup();
    const scan = seedHistory(library, 1, () => ({ status: "incomplete" }))[0];
    if (scan === undefined) throw new Error("没有预置扫描");
    renderApp({ path: "/library" });
    await userEvent.click(await within(await historyTable()).findByRole("link", { name: /^查看详情/ }));
    expect(await heading("扫描详情")).toBeTruthy();
    expect(within(await statusPanel()).getByText("不完整")).toBeTruthy();
  });

  it("超过一页：显示分页，翻页更新地址（第 1 页不写参数），两端的上一页与下一页不可用，翻页请求带页码与页大小", async () => {
    const { api, library } = setup();
    seedHistory(library, 45);
    const { history } = renderApp({ path: "/library" });
    const nav = await screen.findByRole("navigation", { name: "扫描记录分页" });
    expect(within(nav).getByText("第 1 / 3 页").getAttribute("aria-current")).toBe("page");
    expect(within(nav).queryByRole("link", { name: "上一页" })).toBeNull();
    expect(historyRows(await historyTable())).toHaveLength(20);
    expect(lastScanListRequest(api)).toBe("/api/v1/scans?page=1&page_size=20");

    await userEvent.click(within(nav).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(history.location.search).toBe("?scan_page=2"));
    await waitFor(() => expect(within(screen.getByRole("navigation", { name: "扫描记录分页" })).getByText("第 2 / 3 页")).toBeTruthy());
    expect(lastScanListRequest(api)).toBe("/api/v1/scans?page=2&page_size=20");
    expect(historyRows(await historyTable())).toHaveLength(20);

    await userEvent.click(within(screen.getByRole("navigation", { name: "扫描记录分页" })).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(history.location.search).toBe("?scan_page=3"));
    await waitFor(() => expect(historyRows(screen.getByRole("table", { name: "扫描记录" }))).toHaveLength(5));
    const last = screen.getByRole("navigation", { name: "扫描记录分页" });
    expect(within(last).getByText("第 3 / 3 页")).toBeTruthy();
    expect(within(last).queryByRole("link", { name: "下一页" })).toBeNull();

    await userEvent.click(within(last).getByRole("link", { name: "上一页" }));
    await waitFor(() => expect(history.location.search).toBe("?scan_page=2"));
    await userEvent.click(within(await screen.findByRole("navigation", { name: "扫描记录分页" })).getByRole("link", { name: "上一页" }));
    await waitFor(() => expect(history.location.search).toBe(""));
    await waitFor(() => expect(within(screen.getByRole("navigation", { name: "扫描记录分页" })).getByText("第 1 / 3 页")).toBeTruthy());
  });

  it("一页放得下时不显示分页", async () => {
    const { library } = setup();
    seedHistory(library, 20);
    renderApp({ path: "/library" });
    expect(historyRows(await historyTable())).toHaveLength(20);
    expect(screen.queryByRole("navigation", { name: "扫描记录分页" })).toBeNull();
  });

  it.each([
    ["?scan_page=2", 2],
    ["?scan_page=3", 3],
    ["?scan_page=abc", 1],
    ["?scan_page=0", 1],
    ["?scan_page=1", 1],
    ["?scan_page=-2", 1],
    ["?scan_page=2.5", 1],
    ["?scan_page=1000001", 1],
  ])("地址 %s：请求第 %i 页，不合法的值回到第 1 页", async (search, expectedPage) => {
    const { api, library } = setup();
    seedHistory(library, 45);
    renderApp({ path: `/library${search}` });
    await historyTable();
    expect(api.requestsTo("GET", "/api/v1/scans").map((request) => request.path)).toEqual([`/api/v1/scans?page=${expectedPage}&page_size=20`]);
  });

  it("页码超出末页：用替换历史的方式改到末页，不新增历史项，并显示末页的内容", async () => {
    const { library } = setup();
    seedHistory(library, 45);
    const { history } = renderApp({ path: "/library?scan_page=99" });
    await waitFor(() => expect(history.location.search).toBe("?scan_page=3"));
    await waitFor(() => expect(historyRows(screen.getByRole("table", { name: "扫描记录" }))).toHaveLength(5));
    expect(within(screen.getByRole("navigation", { name: "扫描记录分页" })).getByText("第 3 / 3 页")).toBeTruthy();
    expect(history.length).toBe(1);
  });

  it("没有扫描时带了页码：回到第 1 页的空态，地址里不再有参数", async () => {
    setup();
    const { history } = renderApp({ path: "/library?scan_page=3" });
    expect(await within(await historyRegion()).findByText(/还没有扫描记录/)).toBeTruthy();
    await waitFor(() => expect(history.location.search).toBe(""));
    expect(history.length).toBe(1);
  });

  it("读取失败（503）：区段内显示带请求 ID 的错误与重试，其他区段不受影响，重试成功后显示记录", async () => {
    const { api, library } = setup();
    seedHistory(library, 2);
    api.on("GET", "/api/v1/scans", () => sampleResponse("errors/database-unavailable"));
    renderApp({ path: "/library" });
    const region = await historyRegion();
    // 503 会被自动重试（最多 2 次，带退避），耗尽后才显示错误。
    const alert = await within(region).findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();
    expect(screen.getByRole("rowheader", { name: "classical-a" })).toBeTruthy();

    api.on("GET", "/api/v1/scans", () => jsonResponse(200, sampleBody("scans/list")));
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(historyRows(await historyTable())).toHaveLength(4);
    expect(within(region).queryByRole("alert")).toBeNull();
  }, 15_000);

  it("已有扫描在运行（200）：留在本页，扫描记录随之刷新并出现这个运行中的扫描", async () => {
    const { library } = setup();
    seedHistory(library, 1);
    renderApp({ path: "/library" });
    const button = await screen.findByRole("button", { name: "开始扫描" });
    expect(historyRows(await historyTable())).toHaveLength(1);
    // 界面还显示“没有活动扫描”时，别处已经开始了扫描。
    seedScan(library, { started_at: "2026-02-01T00:00:00Z" }, true);
    await userEvent.click(button);

    await waitFor(() => expect(historyRows(screen.getByRole("table", { name: "扫描记录" }))).toHaveLength(2));
    expect(within(rowAt(screen.getByRole("table", { name: "扫描记录" }), 0)).getByText("运行中")).toBeTruthy();
  });

  it("活动扫描结束：卡片更新的同时，扫描记录里的“运行中”也变成终态", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { library } = setup();
    const scan = seedScan(library, { started_at: "2026-02-01T00:00:00Z" }, true);
    renderApp({ path: "/library" });
    expect(within(rowAt(await historyTable(), 0)).getByText("运行中")).toBeTruthy();

    library.scans.set(scan.id, { ...scan, status: "failed", finished_at: "2026-02-01T00:03:00Z" });
    library.activeScanId = null;
    await tick(5000);
    expect(await screen.findByText(/没有正在运行的扫描。扫描会读取/)).toBeTruthy();
    await waitFor(() => expect(within(rowAt(screen.getByRole("table", { name: "扫描记录" }), 0)).getByText("失败")).toBeTruthy());
    expect(within(rowAt(screen.getByRole("table", { name: "扫描记录" }), 0)).queryByText("进行中")).toBeNull();
  });

  it("扫描在详情页结束后回到扫描目录页：扫描记录显示终态，不是缓存里的“运行中”", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { library } = setup();
    const scan = seedScan(library, { started_at: "2026-02-01T00:00:00Z" }, true);
    renderApp({ path: "/library" });
    expect(within(rowAt(await historyTable(), 0)).getByText("运行中")).toBeTruthy();

    await userEvent.click(await screen.findByRole("link", { name: "查看扫描详情" }));
    const panel = await statusPanel();
    library.scans.set(scan.id, { ...scan, status: "succeeded", finished_at: "2026-02-01T00:01:00Z" });
    library.activeScanId = null;
    await tick(2000);
    await waitFor(() => expect(within(panel).getByRole("status").textContent).toBe("成功"));

    await userEvent.click(screen.getByRole("link", { name: "返回扫描目录" }));
    await waitFor(() => expect(within(rowAt(screen.getByRole("table", { name: "扫描记录" }), 0)).getByText("成功")).toBeTruthy());
  });
});

describe("扫描详情：状态", () => {
  const finishedAt = "2026-01-02T00:01:00Z";
  const phases: { label: string; scan: Partial<ScanDTO>; note: RegExp; reconciles: boolean }[] = [
    { label: "运行中", scan: {}, note: /没有预先统计的总量，所以不显示百分比或剩余时间/, reconciles: false },
    {
      label: "取消请求中",
      scan: { cancel_requested_at: "2026-01-02T00:00:30Z" },
      note: /取消请求已提交，等待扫描停止/,
      reconciles: false,
    },
    { label: "成功", scan: { status: "succeeded", finished_at: finishedAt }, note: /此前入库、这次没有再出现的来源已标为缺失/, reconciles: true },
    { label: "失败", scan: { status: "failed", finished_at: finishedAt }, note: /没有执行缺失对账/, reconciles: false },
    { label: "已取消", scan: { status: "canceled", finished_at: finishedAt, cancel_requested_at: "2026-01-02T00:00:30Z" }, note: /没有执行缺失对账/, reconciles: false },
    { label: "不完整", scan: { status: "incomplete", finished_at: finishedAt }, note: /没有执行缺失对账.*请查看诊断/, reconciles: false },
  ];

  it.each(phases)("$label：阶段标签与说明互不混同，不出现百分比或剩余时间", async ({ label, scan: overrides, note }) => {
    const { library } = setup();
    const scan = seedScan(library, overrides);
    renderApp({ path: `/library/scans/${scan.id}` });

    const panel = await statusPanel();
    expect(within(panel).getByRole("status").textContent).toBe(label);
    expect(within(panel).getByText(note)).toBeTruthy();
    // 说明文字会明确写“不显示百分比或剩余时间”；这里只断言界面没有真的显示数值进度或预计时间。
    expect(panel.textContent).not.toMatch(/\d+\s*%|预计剩余|ETA/);
    // 六种阶段里只有这一种标签出现。
    for (const other of phases.filter((phase) => phase.label !== label)) {
      expect(within(panel).getByRole("status").textContent).not.toBe(other.label);
    }
    expect(document.title).toBe("扫描详情 · ROOMusic");
  });

  it("显示开始、结束与取消请求时间、诊断数；运行中没有结束时间", async () => {
    const { library } = setup();
    const scan = seedScan(library, { cancel_requested_at: "2026-01-02T00:00:30Z", diagnostics: { total: 3, counts: [{ kind: "parse_failure", count: 3 }] } });
    renderApp({ path: `/library/scans/${scan.id}` });
    const panel = await statusPanel();
    const facts = within(panel).getByText("开始时间").closest("dl");
    if (facts === null) throw new Error("没有找到事实列表");
    expect(within(facts).getByText("结束时间").nextElementSibling?.textContent).toBe("—");
    expect(within(facts).getByText("请求取消")).toBeTruthy();
    expect(within(facts).getByText("3 条")).toBeTruthy();
  });

  it("运行中每 2 秒轮询，读到终态就停止；回到扫描目录页时活动扫描已是最新", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library } = setup();
    const scan = seedScan(library, {}, true);
    renderApp({ path: "/library" });
    await userEvent.click(await screen.findByRole("link", { name: "查看扫描详情" }));
    const panel = await statusPanel();
    expect(within(panel).getByText("运行中")).toBeTruthy();
    const reads = () => api.requestsTo("GET", scanPath(scan)).length;

    const first = reads();
    await tick(2000);
    expect(reads()).toBeGreaterThan(first);
    const second = reads();
    await tick(2000);
    expect(reads()).toBeGreaterThan(second);

    library.scans.set(scan.id, { ...scan, status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    library.activeScanId = null;
    await tick(2000);
    await waitFor(() => expect(within(panel).getByRole("status").textContent).toBe("成功"));
    const settled = reads();
    await tick(30_000);
    expect(reads()).toBe(settled);

    // 终态让活动扫描的缓存过期：回到扫描目录页时立刻显示没有活动扫描，而不是等 30 秒的保鲜期或下一次轮询。
    await userEvent.click(screen.getByRole("link", { name: "返回扫描目录" }));
    expect(await screen.findByText(/没有正在运行的扫描。扫描会读取/)).toBeTruthy();
  });

  it("页面隐藏时暂停轮询，重新可见时立刻刷新", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library } = setup();
    const scan = seedScan(library);
    renderApp({ path: `/library/scans/${scan.id}` });
    await statusPanel();
    const reads = () => api.requestsTo("GET", scanPath(scan)).length;

    setVisibility("hidden");
    const hiddenAt = reads();
    await tick(20_000);
    expect(reads()).toBe(hiddenAt);

    setVisibility("visible");
    await waitFor(() => expect(reads()).toBeGreaterThan(hiddenAt));
  });

  it("读到终态之后，晚到的轮询不会把它改回运行中（终态不再请求）", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library } = setup();
    const scan = seedScan(library, { status: "failed", finished_at: "2026-01-02T00:01:00Z" });
    renderApp({ path: `/library/scans/${scan.id}` });
    await statusPanel();
    const first = api.requestsTo("GET", scanPath(scan)).length;
    await tick(60_000);
    expect(api.requestsTo("GET", scanPath(scan))).toHaveLength(first);
  });
});

describe("扫描详情：取消", () => {
  it("取消请求被接受后显示“取消请求中”，不宣称已取消，也不再提供取消按钮", async () => {
    const { api, library } = setup();
    const scan = seedScan(library);
    renderApp({ path: `/library/scans/${scan.id}` });
    await userEvent.click(await screen.findByRole("button", { name: "取消扫描" }));

    const panel = await statusPanel();
    await waitFor(() => expect(within(panel).getByRole("status").textContent).toBe("取消请求中"));
    expect(within(panel).getByText(/取消请求已提交，等待扫描停止/)).toBeTruthy();
    expect(within(panel).queryByRole("button", { name: /取消扫描|正在请求取消/ })).toBeNull();
    expect(screen.queryByText("已取消")).toBeNull();
    expect(api.requestsTo("POST", `${scanPath(scan)}/cancel`)).toHaveLength(1);
  });

  it("取消时扫描恰好结束（200）：状态面板显示终态，取消按钮消失，不显示成已取消", async () => {
    const { library } = setup();
    const scan = seedScan(library);
    renderApp({ path: `/library/scans/${scan.id}` });
    const cancel = await screen.findByRole("button", { name: "取消扫描" });
    // 界面还是运行中时，扫描已经结束。
    library.scans.set(scan.id, { ...scan, status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    await userEvent.click(cancel);
    const panel = await statusPanel();
    await waitFor(() => expect(within(panel).getByRole("status").textContent).toBe("成功"));
    expect(screen.queryByRole("button", { name: "取消扫描" })).toBeNull();
    expect(screen.queryByText("已取消")).toBeNull();
  });

  it("取消失败（503）：显示错误与请求 ID，可以重试", async () => {
    const { api, library } = setup();
    const scan = seedScan(library);
    api.on("POST", `${scanPath(scan)}/cancel`, () => sampleResponse("errors/database-unavailable"));
    renderApp({ path: `/library/scans/${scan.id}` });
    await userEvent.click(await screen.findByRole("button", { name: "取消扫描" }));
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("服务暂时不可用，请稍后重试。")).toBeTruthy();
    expect(within(alert).getByRole("button", { name: "重试" })).toBeTruthy();
    expect(within(await statusPanel()).getByRole("status").textContent).toBe("运行中");
  });
});

describe("扫描详情：诊断", () => {
  const multiple = decodeDiagnosticsPage(sampleBody("scans/diagnostics-multiple-kinds"));

  it("按类别汇总并列出明细：已知类别显示中文名，路径只显示相对路径，没有路径的显示占位", async () => {
    const { library } = setup();
    const scan = seedScan(library, { status: "failed", finished_at: "2026-01-02T00:01:00Z", diagnostics: multiple.summary });
    library.diagnostics.set(scan.id, multiple);
    renderApp({ path: `/library/scans/${scan.id}` });

    const summary = await screen.findByRole("list", { name: "按类别汇总" });
    expect(within(summary).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "数据库操作失败1",
      "音频解析失败1",
      "没有读取权限1",
      "格式暂不支持1",
    ]);
    const table = screen.getByRole("table", { name: "诊断明细" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(4);
    expect(within(rows[0] as HTMLElement).getByText("disc-1/01.flac").tagName).toBe("CODE");
    expect(within(rows[0] as HTMLElement).getByText("音频文件解析失败")).toBeTruthy();
    expect(within(rows[2] as HTMLElement).getAllByRole("cell")[2]?.textContent).toBe("—");
  });

  it("未知的诊断类别原样显示，不让界面出错", async () => {
    const { library } = setup();
    const scan = seedScan(library, { status: "incomplete", finished_at: "2026-01-02T00:01:00Z" });
    const unknown = decodeDiagnosticsPage(
      withValue(withValue(sampleBody("scans/diagnostics"), ["items", 0, "kind"], "brand_new_kind"), ["summary", "counts", 0, "kind"], "brand_new_kind"),
    );
    library.diagnostics.set(scan.id, unknown);
    renderApp({ path: `/library/scans/${scan.id}` });
    const summary = await screen.findByRole("list", { name: "按类别汇总" });
    expect(within(summary).getByText("brand_new_kind")).toBeTruthy();
    const table = screen.getByRole("table", { name: "诊断明细" });
    expect(within(table).getAllByText("brand_new_kind").length).toBeGreaterThan(0);
  });

  it("没有诊断：运行中与已结束分别说明", async () => {
    const { library } = setup();
    const running = seedScan(library);
    renderApp({ path: `/library/scans/${running.id}` });
    expect(await screen.findByText("到目前为止没有发现需要检查的问题。")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "诊断明细" })).toBeNull();
  });

  it("已结束的扫描没有诊断：说明本次没有发现问题", async () => {
    const { library } = setup();
    const done = seedScan(library, { status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    renderApp({ path: `/library/scans/${done.id}` });
    expect(await screen.findByText("本次扫描没有发现需要检查的问题。")).toBeTruthy();
  });

  it("诊断总数超过明细上限时，提示只显示前 100 条", async () => {
    const { library } = setup();
    const scan = seedScan(library, { status: "failed", finished_at: "2026-01-02T00:01:00Z", diagnostics: { total: 150, counts: [{ kind: "parse_failure", count: 150 }] } });
    library.diagnostics.set(scan.id, {
      items: Array.from({ length: 100 }, (_, index) => ({ id: String(index + 1), kind: "parse_failure", path: `disc/${index + 1}.flac`, message: "音频文件解析失败" })),
      summary: { total: 150, counts: [{ kind: "parse_failure", count: 150 }] },
    });
    renderApp({ path: `/library/scans/${scan.id}` });
    expect(await screen.findByText("共 150 条诊断，只显示前 100 条明细。")).toBeTruthy();
  });

  it("不轮询诊断：运行中手动刷新，扫描结束时自动刷新一次", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library } = setup();
    const scan = seedScan(library, { diagnostics: { total: 0, counts: [] } });
    renderApp({ path: `/library/scans/${scan.id}` });
    await screen.findByText("到目前为止没有发现需要检查的问题。");
    const reads = () => api.requestsTo("GET", `${scanPath(scan)}/diagnostics`).length;
    expect(reads()).toBe(1);

    // 轮询状态不会带动诊断请求。
    await tick(6000);
    expect(reads()).toBe(1);

    // 手动刷新。
    library.diagnostics.set(scan.id, decodeDiagnosticsPage(sampleBody("scans/diagnostics")));
    await userEvent.click(screen.getByRole("button", { name: "刷新诊断" }));
    expect(await screen.findByRole("table", { name: "诊断明细" })).toBeTruthy();
    expect(reads()).toBe(2);

    // 扫描结束：自动刷新一次，之后不再请求。
    library.scans.set(scan.id, { ...scan, status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    await tick(2000);
    await waitFor(() => expect(reads()).toBe(3));
    await tick(30_000);
    expect(reads()).toBe(3);
  });

  it("一进来就是已结束的扫描：诊断只加载一次", async () => {
    const { api, library } = setup();
    const scan = seedScan(library, { status: "failed", finished_at: "2026-01-02T00:01:00Z" });
    renderApp({ path: `/library/scans/${scan.id}` });
    await screen.findByText("本次扫描没有发现需要检查的问题。");
    expect(api.requestsTo("GET", `${scanPath(scan)}/diagnostics`)).toHaveLength(1);
  });
});

describe("扫描详情：访问", () => {
  it("扫描 ID 不是 UUID：直接显示“扫描不存在”，不发任何扫描请求", async () => {
    const { api } = setup();
    renderApp({ path: "/library/scans/not-a-uuid" });
    expect(await heading("扫描不存在")).toBeTruthy();
    expect(api.requests.filter((request) => request.path.includes("/api/v1/scans/"))).toHaveLength(0);
    expect(screen.getByRole("link", { name: "返回扫描目录" })).toBeTruthy();
  });

  it("扫描 ID 是 UUID 但服务器返回 404：同样显示“扫描不存在”，不显示诊断区段", async () => {
    setup();
    renderApp({ path: "/library/scans/00000000-0000-4000-8000-0000000000ff" });
    expect(await heading("扫描不存在")).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "诊断" })).toBeNull();
  });

  it("成员访问：显示“需要管理员权限”，不请求扫描与诊断", async () => {
    const { api, library } = setup({ role: "user" });
    const scan = seedScan(library);
    renderApp({ path: `/library/scans/${scan.id}` });
    expect(await heading("需要管理员权限")).toBeTruthy();
    expect(api.requests.filter((request) => request.path.includes("/api/v1/scans/"))).toHaveLength(0);
  });

  it("读取失败（503）：显示带请求 ID 的错误与重试，重试成功后显示状态", async () => {
    const { api, library } = setup();
    const scan = seedScan(library);
    let failing = true;
    api.on("GET", scanPath(scan), () => (failing ? sampleResponse("errors/database-unavailable") : jsonResponse(200, library.scans.get(scan.id))));
    renderApp({ path: `/library/scans/${scan.id}` });
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();
    failing = false;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(within(await statusPanel()).getByText("运行中")).toBeTruthy();
  }, 15_000);
});

describe("概览里的入口", () => {
  it("管理员的概览有进入扫描目录与目录管理的链接，活动扫描可进入详情", async () => {
    const { library } = setup();
    const scan = seedScan(library, {}, true);
    renderApp({ path: "/" });
    expect(await screen.findByRole("link", { name: "管理目录" })).toBeTruthy();
    expect(await screen.findByRole("link", { name: "查看扫描详情" })).toBeTruthy();
    const hrefs = screen.getAllByRole("link").map((link) => `${link.textContent}→${link.getAttribute("href")}`);
    expect(hrefs).toContain("管理目录→/library");
    // 导航与扫描区块各有一个“扫描目录”入口。
    expect(hrefs.filter((entry) => entry === "扫描目录→/library")).toHaveLength(2);
    expect(hrefs).toContain(`查看扫描详情→/library/scans/${scan.id}`);
  });

  it("成员的概览只看到扫描状态，没有管理链接", async () => {
    const { library } = setup({ role: "user" });
    seedScan(library, {}, true);
    renderApp({ path: "/" });
    expect(await screen.findByText("运行中")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "扫描目录" })).toBeNull();
    expect(screen.queryByRole("link", { name: "查看扫描详情" })).toBeNull();
    expect(screen.queryByRole("link", { name: "管理目录" })).toBeNull();
  });
});
