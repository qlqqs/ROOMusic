import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { jsonResponse } from "../test/http";
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

describe("概览资料库统计", () => {
  it("显示四张统计卡：只有曲目不链接，其他卡片指向对应列表", async () => {
    setup();
    renderApp();
    const stats = await overview();
    expect((await within(stats).findByRole("link", { name: /专辑/ })).getAttribute("href")).toBe("/albums");
    expect(within(stats).getByText("5").closest("a")).toBeNull();
    expect(within(stats).getByText("曲目")).toBeTruthy();
    expect(within(stats).getByRole("link", { name: /艺术家/ }).getAttribute("href")).toBe("/artists");
    expect(within(stats).getByRole("link", { name: /作曲家/ }).getAttribute("href")).toBe("/artists?role=composer");
  });

  it("艺人目录整理时用破折号显示艺术家与作曲家且不链接", async () => {
    const api = setup();
    api.on("GET", "/api/v1/catalog/stats", () => jsonResponse(200, sampleBody("catalog/stats-artists-preparing")));
    renderApp();
    const stats = await overview();
    expect(await within(stats).findAllByText("—")).toHaveLength(2);
    expect(within(stats).getByText("艺术家").closest("a")).toBeNull();
    expect(within(stats).getByText("作曲家").closest("a")).toBeNull();
    expect(within(stats).getByRole("link", { name: /专辑/ }).getAttribute("href")).toBe("/albums");
  });

  it("空资料库隐藏统计卡并显示原空库文案", async () => {
    const api = setup();
    api.on("GET", "/api/v1/catalog/stats", () => jsonResponse(200, { albums: 0, tracks: 0, artists: 0, composers: 0 }));
    renderApp();
    const stats = await overview();
    expect(await within(stats).findByText("资料库里还没有专辑。添加音乐目录并扫描后就会出现。")).toBeTruthy();
    expect(within(stats).queryByRole("list")).toBeNull();
    expect(within(stats).queryByRole("link")).toBeNull();
  });

  it.each([
    { role: "admin" as const, visible: true },
    { role: "user" as const, visible: false },
  ])("音乐目录与成员仅管理员可见（$role）", async ({ role, visible }) => {
    setup(role);
    renderApp();
    await overview();
    if (visible) {
      expect(await screen.findByRole("region", { name: "音乐目录" })).toBeTruthy();
      expect(await screen.findByRole("region", { name: "成员" })).toBeTruthy();
    } else {
      expect(screen.queryByRole("region", { name: "音乐目录" })).toBeNull();
      expect(screen.queryByRole("region", { name: "成员" })).toBeNull();
    }
  });
});

describe("概览音频分析状态", () => {
  it.each([
    { label: "running 显示进度", progress: { total: 10, analyzed: 3, failed: 0, state: "running" } as AnalysisProgress, contains: ["3 / 10"], missing: ["扫描结束后继续", "已完成"] },
    { label: "等待扫描时说明扫描结束后继续", progress: { total: 10, analyzed: 3, failed: 0, state: "waiting_for_scan" } as AnalysisProgress, contains: ["3 / 10", "扫描结束后继续"], missing: ["已完成"] },
    { label: "全部完成显示已完成", progress: { total: 10, analyzed: 10, failed: 0, state: "idle" } as AnalysisProgress, contains: ["已完成"], missing: ["10 / 10"] },
    { label: "失败曲目显示无法分析数量", progress: { total: 10, analyzed: 7, failed: 2, state: "idle" } as AnalysisProgress, contains: ["7 / 10", "2 首无法分析"], missing: ["已完成"] },
    { label: "unavailable 显示未运行", progress: sampleBody("audio-analysis/progress") as AnalysisProgress, contains: ["未运行"], missing: ["1 / 10", "2 首无法分析", "音频分析进度"] },
  ])("$label", async ({ progress, contains, missing }) => {
    const api = setup("user");
    api.on("GET", "/api/v1/audio-analysis/progress", () => jsonResponse(200, progress));
    renderApp();
    await overview();
    const summary = screen.getByRole("region", { name: "音频分析" });
    for (const text of contains) expect(await within(summary).findByText(text)).toBeTruthy();
    for (const text of missing) expect(within(summary).queryByText(text)).toBeNull();
    if (progress.state === "running" || progress.state === "waiting_for_scan") {
      expect(within(summary).getByRole("progressbar", { name: "音频分析进度" })).toBeTruthy();
    }
  });
});
