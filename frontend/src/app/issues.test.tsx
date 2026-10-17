import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { albumGroup } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { respondToIssues, seedIssues, type GovernanceData } from "../test/governance-data";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// 三张专辑：A 有字段、归组冲突与缺失曲目，B 只有拿不准的标题，C 没有问题。
const albumA = () => albumGroup(1, { title: "Alpha Suites", artist: "Alpha Cellist", year: 1998 });
const albumB = () => albumGroup(2, { title: "Beta Quartets", artist: "Beta Quartet", year: 1990 });
const albumC = () => albumGroup(3, { title: "Gamma Sonatas", artist: "Gamma Duo", year: 2005 });
const releaseOf = (group: ReturnType<typeof albumA>) => group.representative.id;

function seed(governance: GovernanceData) {
  seedIssues(governance, releaseOf(albumA()), {
    fields: [{ field: "media_type", value: "CD", reason: "inconsistent_candidates", candidates: ["CD", "Vinyl"] }],
    grouping: { sourceRefs: ["alpha/01.flac", "alpha/02.flac"], siblings: [{ releaseId: releaseOf(albumC()), groupId: albumC().id, title: "Gamma Sonatas" }] },
    missing: [{ id: "00000000-0000-4000-8000-0000000000e1", title: "Sarabande", medium: 1, position: 4, source: "alpha/04.flac", lastSeenAt: "2026-01-02T08:30:00Z" }],
  });
  seedIssues(governance, releaseOf(albumB()), { fields: [{ field: "title", value: "Beta Quartets", reason: "inconsistent_candidates", candidates: ["Beta Quartets", "Beta Quartet No. 1"] }] });
}

function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: [albumA(), albumB(), albumC()] });
  const governance = installGovernanceApi(api, catalog);
  seed(governance);
  return { api, catalog, governance };
}

const heading = (name: string, level = 1) => screen.findByRole("heading", { level, name });
const issueList = () => screen.getByRole("heading", { level: 2, name: "有问题的版本" }).closest("section") as HTMLElement;

/** jsdom 没有 ResizeObserver：问题页按宽屏处理。窄屏的测试装一个报告指定宽度的替身。 */
const originalResizeObserver = globalThis.ResizeObserver;
afterEach(() => {
  globalThis.ResizeObserver = originalResizeObserver;
});
function narrowContainer(width: number) {
  globalThis.ResizeObserver = class {
    constructor(private readonly callback: ResizeObserverCallback) {}
    observe(target: Element) {
      target.getBoundingClientRect = () => ({ width, height: 600, x: 0, y: 0, top: 0, left: 0, right: width, bottom: 600, toJSON: () => ({}) }) as DOMRect;
      this.callback([], this as unknown as ResizeObserver);
    }
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

describe("元数据问题页：入口与权限", () => {
  it("管理员在“资料库”下看到“元数据问题”，点进去是问题页，不显示数量", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/" });
    const nav = await screen.findByRole("navigation", { name: "主导航" });
    const link = within(nav).getByRole("link", { name: "元数据问题" });
    expect(link.textContent).toBe("元数据问题");
    await user.click(link);
    expect(await heading("元数据问题")).toBeTruthy();
    expect(history.location.pathname).toBe("/issues");
  });

  it("成员直接访问：显示“需要管理员权限”，不请求问题接口，导航里也没有这一项", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: "/issues" });
    expect(await heading("需要管理员权限")).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "主导航" })).queryByRole("link", { name: "元数据问题" })).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/release-issues")).toHaveLength(0);
  });
});

describe("元数据问题页：列表与筛选", () => {
  it("按专辑名列出有待处理问题的版本，每行写类别数目；筛选项带数字；没有问题的版本不列出", async () => {
    const { api } = setup();
    renderApp({ path: "/issues" });
    await screen.findByRole("link", { name: /Alpha Suites/ });
    const links = within(issueList()).getAllByRole("link").filter((link) => link.closest("ul") !== null);
    expect(links.map((link) => link.textContent)).toEqual(["Alpha Suites Remastered · Alpha Cellist 1 个字段 · 归组冲突 · 1 首缺失曲目", "Beta Quartets Remastered · Beta Quartet 1 个字段"]);
    const status = screen.getByRole("navigation", { name: "问题状态" });
    expect(within(status).getByRole("link", { name: "待处理 ，2 个版本" }).getAttribute("aria-current")).toBe("page");
    expect(within(status).getByRole("link", { name: "已核对 ，0 个版本" }).getAttribute("aria-current")).toBeNull();
    const categories = screen.getByRole("navigation", { name: "问题类别" });
    expect(within(categories).getAllByRole("link").map((link) => link.textContent)).toEqual(["全部 2，2 个版本", "字段 2，2 个版本", "归组 1，1 个版本", "缺失曲目 1，1 个版本"]);
    expect(screen.getByText("共 2 个版本，按专辑名排列。")).toBeTruthy();
    expect(screen.getByText("从列表里选一个版本，查看它的问题和依据。")).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/release-issues").at(-1)?.path).toBe("/api/v1/release-issues?status=open&page=1&page_size=30");
  });

  it("按类别筛选与切到“已核对”写进地址，可以刷新与分享；回到全部时参数去掉", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/issues" });
    await screen.findByRole("link", { name: /Alpha Suites/ });
    await user.click(within(screen.getByRole("navigation", { name: "问题类别" })).getByRole("link", { name: /^归组/ }));
    await waitFor(() => expect(history.location.search).toBe("?category=grouping"));
    await waitFor(() => expect(within(issueList()).queryByRole("link", { name: /Beta Quartets/ })).toBeNull());
    expect(api.requestsTo("GET", "/api/v1/release-issues").at(-1)?.path).toBe("/api/v1/release-issues?status=open&page=1&page_size=30&category=grouping");

    await user.click(within(screen.getByRole("navigation", { name: "问题状态" })).getByRole("link", { name: /已核对/ }));
    await waitFor(() => expect(history.location.search).toBe("?status=reviewed&category=grouping"));
    expect(await screen.findByText("没有已核对的问题。")).toBeTruthy();

    await user.click(within(screen.getByRole("navigation", { name: "问题类别" })).getByRole("link", { name: /^全部/ }));
    await waitFor(() => expect(history.location.search).toBe("?status=reviewed"));
  });

  it("没有任何问题时说明问题什么时候出现", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    installGovernanceApi(api, installCatalogApi(api, { groups: [albumC()] }));
    renderApp({ path: "/issues" });
    expect(await screen.findByText("没有待处理的问题。扫描发现新的问题时会出现在这里。")).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "问题列表分页" })).toBeNull();
  });

  it("读取失败时显示原因与“重试”，重试成功后列出问题", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    let failures = 1;
    api.on("GET", "/api/v1/release-issues", (request) => {
      if (failures-- > 0) return jsonResponse(500, { error: { code: "internal_error", message: "服务出错了" }, request_id: "req-1" });
      const result = respondToIssues(governance, new URL(request.path, "http://localhost").searchParams);
      return jsonResponse(result.status, result.body);
    });
    renderApp({ path: "/issues" });
    await user.click(await screen.findByRole("button", { name: "重试" }));
    expect(await screen.findByRole("link", { name: /Alpha Suites/ })).toBeTruthy();
  });
});

describe("元数据问题页：详情", () => {
  it("选中的版本写进地址并带 aria-current；详情逐条列出字段、归组冲突（文件与同一目录的其他版本）与缺失曲目", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/issues" });
    await user.click(await screen.findByRole("link", { name: /Alpha Suites/ }));
    await waitFor(() => expect(history.location.search).toBe(`?release=${releaseOf(albumA())}`));
    expect(within(issueList()).getByRole("link", { name: /Alpha Suites/ }).getAttribute("aria-current")).toBe("page");
    expect(within(issueList()).getByRole("link", { name: /Beta Quartets/ }).getAttribute("aria-current")).toBeNull();

    const detail = (await heading("Alpha Suites", 2)).closest("article") as HTMLElement;
    expect(within(detail).getByText(/需要关注：3/)).toBeTruthy();
    expect(within(detail).getByRole("link", { name: "打开这张专辑" }).getAttribute("href")).toBe(`/albums/${albumA().id}?release=${releaseOf(albumA())}`);

    const fields = within(detail).getByRole("table", { name: "拿不准的字段" });
    const [, media] = within(fields).getAllByRole("row");
    expect(within(media as HTMLElement).getByRole("rowheader").textContent).toBe("介质类型");
    expect(within(media as HTMLElement).getByText("候选值互相不一致")).toBeTruthy();
    expect(within(media as HTMLElement).getByText("CD、Vinyl")).toBeTruthy();

    const grouping = within(detail).getByRole("heading", { level: 3, name: "同一目录的归组冲突" }).closest("section") as HTMLElement;
    expect(within(grouping).getByText("alpha/01.flac")).toBeTruthy();
    expect(within(grouping).getByRole("link", { name: "Gamma Sonatas" }).getAttribute("href")).toBe(`/albums/${albumC().id}?release=${releaseOf(albumC())}`);

    const missing = within(detail).getByRole("table", { name: "缺失的曲目" });
    const [, sarabande] = within(missing).getAllByRole("row");
    expect(within(sarabande as HTMLElement).getByRole("rowheader").textContent).toBe("Sarabande");
    expect(within(sarabande as HTMLElement).getByText("第 1 张 · 第 4 首")).toBeTruthy();
    expect(within(sarabande as HTMLElement).getByText("alpha/04.flac")).toBeTruthy();
    expect(within(sarabande as HTMLElement).getByText(/^2026-01-02 /)).toBeTruthy();
    expect(within(detail).getByRole("heading", { level: 3, name: "缺失的曲目（1 首）" })).toBeTruthy();
  });

  it("拿不准的标题等字段写明在“编辑发行信息”里处理；版本不可见时就地说明", async () => {
    const { catalog } = setup();
    renderApp({ path: `/issues?release=${releaseOf(albumB())}` });
    const detail = (await heading("Beta Quartets", 2)).closest("article") as HTMLElement;
    expect(within(detail).getByText("在“编辑发行信息”里修改或确认扫描值")).toBeTruthy();
    expect(within(detail).queryByRole("heading", { level: 3, name: "同一目录的归组冲突" })).toBeNull();

    catalog.hiddenReleases.add(releaseOf(albumA()));
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    expect(await screen.findByText("这个版本现在不可见（可能刚被重新扫描，文件都找不到了），没有可以处理的问题。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("窄容器时顺序导航：先只有列表，选中之后只有详情与“返回问题列表”，返回时筛选保留", async () => {
    setup();
    narrowContainer(600);
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/issues?category=fields" });
    await user.click(await screen.findByRole("link", { name: /Beta Quartets/ }));
    expect(await heading("Beta Quartets", 2)).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "有问题的版本" })).toBeNull();
    await user.click(screen.getByRole("link", { name: "返回问题列表" }));
    await waitFor(() => expect(history.location.search).toBe("?category=fields"));
    expect(await screen.findByRole("heading", { level: 2, name: "有问题的版本" })).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "Beta Quartets" })).toBeNull();
  });

  it("宽容器时两栏同时显示列表与详情", async () => {
    setup();
    narrowContainer(1200);
    renderApp({ path: `/issues?release=${releaseOf(albumB())}` });
    expect(await heading("Beta Quartets", 2)).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "有问题的版本" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "返回问题列表" })).toBeNull();
    await act(async () => undefined);
  });
});
