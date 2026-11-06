import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { catalogKeys } from "../features/catalog/query-keys";
import { decodeScan, decodeScanReport, type ScanDTO, type ScanReportDTO } from "../features/library/api/decoders";
import { libraryKeys } from "../features/library/query-keys";
import { sampleBody } from "../test/api-samples";
import { claimReleaseId } from "../test/claim-data";
import { alpha, setup as setupClaims } from "../test/claims-setup";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installLibraryApi, seedScan } from "../test/fake-library";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const reportSample = (name = "report-ready") => decodeScanReport(sampleBody(`scan-feedback/${name}`));
const scanSample = (name: string) => decodeScan(sampleBody(`scan-feedback/${name}`));
const reportPath = (id: string) => `/api/v1/scans/${id}/report`;
const tick = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
const reportRegion = () => screen.findByRole("region", { name: "扫描报告" });
const statusRegion = () => screen.findByRole("region", { name: "状态" });

afterEach(() => vi.useRealTimers());

function setup(report = reportSample(), overrides: Partial<ScanDTO> = {}, role: "admin" | "user" = "admin") {
  const api = installFakeApi();
  installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  const library = installLibraryApi(api);
  const scan = seedScan(library, {
    ...scanSample(`terminal-${report.scan_status}`),
    id: report.scan_run_id,
    scan_run_id: report.scan_run_id,
    report_status: report.status,
    ...overrides,
  });
  api.on("GET", reportPath(scan.id), () => jsonResponse(200, report));
  return { api, library, scan, report };
}

function renderDetail(scan: ScanDTO) {
  return renderApp({ path: `/settings/library/scans/${scan.id}` });
}

function expectChanges(region: HTMLElement, report: ScanReportDTO) {
  const table = within(region).getByRole("table", { name: "专辑与曲目的变化" });
  for (const [label, counts] of [["专辑", report.albums], ["曲目", report.tracks]] as const) {
    const row = within(table).getByRole("row", { name: new RegExp(`^${label} `) });
    expect(within(row).getAllByRole("cell").map((cell) => cell.textContent)).toEqual([String(counts.added), String(counts.updated), String(counts.missing)]);
  }
}

describe("扫描报告：真实样本与状态边界", () => {
  it("ready 展示文件、问题、专辑与曲目的增改失以及新增列表", async () => {
    const { api, scan, report } = setup();
    renderDetail(scan);
    const region = await reportRegion();
    await within(region).findByRole("table", { name: "专辑与曲目的变化" });
    expectChanges(region, report);
    expect(within(region).getByText("发现 5 个")).toBeTruthy();
    expect(within(region).getByText("2 条")).toBeTruthy();
    expect(within(region).getByRole("link", { name: "replaced" })).toBeTruthy();
    expect(within(region).getByRole("link", { name: "new" })).toBeTruthy();
    expect(api.requestsTo("GET", reportPath(scan.id)).map((request) => request.path)).toEqual([`${reportPath(scan.id)}?page=1&page_size=20`]);
  });

  it.each(["failed", "canceled", "incomplete"] as const)("%s 的 partial 只说明已保存的变化，不称全部完成", async (status) => {
    const report: ScanReportDTO = { ...reportSample("report-partial"), scan_status: status };
    const { scan } = setup(report);
    renderDetail(scan);
    const region = await reportRegion();
    expect(await within(region).findByText("扫描没有完成，这里只有停下之前已经保存的变化。")).toBeTruthy();
    expect(within(region).getByText("已处理 5 个")).toBeTruthy();
    expectChanges(region, report);
    expect(within(await statusRegion()).queryByText("扫描完成。这次没找到的文件已标为缺失。")).toBeNull();
    expect(within(region).queryByText("发现 5 个")).toBeNull();
  });

  it("报告状态 unavailable 不请求报告，成功扫描仍保持 100% 与成功状态", async () => {
    const { api, scan } = setup(undefined, { report_status: "unavailable" });
    renderDetail(scan);
    expect(await screen.findByText("这次的报告没能生成，扫描结果已经保存。")).toBeTruthy();
    expect(within(await statusRegion()).getByRole("status").textContent).toBe("成功");
    expect(screen.getByRole("progressbar", { name: "扫描进度" }).getAttribute("aria-valuenow")).toBe("100");
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(0);
    expect(within(await reportRegion()).queryByRole("table")).toBeNull();
  });

  it("报告接口 503 scan_report_unavailable 显示降级文案，不重试或改写扫描结果", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, scan } = setup();
    api.on("GET", reportPath(scan.id), () => sampleResponse("scan-feedback/report-unavailable"));
    renderDetail(scan);
    expect(await screen.findByText("这次的报告没能生成，扫描结果已经保存。")).toBeTruthy();
    expect(within(await statusRegion()).getByRole("status").textContent).toBe("成功");
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
    await tick(30_000);
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(1);
  });

  it.each(["legacy", "pending", undefined] as const)("report_status=%s 不显示报告，也不发出无效请求", async (reportStatus) => {
    const { api, scan } = setup(undefined, {
      ...(reportStatus === undefined ? {} : { report_status: reportStatus }),
      status: reportStatus === "pending" ? "running" : "succeeded",
      progress: null,
      root_progress: null,
    });
    if (reportStatus === undefined) delete scan.report_status;
    renderDetail(scan);
    await statusRegion();
    expect(screen.queryByRole("region", { name: "扫描报告" })).toBeNull();
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(0);
  });

  it("pending 随 2 秒状态轮询进入 ready 后只取一次报告，不另开报告轮询", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library, scan } = setup(undefined, { ...scanSample("reading_tags-admin"), report_status: "pending" });
    renderDetail(scan);
    await statusRegion();
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(0);
    library.scans.set(scan.id, { ...scan, status: "succeeded", finished_at: "2026-01-01T00:01:00Z", report_status: "ready" });
    await tick(2000);
    await screen.findByRole("table", { name: "专辑与曲目的变化" });
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(1);
    await tick(30_000);
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(1);
  });

  it("问题数达到存储上限时显示至少 100 条，而非断言恰好 100 条", async () => {
    const { scan } = setup(reportSample("report-issues-truncated"));
    renderDetail(scan);
    expect(await screen.findByText("至少 100 条")).toBeTruthy();
  });

  it("没有新增专辑时保留零值汇总，显示空列表说明且无分页", async () => {
    const { scan, report } = setup(reportSample("report-ready-empty"));
    renderDetail(scan);
    expect(await screen.findByText("这次没有新增专辑。")).toBeTruthy();
    const region = await reportRegion();
    expectChanges(region, report);
    expect(within(region).getByText("没有")).toBeTruthy();
    expect(within(region).queryByRole("navigation", { name: "新增专辑分页" })).toBeNull();
  });

  it("计数后文件变化保留计数时的数字，报告不冒充固定文件快照", async () => {
    const report = reportSample();
    const { scan } = setup({ ...report, files: { ...report.files, processed: 6, total: 5, changed: true } });
    renderDetail(scan);
    expect(await screen.findByText("发现 6 个（数文件时是 5 个）")).toBeTruthy();
  });

  it("普通成员直达详情被权限页拦截，不发扫描、报告、诊断请求", async () => {
    const { api, scan } = setup(undefined, {}, "user");
    renderDetail(scan);
    expect(await screen.findByRole("heading", { name: "需要管理员权限" })).toBeTruthy();
    expect(api.requests.filter((request) => request.path.startsWith(`/api/v1/scans/${scan.id}`))).toHaveLength(0);
  });

  it("报告读取失败可以重试，扫描状态与诊断上下文保留", async () => {
    const { api, scan, report } = setup();
    let failed = true;
    api.on("GET", reportPath(scan.id), () => failed
      ? jsonResponse(500, { error: { code: "internal_error", message: "读取失败" }, request_id: "report-read-failure" })
      : jsonResponse(200, report));
    renderDetail(scan);
    const region = await reportRegion();
    const alert = await within(region).findByRole("alert");
    expect(alert.textContent).toContain("report-read-failure");
    expect(within(await statusRegion()).getByRole("status").textContent).toBe("成功");
    expect(await screen.findByRole("region", { name: "诊断" })).toBeTruthy();
    failed = false;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await within(region).findByRole("table", { name: "专辑与曲目的变化" });
    expectChanges(region, report);
  });
});

describe("扫描报告：新增列表、分页与动态认领", () => {
  it("现在不可见的新项仍保留历史标题与计数，但没有专辑链接", async () => {
    const { scan, report } = setup(reportSample("report-historical-invisible"));
    renderDetail(scan);
    const region = await reportRegion();
    const invisible = await within(region).findByText("现在看不到了");
    expect(invisible.closest("li")?.textContent).toContain("replaced");
    expect(within(region).queryByRole("link", { name: "replaced" })).toBeNull();
    expect(within(region).getByRole("link", { name: "new" })).toBeTruthy();
    expectChanges(region, report);
  });

  it("历史标题链接到现在所在的专辑，而非扫描时的旧组", async () => {
    const { scan, report } = setup(reportSample("report-current-group-changed"));
    const first = report.added_albums.items[0];
    if (first === undefined) throw new Error("报告样本缺少新增专辑");
    expect(first.current_group_id).not.toBe(first.group_id_at_scan);
    renderDetail(scan);
    const link = await screen.findByRole("link", { name: first.title_at_scan });
    expect(link.getAttribute("href")).toBe(`/albums/${first.current_group_id}`);
    expect(screen.queryByText(/可能接替了/)).toBeNull();
  });

  it("认领链接带旧版本和新增版本标识，读屏名称说清楚关系", async () => {
    const { scan, report } = setup();
    const first = report.added_albums.items[0];
    const hint = first?.claim_hints[0];
    if (first === undefined || hint === undefined) throw new Error("报告样本缺少认领提示");
    renderDetail(scan);
    const link = await screen.findByRole("link", { name: `查看并认领《${hint.title}》，它可能被《${first.title_at_scan}》接替了` });
    expect(link.getAttribute("href")).toBe(`/claims?release=${hint.id}&target=${first.release_id}`);
    expect(screen.getByText(`可能接替了《${hint.title}》`)).toBeTruthy();
  });

  it("新增列表按 20 项请求分页，等待下一页时保留内容，完成后焦点留在列表标题", async () => {
    const report = reportSample();
    const template = report.added_albums.items[0];
    if (template === undefined) throw new Error("报告样本缺少新增专辑");
    const items = Array.from({ length: 21 }, (_, index) => ({
      ...template,
      release_id: `00000000-0000-4000-8000-${String(index + 100).padStart(12, "0")}`,
      title_at_scan: `新专辑 ${String(index + 1).padStart(2, "0")}`,
      claim_hints: [],
    }));
    const pageOf = (page: number): ScanReportDTO => ({
      ...report,
      albums: { ...report.albums, added: 21 },
      added_albums: { items: items.slice((page - 1) * 20, page * 20), pagination: { page, page_size: 20, total: 21 } },
    });
    const { api, scan } = setup(pageOf(1));
    let finishPage: (() => void) | undefined;
    const secondPage = new Promise<Response>((resolve) => { finishPage = () => resolve(jsonResponse(200, pageOf(2))); });
    api.on("GET", reportPath(scan.id), (request) => new URL(request.path, "http://localhost").searchParams.get("page") === "2"
      ? secondPage : jsonResponse(200, pageOf(1)));
    renderDetail(scan);
    await screen.findByRole("link", { name: "新专辑 01" });
    expect(screen.getAllByRole("link", { name: /^新专辑 / })).toHaveLength(20);
    const heading = screen.getByRole("heading", { level: 3, name: "新增的专辑" });
    const pagination = screen.getByRole("navigation", { name: "新增专辑分页" });
    await userEvent.click(within(pagination).getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.requestsTo("GET", reportPath(scan.id)).at(-1)?.path).toBe(`${reportPath(scan.id)}?page=2&page_size=20`));
    expect(screen.getByRole("link", { name: "新专辑 01" })).toBeTruthy();
    expect(document.activeElement).toBe(heading);
    await act(async () => {
      if (finishPage === undefined) throw new Error("没有建立第二页请求");
      finishPage();
    });
    await screen.findByRole("link", { name: "新专辑 21" });
    expect(screen.queryByRole("link", { name: "新专辑 01" })).toBeNull();
    expect(screen.getAllByRole("link", { name: /^新专辑 / })).toHaveLength(1);
    expect(document.activeElement).toBe(heading);
    expect(within(pagination).getByText("第 2 / 2 页")).toBeTruthy();
    expectChanges(await reportRegion(), pageOf(2));
  });

  it("从报告进入认领并提交后，所有报告页失效，返回时去掉提示但保持历史计数", async () => {
    const { api, governance } = setupClaims();
    const library = installLibraryApi(api);
    const report = reportSample();
    const first = report.added_albums.items[0];
    if (first === undefined) throw new Error("报告样本缺少新增专辑");
    const sourceId = claimReleaseId(1);
    const target = alpha();
    const withHint: ScanReportDTO = {
      ...report,
      added_albums: {
        ...report.added_albums,
        items: [{ ...first, release_id: target.representative.id, title_at_scan: target.representative.title, current_group_id: target.id,
          claim_hints: [{ id: sourceId, title: "Alpha Suites (corrected)" }] }, ...report.added_albums.items.slice(1)],
      },
    };
    const scan = seedScan(library, { ...scanSample("terminal-succeeded"), id: report.scan_run_id, scan_run_id: report.scan_run_id, report_status: "ready" });
    api.on("GET", reportPath(scan.id), () => jsonResponse(200, governance.claims.has(sourceId) ? withHint : {
      ...withHint, added_albums: { ...withHint.added_albums, items: withHint.added_albums.items.map((item) => ({ ...item, claim_hints: [] })) },
    }));
    const { history, runtime } = renderDetail(scan);
    const link = await screen.findByRole("link", { name: "查看并认领《Alpha Suites (corrected)》，它可能被《Alpha Suites New Rip》接替了" });
    const generation = runtime.session.currentGeneration();
    const cachedPages = [libraryKeys.scanReportPage(generation, scan.id, 2, 20), libraryKeys.scanReportPage(generation, "other-scan", 1, 20)];
    for (const key of cachedPages) runtime.queryClient.setQueryData(key, { data: withHint });
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(1);
    await userEvent.click(link);
    await waitFor(() => expect(history.location.pathname).toBe("/claims"));
    await userEvent.click(await screen.findByRole("button", { name: "认领到《Alpha Suites New Rip》" }));
    await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到《Alpha Suites New Rip》/);
    expect(governance.claims.has(sourceId)).toBe(false);
    for (const key of [libraryKeys.scanReportPage(generation, scan.id, 1, 20), ...cachedPages]) {
      expect(runtime.queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    }
    await act(async () => { history.back(); });
    await screen.findByRole("table", { name: "专辑与曲目的变化" });
    await waitFor(() => expect(screen.queryByRole("link", { name: /查看并认领/ })).toBeNull());
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(2);
    expectChanges(await reportRegion(), report);
    expect(screen.getByRole("link", { name: "Alpha Suites New Rip" })).toBeTruthy();
  });
});

describe("进度页面与轮询接线", () => {
  it.each(["admin", "user"] as const)("%s 概览可读文件进度与目录序号，不显示管理员目录名", async (role) => {
    const { api, library, scan } = setup(undefined, scanSample(`reading_tags-${role === "admin" ? "admin" : "member"}`), role);
    library.activeScanId = scan.id;
    renderApp({ path: "/" });
    const region = await screen.findByRole("region", { name: "扫描" });
    const bar = await within(region).findByRole("progressbar", { name: "扫描进度" });
    expect(bar.getAttribute("aria-valuenow")).toBe("12");
    expect(bar.getAttribute("aria-valuetext")).toBe("第 1 / 共 2 个目录，读取标签，已处理 50 / 400 个文件，整体进度 12%");
    expect(within(region).queryByText(/当前目录：|测试音乐甲/)).toBeNull();
    expect(api.requestsTo("GET", reportPath(scan.id))).toHaveLength(0);
    if (role === "user") {
      expect(api.requestsTo("GET", "/api/v1/library-roots")).toHaveLength(0);
      expect(within(region).queryByRole("link", { name: "查看扫描详情" })).toBeNull();
    }
  });

  it("管理员详情显示目录名称和局部文件数，不与全局累计混淆", async () => {
    const { scan } = setup(undefined, scanSample("reading_tags-admin"));
    renderDetail(scan);
    const region = await statusRegion();
    expect(within(region).getByText("当前目录：测试音乐甲（这个目录 50 / 100 个文件）")).toBeTruthy();
    expect(within(region).getByText("已处理 50 / 400 个文件")).toBeTruthy();
  });

  it.each(["succeeded", "failed", "canceled", "incomplete"] as const)("详情轮询到 %s 后冻结并停止请求，同时使 catalog 所有相关缓存失效", async (status) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library, scan } = setup(undefined, scanSample("second-root-reading"));
    const { runtime } = renderDetail(scan);
    const region = await statusRegion();
    expect(within(region).getByRole("progressbar").getAttribute("aria-valuenow")).toBe("49");
    const generation = runtime.session.currentGeneration();
    const keys = [
      catalogKeys.albumPage(generation, {}), catalogKeys.group(generation, "album"), catalogKeys.release(generation, "release"),
      catalogKeys.totals(generation), catalogKeys.recentlyAdded(generation),
    ];
    for (const key of keys) runtime.queryClient.setQueryData(key, { marker: "原缓存" });
    library.scans.set(scan.id, { ...scanSample(`terminal-${status}`), id: scan.id, scan_run_id: scan.id });
    await tick(2000);
    const expected = status === "succeeded" ? "100" : "49";
    await waitFor(() => expect(within(region).getByRole("progressbar").getAttribute("aria-valuenow")).toBe(expected));
    for (const key of keys) expect(runtime.queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    const reads = api.requestsTo("GET", `/api/v1/scans/${scan.id}`).length;
    await tick(30_000);
    expect(api.requestsTo("GET", `/api/v1/scans/${scan.id}`)).toHaveLength(reads);
    expect(within(region).getByRole("progressbar").getAttribute("aria-valuenow")).toBe(expected);
  });

  it("概览 5 秒轮询：首次与计数换根不刷新 catalog，正式换根、活动 id 换新或消失才刷新", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api, library, scan } = setup(undefined, scanSample("counting-admin"));
    library.activeScanId = scan.id;
    api.on("GET", "/api/v1/release-groups/recently-added", () => sampleResponse("scan-feedback/recent-empty"));
    renderApp({ path: "/" });
    await screen.findByRole("progressbar", { name: "扫描进度" });
    const recentReads = () => api.requestsTo("GET", "/api/v1/release-groups/recently-added").length;
    await waitFor(() => expect(recentReads()).toBe(1));
    library.scans.set(scan.id, { ...scan, root_progress: { index: 2, total: 2 } });
    await tick(5000);
    expect(recentReads()).toBe(1);
    library.scans.set(scan.id, { ...scanSample("first-root-saved"), id: scan.id, scan_run_id: scan.id });
    await tick(5000);
    expect(recentReads()).toBe(1);
    library.scans.set(scan.id, { ...scanSample("second-root-started"), id: scan.id, scan_run_id: scan.id });
    await tick(5000);
    await waitFor(() => expect(recentReads()).toBe(2));
    const nextId = "00000000-0000-4000-8000-000000000099";
    library.scans.set(nextId, { ...scanSample("counting-admin"), id: nextId, scan_run_id: nextId });
    library.activeScanId = nextId;
    await tick(5000);
    await waitFor(() => expect(recentReads()).toBe(3));
    library.activeScanId = null;
    await tick(5000);
    await waitFor(() => expect(recentReads()).toBe(4));
    expect(await screen.findByText("没有正在运行的扫描。")).toBeTruthy();
    const activeReads = api.requestsTo("GET", "/api/v1/scans/active").length;
    await tick(30_000);
    expect(recentReads()).toBe(4);
    expect(api.requestsTo("GET", "/api/v1/scans/active")).toHaveLength(activeReads);
  });
});
