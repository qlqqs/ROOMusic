import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { albumGroup, groupDetailOf, releaseVersions } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// 被移动的专辑 A（1998）、两张可以归入的专辑 B（1990）与 C（2005）、一张有两个版本的专辑 D。
const albumA = () => albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, artwork: true });
const albumB = () => albumGroup(2, { title: "Beta Quartets", artist: "Beta Quartet", year: 1990, artwork: true });
const albumC = () => albumGroup(3, { title: "Gamma Sonatas", artist: "Gamma Duo", year: 2005 });
const albumD = () => albumGroup(4, { title: "Delta Concertos", artist: "Delta Ensemble", year: 1985, release_count: 2 });
const releaseOf = (group: ReturnType<typeof albumA>) => group.representative.id;

function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: [albumA(), albumB(), albumC(), albumD()] });
  const delta = albumD();
  catalog.groupDetails.set(delta.id, groupDetailOf(delta, releaseVersions(delta, 2)));
  const governance = installGovernanceApi(api, catalog);
  return { api, catalog, governance };
}

const groupPath = (releaseId: string) => `/api/v1/releases/${releaseId}/group`;
const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const openJoin = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(await screen.findByRole("button", { name: "归入其他专辑…" }));
  return screen.findByRole("dialog", { name: "把这个版本归入其他专辑" });
};
const search = async (user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, text: string) => {
  await user.type(within(dialog).getByRole("textbox", { name: "搜索专辑" }), `${text}{Enter}`);
};

describe("调整归组：入口", () => {
  it("管理员：单版本的专辑只有“归入其他专辑…”；多版本的专辑里选中的版本还有“移出为独立专辑”", async () => {
    setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    expect(await screen.findByRole("button", { name: "归入其他专辑…" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "移出为独立专辑" })).toBeNull();
  });

  it("多版本的专辑：有“移出为独立专辑”", async () => {
    setup();
    renderApp({ path: `/albums/${albumD().id}` });
    await heading("Delta Concertos");
    expect(await screen.findByRole("button", { name: "移出为独立专辑" })).toBeTruthy();
  });

  it("普通成员没有任何调整归组的入口，也不请求归组接口", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: `/albums/${albumD().id}` });
    await heading("Delta Concertos");
    await screen.findByText("Track 01");
    expect(screen.queryByRole("button", { name: "归入其他专辑…" })).toBeNull();
    expect(screen.queryByRole("button", { name: "移出为独立专辑" })).toBeNull();
    expect(api.requestsTo("POST", /\/group$/)).toHaveLength(0);
  });
});

describe("归入其他专辑", () => {
  it("搜索（不含它现在所在的专辑）、选中后预览、确认：请求带版本号与幂等键，页面跳到目标专辑并选中这个版本，提示里的“撤销”回到原来的专辑", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    expect(within(dialog).getByText("输入标题或艺人，找到要归入的专辑。")).toBeTruthy();
    const confirmButton = within(dialog).getByRole("button", { name: "归入这张专辑" });
    expect(confirmButton.hasAttribute("disabled")).toBe(true);

    // 搜索自己的标题：只有它现在所在的专辑，不能归入自己。
    await search(user, dialog, "Sample");
    expect(await within(dialog).findByText("没有找到可以归入的专辑。可以换个搜索词再试。")).toBeTruthy();

    // 搜索别的专辑：结果显示封面、标题、艺人与版本数，选中后预览。
    await user.clear(within(dialog).getByRole("textbox", { name: "搜索专辑" }));
    await search(user, dialog, "Quartet");
    const beta = await within(dialog).findByRole("radio", { name: /Beta Quartets/ });
    expect(within(dialog).queryByRole("radio", { name: /Gamma Sonatas/ })).toBeNull();
    expect(within(dialog).getByText(/Beta Quartet · 1990/)).toBeTruthy();
    await user.click(beta);
    expect(within(dialog).getByRole("status").textContent).toBe("归入后，这个版本会和《Beta Quartets》的 1 个版本放在一起，同属一张专辑。");
    await user.click(confirmButton);

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本归入其他专辑" })).toBeNull());
    const [request] = api.requestsTo("POST", groupPath(releaseOf(albumA())));
    expect(request?.body).toEqual({ expected_revision: 1, action: "join", group_id: albumB().id });
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

    // 页面跳到目标专辑，选中这个版本（B 年份更早，仍是代表版本，所以 release 参数保留）；提示留在页面上。
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${albumB().id}`));
    expect(new URLSearchParams(history.location.search).get("release")).toBe(releaseOf(albumA()));
    await heading("Sample Symphonies");
    expect(await screen.findByRole("navigation", { name: "版本" })).toBeTruthy();
    expect(screen.getByText("已把这个版本归入《Beta Quartets》。")).toBeTruthy();

    // 撤销：回到原来的专辑（页面导航回去），提示说明。
    await user.click(screen.getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${albumA().id}`));
    expect(await screen.findByText("已撤销归组，这个版本回到了原来的专辑。")).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "版本" })).toBeNull();
    expect(api.requestsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  it("搜索词为空或太长时说明原因，不发请求", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    const before = api.requestsTo("GET", "/api/v1/release-groups").length;
    await user.type(within(dialog).getByRole("textbox", { name: "搜索专辑" }), "   {Enter}");
    expect(within(dialog).getByText("请输入标题、艺人或曲目名。")).toBeTruthy();
    await user.clear(within(dialog).getByRole("textbox", { name: "搜索专辑" }));
    await user.click(within(dialog).getByRole("textbox", { name: "搜索专辑" }));
    await user.paste("古".repeat(70)); // 210 字节
    await user.keyboard("{Enter}");
    expect(within(dialog).getByText(/搜索词最多 200 个字节/)).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/release-groups")).toHaveLength(before);
  });

  it("搜索结果更多时提示输入更精确的搜索词；换搜索词会清除之前的选择", async () => {
    const { catalog } = setup();
    // 12 张都叫 Opus 的专辑：接口按 page_size 只给前 10 张，总数 12。
    catalog.groups.push(...Array.from({ length: 12 }, (_, index) => albumGroup(20 + index, { title: `Opus ${index + 1}`, artist: "Many" })));
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    await search(user, dialog, "Opus");
    await within(dialog).findByRole("radio", { name: /Opus 1 Many/ });
    expect(within(dialog).getAllByRole("radio")).toHaveLength(10);
    expect(within(dialog).getByText("只显示最相关的 10 张专辑；找不到的话，请输入更精确的搜索词。")).toBeTruthy();
    await user.click(within(dialog).getByRole("radio", { name: /Opus 1 Many/ }));
    expect(within(dialog).getByRole("status")).toBeTruthy();
    await user.clear(within(dialog).getByRole("textbox", { name: "搜索专辑" }));
    await search(user, dialog, "Gamma");
    await within(dialog).findByRole("radio", { name: /Gamma Sonatas/ });
    expect(within(dialog).queryByRole("status")).toBeNull();
    expect(within(dialog).getByRole("button", { name: "归入这张专辑" }).hasAttribute("disabled")).toBe(true);
  });

  it("目标专辑已不存在（404 group_not_found）：对话框保持打开并说明，结果刷新", async () => {
    const { api } = setup();
    api.on("POST", groupPath(releaseOf(albumA())), () => jsonResponse(404, { error: { code: "group_not_found", message: "目标专辑不存在" }, request_id: "r-1" }));
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    await search(user, dialog, "Quartet");
    await user.click(await within(dialog).findByRole("radio", { name: /Beta Quartets/ }));
    const before = api.requestsTo("GET", "/api/v1/release-groups").length;
    await user.click(within(dialog).getByRole("button", { name: "归入这张专辑" }));
    expect(await within(dialog).findByText("目标专辑已不存在，或者没有可见的版本。请重新搜索。")).toBeTruthy();
    expect(within(dialog).getByText("没有归入")).toBeTruthy();
    // 提交失败后搜索结果刷新（专辑数据已经失效重读）。
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/release-groups").length).toBeGreaterThan(before));
  });

  it("版本刚被修改过（409）：对话框保持打开，说明页面已刷新，可以再确认一次", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    await search(user, dialog, "Quartet");
    await user.click(await within(dialog).findByRole("radio", { name: /Beta Quartets/ }));
    // 这时另一位管理员修改了这个版本：版本号变成 2。
    const { respondToCorrect } = await import("../test/governance-data");
    respondToCorrect(governance, releaseOf(albumA()), "00000000-0000-4000-8000-0000000000c1", { expected_revision: 1, changes: { genre: { mode: "set", value: "Jazz" } } });
    await user.click(within(dialog).getByRole("button", { name: "归入这张专辑" }));
    expect(await within(dialog).findByText("这个版本刚被修改过")).toBeTruthy();
    expect(within(dialog).getByText("页面已刷新。请再确认一次要归入的专辑。")).toBeTruthy();
    // 页面读到新的版本号之后，再确认就成功了。
    await waitFor(() => expect(api.requestsTo("GET", `/api/v1/releases/${releaseOf(albumA())}`).length).toBeGreaterThan(1));
    await user.click(within(dialog).getByRole("button", { name: "归入这张专辑" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本归入其他专辑" })).toBeNull());
    const requests = api.requestsTo("POST", groupPath(releaseOf(albumA())));
    expect(requests.map((request) => (request.body as { expected_revision: number }).expected_revision)).toEqual([1, 2]);
  });

  it("结果不确定：对话框保持打开，“归入”变成“重试归入”，沿用同一个幂等键", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    await search(user, dialog, "Gamma");
    await user.click(await within(dialog).findByRole("radio", { name: /Gamma Sonatas/ }));
    api.on("POST", groupPath(releaseOf(albumA())), () => {
      throw new Error("connection reset");
    });
    await user.click(within(dialog).getByRole("button", { name: "归入这张专辑" }));
    expect(await within(dialog).findByText("结果未确认")).toBeTruthy();
    const { respondToGroupChange } = await import("../test/governance-data");
    api.on("POST", groupPath(releaseOf(albumA())), (request) => {
      const result = respondToGroupChange(governance, releaseOf(albumA()), request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user.click(within(dialog).getByRole("button", { name: "重试归入" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本归入其他专辑" })).toBeNull());
    const requests = api.requestsTo("POST", groupPath(releaseOf(albumA())));
    expect(requests).toHaveLength(2);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(screen.getByText("已把这个版本归入《Gamma Sonatas》。")).toBeTruthy();
  });

  it("取消：没有请求，焦点回到入口按钮；打开时焦点先在标题上（Esc 由 Playwright 覆盖）", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumA().id}` });
    await heading("Sample Symphonies");
    const dialog = await openJoin(user);
    expect(document.activeElement).toBe(within(dialog).getByRole("heading", { name: "把这个版本归入其他专辑" }));
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本归入其他专辑" })).toBeNull());
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "归入其他专辑…" }));
    expect(api.requestsTo("POST", /\/group$/)).toHaveLength(0);
  });
});

describe("移出为独立专辑", () => {
  it("确认后移出：请求只带版本号与动作，页面跳到新的专辑，原来的专辑少一个版本；提示里的“撤销”回到原来的专辑", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/albums/${albumD().id}` });
    await heading("Delta Concertos");
    await user.click(await screen.findByRole("button", { name: "移出为独立专辑" }));
    const dialog = await screen.findByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    // 确认对话框的初始焦点在“取消”上。
    expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));
    expect(within(dialog).getByText(/会从现在的专辑里分出来，成为单独的一张专辑/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "移出" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本移出为独立专辑？" })).toBeNull());
    const [request] = api.requestsTo("POST", /\/group$/);
    expect(request?.body).toEqual({ expected_revision: 1, action: "detach" });
    await waitFor(() => expect(history.location.pathname).not.toBe(`/albums/${albumD().id}`));
    expect(await screen.findByText("已把这个版本移出为独立专辑。")).toBeTruthy();
    // 新的专辑只有这一个版本：没有版本导航，也没有“移出”。
    expect(screen.queryByRole("navigation", { name: "版本" })).toBeNull();
    expect(screen.queryByRole("button", { name: "移出为独立专辑" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${albumD().id}`));
    expect(await screen.findByText("已撤销移出，这个版本回到了原来的专辑。")).toBeTruthy();
    expect(await screen.findByRole("navigation", { name: "版本" })).toBeTruthy();
  });

  it("结果不确定：确认对话框保持打开，“移出”变成“重试移出”，沿用同一个幂等键", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumD().id}` });
    await heading("Delta Concertos");
    await user.click(await screen.findByRole("button", { name: "移出为独立专辑" }));
    const dialog = await screen.findByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    const releaseId = albumD().representative.id;
    api.on("POST", groupPath(releaseId), () => {
      throw new Error("connection reset");
    });
    await user.click(within(dialog).getByRole("button", { name: "移出" }));
    expect(await within(dialog).findByText("结果未确认")).toBeTruthy();
    const { respondToGroupChange } = await import("../test/governance-data");
    api.on("POST", groupPath(releaseId), (request) => {
      const result = respondToGroupChange(governance, releaseId, request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user.click(within(dialog).getByRole("button", { name: "重试移出" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本移出为独立专辑？" })).toBeNull());
    const requests = api.requestsTo("POST", groupPath(releaseId));
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
  });

  it("版本刚被修改过（409）：对话框关闭，页面提示说明并已刷新", async () => {
    const { api } = setup();
    const releaseId = albumD().representative.id;
    api.on("POST", groupPath(releaseId), () => jsonResponse(409, { error: { code: "revision_conflict", message: "这张专辑刚被修改过，请刷新后再试" }, request_id: "r-2" }));
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumD().id}` });
    await heading("Delta Concertos");
    await user.click(await screen.findByRole("button", { name: "移出为独立专辑" }));
    const dialog = await screen.findByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    await user.click(within(dialog).getByRole("button", { name: "移出" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本移出为独立专辑？" })).toBeNull());
    expect(await screen.findByText("这张专辑刚被修改过")).toBeTruthy();
    expect(screen.getByText("页面已刷新。请核对当前内容后再重新操作。")).toBeTruthy();
  });

  it("取消不提交，焦点回到入口按钮", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${albumD().id}` });
    await heading("Delta Concertos");
    await user.click(await screen.findByRole("button", { name: "移出为独立专辑" }));
    const dialog = await screen.findByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "把这个版本移出为独立专辑？" })).toBeNull());
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "移出为独立专辑" }));
    expect(api.requestsTo("POST", /\/group$/)).toHaveLength(0);
  });
});
