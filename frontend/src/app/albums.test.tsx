import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ReleaseGroupSummaryDTO } from "../features/catalog/api/decoders";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { albumGroup, albumGroups } from "../test/catalog-data";
import { installCatalogApi } from "../test/fake-catalog";
import { deferred } from "../test/deferred";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

/** 默认 120 个组，每第 10 个需要关注（共 12 个）：三页、有筛选可用。 */
const defaultGroups = () => albumGroups(120, (index) => ({ attention_count: index % 10 === 0 ? 1 : 0 }));

function setup(options: { role?: "admin" | "user"; groups?: ReleaseGroupSummaryDTO[] } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: options.groups ?? defaultGroups() });
  return { api, catalog };
}

type Api = ReturnType<typeof setup>["api"];

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const results = (name = "全部专辑") => screen.findByRole("region", { name });
const searchBox = () => screen.getByRole("textbox", { name: "搜索专辑" });
const pagination = () => screen.findByRole("navigation", { name: "专辑分页" });

/** 卡片就是指向专辑详情的链接（分页与其他链接不指向 /albums/<id>）。 */
function albumLinks(): HTMLElement[] {
  return screen.getAllByRole("link").filter((link) => /^\/albums\/[0-9a-f-]{36}$/.test(link.getAttribute("href") ?? ""));
}

const titles = () => albumLinks().map((link) => link.querySelector("span")?.textContent ?? "");
const listRequests = (api: Api) => api.requestsTo("GET", "/api/v1/release-groups").map((request) => request.path);
const lastListRequest = (api: Api) => new URL(listRequests(api).at(-1) ?? "", "http://localhost").searchParams;
const searchOf = (history: { location: { search: string } }) => new URLSearchParams(history.location.search);

describe("专辑列表：浏览", () => {
  it("每页 48 张卡片，顺序与接口一致，请求带页大小；所有登录用户都能打开", async () => {
    // 接口的顺序是标题降序：页面必须原样显示，不自己排序。
    const { api } = setup({ role: "user", groups: defaultGroups().reverse() });
    renderApp({ path: "/albums" });
    expect(await heading("专辑")).toBeTruthy();
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    expect(titles()[0]).toBe("Album 120");
    expect(titles()[47]).toBe("Album 073");
    expect(listRequests(api)).toEqual(["/api/v1/release-groups?page=1&page_size=48"]);
    expect(await results()).toBeTruthy();
    expect(screen.getByText("共 120 张专辑")).toBeTruthy();
  });

  it("首次加载是同结构的占位，不画成空库", async () => {
    const { api } = setup();
    const gate = deferred<Response>();
    api.on("GET", "/api/v1/release-groups", () => gate.promise);
    renderApp({ path: "/albums" });
    expect(await screen.findByRole("status", { name: "" })).toBeTruthy();
    expect(screen.getByText("正在加载专辑")).toBeTruthy();
    expect(screen.queryByText(/还没有专辑/)).toBeNull();
    gate.resolve(jsonResponse(200, { items: [], pagination: { page: 1, page_size: 48, total: 0 } }));
    expect(await screen.findByText(/还没有专辑/)).toBeTruthy();
  });

  it("卡片：标题、专辑艺人（没有则用艺人）、年份、介质与曲目数；多版本与需要关注的标签；单版本没有版本字样", async () => {
    setup({
      groups: [
        albumGroup(1, { title: "第九交响曲", album_artist: "柏林爱乐乐团", artist: "卡拉扬", year: 1977, medium_count: 2, track_count: 18, release_count: 3, attention_count: 2, artwork: true }),
        albumGroup(2, { title: "Goldberg Variations", artist: "Glenn Gould", year: null, medium_count: 1, track_count: 1 }),
      ],
    });
    renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(2));
    const [first, second] = albumLinks();
    if (first === undefined || second === undefined) throw new Error("缺少卡片");

    expect(first.textContent).toContain("第九交响曲");
    expect(first.textContent).toContain("柏林爱乐乐团");
    expect(first.textContent).not.toContain("卡拉扬");
    expect(first.textContent).toContain("1977 · 2 张介质 · 18 首曲目");
    expect(within(first).getByText("3 个版本")).toBeTruthy();
    expect(within(first).getByText("需要关注")).toBeTruthy();
    // 封面是装饰：图片的替代文字为空，且整体对读屏隐藏。
    const cover = first.querySelector("img");
    expect(cover?.getAttribute("src")).toBe("/api/v1/artworks/cover-001.png");
    expect(cover?.getAttribute("alt")).toBe("");

    expect(second.textContent).toContain("Glenn Gould");
    expect(second.textContent).toContain("1 首曲目");
    expect(second.textContent).not.toMatch(/\d{4}/); // 年份未知就不写
    expect(second.textContent).not.toContain("版本");
    expect(second.textContent).not.toContain("需要关注");
    expect(second.querySelector("img")).toBeNull(); // 没有封面：文字版面，不发图片请求
    // 链接名称含标题与艺人，卡片里没有嵌套的按钮。
    expect(screen.getByRole("link", { name: /^第九交响曲 柏林爱乐乐团/ })).toBe(first);
    expect(within(first).queryByRole("button")).toBeNull();
  });

  it("点开卡片进入详情，并带上当前的筛选与页码（供“返回专辑列表”恢复）", async () => {
    // 每个组都需要关注，筛选后仍有两页，第 2 页存在。
    setup({ groups: albumGroups(120, () => ({ attention_count: 1 })) });
    const { history } = renderApp({ path: "/albums?q=Album&attention=required&page=2" });
    await waitFor(() => expect(albumLinks().length).toBeGreaterThan(0));
    const [first] = albumLinks();
    if (first === undefined) throw new Error("没有卡片");
    await userEvent.click(first);
    await waitFor(() => expect(history.location.pathname).toMatch(/^\/albums\/[0-9a-f-]{36}$/));
    expect(history.location.state).toMatchObject({ albumList: { q: "Album", attention: "required", page: 2 } });
  });
});

describe("专辑列表：搜索", () => {
  it("输入后按回车提交：请求带搜索词，地址记录搜索词，标题与结果数说明这是搜索结果", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));

    await userEvent.type(searchBox(), "Album 07{Enter}");
    await waitFor(() => expect(searchOf(history).get("q")).toBe("Album 07"));
    expect(await results("“Album 07”的搜索结果")).toBeTruthy();
    await waitFor(() => expect(albumLinks()).toHaveLength(10));
    expect(lastListRequest(api).get("q")).toBe("Album 07");
    expect(lastListRequest(api).get("page")).toBe("1");
    expect(screen.getByText("找到 10 张专辑")).toBeTruthy();
    expect(titles()[0]).toBe("Album 070");
  });

  it("按“搜索”按钮同样提交；输入过程中不请求（不做输入即搜）", async () => {
    const { api } = setup();
    renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    const before = listRequests(api).length;
    await userEvent.type(searchBox(), "Album 1");
    expect(listRequests(api)).toHaveLength(before);
    await userEvent.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(lastListRequest(api).get("q")).toBe("Album 1"));
  });

  it("“清除”回到没有搜索词的列表，输入框清空并保持焦点", async () => {
    setup();
    const { history } = renderApp({ path: "/albums?q=Album%2007" });
    await waitFor(() => expect(albumLinks()).toHaveLength(10));
    expect((searchBox() as HTMLInputElement).value).toBe("Album 07");

    await userEvent.click(screen.getByRole("button", { name: "清除" }));
    await waitFor(() => expect(history.location.search).toBe(""));
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    expect((searchBox() as HTMLInputElement).value).toBe("");
    expect(document.activeElement).toBe(searchBox());
    expect(screen.queryByRole("button", { name: "清除" })).toBeNull();
    expect(screen.getByText("共 120 张专辑")).toBeTruthy();
  });

  it("提交新搜索回到第 1 页：地址里不再有页码", async () => {
    setup();
    const { history } = renderApp({ path: "/albums?page=2" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    await userEvent.type(searchBox(), "Album{Enter}");
    await waitFor(() => expect(searchOf(history).get("q")).toBe("Album"));
    expect(searchOf(history).has("page")).toBe(false);
  });

  it("只有空白：当作没有搜索词，不发搜索请求，也不新增历史项", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    await userEvent.type(searchBox(), "   {Enter}");
    await waitFor(() => expect(history.location.search).toBe(""));
    expect(listRequests(api).every((path) => !path.includes("q="))).toBe(true);
  });

  it("超过 200 字节：不发请求，提示搜索词太长，不静默丢弃", async () => {
    const { api } = setup();
    renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    const before = listRequests(api).length;
    await userEvent.type(searchBox(), "长".repeat(70) + "{Enter}"); // 70 个汉字 = 210 字节
    expect(await screen.findByText("搜索词太长了，请短一些。")).toBeTruthy();
    expect(searchBox().getAttribute("aria-invalid")).toBe("true");
    expect(listRequests(api)).toHaveLength(before);
  });

  it("地址里的数字搜索词（?q=1984）按原文本搜索；应用自己提交的数字也能往返", async () => {
    const { api } = setup({ groups: [albumGroup(1, { title: "1984" }), albumGroup(2, { title: "Other" })] });
    const { history } = renderApp({ path: "/albums?q=1984" });
    await waitFor(() => expect(albumLinks()).toHaveLength(1));
    expect(lastListRequest(api).get("q")).toBe("1984");
    expect((searchBox() as HTMLInputElement).value).toBe("1984");

    await userEvent.clear(searchBox());
    await userEvent.type(searchBox(), "Other{Enter}");
    await waitFor(() => expect(titles()).toEqual(["Other"]));
    await userEvent.clear(searchBox());
    await userEvent.type(searchBox(), "1984{Enter}");
    await waitFor(() => expect(titles()).toEqual(["1984"]));
    expect(history.location.search).toContain("1984");
  });

  it("后退与前进：输入框、结果与地址保持一致", async () => {
    setup();
    const { history } = renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    await userEvent.type(searchBox(), "Album 07{Enter}");
    await waitFor(() => expect(albumLinks()).toHaveLength(10));

    history.back();
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    expect((searchBox() as HTMLInputElement).value).toBe("");
    history.forward();
    await waitFor(() => expect(albumLinks()).toHaveLength(10));
    expect((searchBox() as HTMLInputElement).value).toBe("Album 07");
  });

  it("没有符合条件的专辑：说明并提供一键清除，结果数为 0", async () => {
    setup();
    const { history } = renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    await userEvent.type(searchBox(), "不存在的专辑{Enter}");
    expect(await screen.findByText(/没有符合条件的专辑。/)).toBeTruthy();
    expect(screen.getByText("找到 0 张专辑")).toBeTruthy();
    expect(screen.queryByText(/还没有专辑/)).toBeNull();
    expect(screen.queryByRole("navigation", { name: "专辑分页" })).toBeNull();

    const clear = screen.getByRole("link", { name: "清除筛选" });
    expect(clear.getAttribute("href")).toBe("/albums");
    await userEvent.click(clear);
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    expect(history.location.search).toBe("");
  });
});

describe("专辑列表：只看需要关注", () => {
  it("勾选立即生效：请求带 attention=required，只显示需要关注的专辑，回到第 1 页", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: "/albums?page=2" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));

    await userEvent.click(screen.getByRole("checkbox", { name: "只看需要关注" }));
    await waitFor(() => expect(searchOf(history).get("attention")).toBe("required"));
    expect(searchOf(history).has("page")).toBe(false);
    expect(await results("需要关注的专辑")).toBeTruthy();
    await waitFor(() => expect(albumLinks()).toHaveLength(12));
    expect(lastListRequest(api).get("attention")).toBe("required");
    expect(screen.getByText("找到 12 张专辑")).toBeTruthy();
    expect((screen.getByRole("checkbox", { name: "只看需要关注" }) as HTMLInputElement).checked).toBe(true);

    await userEvent.click(screen.getByRole("checkbox", { name: "只看需要关注" }));
    await waitFor(() => expect(history.location.search).toBe(""));
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
  });

  it("与搜索组合：标题说明两个条件，结果同时满足", async () => {
    setup();
    const { history } = renderApp({ path: "/albums?attention=required" });
    await waitFor(() => expect(albumLinks()).toHaveLength(12));
    await userEvent.type(searchBox(), "Album 01{Enter}");
    expect(await results("“Album 01”中需要关注的专辑")).toBeTruthy();
    await waitFor(() => expect(titles()).toEqual(["Album 010"]));
    expect(searchOf(history).get("attention")).toBe("required");
  });

  it("需要关注的标签不把它说成错误；没有需要关注的专辑时给出说明", async () => {
    setup({ groups: albumGroups(3) });
    renderApp({ path: "/albums?attention=required" });
    expect(await screen.findByText(/没有符合条件的专辑。/)).toBeTruthy();
  });
});

describe("专辑列表：分页与地址", () => {
  it("超过一页：显示分页，翻页更新地址（第 1 页不写参数），请求带页码与页大小，末页是剩余的卡片", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: "/albums" });
    const nav = await pagination();
    expect(within(nav).getByText("第 1 / 3 页").getAttribute("aria-current")).toBe("page");
    expect(within(nav).queryByRole("link", { name: "上一页" })).toBeNull();
    await waitFor(() => expect(albumLinks()).toHaveLength(48));

    await userEvent.click(within(nav).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
    await waitFor(() => expect(within(screen.getByRole("navigation", { name: "专辑分页" })).getByText("第 2 / 3 页")).toBeTruthy());
    expect(listRequests(api).at(-1)).toBe("/api/v1/release-groups?page=2&page_size=48");
    await waitFor(() => expect(titles()[0]).toBe("Album 049"));

    await userEvent.click(within(screen.getByRole("navigation", { name: "专辑分页" })).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(history.location.search).toBe("?page=3"));
    await waitFor(() => expect(albumLinks()).toHaveLength(24));
    expect(within(screen.getByRole("navigation", { name: "专辑分页" })).queryByRole("link", { name: "下一页" })).toBeNull();

    history.back();
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
    await waitFor(() => expect(titles()[0]).toBe("Album 049"));
  });

  it("翻页时保留搜索词与筛选", async () => {
    setup();
    const { history } = renderApp({ path: "/albums?q=Album" });
    const nav = await pagination();
    await userEvent.click(within(nav).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(searchOf(history).get("page")).toBe("2"));
    expect(searchOf(history).get("q")).toBe("Album");
  });

  it("一页放得下时不显示分页", async () => {
    setup({ groups: albumGroups(48) });
    renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    expect(screen.queryByRole("navigation", { name: "专辑分页" })).toBeNull();
  });

  it.each([
    ["?page=2", "page=2&page_size=48"],
    ["?page=abc", "page=1&page_size=48"],
    ["?page=0", "page=1&page_size=48"],
    ["?page=1", "page=1&page_size=48"],
    ["?page=-2", "page=1&page_size=48"],
    ["?page=2.5", "page=1&page_size=48"],
    ["?attention=yes", "page=1&page_size=48"],
    ["?attention=required", "page=1&page_size=48&attention=required"],
    ["?q=%20%20", "page=1&page_size=48"],
    ["?q=", "page=1&page_size=48"],
    ["?unknown=1", "page=1&page_size=48"],
  ])("地址 %s：请求 %s，不合法的值不会原样发给后端", async (search, expected) => {
    const { api } = setup();
    renderApp({ path: `/albums${search}` });
    await waitFor(() => expect(albumLinks().length).toBeGreaterThan(0));
    expect(listRequests(api)).toEqual([`/api/v1/release-groups?${expected}`]);
  });

  it("搜索词超过 200 字节的地址：当作没有搜索词", async () => {
    const { api } = setup();
    renderApp({ path: `/albums?q=${encodeURIComponent("长".repeat(70))}` });
    await waitFor(() => expect(albumLinks().length).toBeGreaterThan(0));
    expect(listRequests(api)).toEqual(["/api/v1/release-groups?page=1&page_size=48"]);
  });

  it("页码超出末页：用替换历史的方式改到末页，不新增历史项，并显示末页的内容", async () => {
    setup();
    const { history } = renderApp({ path: "/albums?page=99" });
    await waitFor(() => expect(history.location.search).toBe("?page=3"));
    await waitFor(() => expect(albumLinks()).toHaveLength(24));
    expect(within(screen.getByRole("navigation", { name: "专辑分页" })).getByText("第 3 / 3 页")).toBeTruthy();
    expect(history.length).toBe(1);
  });

  it("总数缩小到只剩一页：回到第 1 页", async () => {
    setup({ groups: albumGroups(10) });
    const { history } = renderApp({ path: "/albums?page=3" });
    await waitFor(() => expect(history.location.search).toBe(""));
    await waitFor(() => expect(albumLinks()).toHaveLength(10));
    expect(history.length).toBe(1);
  });

  it("没有专辑时带了页码：回到第 1 页的空库说明，地址里不再有参数", async () => {
    setup({ groups: [] });
    const { history } = renderApp({ path: "/albums?page=3" });
    expect(await screen.findByText(/资料库里还没有专辑/)).toBeTruthy();
    await waitFor(() => expect(history.location.search).toBe(""));
    expect(history.length).toBe(1);
  });

  it("翻页时旧结果保持可见并标明正在更新，新结果到达后替换", async () => {
    const { api } = setup();
    renderApp({ path: "/albums" });
    const nav = await pagination();
    await waitFor(() => expect(albumLinks()).toHaveLength(48));
    const gate = deferred<Response>();
    const firstPage = jsonResponse(200, { items: [], pagination: { page: 2, page_size: 48, total: 120 } });
    api.on("GET", "/api/v1/release-groups?page=2&page_size=48", () => gate.promise);

    await userEvent.click(within(nav).getByRole("link", { name: "下一页" }));
    expect(await screen.findByText("，正在更新…")).toBeTruthy();
    expect(titles()[0]).toBe("Album 001"); // 旧结果仍在
    const region = await results();
    expect(region.querySelector('[aria-busy="true"]')).not.toBeNull();

    gate.resolve(firstPage);
    await waitFor(() => expect(screen.queryByText("，正在更新…")).toBeNull());
  });
});

describe("专辑列表：翻页后的滚动与焦点", () => {
  it("点页码链接：结果标题滚到视野顶部并获得焦点，接着 Tab 就是新一页的第一张卡片", async () => {
    setup();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    try {
      renderApp({ path: "/albums" });
      const nav = await pagination();
      await waitFor(() => expect(albumLinks()).toHaveLength(48));
      expect(scroll).not.toHaveBeenCalled();

      await userEvent.click(within(nav).getByRole("link", { name: "下一页" }));
      const resultsHeading = screen.getByRole("heading", { level: 2, name: "全部专辑" });
      expect(document.activeElement).toBe(resultsHeading);
      expect(scroll).toHaveBeenCalledTimes(1);
      expect(scroll.mock.contexts[0]).toBe(resultsHeading);

      await waitFor(() => expect(titles()[0]).toBe("Album 049"));
      await userEvent.tab();
      expect(document.activeElement).toBe(albumLinks()[0]);
    } finally {
      scroll.mockRestore();
    }
  });

  it("上一页同样；而改搜索词或筛选不挪动焦点（用户还在筛选区里）", async () => {
    setup();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    try {
      renderApp({ path: "/albums?page=2" });
      const nav = await pagination();
      await waitFor(() => expect(albumLinks()).toHaveLength(48));
      await userEvent.click(within(nav).getByRole("link", { name: "上一页" }));
      expect(document.activeElement).toBe(screen.getByRole("heading", { level: 2, name: "全部专辑" }));
      expect(scroll).toHaveBeenCalledTimes(1);

      const checkbox = screen.getByRole("checkbox", { name: "只看需要关注" });
      await userEvent.click(checkbox);
      await screen.findByRole("region", { name: "需要关注的专辑" });
      expect(document.activeElement).toBe(checkbox);
      expect(scroll).toHaveBeenCalledTimes(1);
    } finally {
      scroll.mockRestore();
    }
  });
});

describe("专辑列表：空库与失败", () => {
  it("资料库没有专辑：管理员提示去扫描目录，并给出链接", async () => {
    setup({ groups: [] });
    renderApp({ path: "/albums" });
    expect(await screen.findByText(/资料库里还没有专辑。添加音乐目录并扫描后就会出现。/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "前往扫描目录" }).getAttribute("href")).toBe("/settings/library");
    expect(screen.getByText("共 0 张专辑")).toBeTruthy();
  });

  it("资料库没有专辑：普通用户只显示空库提示，没有管理入口", async () => {
    setup({ role: "user", groups: [] });
    renderApp({ path: "/albums" });
    expect(await screen.findByText("资料库里还没有专辑。")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "前往扫描目录" })).toBeNull();
  });

  it("读取失败（503）：显示带请求 ID 的错误与重试，重试成功后显示专辑", async () => {
    const { catalog } = setup({ groups: albumGroups(5) });
    catalog.listFailure = sampleResponse("errors/database-unavailable");
    renderApp({ path: "/albums" });
    // 503 会被自动重试（最多 2 次，带退避），耗尽后才显示错误。
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();
    expect(albumLinks()).toHaveLength(0);

    catalog.listFailure = null;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(albumLinks()).toHaveLength(5));
    expect(screen.queryByRole("alert")).toBeNull();
  }, 15_000);

  it("会话失效（401）：进入会话恢复，回到登录页", async () => {
    const { catalog } = setup();
    catalog.listFailure = sampleResponse("errors/unauthorized");
    const { history } = renderApp({ path: "/albums" });
    await waitFor(() => expect(history.location.pathname).toBe("/login"));
  });
});

describe("导航与概览", () => {
  it.each([["admin" as const], ["user" as const]])("%s：主导航有“专辑”，打开专辑页时它是当前页", async (role) => {
    setup({ role });
    renderApp({ path: "/albums" });
    const nav = await screen.findByRole("navigation", { name: "主导航" });
    expect(within(nav).getByRole("link", { name: "专辑" }).getAttribute("aria-current")).toBe("page");
    expect(within(nav).getByRole("link", { name: "概览" }).getAttribute("aria-current")).toBeNull();
    // 配置入口移到设置分栏，仍只对管理员显示。
    expect(within(nav).queryByRole("link", { name: "扫描目录" })).toBeNull();
    await userEvent.click(within(screen.getByRole("region", { name: "当前用户" })).getByRole("button"));
    await userEvent.click(screen.getByRole("menuitem", { name: "设置" }));
    const settings = await screen.findByRole("navigation", { name: "设置" });
    expect(within(settings).queryByRole("link", { name: "扫描目录" }) !== null).toBe(role === "admin");
  });

  it("概览的两个数字分别链接到全部专辑与只看需要关注的专辑", async () => {
    setup({ groups: albumGroups(30, (index) => ({ attention_count: index <= 4 ? 1 : 0 })) });
    const { history } = renderApp({ path: "/" });
    const summary = await screen.findByRole("region", { name: "资料库" });
    const all = await within(summary).findByRole("link", { name: "30，查看全部专辑" });
    const attention = within(summary).getByRole("link", { name: "4，查看需要关注的专辑" });
    expect(all.getAttribute("href")).toBe("/albums");
    expect(attention.getAttribute("href")).toBe("/albums?attention=required");

    await userEvent.click(attention);
    await waitFor(() => expect(history.location.pathname).toBe("/albums"));
    expect(searchOf(history).get("attention")).toBe("required");
    expect(await results("需要关注的专辑")).toBeTruthy();
    await waitFor(() => expect(albumLinks()).toHaveLength(4));
  });
});

// 示例数据来自真实样本：样本里的专辑摘要没有额外字段被页面误读。
describe("样本数据", () => {
  it("后端样本里的专辑摘要可以直接显示", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    api.on("GET", "/api/v1/release-groups", () => jsonResponse(200, sampleBody("release-groups/list-one-with-artwork")));
    renderApp({ path: "/albums" });
    await waitFor(() => expect(albumLinks()).toHaveLength(1));
    expect(titles()).toEqual(["Sample Preludes"]);
    expect(albumLinks()[0]?.textContent).toContain("Sample Pianist");
    expect(albumLinks()[0]?.querySelector("img")?.getAttribute("src")).toBe("/api/v1/artworks/sample-cover.webp");
  });
});
