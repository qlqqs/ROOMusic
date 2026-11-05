import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { artistDataOf, artistTestId, primaryArtistId, seedArtist } from "../test/artist-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { readyCatalogState } from "../test/search-data";
import { deferred } from "../test/deferred";

import { FakeAudio } from "../test/fake-audio";

beforeEach(() => { vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } }); });
const id = primaryArtistId();
function setup() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "curator" });
  const data = installArtistsApi(api, artistDataOf(), session);
  return { api, data };
}
const getDetail = (data: ReturnType<typeof artistDataOf>) => {
  const detail = data.details.get(id);
  if (!detail) throw new Error("缺少主艺人样本");
  return detail;
};

describe("艺人浏览与详情（⑤）", () => {
  it("详情整理中只显示一句提示，不展示404或残缺详情", async () => {
    const { data } = setup();
    data.state = "preparing";
    const { runtime } = renderApp({ path: `/artists/${id}` });
    runtime.queryClient.setDefaultOptions({ queries: { retry: false } });
    expect(await screen.findByText("艺人信息正在整理，请稍后再试。")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "艺人不存在" })).toBeNull();
    expect(screen.queryByRole("button", { name: "播放全部" })).toBeNull();
  });

  it("名字排序按后端首字母分组，数字为全角色计数；切换专辑数排序不分组", async () => {
    const { data } = setup();
    const base = getDetail(data).artist;
    seedArtist(data, { ...base, id: artistTestId(1), name: "かな", initial_group: "kana" });
    seedArtist(data, { ...base, id: artistTestId(2), name: "#未知", initial_group: "#" });
    const { history } = renderApp({ path: "/artists" });
    expect(await screen.findByRole("region", { name: "D" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "假名" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "#" })).toBeTruthy();
    expect(screen.getByText("9 位艺人")).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "全部艺人" })).getAllByRole("heading", { level: 3 }).map((heading) => heading.textContent)).toEqual(["D", "T", "Y", "Z", "假名", "#"]);
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "排序" }), "album_count");
    await waitFor(() => expect(history.location.search).toContain("sort=album_count"));
    expect(screen.queryByRole("navigation", { name: "首字母" })).toBeNull();
    expect(screen.queryByRole("region", { name: "D" })).toBeNull();
  });

  it("I6：双专辑级角色摘要去重，角色下拉只有一个专辑艺人且提交 album_artist", async () => {
    const { api, data } = setup();
    getDetail(data).artist.roles = ["release_artist", "album_artist", "composer"];
    const { history } = renderApp({ path: "/artists" });
    const artistLink = await screen.findByRole("link", { name: "邓丽君" });
    const item = artistLink.closest("li");
    if (item === null) throw new Error("缺少艺人列表项");
    const summary = within(item).getByText(/专辑艺人/);
    expect(summary.textContent?.match(/专辑艺人/g)).toHaveLength(1);
    expect(summary.textContent).toMatch(/专辑艺人、作曲$/);
    const roles = screen.getByRole("combobox", { name: "角色" });
    const options = within(roles).getAllByRole("option", { name: "专辑艺人" });
    expect(options).toHaveLength(1);
    expect(options[0]?.getAttribute("value")).toBe("album_artist");
    expect(within(roles).queryByRole("option", { name: "艺人" })).toBeNull();
    expect(within(roles).getAllByRole("option").map((option) => option.getAttribute("value"))).not.toContain("release_artist");
    await userEvent.selectOptions(roles, "album_artist");
    await waitFor(() => expect(new URLSearchParams(history.location.search).get("role")).toBe("album_artist"));
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(2));
    expect(new URL(api.requestsTo("GET", "/api/v1/artists").at(-1)?.path ?? "", "http://test").searchParams.get("role")).toBe("album_artist");
    expect(await screen.findByRole("region", { name: "担任专辑艺人的艺人" })).toBeTruthy();
  });

  it("I6：旧 URL 的 release_artist 保持合法并原样请求，筛选显示专辑艺人", async () => {
    const { api, data } = setup();
    getDetail(data).artist.roles = ["release_artist"];
    const { history } = renderApp({ path: "/artists?role=release_artist" });
    await screen.findByRole("link", { name: "邓丽君" });
    expect(new URLSearchParams(history.location.search).get("role")).toBe("release_artist");
    expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(1);
    expect(new URL(api.requestsTo("GET", "/api/v1/artists")[0]?.path ?? "", "http://test").searchParams.get("role")).toBe("release_artist");
    const roles = screen.getByRole<HTMLSelectElement>("combobox", { name: "角色" });
    expect(roles.value).toBe("album_artist");
    expect(within(roles).getAllByRole("option", { name: "专辑艺人" })).toHaveLength(1);
    expect(within(roles).getByRole<HTMLOptionElement>("option", { name: "专辑艺人" }).selected).toBe(true);
    expect(screen.getByRole("region", { name: "担任专辑艺人的艺人" })).toBeTruthy();
  });

  it.each(["首字母", "角色", "查找", "排序"] as const)("在有效第2页改变%s，回到第1页且请求保留相应筛选", async (filter) => {
    const { api, data } = setup();
    const detail = getDetail(data);
    detail.artist.roles.push("composer");
    detail.artist.album_count = 12;
    // 第1页有50项，第2页确实有结果，不能靠越界规范化伪装筛选重置成功。
    for (let i = 0; i < 60; i++) seedArtist(data, { ...detail.artist, id: artistTestId(1000 + i), name: `A作曲者${String(i).padStart(2, "0")}`, initial_group: "A", album_count: 1 });
    const { history } = renderApp({ path: "/artists?role=composer&page=2" });
    await screen.findByRole("link", { name: "邓丽君" });
    expect(history.location.search).toContain("page=2");
    expect(screen.getByText("第 2 / 2 页")).toBeTruthy();
    expect(screen.getByText(/12 张专辑/)).toBeTruthy();
    expect(new URL(api.requestsTo("GET", "/api/v1/artists")[0]?.path ?? "", "http://test").searchParams.get("page")).toBe("2");
    const user = userEvent.setup();
    switch (filter) {
      case "首字母": await user.click(within(screen.getByRole("navigation", { name: "首字母" })).getByRole("link", { name: "D" })); break;
      case "角色": await user.selectOptions(screen.getByRole("combobox", { name: "角色" }), "performer"); break;
      case "查找": await user.type(screen.getByRole("textbox", { name: "查找艺人" }), "邓丽君"); await user.click(screen.getByRole("button", { name: "查找" })); break;
      case "排序": await user.selectOptions(screen.getByRole("combobox", { name: "排序" }), "album_count"); break;
    }
    await waitFor(() => expect(new URLSearchParams(history.location.search).has("page")).toBe(false));
    await screen.findByRole("link", { name: filter === "角色" ? "演奏者" : "邓丽君" });
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(2));
    const params = new URL(api.requestsTo("GET", "/api/v1/artists").at(-1)?.path ?? "", "http://test").searchParams;
    expect(params.get("page")).toBe("1");
    expect(params.get("role")).toBe(filter === "角色" ? "performer" : "composer");
    if (filter === "首字母") expect(params.get("initial")).toBe("D");
    if (filter === "查找") expect(params.get("q")).toBe("邓丽君");
    if (filter === "排序") expect(params.get("sort")).toBe("album_count");
  });

  it("越界列表用 replace 到末页，返回键不回到越界页", async () => {
    const { api } = setup();
    const { history } = renderApp({ path: "/artists?page=99" });
    // 初次进入与越界替换是两次路由阶段，分别等待；不把会话、两次读取和渲染挤进一次全页链接查找。
    await screen.findByRole("heading", { name: "艺人", level: 1 });
    await waitFor(() => expect(history.location.search).toBe(""));
    const results = screen.getByRole("region", { name: "全部艺人" });
    expect(await within(results).findByRole("link", { name: "邓丽君" })).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/artists").map((request) => new URL(request.path, "http://test").searchParams.get("page"))).toEqual(["99", "1"]);
    expect(history.location.search).toBe("");
    expect(history.length).toBe(1);
  });

  it("尚未读到目录状态不请求列表；整理中只显示一句而非空列表", async () => {
    const { api, data } = setup();
    const gate = deferred<void>();
    data.state = "preparing";
    api.on("GET", "/api/v1/catalog/status", async () => { await gate.promise; return jsonResponse(200, { ...readyCatalogState(), artists_state: "preparing" }); });
    const { runtime } = renderApp({ path: "/artists" });
    runtime.queryClient.setDefaultOptions({ queries: { retry: false } });
    await screen.findByRole("heading", { name: "艺人", level: 1 });
    expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(0);
    await act(async () => gate.resolve());
    expect(await screen.findByText("艺人信息正在整理，请稍后再试。")).toBeTruthy();
    expect(screen.queryByText("资料库里还没有艺人。")).toBeNull();
  });

  it("旧身份详情用 replace 导航现在的身份，丢弃旧身份分页", async () => {
    const { data } = setup();
    const oldId = artistTestId(3);
    seedArtist(data, { ...getDetail(data).artist, id: oldId, name: "旧名字" }, { redirect_artist_id: id });
    const { history } = renderApp({ path: `/artists/${oldId}?names_page=2` });
    await screen.findByRole("heading", { level: 1, name: "邓丽君" });
    expect(history.location.pathname).toBe(`/artists/${id}`);
    expect(history.location.search).toBe("");
    expect(history.length).toBe(1);
  });

  it.each(["not-a-uuid", artistTestId(999)])("非法或不存在的身份 %s 显示404，不误作整理", async (artistId) => {
    const { api } = setup();
    renderApp({ path: `/artists/${artistId}` });
    expect(await screen.findByRole("heading", { name: "艺人不存在" })).toBeTruthy();
    if (artistId === "not-a-uuid") expect(api.requestsTo("GET", `/api/v1/artists/${artistId}`)).toHaveLength(0);
  });

  it("超过20个名字先展开再分页；专辑链接指定真实参与的非代表版本，卡内没有艺人嵌套链接", async () => {
    const { data, api } = setup();
    data.names.set(id, Array.from({ length: 51 }, (_, i) => ({ id: artistTestId(100 + i), key_id: artistTestId(200 + i), name: `别名${i + 1}`, is_display_name: i === 0, origin: "scan" })));
    const album = data.albums.get(id)?.[0];
    if (!album) throw new Error("缺少专辑样本");
    album.credited_release_id = artistTestId(300);
    const { history } = renderApp({ path: `/artists/${id}` });
    await screen.findByRole("button", { name: "显示全部 51 个名字" });
    expect(screen.queryByText("别名21")).toBeNull();
    expect(api.requestsTo("GET", `/api/v1/artists/${id}/names`)).toHaveLength(0);
    const card = await within(await screen.findByRole("region", { name: "专辑" })).findByRole("link", { name: /海边夜曲/ });
    expect(card.getAttribute("href")).toContain(`release=${artistTestId(300)}`);
    expect(within(card).queryByRole("link")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "显示全部 51 个名字" }));
    await screen.findByText("别名50");
    await userEvent.click(within(screen.getByRole("navigation", { name: "名字分页" })).getByRole("link", { name: "下一页" }));
    expect(await screen.findByText("别名51")).toBeTruthy();
    expect(history.location.search).toContain("names_page=2");
    expect(screen.queryByText("别名1", { exact: true })).toBeNull();
  });

  it("曲目换角色重置自己的分页，不清掉名字与专辑页码；缺失曲目保留且不可播放", async () => {
    const { data, api } = setup();
    const detail = getDetail(data);
    detail.role_counts.push({ role: "composer", present: 1, total: 1 });
    const tracks = data.tracks.get(`${id}:track_artist`) ?? [];
    const sample = tracks[0];
    if (!sample) throw new Error("缺少曲目样本");
    data.tracks.set(`${id}:track_artist`, Array.from({ length: 51 }, (_, i) => ({ ...sample, track_id: artistTestId(400 + i), title: `参与曲目${i}`, present: i !== 50 })));
    data.tracks.set(`${id}:composer`, [{ ...sample, title: "作曲作品" }]);
    const { history } = renderApp({ path: `/artists/${id}?track_role=track_artist&tracks_page=2&names_page=2&albums_page=2` });
    const row = await screen.findByRole("row", { name: /参与曲目50/ });
    expect(within(row).getByText("文件缺失")).toBeTruthy();
    expect(within(row).getByRole("button", { name: /^播放 / }).hasAttribute("disabled")).toBe(true);
    await userEvent.click(within(screen.getByRole("navigation", { name: "按角色" })).getByRole("link", { name: /作曲/ }));
    expect(await screen.findByText("作曲作品")).toBeTruthy();
    expect(history.location.search).toContain("names_page=2");
    expect(history.location.search).toContain("albums_page=2");
    expect(history.location.search).not.toContain("tracks_page");
    expect(api.requestsTo("GET", `/api/v1/artists/${id}/tracks`).at(-1)?.path).toContain("page=1");
  });
});
