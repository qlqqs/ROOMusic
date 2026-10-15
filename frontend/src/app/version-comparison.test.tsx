import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ReleaseSummaryDTO } from "../features/catalog/api/decoders";
import { albumGroup, groupDetailOf, mediaOf, releaseDetailOf, releaseVersions } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const comparisonRequests = (api: ReturnType<typeof setup>["api"]) => api.requestsTo("GET", /^\/api\/v1\/release-groups\/[^/]+\/comparison$/);

/**
 * 一张三个版本的专辑：版本 1（1998，2 张介质 3 首曲目，FLAC 16 bit / 44.1 kHz）、版本 2（2005，24 bit / 96 kHz 的 ALAC，有版本说明）、
 * 版本 3（2010，没有年份，厂牌被人工修正，有一首曲目时长未知）。defaultFirst 为真时第一个版本就是生效中的默认版本。
 */
function setup(options: { role?: "admin" | "user"; defaultFirst?: boolean } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const album = albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, medium_count: 2, track_count: 3, release_count: 3, artwork: true });
  const catalog = installCatalogApi(api, { groups: [album] });
  const [first, second, third] = releaseVersions(album, 3, (version) => (version === 2 ? { year: 2005, track_count: 4 } : { year: null, track_count: 2, medium_count: 1 })) as [ReleaseSummaryDTO, ReleaseSummaryDTO, ReleaseSummaryDTO];
  catalog.groupDetails.set(album.id, { ...groupDetailOf(album, [first, second, third]), default_release_id: options.defaultFirst === true ? first.id : null });
  catalog.releases.set(second.id, releaseDetailOf(second, { edition: "Hi-Res", label: "Sample Label", media: mediaOf(second, () => ({ codec: "alac", bit_depth: 24, sample_rate: 96000 })) }));
  catalog.releases.set(
    third.id,
    releaseDetailOf(third, {
      label: "Corrected Label",
      corrected_fields: ["label"],
      media: mediaOf(third, (_medium, track) => (track === 1 ? { duration_seconds: null } : {})),
    }),
  );
  return { api, catalog, album, versions: [first, second, third] as const };
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const openDialog = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(await screen.findByRole("button", { name: "对比版本" }));
  return screen.findByRole("dialog", { name: "对比版本" });
};
/** 对比表里一行的各格（不含行标题）。 */
const cellsOf = (dialog: HTMLElement, label: string) => {
  const rowHeader = within(dialog).getByRole("rowheader", { name: label });
  return Array.from(rowHeader.parentElement?.querySelectorAll("td") ?? []);
};
const textOf = (cell: Element | undefined) => cell?.textContent ?? "";

describe("版本导航：默认版本标签", () => {
  it("没有设定默认版本时，任何版本都没有“默认版本”标签", async () => {
    const { album } = setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const nav = await screen.findByRole("navigation", { name: "版本" });
    expect(within(nav).queryByText("默认版本")).toBeNull();
    expect(within(nav).getAllByRole("link").map((link) => link.textContent)).toEqual([
      "版本 1 1998 · 2 张介质 · 3 首曲目",
      "版本 2 2005 · 2 张介质 · 4 首曲目",
      "版本 3 2 首曲目",
    ]);
  });

  it.each(["admin", "user"] as const)("生效中的默认版本带标签，只有它带（%s 都看得到）", async (role) => {
    const { album } = setup({ role, defaultFirst: true });
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const nav = await screen.findByRole("navigation", { name: "版本" });
    expect(within(nav).getAllByText("默认版本")).toHaveLength(1);
    expect(within(nav).getAllByRole("link").map((link) => link.textContent)).toEqual([
      "版本 1 默认版本 1998 · 2 张介质 · 3 首曲目",
      "版本 2 2005 · 2 张介质 · 4 首曲目",
      "版本 3 2 首曲目",
    ]);
  });
});

describe("版本对比：入口", () => {
  it.each(["admin", "user"] as const)("多版本的专辑：%s 都有“对比版本”，打开前不请求对比数据", async (role) => {
    const { api, album } = setup({ role });
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const button = await screen.findByRole("button", { name: "对比版本" });
    expect(button.closest("nav")?.getAttribute("aria-label")).toBe("版本");
    expect(comparisonRequests(api)).toHaveLength(0);
    await userEvent.setup().click(button);
    await screen.findByRole("dialog", { name: "对比版本" });
    expect(comparisonRequests(api)).toHaveLength(1);
  });

  it("单版本的专辑没有“对比版本”，也没有版本导航", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    const album = albumGroup(1, { title: "Single", artist: "Solo" });
    installCatalogApi(api, { groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await heading("Single");
    await screen.findByText("Track 01");
    expect(screen.queryByRole("button", { name: "对比版本" })).toBeNull();
    expect(comparisonRequests(api)).toHaveLength(0);
  });
});

describe("版本对比：对话框", () => {
  it("表格以版本 1 为基准：差异格有标记与读屏文字，没有值写“—”，人工修正有标记，音频格式写具体参数", async () => {
    const { album } = setup({ defaultFirst: true });
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    const table = await within(dialog).findByRole("table", { name: "各个版本的对比" });

    // 列头：版本名；默认版本带标签；每列一个“查看这个版本”。
    const headers = within(table).getAllByRole("columnheader").map((header) => header.textContent);
    expect(headers).toEqual(["项目", "版本 1 默认版本查看这个版本（版本 1）", "版本 2查看这个版本（版本 2）", "版本 3查看这个版本（版本 3）"]);
    expect(within(dialog).getByText(/以 版本 1 为基准/)).toBeTruthy();
    expect(within(dialog).getByText(/版本 1 是这张专辑的默认版本/)).toBeTruthy();

    // 行：标题与专辑艺人各版本相同，不出现；介质类型与来源类型都没有值，整行隐藏。
    const rowNames = within(table).getAllByRole("rowheader").map((row) => row.textContent);
    expect(rowNames).toEqual(["年份", "版本说明", "厂牌", "编号", "条码", "流派", "介质与曲目", "总时长", "音频格式"]);

    // 年份：版本 3 没有年份；与基准不同的格带读屏文字，基准列自己没有。
    const year = cellsOf(dialog, "年份");
    expect(year.map(textOf)).toEqual(["1998", "2005（与版本 1 不同）", "—没有记录（与版本 1 不同）"]);
    expect(year.map((cell) => cell.querySelector("[data-differs]") !== null)).toEqual([false, true, true]);

    // 版本说明：基准列有值（样本里的 Remastered），版本 2 不同，版本 3 与基准相同。
    expect(cellsOf(dialog, "版本说明").map(textOf)).toEqual(["Remastered", "Hi-Res（与版本 1 不同）", "Remastered"]);
    // 厂牌：版本 3 的值是人工修正的，带“人工修正”标记。
    const label = cellsOf(dialog, "厂牌");
    expect(label.map(textOf)).toEqual(["Sample Label", "Sample Label", "Corrected Label 人工修正（与版本 1 不同）"]);
    expect(within(label[2] as HTMLElement).getByText("人工修正")).toBeTruthy();
    expect(within(label[0] as HTMLElement).queryByText("人工修正")).toBeNull();

    // 介质与曲目、总时长（版本 3 有一首曲目时长未知，所以写“—”而不是已知部分之和）、音频格式。
    expect(cellsOf(dialog, "介质与曲目").map(textOf)).toEqual(["2 张介质 · 3 首曲目", "2 张介质 · 4 首曲目（与版本 1 不同）", "1 张介质 · 2 首曲目（与版本 1 不同）"]);
    const duration = cellsOf(dialog, "总时长").map(textOf);
    expect(duration[0]).toMatch(/^\d+:\d{2}$/);
    expect(duration[2]).toBe("—没有记录（与版本 1 不同）");
    expect(cellsOf(dialog, "音频格式").map(textOf)).toEqual(["FLAC · 16 bit / 44.1 kHz", "ALAC · 24 bit / 96 kHz（与版本 1 不同）", "FLAC · 16 bit / 44.1 kHz"]);

    // 不做任何质量结论。
    expect(dialog.textContent).not.toMatch(/最高|更好|最佳|高解析|音质/);
  });

  it("自动选择时对话框里没有“默认版本”标签与说明", async () => {
    const { album } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    await within(dialog).findByRole("table", { name: "各个版本的对比" });
    expect(within(dialog).queryByText("默认版本")).toBeNull();
    expect(within(dialog).queryByText(/是这张专辑的默认版本/)).toBeNull();
  });

  it("各版本的标题或专辑艺人不同时才出现这两行", async () => {
    const { album, catalog, versions } = setup();
    catalog.releases.set(versions[1].id, releaseDetailOf(versions[1], { edition: "Hi-Res" }));
    catalog.groupDetails.set(album.id, groupDetailOf(album, [versions[0], { ...versions[1], title: "Sample Symphonies (Remastered)" }, versions[2]]));
    catalog.releases.set(versions[1].id, { ...releaseDetailOf({ ...versions[1], title: "Sample Symphonies (Remastered)" }), title: "Sample Symphonies (Remastered)" });
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    await within(dialog).findByRole("table", { name: "各个版本的对比" });
    expect(cellsOf(dialog, "标题").map(textOf)).toEqual(["Sample Symphonies", "Sample Symphonies (Remastered)（与版本 1 不同）", "Sample Symphonies"]);
  });

  it("列头的“查看这个版本”：关闭对话框并切换到该版本，焦点回到“对比版本”；第一个版本的链接不带 release", async () => {
    const { album, versions } = setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    const links = await within(dialog).findAllByRole("link", { name: /^查看这个版本/ });
    expect(links.map((link) => link.getAttribute("href"))).toEqual([`/albums/${album.id}`, `/albums/${album.id}?release=${versions[1].id}`, `/albums/${album.id}?release=${versions[2].id}`]);

    await user.click(within(dialog).getByRole("link", { name: "查看这个版本（版本 2）" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "对比版本" })).toBeNull());
    await waitFor(() => expect(new URLSearchParams(history.location.search).get("release")).toBe(versions[1].id));
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "对比版本" }));
    await waitFor(() => expect(screen.getByRole("link", { name: /^版本 2 / }).getAttribute("aria-current")).toBe("page"));
  });

  it("“关闭”关闭对话框，焦点回到“对比版本”；再打开仍是同一份内容", async () => {
    const { album } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    await within(dialog).findByRole("table", { name: "各个版本的对比" });
    // 打开后焦点在对话框标题上（稳定的落点）。
    expect(document.activeElement).toBe(within(dialog).getByRole("heading", { level: 2, name: "对比版本" }));
    await user.click(within(dialog).getByRole("button", { name: "关闭" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "对比版本" })).toBeNull());
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "对比版本" }));
    const again = await openDialog(user);
    expect(await within(again).findByRole("table", { name: "各个版本的对比" })).toBeTruthy();
  });

  it("读取失败：显示带请求 ID 的错误和重试，重试后显示对比", async () => {
    const { catalog, album } = setup();
    // 500 不会被查询自动重试，错误立刻显示。
    catalog.comparisonFailure = jsonResponse(500, { error: { code: "internal_error", message: "服务器内部错误" }, request_id: "req-compare" });
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    const alert = await within(dialog).findByRole("alert");
    expect(alert.textContent).toContain("req-compare");
    catalog.comparisonFailure = null;
    await user.click(within(dialog).getByRole("button", { name: "重试" }));
    expect(await within(dialog).findByRole("table", { name: "各个版本的对比" })).toBeTruthy();
    expect(within(dialog).queryByRole("alert")).toBeNull();
  });

  it("打开时专辑只剩一个可见版本（刚被重新扫描）：如实说明，没有表格", async () => {
    const { catalog, album, versions } = setup();
    catalog.hiddenReleases.add(versions[1].id);
    catalog.hiddenReleases.add(versions[2].id);
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    const dialog = await openDialog(user);
    expect(await within(dialog).findByText("现在只有一个可见版本")).toBeTruthy();
    expect(within(dialog).queryByRole("table")).toBeNull();
  });

  it("版本超过 100 个时说明只比较前 100 个", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    const album = albumGroup(1, { title: "Many Versions", artist: "Collector", release_count: 120 });
    const catalog = installCatalogApi(api, { groups: [album] });
    catalog.groupDetails.set(album.id, groupDetailOf(album, releaseVersions(album, 100)));
    const user = userEvent.setup();
    renderApp({ path: `/albums/${album.id}` });
    await heading("Many Versions");
    const dialog = await openDialog(user);
    expect(await within(dialog).findByText("共 120 个版本，这里只比较前 100 个。")).toBeTruthy();
    expect(within(dialog).getAllByRole("columnheader")).toHaveLength(101);
  });
});
