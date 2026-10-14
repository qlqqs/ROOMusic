import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { albumGroup } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { respondToCorrect, respondToGroupChange, respondToRevert, type GovernanceData } from "../test/governance-data";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// A（1998）、B（1990）、C（2005）三张单版本专辑。
const albumA = () => albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, artwork: true });
const albumB = () => albumGroup(2, { title: "Beta Quartets", artist: "Beta Quartet", year: 1990, artwork: true });
const albumC = () => albumGroup(3, { title: "Gamma Sonatas", artist: "Gamma Duo", year: 2005 });
const releaseOf = (group: ReturnType<typeof albumA>) => group.representative.id;

function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: [albumA(), albumB(), albumC()] });
  const governance = installGovernanceApi(api, catalog);
  return { api, catalog, governance };
}

// 种子操作直接走替身的接口函数：幂等键只需要互不相同。
let keyCounter = 0;
const nextKey = () => `10000000-0000-4000-8000-${String((keyCounter += 1)).padStart(12, "0")}`;

function correct(governance: GovernanceData, releaseId: string, revision: number, field: "title" | "genre" | "label", value: string) {
  const result = respondToCorrect(governance, releaseId, nextKey(), { expected_revision: revision, changes: { [field]: { mode: "set", value } } });
  if (result.status !== 200) throw new Error(`种子修正失败：${JSON.stringify(result.body)}`);
}

function join(governance: GovernanceData, releaseId: string, revision: number, groupId: string) {
  const result = respondToGroupChange(governance, releaseId, nextKey(), { expected_revision: revision, action: "join", group_id: groupId });
  if (result.status !== 200) throw new Error(`种子归组失败：${JSON.stringify(result.body)}`);
}

function revertFirst(governance: GovernanceData, operationIndex: number) {
  const operation = governance.operations[operationIndex];
  if (operation === undefined) throw new Error("没有这条种子操作");
  const result = respondToRevert(governance, operation.id, nextKey());
  if (result.status !== 200) throw new Error(`种子撤销失败：${JSON.stringify(result.body)}`);
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
/** “修改记录”折叠面板的标题行（展开后表格的 caption 也叫这个名字，所以限定在 summary 里）。 */
const historySummary = () => screen.getByText("修改记录", { selector: "summary span" });
const revertPath = (operationId: string) => `/api/v1/operations/${operationId}/revert`;

/** 种子：A 改标题（op0）、A 归入 B（op1）、撤销 op1（op2）；C 改流派（op3，现在唯一可以撤销的）。 */
function seedMixed(governance: GovernanceData) {
  correct(governance, releaseOf(albumA()), 1, "title", "Sample Symphonies (corrected)");
  join(governance, releaseOf(albumA()), 2, albumB().id);
  revertFirst(governance, 1);
  correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
}

describe("操作记录页：入口与权限", () => {
  it("管理员在“资料库”下看到“操作记录”，点进去是操作记录页", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/" });
    const nav = await screen.findByRole("navigation", { name: "主导航" });
    await user.click(within(nav).getByRole("link", { name: "操作记录" }));
    expect(await heading("操作记录")).toBeTruthy();
    expect(history.location.pathname).toBe("/operations");
    expect(within(nav).getByRole("link", { name: "操作记录" }).getAttribute("aria-current")).toBe("page");
  });

  it("成员直接访问：显示“需要管理员权限”，不请求操作记录接口，导航里也没有这一项", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: "/operations" });
    expect(await heading("需要管理员权限")).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "主导航" })).queryByRole("link", { name: "操作记录" })).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/operations")).toHaveLength(0);
  });
});

describe("操作记录页：列表", () => {
  it("没有记录时说明记录什么时候出现，没有表格与分页", async () => {
    setup();
    renderApp({ path: "/operations" });
    expect(await screen.findByText("还没有操作记录。修改专辑信息或调整归组之后，记录会出现在这里。")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("navigation", { name: "操作记录分页" })).toBeNull();
  });

  it("最新的在前，写明时间、操作者、专辑与改动；只有可以撤销的记录有“撤销”，其余在同一位置写出原因", async () => {
    const { governance } = setup();
    seedMixed(governance);
    renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(4);

    // 最新的在前：C 的流派修改（可撤销）、撤销记录、已撤销的归组、A 的标题修改（之后又有修改）。
    const [genre, revert, joined, title] = rows as [HTMLElement, HTMLElement, HTMLElement, HTMLElement];
    expect(within(genre).getByText("修改发行信息")).toBeTruthy();
    expect(within(genre).getByText("curator")).toBeTruthy();
    expect(within(genre).getByRole("link", { name: "Gamma Sonatas" }).getAttribute("href")).toBe(`/albums/${albumC().id}?release=${releaseOf(albumC())}`);
    expect(within(genre).getByText(/^流派：/)).toBeTruthy();
    expect(within(genre).getByRole("button", { name: /^撤销/ })).toBeTruthy();

    expect(within(revert).getByText("撤销归组调整")).toBeTruthy();
    expect(within(revert).getByText("撤销不能再撤销")).toBeTruthy();
    expect(within(joined).getByText("调整归组")).toBeTruthy();
    expect(within(joined).getByText("已撤销")).toBeTruthy();
    expect(within(joined).getByText("从《Sample Symphonies (corrected)》移到《Beta Quartets》")).toBeTruthy();
    expect(within(title).getByText("标题：Sample Symphonies → Sample Symphonies (corrected)")).toBeTruthy();
    expect(within(title).getByText("之后又有修改")).toBeTruthy();

    // 整张表只有一个“撤销”按钮，它的可访问名称带上时间与类型，读屏的按钮列表里能分辨。
    expect(screen.getAllByRole("button", { name: /^撤销/ })).toHaveLength(1);
    expect(within(genre).getByRole("button", { name: /^撤销（\d{4}-\d{2}-\d{2} \d{2}:\d{2} 的修改发行信息）$/ })).toBeTruthy();
  });

  it("每页 20 条：请求带页码与页大小，分页导航写明页数，下一页进入第 2 页", async () => {
    const { api, governance } = setup();
    for (let revision = 1; revision <= 25; revision += 1) correct(governance, releaseOf(albumC()), revision, "label", `Label ${revision}`);
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).getAllByRole("row")).toHaveLength(21);
    expect(screen.getByText("共 25 条记录，最新的在前，每页 20 条。")).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/operations")[0]?.path).toContain("page=1&page_size=20");

    const pagination = screen.getByRole("navigation", { name: "操作记录分页" });
    expect(within(pagination).getByText("第 1 / 2 页")).toBeTruthy();
    await user.click(within(pagination).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
    await waitFor(() => expect(within(screen.getByRole("table", { name: "操作记录" })).getAllByRole("row")).toHaveLength(6));
    expect(within(screen.getByRole("navigation", { name: "操作记录分页" })).getByText("第 2 / 2 页")).toBeTruthy();
  });

  it("页码超出末页时地址改到末页（替换历史），不显示越界的空页", async () => {
    const { governance } = setup();
    for (let revision = 1; revision <= 25; revision += 1) correct(governance, releaseOf(albumC()), revision, "label", `Label ${revision}`);
    const { history } = renderApp({ path: "/operations?page=9" });
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
    expect(await screen.findByRole("table", { name: "操作记录" })).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "操作记录分页" })).getByText("第 2 / 2 页")).toBeTruthy();
  });

  it("按版本筛选：说明在看哪张专辑的记录，只列这个版本的，“查看全部记录”回到全部", async () => {
    const { api, governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/operations?release=${releaseOf(albumC())}` });
    expect(await screen.findByText("只显示《Gamma Sonatas》的记录。")).toBeTruthy();
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    expect(api.requestsTo("GET", "/api/v1/operations")[0]?.path).toContain(`release_id=${releaseOf(albumC())}`);
    expect(screen.getByRole("heading", { level: 2, name: "这个版本的记录" })).toBeTruthy();

    await user.click(screen.getByRole("link", { name: "查看全部记录" }));
    await waitFor(() => expect(history.location.search).toBe(""));
    await waitFor(() => expect(within(screen.getByRole("table", { name: "操作记录" })).getAllByRole("row")).toHaveLength(5));
    expect(screen.queryByText(/^只显示/)).toBeNull();
  });

  it("筛选的版本没有记录时说明这一点；release 不是 UUID 时看全部记录，不向后端发无效的筛选", async () => {
    const { api, governance } = setup();
    correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
    const first = renderApp({ path: `/operations?release=${releaseOf(albumA())}` });
    expect(await screen.findByText("这个版本还没有操作记录。")).toBeTruthy();
    first.view.unmount();

    renderApp({ path: "/operations?release=not-a-uuid" });
    expect(await screen.findByRole("table", { name: "操作记录" })).toBeTruthy();
    expect(screen.queryByText(/^只显示/)).toBeNull();
    const last = api.requestsTo("GET", "/api/v1/operations").at(-1);
    expect(last?.path).not.toContain("release_id");
  });

  it("记录里的专辑已不可见时只写标题，不给打不开的链接", async () => {
    const { catalog, governance } = setup();
    correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
    // 专辑被重新扫描移除：组与版本都不再可见。
    catalog.groups.splice(catalog.groups.findIndex((group) => group.id === albumC().id), 1);
    catalog.groupDetails.delete(albumC().id);
    catalog.releases.delete(releaseOf(albumC()));
    renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).getByText("专辑已不存在")).toBeTruthy();
    expect(within(table).queryByRole("link")).toBeNull();
    expect(within(table).queryByRole("button", { name: /^撤销/ })).toBeNull();
    expect(within(table).getByText("专辑已不可见")).toBeTruthy();
  });

  it("读取失败时有带请求 ID 的错误与重试；重试成功后显示记录", async () => {
    const { api, governance } = setup();
    correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
    // 500 立即显示失败（503 与网络错误会被查询自动重试）。
    api.on("GET", "/api/v1/operations", () => jsonResponse(500, { error: { code: "internal_error", message: "boom" }, request_id: "req-ops-1" }));
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    expect(await screen.findByText(/req-ops-1/)).toBeTruthy();
    const { respondToOperations } = await import("../test/governance-data");
    api.on("GET", "/api/v1/operations", (request) => {
      const result = respondToOperations(governance, new URL(request.path, "http://localhost").searchParams);
      return jsonResponse(result.status, result.body);
    });
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("table", { name: "操作记录" })).toBeTruthy();
  });
});

describe("操作记录页：撤销", () => {
  const openRevert = async (user: ReturnType<typeof userEvent.setup>) => {
    const table = await screen.findByRole("table", { name: "操作记录" });
    await user.click(within(table).getByRole("button", { name: /^撤销/ }));
    return screen.findByRole("dialog", { name: "撤销这次修改？" });
  };

  it("确认对话框写明会改回什么；确认后发出撤销请求（带幂等键、没有请求体），提示成功，列表显示新记录与“已撤销”", async () => {
    const { api, governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    const dialog = await openRevert(user);
    // 初始焦点在“取消”上，避免回车直接确认；写明谁在何时做的什么、会改回什么，以及撤销本身的性质。
    expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));
    expect(within(dialog).getByText(/撤销 curator 在 .+ 做的“修改发行信息”。《Gamma Sonatas》会改回：/)).toBeTruthy();
    expect(within(dialog).getByText(/^流派：Sonata → .+（恢复为扫描值）$/)).toBeTruthy();
    expect(within(dialog).getByText("不会改动音乐文件。撤销会留下一条自己的记录，它不能再被撤销；需要时可以重新修改。")).toBeTruthy();

    const target = governance.operations[3];
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const [request] = api.requestsTo("POST", revertPath(target?.id ?? ""));
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(request?.body).toBeUndefined();

    expect(await screen.findByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeTruthy();
    // 刷新之后：多了一条“撤销修改”，原来那条变成“已撤销”，整张表没有可撤销的记录了。
    const table = await screen.findByRole("table", { name: "操作记录" });
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(6));
    expect(within(table).getByText("撤销修改")).toBeTruthy();
    expect(within(table).getAllByText("已撤销")).toHaveLength(2);
    expect(within(table).queryByRole("button", { name: /^撤销/ })).toBeNull();
    // 成功提示里没有“撤销”（撤销不能再撤销）。
    const notice = screen.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。").closest<HTMLElement>('[role="status"]');
    expect(notice).not.toBeNull();
    expect(within(notice as HTMLElement).queryByRole("button", { name: "撤销" })).toBeNull();
  });

  it("撤销成功后触发它的按钮已经消失：焦点带到区块标题，不掉回页面顶部", async () => {
    const { governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    const dialog = await openRevert(user);
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("heading", { level: 2, name: "全部记录" })));
  });

  it("取消：没有请求，焦点回到“撤销”按钮，记录不变", async () => {
    const { api, governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    const dialog = await openRevert(user);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(within(screen.getByRole("table", { name: "操作记录" })).getByRole("button", { name: /^撤销/ }));
    expect(api.requestsTo("POST", /\/revert$/)).toHaveLength(0);
  });

  it("之后又有修改（409 revert_conflict）：对话框关闭，页面提示说明原因，列表刷新后这条不再能撤销", async () => {
    const { api, governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    const dialog = await openRevert(user);
    // 打开对话框之后别人又改了这张专辑：服务器上这条已经不是最近一次修改。
    correct(governance, releaseOf(albumC()), 2, "label", "Another Label");
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("操作没有完成")).toBeTruthy();
    expect(screen.getByText("这张专辑之后又被修改过，不能撤销这一条。只有最近一次修改可以撤销。")).toBeTruthy();
    const table = await screen.findByRole("table", { name: "操作记录" });
    // 新的修改成了最近一次，它自己可以撤销；原来的流派修改写出原因。
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(6));
    expect(within(table).getAllByText("之后又有修改")).toHaveLength(2);
    expect(within(table).getAllByRole("button", { name: /^撤销/ })).toHaveLength(1);
    expect(api.requestsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  it("这条操作已经被撤销（409 already_reverted）：对话框关闭，提示说明，记录已刷新", async () => {
    const { api, governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    const dialog = await openRevert(user);
    // 另一位管理员刚撤销了它。
    revertFirst(governance, 3);
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("这条操作已经撤销过了，记录已刷新。")).toBeTruthy();
    const table = await screen.findByRole("table", { name: "操作记录" });
    await waitFor(() => expect(within(table).queryByRole("button", { name: /^撤销/ })).toBeNull());
    expect(api.requestsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  it("结果不确定：对话框保持打开，“撤销”变成“重试撤销”，沿用同一个幂等键，成功后关闭", async () => {
    const { api, governance } = setup();
    seedMixed(governance);
    const user = userEvent.setup();
    renderApp({ path: "/operations" });
    const dialog = await openRevert(user);
    const target = governance.operations[3];
    api.on("POST", revertPath(target?.id ?? ""), () => {
      throw new Error("connection reset");
    });
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    expect(await within(dialog).findByText("结果未确认")).toBeTruthy();
    // 失败由对话框显示，页面上的提示不重复。
    expect(screen.getAllByText("结果未确认")).toHaveLength(1);

    api.on("POST", revertPath(target?.id ?? ""), (request) => {
      const result = respondToRevert(governance, target?.id ?? "", request.headers.get("Idempotency-Key"));
      return jsonResponse(result.status, result.body);
    });
    await user.click(within(dialog).getByRole("button", { name: "重试撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const requests = api.requestsTo("POST", revertPath(target?.id ?? ""));
    expect(requests).toHaveLength(2);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(await screen.findByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeTruthy();
  });

  it("撤销归组调整：留在操作记录页（不跟随专辑的新位置），提示写明回到了原来的专辑", async () => {
    const { governance } = setup();
    correct(governance, releaseOf(albumA()), 1, "label", "Corrected Label");
    join(governance, releaseOf(albumA()), 2, albumB().id);
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    await user.click(within(table).getByRole("button", { name: /^撤销/ }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这次归组调整？" });
    expect(within(dialog).getByText("从《Beta Quartets》移回《Sample Symphonies》")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销这次归组调整，这个版本回到了原来的专辑。")).toBeTruthy();
    expect(history.location.pathname).toBe("/operations");
  });
});

describe("专辑详情：修改记录", () => {
  const detailPath = (group: ReturnType<typeof albumA>) => `/albums/${group.id}`;

  it("管理员有折叠的“修改记录”：默认折叠且不请求，展开后读取这个版本最近的记录", async () => {
    const { api, governance } = setup();
    correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
    correct(governance, releaseOf(albumA()), 1, "title", "Sample Symphonies (corrected)");
    const user = userEvent.setup();
    renderApp({ path: detailPath(albumC()) });
    await heading("Gamma Sonatas");
    await screen.findByText("Track 01");
    expect(api.requestsTo("GET", "/api/v1/operations")).toHaveLength(0);
    expect(screen.queryByRole("table", { name: "修改记录" })).toBeNull();

    await user.click(historySummary());
    const table = await screen.findByRole("table", { name: "修改记录" });
    // 只有这个版本的记录，没有“专辑”列。
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    expect(within(table).queryByRole("columnheader", { name: "专辑" })).toBeNull();
    expect(within(table).getByText(/^流派：/)).toBeTruthy();
    const [request] = api.requestsTo("GET", "/api/v1/operations");
    expect(request?.path).toContain(`release_id=${releaseOf(albumC())}`);
    expect(request?.path).toContain("page_size=5");
    expect(screen.getByText("共 1 条。")).toBeTruthy();
    expect(screen.getByRole("link", { name: "全部记录" }).getAttribute("href")).toBe(`/operations?release=${releaseOf(albumC())}`);
  });

  it("没有记录时说明；记录多于 5 条时只显示最近 5 条并给出总数", async () => {
    const { governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: detailPath(albumC()) });
    await heading("Gamma Sonatas");
    await screen.findByText("Track 01");
    await user.click(historySummary());
    expect(await screen.findByText("这个版本还没有修改记录。管理员修改发行信息或调整归组之后，记录会出现在这里。")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "全部记录" })).toBeNull();

    for (let revision = 1; revision <= 7; revision += 1) correct(governance, releaseOf(albumC()), revision, "label", `Label ${revision}`);
    // 收起再展开是新的挂载：每次展开都重新读取，别的管理员刚做的修改也能看到。
    await user.click(historySummary());
    await user.click(historySummary());
    const table = await screen.findByRole("table", { name: "修改记录" });
    expect(within(table).getAllByRole("row")).toHaveLength(6);
    expect(screen.getByText("只显示最近 5 条，共 7 条。")).toBeTruthy();
  });

  it("在“修改记录”里撤销：结果提示显示在记录区（折叠区块外），头部的操作区没有这条提示，列表随之刷新", async () => {
    const { governance } = setup();
    correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
    const user = userEvent.setup();
    renderApp({ path: detailPath(albumC()) });
    await heading("Gamma Sonatas");
    await screen.findByText("Track 01");
    await user.click(historySummary());
    const table = await screen.findByRole("table", { name: "修改记录" });
    await user.click(within(table).getByRole("button", { name: /^撤销/ }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这次修改？" });
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const notice = await screen.findByText("已撤销这次修改，相关字段恢复到修改之前的状态。");
    expect(screen.getAllByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toHaveLength(1);
    // 提示与折叠面板同属“修改记录”区块，不在头部的操作区里。
    const header = screen.getByRole("button", { name: "编辑发行信息" }).closest("div");
    expect(header?.contains(notice)).toBe(false);
    await waitFor(() => expect(within(screen.getByRole("table", { name: "修改记录" })).getAllByRole("row")).toHaveLength(3));
    // 焦点回到标题行（触发撤销的按钮已经消失）。
    await waitFor(() => expect(document.activeElement).toBe(historySummary().closest("summary")));
  });

  it("撤销归组调整：页面回到原来的专辑，结果提示仍在页面上", async () => {
    const { governance } = setup();
    join(governance, releaseOf(albumA()), 1, albumB().id);
    const user = userEvent.setup();
    const { history } = renderApp({ path: `${detailPath(albumB())}?release=${releaseOf(albumA())}` });
    await heading("Sample Symphonies");
    await screen.findByText("Track 01");
    await user.click(historySummary());
    const table = await screen.findByRole("table", { name: "修改记录" });
    await user.click(within(table).getByRole("button", { name: /^撤销/ }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这次归组调整？" });
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(history.location.pathname).toBe(detailPath(albumA())));
    expect(await screen.findByText("已撤销这次归组调整，这个版本回到了原来的专辑。")).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "版本" })).toBeNull();
  });

  it("普通成员没有“修改记录”，也从不请求操作记录接口", async () => {
    const { api, governance } = setup({ role: "user" });
    correct(governance, releaseOf(albumC()), 1, "genre", "Sonata");
    renderApp({ path: detailPath(albumC()) });
    await heading("Gamma Sonatas");
    await screen.findByText("Track 01");
    expect(screen.queryByText("修改记录")).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/operations")).toHaveLength(0);
  });
});
