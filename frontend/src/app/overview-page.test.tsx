import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

type AnalysisProgress = { total: number; analyzed: number; failed: number; state: "running" | "waiting_for_scan" | "idle" | "unavailable" };

function setup(role: "admin" | "user" = "admin") {
  const api = installFakeApi();
  installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  return api;
}

const overview = async () => {
  await screen.findByRole("heading", { level: 1, name: "概览" });
  return screen.findByRole("region", { name: "资料库" });
};

const statusTable = () => screen.findByRole("region", { name: "资料库状态" });

/** 状态表里的一行（按名称找）；名称是行里单独的一段文字，说明文字里出现同样的词不算。 */
function statusRow(table: HTMLElement, label: string): HTMLElement {
  const row = within(table).getAllByRole("listitem").find((item) => item.querySelector("span span")?.textContent === label);
  if (row === undefined) throw new Error(`状态表里没有“${label}”`);
  return row;
}

/** 状态表各行的名称，按显示顺序。 */
function statusLabels(table: HTMLElement): string[] {
  return within(table).getAllByRole("listitem").map((item) => item.querySelector("span span")?.textContent ?? "");
}

describe("概览资料库卡片", () => {
  it("四行统计：专辑、艺人、作曲家链接到对应列表，曲目不链接", async () => {
    setup();
    renderApp();
    const stats = await overview();
    expect(within(stats).getByRole("heading", { level: 2, name: "资料库" })).toBeTruthy();
    expect((await within(stats).findByRole("link", { name: /张专辑/ })).getAttribute("href")).toBe("/albums");
    expect(within(stats).getByRole("link", { name: /张专辑/ }).textContent).toBe("12张专辑");
    expect(within(stats).getByText("首曲目").closest("a")).toBeNull();
    expect(within(stats).getByText("首曲目").previousElementSibling?.textContent).toBe("5");
    expect(within(stats).getByRole("link", { name: /位艺人/ }).getAttribute("href")).toBe("/artists");
    expect(within(stats).getByRole("link", { name: /位作曲家/ }).getAttribute("href")).toBe("/artists?role=composer");
    expect(within(stats).getAllByRole("link")).toHaveLength(3);
    expect(within(stats).getAllByRole("listitem").map((row) => row.textContent)).toEqual(["12张专辑", "5首曲目", "7位艺人", "1位作曲家"]);
  });

  it("艺人目录整理时用破折号显示艺人与作曲家且不链接", async () => {
    const api = setup();
    api.on("GET", "/api/v1/catalog/stats", () => jsonResponse(200, sampleBody("catalog/stats-artists-preparing")));
    renderApp();
    const stats = await overview();
    expect(await within(stats).findAllByText("—")).toHaveLength(2);
    expect(within(stats).getByText("位艺人").closest("a")).toBeNull();
    expect(within(stats).getByText("位作曲家").closest("a")).toBeNull();
    expect(within(stats).getByRole("link", { name: /张专辑/ }).getAttribute("href")).toBe("/albums");
  });

  it("空资料库不显示统计列表，只说一句空库文案", async () => {
    const api = setup();
    api.on("GET", "/api/v1/catalog/stats", () => jsonResponse(200, { albums: 0, tracks: 0, artists: 0, composers: 0 }));
    renderApp();
    const stats = await overview();
    expect(await within(stats).findByText("资料库里还没有专辑。添加音乐目录并扫描后就会出现。")).toBeTruthy();
    expect(within(stats).queryByRole("list")).toBeNull();
    expect(within(stats).queryByRole("link")).toBeNull();
  });
});

describe("概览资料库状态表", () => {
  it("管理员看到六行：扫描、音频分析、需要关注、自动整理、音乐目录、成员；除音频分析外整行是链接", async () => {
    setup("admin");
    renderApp();
    const table = await statusTable();
    await waitFor(() => expect(within(table).queryByText("正在读取…")).toBeNull());
    expect(statusLabels(table)).toEqual(["扫描", "音频分析", "需要关注", "自动整理", "音乐目录", "成员"]);
    const hrefOf = (label: string) => within(statusRow(table, label)).queryByRole("link")?.getAttribute("href") ?? null;
    expect(hrefOf("扫描")).toBe("/settings/library");
    expect(hrefOf("音频分析")).toBeNull();
    expect(hrefOf("需要关注")).toBe("/albums?attention=required");
    expect(hrefOf("自动整理")).toBe("/settings/curation");
    expect(statusRow(table, "自动整理").textContent).toContain("未打开");
    expect(hrefOf("音乐目录")).toBe("/settings/library");
    expect(hrefOf("成员")).toBe("/settings/members");
    expect(statusRow(table, "扫描").textContent).toContain("没有正在运行的扫描");
    expect(statusRow(table, "需要关注").textContent).toContain("3 张专辑有待核对的问题");
    // 样本：一个启用、一个停用的目录；两位启用、一位停用的成员。
    expect(statusRow(table, "音乐目录").textContent).toBe("音乐目录1 个已停用1");
    expect(statusRow(table, "成员").textContent).toBe("成员1 位已停用2");
  });

  it("普通成员只看到前三行，扫描不是链接，也不请求目录与成员", async () => {
    const api = setup("user");
    renderApp();
    const table = await statusTable();
    await waitFor(() => expect(within(table).queryByText("正在读取…")).toBeNull());
    expect(statusLabels(table)).toEqual(["扫描", "音频分析", "需要关注"]);
    expect(within(statusRow(table, "扫描")).queryByRole("link")).toBeNull();
    expect(within(statusRow(table, "需要关注")).getByRole("link").getAttribute("href")).toBe("/albums?attention=required");
    expect(api.requestsTo("GET", "/api/v1/library-roots")).toHaveLength(0);
    expect(api.requestsTo("GET", "/api/v1/users")).toHaveLength(0);
  });

  it("没有需要关注的专辑时写一句说明，这一行不是链接", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "listener" });
    installOverviewApi(api, { attention: 0 });
    renderApp();
    const table = await statusTable();
    await waitFor(() => expect(statusRow(table, "需要关注").textContent).toContain("没有需要关注的专辑"));
    expect(within(statusRow(table, "需要关注")).queryByRole("link")).toBeNull();
  });

  it.each(["admin", "user"] as const)("%s：扫描进行中写阶段与百分比，进度条带读屏文字", async (role) => {
    const api = setup(role);
    api.on("GET", "/api/v1/scans/active", () => sampleResponse("scan-feedback/active-member"));
    renderApp();
    const table = await statusTable();
    const bar = await within(table).findByRole("progressbar", { name: "扫描进度" });
    const row = statusRow(table, "扫描");
    expect(row.contains(bar)).toBe(true);
    expect(bar.getAttribute("aria-valuenow")).toBe("49");
    expect(bar.getAttribute("aria-valuetext")).toContain("整体进度 49%");
    expect(within(row).getByText("49%")).toBeTruthy();
    expect(within(row).getByText(/^正在扫描 · 第 2 \/ 共 2 个目录/)).toBeTruthy();
    const link = within(row).queryByRole("link");
    if (role === "admin") expect(link?.getAttribute("href")).toBe("/settings/library/scans/00000000-0000-4000-8000-000000000001");
    else expect(link).toBeNull();
  });

  it("第一次读取失败时这一行写“无法读取”并可重试，其余行照常", async () => {
    const api = setup("user");
    api.on("GET", "/api/v1/audio-analysis/progress", () => jsonResponse(500, { error: { code: "internal_error", message: "服务器内部错误" }, request_id: "req-1" }));
    renderApp();
    const table = await statusTable();
    await waitFor(() => expect(statusRow(table, "音频分析").textContent).toContain("无法读取"));
    expect(statusRow(table, "扫描").textContent).toContain("没有正在运行的扫描");
    api.on("GET", "/api/v1/audio-analysis/progress", () => jsonResponse(200, { total: 10, analyzed: 3, failed: 0, state: "running" }));
    await userEvent.click(within(statusRow(table, "音频分析")).getByRole("button", { name: "重试读取音频分析" }));
    await waitFor(() => expect(statusRow(table, "音频分析").textContent).toContain("已分析 3 / 10 首"));
  });
});

describe("概览音频分析状态", () => {
  it.each([
    { label: "running 显示已分析数与百分比", progress: { total: 10, analyzed: 3, failed: 0, state: "running" } as AnalysisProgress, contains: ["已分析 3 / 10 首", "30%"], missing: ["扫描结束后继续", "已完成"] },
    { label: "等待扫描时说明扫描结束后继续", progress: { total: 10, analyzed: 3, failed: 0, state: "waiting_for_scan" } as AnalysisProgress, contains: ["已分析 3 / 10 首，扫描结束后继续"], missing: ["已完成"] },
    { label: "全部完成显示已完成", progress: { total: 10, analyzed: 10, failed: 0, state: "idle" } as AnalysisProgress, contains: ["已完成", "已分析 10 首"], missing: ["10 / 10"] },
    { label: "失败曲目显示无法分析数量", progress: { total: 10, analyzed: 7, failed: 2, state: "idle" } as AnalysisProgress, contains: ["已分析 7 / 10 首，2 首无法分析", "70%"], missing: ["已完成"] },
    { label: "unavailable 显示未运行", progress: sampleBody("audio-analysis/progress") as AnalysisProgress, contains: ["未运行"], missing: ["1 / 10", "2 首无法分析"] },
  ])("$label", async ({ progress, contains, missing }) => {
    const api = setup("user");
    api.on("GET", "/api/v1/audio-analysis/progress", () => jsonResponse(200, progress));
    renderApp();
    const table = await statusTable();
    await waitFor(() => expect(statusRow(table, "音频分析").textContent).not.toContain("正在读取…"));
    const row = statusRow(table, "音频分析");
    for (const text of contains) expect(within(row).getByText(text)).toBeTruthy();
    for (const text of missing) expect(row.textContent).not.toContain(text);
    expect(within(row).queryByRole("link")).toBeNull();
    expect(within(row).queryByRole("progressbar")).toBeNull();
  });
});
