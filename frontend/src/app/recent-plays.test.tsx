import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { decodeRecentReleases, type RecentReleaseDTO } from "../features/listening/api/decoders";
import { listeningKeys } from "../features/listening";
import { formatDateTime } from "../shared/lib/time";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// 概览的“最近播放”区块（阶段 P）：读取、卡片、空态、错误与清除自己的播放历史。

const recentPath = "/api/v1/me/plays/recent-releases";
const playsPath = "/api/v1/me/plays";
const emptyText = "还没有播放记录。一首曲目播放到一半（最长 4 分钟）就会记在这里。";
const hiddenText = "没有可显示的播放记录：播放过的曲目目前都不在资料库里。";

function sampleItem(): RecentReleaseDTO {
  const item = decodeRecentReleases(sampleBody("listening/recent-releases")).items[0];
  if (item === undefined) throw new Error("样本缺少最近播放");
  return item;
}
const template = sampleItem();

const uuid = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;

/** 第 index 张专辑：没有专辑艺人（显示艺人）、没有封面；需要时覆盖发行摘要的个别字段。 */
function recentRelease(index: number, overrides: Partial<RecentReleaseDTO["release"]> = {}): RecentReleaseDTO {
  return {
    ...template,
    group_id: uuid("a", index),
    last_played_at: `2026-01-0${index}T08:30:00Z`,
    release: { ...template.release, id: uuid("b", index), title: `专辑 ${index}`, artist: `艺术家 ${index}`, album_artist: null, artwork: null, ...overrides },
  };
}

/** 服务端的播放历史替身：GET 读取、DELETE 清除并返回删掉的条数；测试可以直接改 state。 */
function setup(items: readonly RecentReleaseDTO[] = [], hasHistory = items.length > 0) {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  const state = { items: [...items], hasHistory };
  api.on("GET", recentPath, () => jsonResponse(200, { items: state.items, has_history: state.hasHistory }));
  api.on("DELETE", playsPath, () => {
    const deleted = state.items.length;
    state.items = [];
    state.hasHistory = false;
    return jsonResponse(200, { deleted });
  });
  return { api, state };
}

const region = () => screen.findByRole("region", { name: "最近播放" });
const clearButton = (scope: HTMLElement) => within(scope).queryByRole("button", { name: "清除播放历史" });

describe("概览的“最近播放”", () => {
  it("排在概览最前；读取期间显示占位，没有记录时说明计次规则，也不提供清除", async () => {
    const { api } = setup();
    const pending = deferred<Response>();
    api.on("GET", recentPath, () => pending.promise);
    renderApp();
    const panel = await region();
    expect(within(panel).getByRole("status").textContent).toContain("正在加载最近播放");
    expect(screen.getAllByRole("heading", { level: 2 })[0]?.textContent).toBe("最近播放");

    pending.resolve(sampleResponse("listening/recent-releases-empty"));
    expect(await within(panel).findByText(emptyText)).toBeTruthy();
    expect(within(panel).queryByRole("status")).toBeNull();
    expect(clearButton(panel)).toBeNull();
    expect(within(panel).queryByRole("link", { name: "全部历史" })).toBeNull();
  });

  it("按接口给的顺序显示专辑：整张卡片是链接，指向播放过的版本，写明专辑艺人与上次播放时间", async () => {
    const items = [recentRelease(3), recentRelease(2, { album_artist: "专辑艺人 2" }), recentRelease(1)];
    setup(items);
    renderApp();
    const panel = await region();
    const links = await within(panel).findAllByRole("link", { name: /^专辑 / });
    expect(within(panel).getByRole("link", { name: "全部历史" }).getAttribute("href")).toBe("/me/history");
    expect(within(panel).getAllByRole("link")).toHaveLength(items.length + 1);
    expect(links.map((link) => link.getAttribute("href"))).toEqual(items.map((item) => `/albums/${item.group_id}?release=${item.release.id}`));
    // 专辑艺人缺失时退回到艺人；链接名称里写着上次播放的时间。封面是装饰，不进名称。
    expect(within(panel).getByRole("link", { name: `专辑 3 艺术家 3 上次播放 ${formatDateTime("2026-01-03T08:30:00Z")}` })).toBe(links[0]);
    expect(within(panel).getByRole("link", { name: `专辑 2 专辑艺人 2 上次播放 ${formatDateTime("2026-01-02T08:30:00Z")}` })).toBe(links[1]);
    expect(within(panel).getByRole("link", { name: `专辑 1 艺术家 1 上次播放 ${formatDateTime("2026-01-01T08:30:00Z")}` })).toBe(links[2]);
    expect(within(panel).queryByText(emptyText)).toBeNull();
    expect(clearButton(panel)).toBeTruthy();
  });

  it("点击卡片进入播放过的那个版本", async () => {
    const items = [recentRelease(2)];
    setup(items);
    const { history } = renderApp();
    const panel = await region();
    await userEvent.click(await within(panel).findByRole("link", { name: /^专辑 2/ }));
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${items[0]?.group_id}`));
    expect(new URLSearchParams(history.location.search).get("release")).toBe(items[0]?.release.id);
  });

  it("有记录、但播放过的曲目都不在资料库里：说明没有可显示的记录，仍可以清除", async () => {
    setup([], true);
    renderApp();
    const panel = await region();
    expect(await within(panel).findByText(hiddenText)).toBeTruthy();
    expect(within(panel).getByRole("link", { name: "全部历史" }).getAttribute("href")).toBe("/me/history");
    expect(within(panel).queryByText(emptyText)).toBeNull();
    expect(clearButton(panel)).toBeTruthy();
  });

  it("读取失败显示带请求 ID 的错误与重试，其余区块照常；重试成功后显示卡片", async () => {
    const { api } = setup([recentRelease(1)]);
    // 500 不会被自动重试（503 与网络失败会，退避要等好几秒），错误立即显示。
    api.on("GET", recentPath, () => jsonResponse(500, { error: { code: "internal_error", message: "服务器内部错误" }, request_id: "req-recent-1" }));
    renderApp();
    const panel = await region();
    const alert = await within(panel).findByRole("alert");
    expect(within(alert).getByText(/请求 ID：/).textContent).toContain("req-recent-1");
    expect(within(panel).queryByRole("link")).toBeNull();
    expect(await screen.findByRole("region", { name: "资料库" })).toBeTruthy();

    api.on("GET", recentPath, () => jsonResponse(200, { items: [recentRelease(1)], has_history: true }));
    await userEvent.click(within(panel).getByRole("button", { name: "重试" }));
    expect(await within(panel).findByRole("link", { name: /^专辑 1/ })).toBeTruthy();
    expect(within(panel).queryByRole("alert")).toBeNull();
  });

  describe("清除播放历史", () => {
    it("先确认：取消不发请求，焦点回到按钮；确认后区块为空、提示结果、焦点到标题，按钮消失", async () => {
      const { api } = setup([recentRelease(2), recentRelease(1)]);
      renderApp();
      const panel = await region();
      await within(panel).findAllByRole("link");

      const opener = clearButton(panel);
      if (opener === null) throw new Error("没有清除按钮");
      await userEvent.click(opener);
      const dialog = await screen.findByRole("dialog", { name: "清除播放历史？" });
      expect(within(dialog).getByText("将清除你自己的全部播放记录，不影响其他成员。")).toBeTruthy();
      expect(within(dialog).getByText("清除后不能恢复。")).toBeTruthy();
      expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));

      await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(api.requestsTo("DELETE", playsPath)).toHaveLength(0);
      expect(document.activeElement).toBe(opener);
      expect(within(panel).getAllByRole("link")).toHaveLength(3);

      await userEvent.click(opener);
      await userEvent.click(within(await screen.findByRole("dialog", { name: "清除播放历史？" })).getByRole("button", { name: "清除播放历史" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(api.requestsTo("DELETE", playsPath)).toHaveLength(1);
      expect(await within(panel).findByText("已清除你的播放历史。")).toBeTruthy();
      expect(within(panel).getByText(emptyText)).toBeTruthy();
      expect(within(panel).queryByRole("link")).toBeNull();
      expect(clearButton(panel)).toBeNull();
      expect(document.activeElement).toBe(within(panel).getByRole("heading", { level: 2, name: "最近播放" }));
    });

    it("清除成功就立刻显示为空，不等后台重新读取完成", async () => {
      const { api } = setup([recentRelease(1)]);
      renderApp();
      const panel = await region();
      await userEvent.click(await within(panel).findByRole("button", { name: "清除播放历史" }));
      const refetch = deferred<Response>();
      api.on("GET", recentPath, () => refetch.promise);
      await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "清除播放历史" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(within(panel).getByText(emptyText)).toBeTruthy();
      expect(within(panel).queryByRole("link")).toBeNull();
      expect(await within(panel).findByText("已清除你的播放历史。")).toBeTruthy();
      await waitFor(() => expect(api.requestsTo("GET", recentPath)).toHaveLength(2));
      refetch.resolve(sampleResponse("listening/recent-releases-empty"));
    });

    it("清除之后又有新的播放：上报成功让“最近播放”失效，区块显示新的记录并收起“已清除”的提示", async () => {
      const { state } = setup([recentRelease(2)]);
      const { runtime } = renderApp();
      const panel = await region();
      await within(panel).findAllByRole("link");
      await userEvent.click(clearButton(panel) as HTMLElement);
      await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "清除播放历史" }));
      expect(await within(panel).findByText("已清除你的播放历史。")).toBeTruthy();

      state.items = [recentRelease(5)];
      state.hasHistory = true;
      await runtime.queryClient.invalidateQueries({ queryKey: listeningKeys.recentReleases(runtime.session.currentGeneration()) });
      expect(await within(panel).findByRole("link", { name: /^专辑 5/ })).toBeTruthy();
      expect(within(panel).queryByText("已清除你的播放历史。")).toBeNull();
      expect(clearButton(panel)).toBeTruthy();
    });

    /** 第一次 DELETE 失败（503），之后成功并清空服务端的记录。 */
    function failOnceThenSucceed(context: ReturnType<typeof setup>) {
      let attempts = 0;
      context.api.on("DELETE", playsPath, () => {
        attempts += 1;
        if (attempts === 1) return sampleResponse("errors/database-unavailable");
        context.state.items = [];
        context.state.hasHistory = false;
        return jsonResponse(200, { deleted: 1 });
      });
    }

    it("清除失败：对话框保持打开并显示错误，按钮变成“重试清除”，重试成功后关闭", async () => {
      const context = setup([recentRelease(1)]);
      failOnceThenSucceed(context);
      renderApp();
      const panel = await region();
      await userEvent.click(await within(panel).findByRole("button", { name: "清除播放历史" }));
      const dialog = await screen.findByRole("dialog", { name: "清除播放历史？" });
      await userEvent.click(within(dialog).getByRole("button", { name: "清除播放历史" }));
      expect(await within(dialog).findByRole("alert")).toBeTruthy();
      expect(within(dialog).getByText("结果未确认")).toBeTruthy();
      expect(within(panel).getAllByRole("link")).toHaveLength(2);

      await userEvent.click(within(dialog).getByRole("button", { name: "重试清除" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(context.api.requestsTo("DELETE", playsPath)).toHaveLength(2);
      expect(await within(panel).findByText("已清除你的播放历史。")).toBeTruthy();
    });

    it("清除失败后取消再打开：上一次的错误不再显示，按钮回到“清除播放历史”", async () => {
      const context = setup([recentRelease(1)]);
      failOnceThenSucceed(context);
      renderApp();
      const panel = await region();
      await userEvent.click(await within(panel).findByRole("button", { name: "清除播放历史" }));
      const dialog = await screen.findByRole("dialog", { name: "清除播放历史？" });
      await userEvent.click(within(dialog).getByRole("button", { name: "清除播放历史" }));
      expect(await within(dialog).findByRole("alert")).toBeTruthy();

      await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
      await userEvent.click(within(panel).getByRole("button", { name: "清除播放历史" }));
      const reopened = await screen.findByRole("dialog", { name: "清除播放历史？" });
      expect(within(reopened).queryByRole("alert")).toBeNull();
      expect(within(reopened).queryByRole("button", { name: "重试清除" })).toBeNull();
      await userEvent.click(within(reopened).getByRole("button", { name: "清除播放历史" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(await within(panel).findByText("已清除你的播放历史。")).toBeTruthy();
    });

    it("请求进行中确认按钮忙碌、取消不可用，避免关闭一个仍在提交的操作", async () => {
      const { api } = setup([recentRelease(1)]);
      const pending = deferred<Response>();
      api.on("DELETE", playsPath, () => pending.promise);
      renderApp();
      const panel = await region();
      await userEvent.click(await within(panel).findByRole("button", { name: "清除播放历史" }));
      const dialog = await screen.findByRole("dialog", { name: "清除播放历史？" });
      await userEvent.click(within(dialog).getByRole("button", { name: "清除播放历史" }));
      await waitFor(() => expect((within(dialog).getByRole("button", { name: "取消" }) as HTMLButtonElement).disabled).toBe(true));
      pending.resolve(jsonResponse(200, { deleted: 1 }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    });

    it("只用键盘就能完成：Tab 到按钮、Enter 打开，对话框里 Tab 到确认、Enter 清除", async () => {
      const { api } = setup([recentRelease(1)]);
      renderApp();
      const panel = await region();
      const opener = await within(panel).findByRole("button", { name: "清除播放历史" });
      // 区块里依次是卡片链接与清除按钮（标题右侧的按钮在 DOM 里先于卡片，Tab 顺序按 DOM）。
      opener.focus();
      await userEvent.keyboard("{Enter}");
      const dialog = await screen.findByRole("dialog", { name: "清除播放历史？" });
      expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));
      await userEvent.tab();
      expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "清除播放历史" }));
      await userEvent.keyboard("{Enter}");
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(api.requestsTo("DELETE", playsPath)).toHaveLength(1);
      expect(document.activeElement).toBe(within(panel).getByRole("heading", { level: 2, name: "最近播放" }));
    });
  });
});
