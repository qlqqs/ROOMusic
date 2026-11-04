import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ReleaseSummaryDTO } from "../features/catalog/api/decoders";
import { albumGroup, groupDetailOf, releaseVersions, respondToGroupDetail } from "../test/catalog-data";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { respondToGroupDefault, respondToRevert, type GovernanceData } from "../test/governance-data";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

/**
 * 一张三个版本的专辑：自动规则下年份最早的版本 1（1998，3 首曲目）是代表版本，版本 2（1999，4 首）、版本 3（2000，5 首）；
 * 头部“曲目”的数量说明页面正在显示哪个版本。另有一张单版本的专辑。
 */
function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const album = albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, medium_count: 2, track_count: 3, release_count: 3, artwork: true });
  const single = albumGroup(2, { title: "Solo Preludes", artist: "Solo", year: 2001 });
  const catalog = installCatalogApi(api, { groups: [album, single] });
  const versions = releaseVersions(album, 3) as [ReleaseSummaryDTO, ReleaseSummaryDTO, ReleaseSummaryDTO];
  catalog.groupDetails.set(album.id, groupDetailOf(album, versions));
  const governance = installGovernanceApi(api, catalog);
  return { api, catalog, governance, album, single, versions };
}

type Context = ReturnType<typeof setup>;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
let keyCounter = 0;
const nextKey = () => `20000000-0000-4000-8000-${String((keyCounter += 1)).padStart(12, "0")}`;

const defaultPath = (groupId: string) => `/api/v1/release-groups/${groupId}/default`;
const revertPath = (operationId: string) => `/api/v1/operations/${operationId}/revert`;
const searchOf = (history: { location: { search: string } }) => new URLSearchParams(history.location.search);
const historySummary = () => screen.getByText("修改记录", { selector: "summary span" });

/** 头部概况里的“曲目”（dt 的下一个兄弟 dd）：每个版本的曲目数不同，据此知道页面显示的是哪个版本。 */
const shownTracks = () => screen.getAllByText("曲目", { selector: "dt" })[0]?.nextElementSibling?.textContent ?? null;
const onVersion = (tracks: string) => waitFor(() => expect(shownTracks()).toBe(tracks));
const navLinks = () => within(screen.getByRole("navigation", { name: "版本" })).getAllByRole("link").map((link) => link.textContent);

/** 种子：直接走替身的接口函数设定（或清除）默认版本，返回这次操作的 ID。 */
function seedDefault(governance: GovernanceData, groupId: string, revision: number, releaseId: string | null): string {
  const result = respondToGroupDefault(governance, groupId, nextKey(), { expected_revision: revision, release_id: releaseId });
  if (result.status !== 200) throw new Error(`种子设定失败：${JSON.stringify(result.body)}`);
  return (result.body as { operation_id: string }).operation_id;
}

const openAlbum = async (context: Context, release?: ReleaseSummaryDTO) => {
  const rendered = renderApp({ path: `/albums/${context.album.id}${release === undefined ? "" : `?release=${release.id}`}` });
  await screen.findByRole("heading", { level: 1, name: "Sample Symphonies" });
  await screen.findByRole("navigation", { name: "版本" });
  return rendered;
};

describe("设定默认版本：入口与权限", () => {
  it("管理员：多版本的专辑里，不是默认版本的版本有“设为默认版本”（自动选出的代表版本也有），默认版本是“改回自动选择”", async () => {
    const context = setup();
    await openAlbum(context);
    // 自动规则：版本 1 是代表版本，没有指定默认版本，仍然可以把它设为默认版本（钉住）。
    expect(await screen.findByRole("button", { name: "设为默认版本" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "改回自动选择" })).toBeNull();
  });

  it("默认版本上显示“改回自动选择”，别的版本上显示“设为默认版本”", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const { history } = await openAlbum(context);
    expect(shownTracks()).toBe("4 首"); // 默认版本现在是代表版本，页面先显示它
    expect(await screen.findByRole("button", { name: "改回自动选择" })).toBeTruthy();
    expect(searchOf(history).get("release")).toBeNull();
    // 去看别的版本：那里是“设为默认版本”。
    await userEvent.setup().click(screen.getByRole("link", { name: /^版本 2 / }));
    await onVersion("3 首");
    expect(await screen.findByRole("button", { name: "设为默认版本" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "改回自动选择" })).toBeNull();
  });

  it("普通成员没有任何入口，也不请求写接口；但看得到“默认版本”标签", async () => {
    const context = setup({ role: "user" });
    seedDefault(context.governance, context.album.id, 1, context.versions[2].id);
    await openAlbum(context);
    expect(navLinks()[0]).toBe("版本 1 默认版本 2000 · 2 张介质 · 5 首曲目");
    expect(screen.queryByRole("button", { name: "设为默认版本" })).toBeNull();
    expect(screen.queryByRole("button", { name: "改回自动选择" })).toBeNull();
    expect(context.api.requestsTo("POST", /\/default$/)).toHaveLength(0);
  });

  it("单版本的专辑没有这个按钮", async () => {
    const context = setup();
    renderApp({ path: `/albums/${context.single.id}` });
    await screen.findByRole("heading", { level: 1, name: "Solo Preludes" });
    await screen.findByRole("button", { name: "编辑发行信息" });
    expect(screen.queryByRole("button", { name: "设为默认版本" })).toBeNull();
    expect(screen.queryByRole("button", { name: "改回自动选择" })).toBeNull();
  });
});

describe("设定与清除默认版本", () => {
  it("设为默认版本：请求带专辑版本号与幂等键，页面停在这个版本，它成为代表版本并带标签，提示里有“撤销”", async () => {
    const context = setup();
    const user = userEvent.setup();
    const { history } = await openAlbum(context, context.versions[1]);
    await onVersion("4 首");
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));

    expect(await screen.findByText("已把这个版本设为默认版本。")).toBeTruthy();
    const [request] = context.api.requestsTo("POST", defaultPath(context.album.id));
    expect(request?.body).toEqual({ expected_revision: 1, release_id: context.versions[1].id });
    expect(request?.headers.get("Idempotency-Key")).toMatch(UUID);
    // 页面仍显示这个版本；它现在就是代表版本，地址不再带 release。
    expect(shownTracks()).toBe("4 首");
    await waitFor(() => expect(searchOf(history).get("release")).toBeNull());
    expect(navLinks()).toEqual(["版本 1 默认版本 1999 · 2 张介质 · 4 首曲目", "版本 2 1998 · 2 张介质 · 3 首曲目", "版本 3 2000 · 2 张介质 · 5 首曲目"]);
    expect(screen.getByRole("button", { name: "改回自动选择" })).toBeTruthy();
    // 专辑列表里的代表版本也换了。
    expect(context.catalog.groups.find((group) => group.id === context.album.id)?.representative.id).toBe(context.versions[1].id);
  });

  it("把自动选出的代表版本设为默认版本：以后归入更早的版本也不会把它挤下去", async () => {
    const context = setup();
    const user = userEvent.setup();
    await openAlbum(context);
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));
    expect(await screen.findByText(/已把这个版本设为默认版本/)).toBeTruthy();
    const [request] = context.api.requestsTo("POST", defaultPath(context.album.id));
    expect(request?.body).toEqual({ expected_revision: 1, release_id: context.versions[0].id });
    expect(navLinks()[0]).toBe("版本 1 默认版本 1998 · 2 张介质 · 3 首曲目");
  });

  it("改回自动选择：请求的 release_id 是 null，页面仍停在刚才的版本（它不再是代表版本，地址带上 release）", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const user = userEvent.setup();
    const { history } = await openAlbum(context);
    expect(shownTracks()).toBe("4 首");
    await user.click(await screen.findByRole("button", { name: "改回自动选择" }));

    expect(await screen.findByText("已改回自动选择：这张专辑默认显示年份最早的版本。")).toBeTruthy();
    const [request] = context.api.requestsTo("POST", defaultPath(context.album.id));
    expect(request?.body).toEqual({ expected_revision: 2, release_id: null });
    // 页面还在这个版本上，没有被带到新的代表版本。
    await waitFor(() => expect(searchOf(history).get("release")).toBe(context.versions[1].id));
    expect(shownTracks()).toBe("4 首");
    expect(navLinks()).toEqual(["版本 1 1998 · 2 张介质 · 3 首曲目", "版本 2 1999 · 2 张介质 · 4 首曲目", "版本 3 2000 · 2 张介质 · 5 首曲目"]);
    expect(screen.getByRole("button", { name: "设为默认版本" })).toBeTruthy();
  });

  it("改回自动选择时专辑还在刷新：地址不按旧数据规范化（旧数据里这个版本还是代表版本），刷新完成后页面仍在这个版本", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const user = userEvent.setup();
    const { history } = await openAlbum(context);
    expect(shownTracks()).toBe("4 首");
    // 让刷新里的组详情请求停在半路：命令成功后地址先被带到 ?release=<这个版本>，此时缓存里还是旧数据（它是代表版本）。
    const gate = deferred<void>();
    context.api.on("GET", `/api/v1/release-groups/${context.album.id}`, async () => {
      await gate.promise;
      const result = respondToGroupDetail(context.catalog, context.album.id);
      return jsonResponse(result.status, result.body);
    });
    await user.click(await screen.findByRole("button", { name: "改回自动选择" }));
    await waitFor(() => expect(searchOf(history).get("release")).toBe(context.versions[1].id));
    await new Promise((resolve) => setTimeout(resolve, 60));
    expect(searchOf(history).get("release")).toBe(context.versions[1].id);

    gate.resolve();
    expect(await screen.findByText("已改回自动选择：这张专辑默认显示年份最早的版本。")).toBeTruthy();
    await waitFor(() => expect(navLinks()[0]).toBe("版本 1 1998 · 2 张介质 · 3 首曲目"));
    expect(searchOf(history).get("release")).toBe(context.versions[1].id);
    expect(shownTracks()).toBe("4 首");
  });

  it("提示里的“撤销”恢复前一个设定，页面仍停在这个版本；撤销的提示没有“撤销”", async () => {
    const context = setup();
    const user = userEvent.setup();
    const { history } = await openAlbum(context, context.versions[1]);
    await onVersion("4 首");
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));
    await screen.findByText(/已把这个版本设为默认版本/);
    await user.click(screen.getByRole("button", { name: "撤销" }));

    expect(await screen.findByText("已撤销默认版本的调整。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "撤销" })).toBeNull();
    expect(context.api.requestsTo("POST", /\/revert$/)).toHaveLength(1);
    await waitFor(() => expect(searchOf(history).get("release")).toBe(context.versions[1].id));
    expect(shownTracks()).toBe("4 首");
    expect(navLinks()[0]).toBe("版本 1 1998 · 2 张介质 · 3 首曲目");
    expect(screen.getByRole("button", { name: "设为默认版本" })).toBeTruthy();
  });

  it("专辑刚被别人设定过（409）：提示说明并已刷新，再点一次用新的版本号成功", async () => {
    const context = setup();
    const user = userEvent.setup();
    await openAlbum(context, context.versions[1]);
    await onVersion("4 首");
    // 页面读到的专辑版本号是 1；在页面之外另一位管理员把版本 3 设为默认版本，版本号变成 2。
    seedDefault(context.governance, context.album.id, 1, context.versions[2].id);
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));

    expect(await screen.findByText("这张专辑刚被修改过")).toBeTruthy();
    expect(screen.getByText("页面已刷新。请核对当前内容后再重新操作。")).toBeTruthy();
    await waitFor(() => expect(navLinks()[0]).toBe("版本 1 默认版本 2000 · 2 张介质 · 5 首曲目"));
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));
    expect(await screen.findByText(/已把这个版本设为默认版本/)).toBeTruthy();
    expect(context.api.requestsTo("POST", defaultPath(context.album.id)).map((request) => (request.body as { expected_revision: number }).expected_revision)).toEqual([1, 2]);
  });

  it("结果不确定：提示“结果未确认”，“重试”沿用同一个幂等键，重放得到第一次的结果", async () => {
    const context = setup();
    const user = userEvent.setup();
    await openAlbum(context, context.versions[1]);
    await onVersion("4 首");
    context.api.on("POST", defaultPath(context.album.id), () => {
      throw new Error("connection reset");
    });
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));
    expect(await screen.findByText("结果未确认")).toBeTruthy();
    context.api.on("POST", defaultPath(context.album.id), (request) => {
      const result = respondToGroupDefault(context.governance, context.album.id, request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText(/已把这个版本设为默认版本/)).toBeTruthy();
    const requests = context.api.requestsTo("POST", defaultPath(context.album.id));
    expect(requests).toHaveLength(2);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(context.governance.operations).toHaveLength(1);
  });

  it("版本已不在这张专辑里（409 not_in_group）：说明原因，页面已刷新", async () => {
    const context = setup();
    const user = userEvent.setup();
    await openAlbum(context, context.versions[1]);
    await onVersion("4 首");
    context.api.on("POST", defaultPath(context.album.id), () => jsonResponse(409, { error: { code: "not_in_group", message: "这个版本不在这张专辑里，或现在不可见" }, request_id: "r-default" }));
    await user.click(await screen.findByRole("button", { name: "设为默认版本" }));
    expect(await screen.findByText("这个版本已经不在这张专辑里了，页面已刷新。")).toBeTruthy();
  });
});

describe("移出默认版本", () => {
  it("要移出的是默认版本：确认对话框说明移出后自动另选版本；撤销移出后它重新是默认版本", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const user = userEvent.setup();
    const { history } = await openAlbum(context);
    expect(shownTracks()).toBe("4 首");
    await user.click(await screen.findByRole("button", { name: "移出为独立专辑" }));
    const dialog = await screen.findByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    expect(within(dialog).getByText("它是这张专辑的默认版本，移出后会自动另选一个。")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "移出" }));
    await screen.findByText("已把这个版本移出为独立专辑。");

    // 原来的专辑按自动规则显示：版本 1（1998）又是代表版本，已存的默认版本失效但没有被清除。
    expect(context.catalog.groupDetails.get(context.album.id)?.default_release_id).toBeNull();
    expect(context.catalog.groupDetails.get(context.album.id)?.representative.id).toBe(context.versions[0].id);
    expect(context.governance.groups.get(context.album.id)?.defaultReleaseId).toBe(context.versions[1].id);

    await user.click(screen.getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${context.album.id}`));
    await screen.findByText("已撤销移出，这个版本回到了原来的专辑。");
    await waitFor(() => expect(navLinks()[0]).toBe("版本 1 默认版本 1999 · 2 张介质 · 4 首曲目"));
  });

  it("要移出的不是默认版本：对话框里没有这段说明", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const user = userEvent.setup();
    await openAlbum(context, context.versions[2]);
    await onVersion("5 首");
    await user.click(await screen.findByRole("button", { name: "移出为独立专辑" }));
    const dialog = await screen.findByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    expect(within(dialog).queryByText(/它是这张专辑的默认版本/)).toBeNull();
  });
});

describe("修改记录与操作记录里的默认版本", () => {
  it("“修改记录”同时列出这个版本和这张专辑的记录：专辑级记录写“默认版本：自动选择 → 《标题》（年份 · 版本说明）”，可以从那里撤销，页面仍停在当前版本", async () => {
    const context = setup();
    const operationId = seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const user = userEvent.setup();
    const { history } = await openAlbum(context, context.versions[2]);
    await onVersion("5 首");
    await user.click(historySummary());
    const table = await screen.findByRole("table", { name: "修改记录" });
    const [request] = context.api.requestsTo("GET", "/api/v1/operations");
    expect(request?.path).toContain(`release_id=${context.versions[2].id}`);
    expect(request?.path).toContain(`group_id=${context.album.id}`);
    expect(within(table).getByText("设定默认版本")).toBeTruthy();
    expect(within(table).getByText("默认版本：自动选择 → 《Sample Symphonies》（1999 · Remastered）")).toBeTruthy();
    expect(screen.getByRole("link", { name: "全部记录" }).getAttribute("href")).toBe(`/operations?release=${context.versions[2].id}&group=${context.album.id}`);

    await user.click(within(table).getByRole("button", { name: /^撤销/ }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这次默认版本调整？" });
    expect(within(dialog).getByText("默认版本：《Sample Symphonies》（1999 · Remastered） → 自动选择")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "撤销这次默认版本调整？" })).toBeNull());
    expect(context.api.requestsTo("POST", revertPath(operationId))).toHaveLength(1);
    expect(await screen.findByText("已撤销默认版本的调整。")).toBeTruthy();
    // 页面仍显示刚才的版本，专辑回到自动选择。
    await waitFor(() => expect(searchOf(history).get("release")).toBe(context.versions[2].id));
    expect(shownTracks()).toBe("5 首");
    expect(navLinks()[0]).toBe("版本 1 1998 · 2 张介质 · 3 首曲目");
  });

  it("操作记录页：专辑列链接到专辑，写出类型与改动，可以撤销，撤销之后留在记录页", async () => {
    const context = setup();
    const operationId = seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/operations" });
    await screen.findByRole("heading", { level: 1, name: "操作记录" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).getByText("设定默认版本")).toBeTruthy();
    expect(within(table).getByText("默认版本：自动选择 → 《Sample Symphonies》（1999 · Remastered）")).toBeTruthy();
    expect(within(table).getByRole("link", { name: "Sample Symphonies" }).getAttribute("href")).toBe(`/albums/${context.album.id}`);

    await user.click(within(table).getByRole("button", { name: /^撤销/ }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这次默认版本调整？" });
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销默认版本的调整。")).toBeTruthy();
    expect(history.location.pathname).toBe("/operations");
    expect(context.api.requestsTo("POST", revertPath(operationId))).toHaveLength(1);
    const rows = within(await screen.findByRole("table", { name: "操作记录" })).getAllByRole("row");
    expect(rows).toHaveLength(3); // 表头、撤销记录、被撤销的记录
    expect(within(rows[1] as HTMLElement).getByText("撤销默认版本调整")).toBeTruthy();
    expect(within(rows[2] as HTMLElement).getByText("已撤销")).toBeTruthy();
  });

  it("专辑之后又有调整时，更早的那条记录不能撤销，并写出原因", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    seedDefault(context.governance, context.album.id, 2, null);
    renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    // 最新的一条（改回自动选择）可以撤销；更早的一条写“之后又有修改”。
    expect(within(rows[0] as HTMLElement).getByRole("button", { name: /^撤销/ })).toBeTruthy();
    expect(within(rows[1] as HTMLElement).queryByRole("button", { name: /^撤销/ })).toBeNull();
    expect(within(rows[1] as HTMLElement).getByText("之后又有修改")).toBeTruthy();
  });

  it("按专辑筛选的记录页：说明在看这张专辑的记录并请求 group_id；与版本一起筛选时说明是这个版本和它所在的专辑", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const first = renderApp({ path: `/operations?group=${context.album.id}` });
    expect(await screen.findByText("只显示这张专辑的记录。")).toBeTruthy();
    await screen.findByRole("table", { name: "操作记录" });
    expect(context.api.requestsTo("GET", "/api/v1/operations")[0]?.path).toContain(`group_id=${context.album.id}`);
    expect(screen.getByRole("heading", { level: 2, name: "这张专辑的记录" })).toBeTruthy();
    first.view.unmount();

    renderApp({ path: `/operations?release=${context.versions[1].id}&group=${context.album.id}` });
    expect(await screen.findByText("只显示这个版本和它所在的专辑的记录。")).toBeTruthy();
    expect(await screen.findByRole("heading", { level: 2, name: "这个版本和专辑的记录" })).toBeTruthy();
  });

  it("专辑已不可见（没有可见版本）的默认版本记录：只写标题加“（已不可见）”，没有链接，不能撤销", async () => {
    const context = setup();
    seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    // 专辑里的版本都被重新扫描掉了：组详情没有了。
    context.catalog.groups.splice(0, 1);
    context.catalog.groupDetails.delete(context.album.id);
    renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).queryByRole("link", { name: /Sample Symphonies/ })).toBeNull();
    expect(within(table).getByText("（已不可见）")).toBeTruthy();
    expect(within(table).getByText("专辑已不可见")).toBeTruthy();
    expect(within(table).queryByRole("button", { name: /^撤销/ })).toBeNull();
  });
});

describe("撤销之间的串行", () => {
  it("撤销默认版本的调整之后，更早的记录仍然不能撤销（撤销记录自己不能再撤销）", async () => {
    const context = setup();
    const first = seedDefault(context.governance, context.album.id, 1, context.versions[1].id);
    const result = respondToRevert(context.governance, first, nextKey());
    expect(result.status).toBe(200);
    renderApp({ path: "/operations" });
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).queryByRole("button", { name: /^撤销/ })).toBeNull();
    expect(within(table).getByText("撤销不能再撤销")).toBeTruthy();
    expect(within(table).getByText("已撤销")).toBeTruthy();
  });
});
