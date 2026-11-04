import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppRoot } from "./providers";
import { catalogKeys } from "../features/catalog/query-keys";
import { albumGroup, groupDetailOf, releaseDetailOf, releaseVersions } from "../test/catalog-data";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { FakeAudio } from "../test/fake-audio";
import { renderApp } from "../test/render-app";

beforeEach(() => {
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  sessionStorage.clear();
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const album = albumGroup(1, { release_count: 2, track_count: 3, medium_count: 2 });
  const versions = releaseVersions(album, 2);
  const other = versions[1];
  if (other === undefined) throw new Error("缺少第二个版本");
  const release = releaseDetailOf(album.representative);
  const target = release.media[1]?.tracks[0];
  if (target === undefined) throw new Error("缺少第二张介质的目标曲目");
  const catalog = installCatalogApi(api, { groups: [album] });
  catalog.groupDetails.set(album.id, groupDetailOf(album, versions));
  catalog.releases.set(release.id, release);
  catalog.releases.set(other.id, releaseDetailOf(other));
  const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
  const row = () => screen.getByText(target.title).closest("li");
  const locatedCalls = () => scroll.mock.instances.filter((element) => element === row());
  return { api, album, release, target, other, catalog, scroll, row, locatedCalls };
}

async function frames() {
  await act(async () => { await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))); });
}

describe("搜索进入专辑后的曲目定位", () => {
  it.each(["代表版本", "不属于本组的版本"])("%s地址规范化保留 track，只定位一次；数据刷新不再抢焦点", async (kind) => {
    const fixture = setup();
    const { album, release, target, catalog, row, locatedCalls, api } = fixture;
    const requested = kind === "代表版本" ? release.id : "00000000-0000-4000-8000-0000000000ff";
    const { history, runtime, view } = renderApp({ path: `/albums/${album.id}?release=${requested}&track=${target.id}` });
    // 等目录加载与地址规范化完成、定位请求落到行上，再经过真实动画帧。
    // 不把启动请求和 RAF 排队挤进同一个焦点轮询时限；聚焦与仅滚动一次仍严格断言。
    await waitFor(() => expect(row()?.hasAttribute("data-located")).toBe(true));
    await frames();
    expect(document.activeElement).toBe(row());
    expect(new URLSearchParams(history.location.search)).toEqual(new URLSearchParams({ track: target.id }));
    expect(row()?.hasAttribute("data-located")).toBe(true);
    expect(locatedCalls()).toHaveLength(1);
    const search = screen.getAllByRole("combobox", { name: "搜索" })[0];
    if (search === undefined) throw new Error("缺少搜索框");
    act(() => search.focus());
    const session = runtime.session.getSnapshot();
    if (session.status !== "authenticated") throw new Error("会话未就绪");
    catalog.releases.set(release.id, { ...structuredClone(release), genre: "刷新后的流派" });
    await act(async () => { await runtime.queryClient.invalidateQueries({ queryKey: catalogKeys.all(session.generation) }); });
    expect(await screen.findAllByText("刷新后的流派")).toHaveLength(2);
    await frames();
    expect(api.requestsTo("GET", `/api/v1/releases/${release.id}`)).toHaveLength(2);
    expect(locatedCalls()).toHaveLength(1);
    expect(document.activeElement).toBe(search);
    expect(new URLSearchParams(history.location.search).get("track")).toBe(target.id);
    // 明确覆盖同一个组件树的重绘，不依赖查询刷新恰好跳过提交。
    view.rerender(<AppRoot runtime={runtime} />);
    await frames();
    expect(locatedCalls()).toHaveLength(1);
    expect(document.activeElement).toBe(search);
  });

  it("目标不在所选版本中静默不定位；切换版本链接去掉 track", async () => {
    const { album, target, other, scroll } = setup();
    const { history } = renderApp({ path: `/albums/${album.id}?release=${other.id}&track=${target.id}` });
    const region = await screen.findByRole("region", { name: "曲目" });
    await within(region).findAllByRole("listitem");
    await frames();
    expect(document.querySelector("[data-located]")).toBeNull();
    expect(scroll).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(new URLSearchParams(history.location.search).get("track")).toBe(target.id);
    const nav = screen.getByRole("navigation", { name: "版本" });
    for (const link of within(nav).getAllByRole("link")) {
      const url = new URL(link.getAttribute("href") ?? "", "http://test");
      expect(url.searchParams.has("track")).toBe(false);
    }
    await userEvent.click(within(nav).getByRole("link", { name: /^版本 1 / }));
    await waitFor(() => expect(new URLSearchParams(history.location.search).has("track")).toBe(false));
    await screen.findByText(target.title);
    await frames();
    expect(document.querySelector("[data-located]")).toBeNull();
    expect(scroll).not.toHaveBeenCalled();
  });
});
