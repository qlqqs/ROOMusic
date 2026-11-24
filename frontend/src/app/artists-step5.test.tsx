import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { artistDataOf, artistTestId, primaryArtistId, seedArtist } from "../test/artist-data";
import { artistBio, artistImage } from "../test/artist-media-data";
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
/** 艺人行链接的说明（角色与专辑数，经 aria-describedby）。 */
const descriptionOf = (link: HTMLElement) => (link.getAttribute("aria-describedby") ?? "").split(" ").filter((part) => part !== "").map((part) => document.getElementById(part)?.textContent ?? "").join(" | ");
/** 排序菜单：打开后选一项。 */
async function chooseSort(user: ReturnType<typeof userEvent.setup>, label: string) {
  await user.click(screen.getByRole("button", { name: /^排序：/ }));
  await user.click(screen.getByRole("menuitem", { name: new RegExp(`^${label}`) }));
}
const roleChips = () => within(screen.getByRole("group", { name: "角色" })).getAllByRole("button");
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
    await chooseSort(userEvent.setup(), "按专辑数");
    await waitFor(() => expect(history.location.search).toContain("sort=album_count"));
    expect(screen.queryByRole("navigation", { name: "首字母" })).toBeNull();
    expect(screen.queryByRole("region", { name: "D" })).toBeNull();
  });

  it("列表一行：链接名只是名字，角色（发行艺人与专辑艺人只写一次“专辑艺人”）与专辑数是它的说明；角色胶囊只有一个专辑艺人且提交 album_artist", async () => {
    const { api, data } = setup();
    const detail = getDetail(data);
    detail.artist.roles = ["release_artist", "album_artist", "composer"];
    detail.artist.album_count = 12;
    const { history } = renderApp({ path: "/artists" });
    const artistLink = await screen.findByRole("link", { name: "邓丽君" });
    expect(descriptionOf(artistLink)).toBe("专辑艺人、作曲 | 12 张专辑");
    expect(artistLink.textContent).toContain("专辑艺人、作曲");
    // 每行的播放键在链接之外（链接里不嵌按钮），由 app 组合。
    const item = artistLink.closest("li");
    if (item === null) throw new Error("缺少艺人列表项");
    expect(within(item).getByRole("button", { name: "播放 邓丽君 的全部曲目" }).closest("a")).toBeNull();
    const chips = roleChips();
    expect(chips.map((chip) => chip.textContent)).toEqual(["全部", "专辑艺人", "曲目艺人", "作词", "作曲", "编曲", "指挥", "演奏", "制作人"]);
    expect(chips.map((chip) => chip.getAttribute("aria-pressed"))).toEqual(["true", ...Array<string>(8).fill("false")]);
    await userEvent.click(within(screen.getByRole("group", { name: "角色" })).getByRole("button", { name: "专辑艺人" }));
    await waitFor(() => expect(new URLSearchParams(history.location.search).get("role")).toBe("album_artist"));
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(2));
    expect(new URL(api.requestsTo("GET", "/api/v1/artists").at(-1)?.path ?? "", "http://test").searchParams.get("role")).toBe("album_artist");
    expect(await screen.findByRole("region", { name: "担任专辑艺人的艺人" })).toBeTruthy();
    expect(within(screen.getByRole("group", { name: "角色" })).getByRole("button", { name: "专辑艺人" }).getAttribute("aria-pressed")).toBe("true");
    expect(within(screen.getByRole("group", { name: "角色" })).getByRole("button", { name: "全部" }).getAttribute("aria-pressed")).toBe("false");
    // “全部”去掉角色。
    await userEvent.click(within(screen.getByRole("group", { name: "角色" })).getByRole("button", { name: "全部" }));
    await waitFor(() => expect(new URLSearchParams(history.location.search).has("role")).toBe(false));
  });

  it("没有角色、没有专辑的艺人：链接没有说明，行里不留空的角色与数量", async () => {
    const { data } = setup();
    const detail = getDetail(data);
    detail.artist.roles = [];
    detail.artist.album_count = 0;
    renderApp({ path: "/artists" });
    const artistLink = await screen.findByRole("link", { name: "邓丽君" });
    expect(artistLink.hasAttribute("aria-describedby")).toBe(false);
    expect(artistLink.textContent).not.toContain("张专辑");
  });

  it("I6：旧 URL 的 release_artist 保持合法并原样请求，筛选显示专辑艺人", async () => {
    const { api, data } = setup();
    getDetail(data).artist.roles = ["release_artist"];
    const { history } = renderApp({ path: "/artists?role=release_artist" });
    await screen.findByRole("link", { name: "邓丽君" });
    expect(new URLSearchParams(history.location.search).get("role")).toBe("release_artist");
    expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(1);
    expect(new URL(api.requestsTo("GET", "/api/v1/artists")[0]?.path ?? "", "http://test").searchParams.get("role")).toBe("release_artist");
    const pressed = roleChips().filter((chip) => chip.getAttribute("aria-pressed") === "true");
    expect(pressed.map((chip) => chip.textContent)).toEqual(["专辑艺人"]);
    expect(roleChips().filter((chip) => chip.textContent === "专辑艺人")).toHaveLength(1);
    expect(screen.getByRole("region", { name: "担任专辑艺人的艺人" })).toBeTruthy();
  });

  it.each(["首字母", "角色", "筛选", "排序"] as const)("在有效第2页改变%s，回到第1页且请求保留相应筛选", async (filter) => {
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
    expect(descriptionOf(screen.getByRole("link", { name: "邓丽君" }))).toContain("12 张专辑");
    expect(new URL(api.requestsTo("GET", "/api/v1/artists")[0]?.path ?? "", "http://test").searchParams.get("page")).toBe("2");
    const user = userEvent.setup();
    switch (filter) {
      case "首字母": await user.click(within(screen.getByRole("navigation", { name: "首字母" })).getByRole("link", { name: "D" })); break;
      case "角色": await user.click(within(screen.getByRole("group", { name: "角色" })).getByRole("button", { name: "演奏" })); break;
      case "筛选": await user.type(screen.getByRole("textbox", { name: "筛选艺人" }), "邓丽君"); break;
      case "排序": await chooseSort(user, "按专辑数"); break;
    }
    await waitFor(() => expect(new URLSearchParams(history.location.search).has("page")).toBe(false));
    await screen.findByRole("link", { name: filter === "角色" ? "演奏者" : "邓丽君" });
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/artists")).toHaveLength(2));
    const params = new URL(api.requestsTo("GET", "/api/v1/artists").at(-1)?.path ?? "", "http://test").searchParams;
    expect(params.get("page")).toBe("1");
    expect(params.get("role")).toBe(filter === "角色" ? "performer" : "composer");
    if (filter === "首字母") expect(params.get("initial")).toBe("D");
    if (filter === "筛选") expect(params.get("q")).toBe("邓丽君");
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

it("艺人列表使用摘要图片，艺人页头使用详情图片并在弹层显示图片署名", async () => {
  const { data } = setup();
  const detail = getDetail(data);
  detail.artist.image = "artist-list-portrait";
  detail.image = artistImage;
  detail.bio = artistBio;
  renderApp({ path: "/artists" });

  const link = await screen.findByRole("link", { name: detail.artist.name });
  expect(within(link).getByAltText("").getAttribute("src")).toBe("/api/v1/artworks/artist-list-portrait");
  await userEvent.click(link);

  const title = await screen.findByRole("heading", { name: detail.artist.name, level: 1 });
  const header = title.closest("header");
  if (header === null) throw new Error("找不到艺人页头");
  const scope = within(header);
  expect(scope.getByAltText("").getAttribute("src")).toBe("/api/v1/artworks/m8-artist-portrait");
  expect(scope.getByText(artistBio.text)).toBeTruthy();
  expect(scope.queryByText("合成样本作者")).toBeNull();
  await userEvent.click(scope.getByRole("button", { name: "图片来源：Wikimedia Commons · CC BY 4.0" }));
  const source = within(scope.getByRole("dialog", { name: "图片来源" }));
  expect(source.getByRole("link", { name: "Wikimedia Commons" }).getAttribute("href")).toBe(artistImage.source_url);
  expect(source.getByText("合成样本作者")).toBeTruthy();
  expect(source.getByRole("link", { name: "CC BY 4.0" }).getAttribute("href")).toBe(artistImage.license_url);
});
