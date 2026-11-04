import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { albumGroups } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { renderApp } from "../test/render-app";

function setup(path: string) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  installOverviewApi(api);
  installCatalogApi(api, { groups: albumGroups(100, (index) => ({ attention_count: 1, added_at: `2026-11-${String(index % 3 + 1).padStart(2, "0")}T00:00:00Z` })) });
  return { api, ...renderApp({ path }) };
}

const searchOf = (history: { location: { search: string } }) => new URLSearchParams(history.location.search);
const lastQuery = (api: ReturnType<typeof setup>["api"]) => new URL(api.requestsTo("GET", "/api/v1/release-groups").at(-1)?.path ?? "", "http://localhost").searchParams;
const ready = () => screen.findByRole("navigation", { name: "专辑分页" });
const searchBox = () => screen.getByRole("textbox", { name: "搜索专辑" });

describe("专辑排序地址状态", () => {
  it("搜索保留排序并回第一页，清除搜索仍保留排序", async () => {
    const { api, history } = setup("/albums?sort=added_at_desc&page=2");
    await ready();
    await userEvent.type(searchBox(), "Album{Enter}");
    await waitFor(() => expect(lastQuery(api).get("q")).toBe("Album"));
    expect(searchOf(history).get("sort")).toBe("added_at_desc");
    expect(searchOf(history).has("page")).toBe(false);
    expect(lastQuery(api).get("sort")).toBe("added_at_desc");
    await userEvent.click(screen.getByRole("button", { name: "清除" }));
    await waitFor(() => expect(searchOf(history).has("q")).toBe(false));
    expect(searchOf(history).get("sort")).toBe("added_at_desc");
    expect(document.activeElement).toBe(searchBox());
  });

  it("切换需要关注保留排序和搜索，并回第一页", async () => {
    const { api, history } = setup("/albums?sort=added_at_desc&q=Album&page=2");
    await ready();
    await userEvent.click(screen.getByRole("checkbox", { name: "只看需要关注" }));
    await waitFor(() => expect(lastQuery(api).get("attention")).toBe("required"));
    expect(Object.fromEntries(searchOf(history))).toEqual({ q: "Album", attention: "required", sort: "added_at_desc" });
    expect(lastQuery(api).get("sort")).toBe("added_at_desc");
    expect(lastQuery(api).get("page")).toBe("1");
  });

  it("翻页保留排序、搜索和筛选，焦点落在结果标题", async () => {
    const { api, history } = setup("/albums?sort=added_at_desc&q=Album&attention=required");
    const pagination = await ready();
    const next = within(pagination).getByRole("link", { name: "下一页" });
    expect(new URL(next.getAttribute("href") ?? "", "http://localhost").searchParams.get("sort")).toBe("added_at_desc");
    await userEvent.click(next);
    await waitFor(() => expect(lastQuery(api).get("page")).toBe("2"));
    expect(Object.fromEntries(searchOf(history))).toEqual({ q: "Album", attention: "required", sort: "added_at_desc", page: "2" });
    expect(lastQuery(api).get("sort")).toBe("added_at_desc");
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "“Album”中需要关注的专辑" }));
  });

  it("空结果的清除筛选保留排序", async () => {
    const { api, history } = setup("/albums?sort=added_at_desc&q=不存在的专辑&attention=required");
    await userEvent.click(await screen.findByRole("link", { name: "清除筛选" }));
    await ready();
    expect(Object.fromEntries(searchOf(history))).toEqual({ sort: "added_at_desc" });
    expect(lastQuery(api).get("sort")).toBe("added_at_desc");
    expect(lastQuery(api).has("q")).toBe(false);
    expect(lastQuery(api).has("attention")).toBe(false);
  });

  it("切换排序回第一页，同时保留其他条件；默认标题排序不留 sort 参数", async () => {
    const { api, history } = setup("/albums?q=Album&attention=required&page=2");
    await ready();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "排序" }), "added_at_desc");
    await waitFor(() => expect(lastQuery(api).get("sort")).toBe("added_at_desc"));
    expect(Object.fromEntries(searchOf(history))).toEqual({ q: "Album", attention: "required", sort: "added_at_desc" });
    expect(lastQuery(api).get("page")).toBe("1");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "排序" }), "title");
    await waitFor(() => expect(lastQuery(api).has("sort")).toBe(false));
    expect(Object.fromEntries(searchOf(history))).toEqual({ q: "Album", attention: "required" });
  });

  it("切换排序后卡片实际重排，浏览器后退与前进恢复各自结果", async () => {
    const { history } = setup("/albums");
    await ready();
    const firstCard = () => within(screen.getByRole("region", { name: "全部专辑" }))
      .getAllByRole("link").find((link) => /\/albums\//.test(link.getAttribute("href") ?? ""))?.getAttribute("href");
    const titleFirst = "/albums/00000000-0000-4000-8000-a00000000001";
    // 98 是最新一天（11 月 3 日）中 ID 最大的组；不从 fake 排序函数计算期望。
    const recentFirst = "/albums/00000000-0000-4000-8000-a00000000098";
    expect(firstCard()).toBe(titleFirst);
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "排序" }), "added_at_desc");
    await waitFor(() => expect(firstCard()).toBe(recentFirst));
    history.back();
    await waitFor(() => {
      expect(searchOf(history).has("sort")).toBe(false);
      expect(firstCard()).toBe(titleFirst);
      expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "排序" }).value).toBe("title");
    });
    history.forward();
    await waitFor(() => {
      expect(searchOf(history).get("sort")).toBe("added_at_desc");
      expect(firstCard()).toBe(recentFirst);
      expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "排序" }).value).toBe("added_at_desc");
    });
  });

  it("详情返回列表恢复排序、筛选、搜索和页码", async () => {
    const { history } = setup("/albums?sort=added_at_desc&q=Album&attention=required&page=2");
    await ready();
    const region = screen.getByRole("region", { name: "“Album”中需要关注的专辑" });
    const card = within(region).getAllByRole("link").find((link) => /\/albums\//.test(link.getAttribute("href") ?? ""));
    if (card === undefined) throw new Error("没有专辑卡片");
    await userEvent.click(card);
    const back = await screen.findByRole("link", { name: "返回专辑列表" });
    expect(new URL(back.getAttribute("href") ?? "", "http://localhost").searchParams.get("sort")).toBe("added_at_desc");
    await userEvent.click(back);
    await ready();
    expect(Object.fromEntries(searchOf(history))).toEqual({ q: "Album", attention: "required", sort: "added_at_desc", page: "2" });
  });
});
