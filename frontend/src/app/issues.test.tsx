import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { albumGroup } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { markUncertain, respondToIssueReview, respondToIssues, respondToReleaseIssues, scanIssues, seedIssues, type GovernanceData } from "../test/governance-data";
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
    expect(within(status).getByRole("link", { name: "待处理，2 个版本" }).getAttribute("aria-current")).toBe("page");
    expect(within(status).getByRole("link", { name: "已核对，0 个版本" }).getAttribute("aria-current")).toBeNull();
    const categories = screen.getByRole("navigation", { name: "问题类别" });
    expect(within(categories).getAllByRole("link").map((link) => link.getAttribute("aria-label"))).toEqual(["全部，2 个版本", "字段，2 个版本", "归组，1 个版本", "缺失曲目，1 个版本"]);
    expect(within(categories).getAllByRole("link").map((link) => link.textContent)).toEqual(["全部2", "字段2", "归组1", "缺失曲目1"]);
    expect(screen.getByText("共 2 个版本")).toBeTruthy();
    expect(screen.getByText("从列表里选一个版本。")).toBeTruthy();
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
    expect(await screen.findByText("没有待处理的问题。")).toBeTruthy();
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
    expect(await screen.findByText("这个版本已经找不到了，可能刚重新扫描过。")).toBeTruthy();
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

const reviewPath = (releaseId: string) => `/api/v1/releases/${releaseId}/issue-review`;
const detailOf = async (title: string) => (await heading(title, 2)).closest("article") as HTMLElement;
const sectionOf = (detail: HTMLElement, name: string) => within(detail).getByRole("heading", { level: 3, name }).closest("section") as HTMLElement;

describe("元数据问题页：核对、取消核对与撤销", () => {
  it("核对归组冲突：请求带版本号、问题与依据令牌，不弹确认；之后显示“已核对 · 用户名 · 时间”，列表与数字一起变少", async () => {
    const { api, catalog } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    const detail = await detailOf("Alpha Suites");
    expect(within(detail).getByText(/需要关注：3/)).toBeTruthy();
    await user.click(within(sectionOf(detail, "同一目录的归组冲突")).getByRole("button", { name: "标记为已核对：归组冲突" }));

    expect(await screen.findByText("已把“归组冲突”标记为已核对。")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    const [request] = api.requestsTo("POST", reviewPath(releaseOf(albumA())));
    expect(request?.body).toEqual({ expected_revision: 1, issue: "grouping", action: "review", basis: expect.stringMatching(/^[0-9a-f]{64}$/) });
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);

    const refreshed = await detailOf("Alpha Suites");
    await waitFor(() => expect(within(refreshed).getByText(/需要关注：2/)).toBeTruthy());
    const grouping = sectionOf(refreshed, "同一目录的归组冲突");
    expect(within(grouping).getByText("已核对")).toBeTruthy();
    expect(within(grouping).getByText(/^curator · 2026-01-03 /)).toBeTruthy();
    expect(within(grouping).queryByRole("button", { name: /^标记为已核对/ })).toBeNull();
    expect(within(grouping).getByRole("button", { name: "取消核对：归组冲突" })).toBeTruthy();
    // 列表行与筛选项、专辑列表的“需要关注”一起变化。
    await waitFor(() => expect(within(issueList()).getByRole("link", { name: /Alpha Suites/ }).textContent).toBe("Alpha Suites Remastered · Alpha Cellist 1 个字段 · 1 首缺失曲目"));
    expect(within(screen.getByRole("navigation", { name: "问题状态" })).getByRole("link", { name: "已核对，1 个版本" })).toBeTruthy();
    expect(catalog.groups.find((group) => group.id === albumA().id)?.attention_count).toBe(2);
  });

  it("核对之后按钮换成“取消核对”：焦点落在这一节的标题上，不掉回页面顶部", async () => {
    setup();
    const user = userEvent.setup();
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    const button = within(sectionOf(await detailOf("Alpha Suites"), "同一目录的归组冲突")).getByRole("button", { name: "标记为已核对：归组冲突" });
    button.focus();
    await user.keyboard("{Enter}");
    await screen.findByText(/已把“归组冲突”标记为已核对/);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("heading", { level: 3, name: "同一目录的归组冲突" })));
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "取消核对：归组冲突" }));
  });

  it("结果提示里的“撤销”恢复核对之前的状态；取消核对也可以撤销，恢复原来的核对", async () => {
    const { governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    let detail = await detailOf("Alpha Suites");
    await user.click(within(sectionOf(detail, "缺失的曲目（1 首）")).getByRole("button", { name: "标记为已核对：缺失曲目" }));
    await screen.findByText(/已把“缺失曲目”标记为已核对/);
    await user.click(screen.getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销这次核对，问题恢复到之前的状态。")).toBeTruthy();
    detail = await detailOf("Alpha Suites");
    await waitFor(() => expect(within(sectionOf(detail, "缺失的曲目（1 首）")).getByRole("button", { name: "标记为已核对：缺失曲目" })).toBeTruthy());
    expect(governance.operations.map((operation) => operation.type)).toEqual(["release_issue_review", "revert"]);

    // 核对、取消核对，再撤销取消：回到已核对，核对人与时间是原来的那一次。
    await user.click(within(sectionOf(await detailOf("Alpha Suites"), "缺失的曲目（1 首）")).getByRole("button", { name: "标记为已核对：缺失曲目" }));
    await screen.findByText(/已把“缺失曲目”标记为已核对/);
    const reviewedAt = within(sectionOf(await detailOf("Alpha Suites"), "缺失的曲目（1 首）")).getByText(/^curator · /).textContent;
    await user.click(within(sectionOf(await detailOf("Alpha Suites"), "缺失的曲目（1 首）")).getByRole("button", { name: "取消核对：缺失曲目" }));
    expect(await screen.findByText("已取消“缺失曲目”的核对。")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "撤销" }));
    await screen.findByText("已撤销这次核对，问题恢复到之前的状态。");
    await waitFor(() => expect(within(sectionOf(screen.getByRole("article"), "缺失的曲目（1 首）")).getByText(/^curator · /).textContent).toBe(reviewedAt));
  });

  it("页面打开之后扫描改了问题（409 issue_changed）：说明“刚刚变了”，详情重新读取，按新的情况可以再核对", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    const detail = await detailOf("Alpha Suites");
    // 页面打开之后的一次扫描：涉及的文件变了。
    scanIssues(governance, releaseOf(albumA()), {
      ...governance.issues.get(releaseOf(albumA()))?.seed,
      grouping: { sourceRefs: ["alpha/01.flac", "alpha/02.flac", "alpha/03.flac"] },
    });
    await user.click(within(sectionOf(detail, "同一目录的归组冲突")).getByRole("button", { name: "标记为已核对：归组冲突" }));
    expect(await screen.findByText("这个问题刚刚变了")).toBeTruthy();
    expect(screen.getByText("扫描刚刚改变了这个问题，已重新读取，请看过之后再核对。")).toBeTruthy();
    await waitFor(() => expect(within(sectionOf(screen.getByRole("article"), "同一目录的归组冲突")).getByText("alpha/03.flac")).toBeTruthy());
    expect(governance.operations).toHaveLength(0);
    await user.click(within(sectionOf(screen.getByRole("article"), "同一目录的归组冲突")).getByRole("button", { name: "标记为已核对：归组冲突" }));
    expect(await screen.findByText(/已把“归组冲突”标记为已核对/)).toBeTruthy();
    const bases = api.requestsTo("POST", reviewPath(releaseOf(albumA()))).map((request) => (request.body as { basis: string }).basis);
    expect(bases).toHaveLength(2);
    expect(bases[0]).not.toBe(bases[1]);
  });

  it("核对之后情况变了：标出“核对之后涉及的文件变了”，可以按现在的情况重新核对，也可以取消", async () => {
    const { governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    await user.click(within(sectionOf(await detailOf("Alpha Suites"), "同一目录的归组冲突")).getByRole("button", { name: "标记为已核对：归组冲突" }));
    await screen.findByText(/已把“归组冲突”标记为已核对/);
    scanIssues(governance, releaseOf(albumA()), { ...governance.issues.get(releaseOf(albumA()))?.seed, grouping: { sourceRefs: ["alpha/09.flac"] } });
    // 重新进入这个版本（换一个再换回来）：查询每次都重新读取。
    await user.click(within(issueList()).getByRole("link", { name: /Beta Quartets/ }));
    await heading("Beta Quartets", 2);
    await user.click(within(issueList()).getByRole("link", { name: /Alpha Suites/ }));
    const grouping = sectionOf(await detailOf("Alpha Suites"), "同一目录的归组冲突");
    expect(within(grouping).getByText("核对之后涉及的文件变了")).toBeTruthy();
    expect(within(grouping).getByRole("button", { name: "按现在的情况重新核对：归组冲突" })).toBeTruthy();
    expect(within(grouping).getByRole("button", { name: "取消核对：归组冲突" })).toBeTruthy();
  });

  it("结果不确定：提示“结果未确认”，“重试”沿用同一个幂等键，不会重复核对", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    const detail = await detailOf("Alpha Suites");
    api.on("POST", /^\/api\/v1\/releases\/[^/]+\/issue-review$/, () => {
      throw new Error("connection reset");
    });
    await user.click(within(sectionOf(detail, "同一目录的归组冲突")).getByRole("button", { name: "标记为已核对：归组冲突" }));
    expect(await screen.findByText("结果未确认")).toBeTruthy();
    api.on("POST", /^\/api\/v1\/releases\/[^/]+\/issue-review$/, (request) => {
      const result = respondToIssueReview(governance, releaseOf(albumA()), request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText(/已把“归组冲突”标记为已核对/)).toBeTruthy();
    const requests = api.requestsTo("POST", reviewPath(releaseOf(albumA())));
    expect(requests).toHaveLength(2);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(governance.operations).toHaveLength(1);
  });

  it("来源与介质类型在字段表里核对；其余字段由“编辑发行信息”处理，确认扫描值之后字段不再列出", async () => {
    const { governance } = setup();
    const user = userEvent.setup();
    markUncertain(governance, releaseOf(albumB()), ["title"]);
    renderApp({ path: `/issues?release=${releaseOf(albumA())}` });
    const fields = sectionOf(await detailOf("Alpha Suites"), "拿不准的字段");
    await user.click(within(fields).getByRole("button", { name: "标记为已核对：介质类型" }));
    expect(await screen.findByText(/已把“介质类型”标记为已核对/)).toBeTruthy();
    // 只有来源或介质类型时没有“编辑发行信息”：那两个字段不能在编辑里修改。
    expect(within(sectionOf(await detailOf("Alpha Suites"), "拿不准的字段")).queryByRole("button", { name: "编辑发行信息" })).toBeNull();

    await user.click(within(issueList()).getByRole("link", { name: /Beta Quartets/ }));
    await user.click(within(sectionOf(await detailOf("Beta Quartets"), "拿不准的字段")).getByRole("button", { name: "编辑发行信息" }));
    const dialog = await screen.findByRole("dialog", { name: /编辑发行信息/ });
    await user.click(await within(dialog).findByRole("button", { name: /确认扫描值/ }));
    await user.click(within(dialog).getByRole("button", { name: "保存修改" }));
    expect(await screen.findByText(/已保存修改：标题/)).toBeTruthy();
    await waitFor(() => expect(screen.getByText("已经没有问题了。")).toBeTruthy());
    expect(screen.getByText(/这个版本已经没有待处理的问题。/)).toBeTruthy();
    // 处理完的版本仍然选中，列表里说明已经没有待处理的问题（它不在当前筛选里了，但不跳走）。
    expect(within(issueList()).queryByRole("link", { name: /Beta Quartets/ })).toBeNull();
    expect(screen.getByRole("heading", { level: 2, name: "Beta Quartets" })).toBeTruthy();
  });
});

describe("专辑详情的“查看问题”", () => {
  it("当前版本需要关注时管理员有“查看问题”，打开问题页并选中这个版本；没有问题的版本没有", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/albums/${albumA().id}` });
    await user.click(await screen.findByRole("link", { name: "查看问题" }));
    await waitFor(() => expect(history.location.pathname).toBe("/issues"));
    expect(history.location.search).toBe(`?release=${releaseOf(albumA())}`);
    expect(await heading("Alpha Suites", 2)).toBeTruthy();

    renderApp({ path: `/albums/${albumC().id}` });
    await screen.findByRole("button", { name: "编辑发行信息" });
    expect(screen.queryByRole("link", { name: "查看问题" })).toBeNull();
  });
});

describe("操作记录里的核对", () => {
  it("核对与撤销核对各有一条记录，名称与改动行写明问题；只有最近一条可以撤销", async () => {
    const { governance } = setup();
    const releaseId = releaseOf(albumA());
    const detail = respondToReleaseIssues(governance, releaseId).body as { grouping: { basis: string }; missing_tracks: { basis: string } };
    const review = (issue: string, revision: number, basis: string | undefined, key: string) => {
      const result = respondToIssueReview(governance, releaseId, key, { expected_revision: revision, issue, action: basis === undefined ? "unreview" : "review", basis });
      if (result.status !== 200) throw new Error(`种子核对失败：${JSON.stringify(result.body)}`);
    };
    review("grouping", 1, detail.grouping.basis, "20000000-0000-4000-8000-000000000001");
    review("missing_tracks", 2, detail.missing_tracks.basis, "20000000-0000-4000-8000-000000000002");
    review("grouping", 3, undefined, "20000000-0000-4000-8000-000000000003");
    renderApp({ path: "/settings/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((row) => [within(row).getByText(/^核对问题$|^撤销核对$/).textContent, within(row).getByText(/：(标记为已核对|取消核对)$/).textContent])).toEqual([
      ["核对问题", "归组冲突：取消核对"],
      ["核对问题", "缺失曲目（1 首）：标记为已核对"],
      ["核对问题", "归组冲突：标记为已核对"],
    ]);
    expect(within(rows[1] as HTMLElement).getByText("之后又有修改")).toBeTruthy();

    // 在操作记录页撤销最近一条（取消核对）：确认对话框写明会改回什么，成功提示说的是核对，不是字段修改。
    const user = userEvent.setup();
    await user.click(within(rows[0] as HTMLElement).getByRole("button", { name: /^撤销/ }));
    const confirm = await screen.findByRole("dialog", { name: "撤销这次核对？" });
    expect(within(confirm).getByText("归组冲突：标记为已核对")).toBeTruthy();
    await user.click(within(confirm).getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销这次核对，问题恢复到之前的状态。")).toBeTruthy();
  });
});
