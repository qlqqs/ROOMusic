import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { decodeReleaseDetail, type ReleaseGroupSummaryDTO } from "../features/catalog/api/decoders";
import { sampleBody } from "../test/api-samples";
import { albumGroup, albumGroups, groupDetailOf, mediaOf, releaseDetailOf, releaseEvidenceOf, releaseVersions } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

/** 双碟三首曲目的专辑：详情按摘要合成，两张介质各 2 首与 1 首，总时长 344 秒（5:44）。 */
const symphonies = (overrides: Parameters<typeof albumGroup>[1] = {}) =>
  albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, medium_count: 2, track_count: 3, artwork: true, ...overrides });

function setup(options: { role?: "admin" | "user"; groups?: ReleaseGroupSummaryDTO[] } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: options.groups ?? [symphonies()] });
  return { api, catalog };
}

type Api = ReturnType<typeof setup>["api"];

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const groupRequests = (api: Api) => api.requestsTo("GET", /^\/api\/v1\/release-groups\/[^/]+$/);
const releaseRequests = (api: Api) => api.requestsTo("GET", /^\/api\/v1\/releases\/[^/]+$/);
const evidenceRequests = (api: Api) => api.requestsTo("GET", /^\/api\/v1\/releases\/[^/]+\/evidence$/);
const trackRows = () => Array.from(document.querySelectorAll("ol > li"));
/** 曲目行自身的文字（序号、标题、艺人与署名、时长、标记），不含播放器放进行末插槽的动作；插槽内容由播放器测试覆盖。 */
function rowFacts(row: Element | undefined): string | null | undefined {
  if (row === undefined) return undefined;
  const copy = row.cloneNode(true) as Element;
  for (const child of Array.from(copy.children)) if (child.querySelector("button") !== null) child.remove();
  return copy.textContent;
}
const searchOf = (history: { location: { search: string } }) => new URLSearchParams(history.location.search);

/** 头部概况里的一项（dt 的下一个兄弟 dd）；流派在头部与侧栏各有一份，所以取第一个（头部）。 */
function headerFact(label: string): string | null {
  const term = screen.getAllByText(label, { selector: "dt" })[0];
  return term?.nextElementSibling?.textContent ?? null;
}

describe("专辑详情：单版本", () => {
  it("标题与专辑艺人、头部概况、侧栏发行信息与制作人员；没有版本导航与“个版本”字样", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });

    const title = await heading("Sample Symphonies");
    expect(title.parentElement?.textContent).toContain("Sample Orchestra");
    expect(document.title).toBe("Sample Symphonies · ROOMusic");
    await screen.findByText("Track 1-01");

    expect(headerFact("年份")).toBe("1998");
    expect(headerFact("流派")).toBe("Classical");
    expect(headerFact("介质")).toBe("2 张");
    expect(headerFact("曲目")).toBe("3 首");
    expect(headerFact("总时长")).toBe("5:44");

    const aside = screen.getByRole("complementary", { name: "发行信息" });
    // 单版本的组：区块叫“发行信息”，页面上没有“此版本”之类的版本字样。
    const facts = within(within(aside).getByRole("region", { name: "发行信息" }));
    expect(facts.getByText("厂牌").nextElementSibling?.textContent).toBe("Sample Label");
    expect(facts.getByText("编号").nextElementSibling?.textContent).toBe("SL-0001");
    expect(facts.getByText("条码").nextElementSibling?.textContent).toBe("0123456789012");
    expect(facts.getByText("版本说明").nextElementSibling?.textContent).toBe("Remastered");
    expect(facts.queryByText("来源类型")).toBeNull(); // 没有值的条目不列出
    const credits = within(within(aside).getByRole("region", { name: "制作人员" }));
    expect(credits.getByText("专辑艺人").nextElementSibling?.textContent).toBe("Sample Orchestra");

    expect(screen.queryByRole("navigation", { name: "版本" })).toBeNull();
    expect(screen.queryByText(/个版本/)).toBeNull();
    expect(screen.queryByText("此版本")).toBeNull();
    expect(screen.queryByRole("link", { name: /^版本 \d/ })).toBeNull();
    expect(screen.getByRole("link", { name: "返回专辑列表" }).getAttribute("href")).toBe("/albums");
  });

  it("头部先用组详情里的摘要渲染，曲目区单独显示加载态；版本详情到达后补上流派与总时长", async () => {
    const album = symphonies();
    const { api } = setup({ groups: [album] });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    api.on("GET", /^\/api\/v1\/releases\/[^/]+$/, async () => {
      await gate;
      return sampleResponse("errors/not-found-release");
    });
    renderApp({ path: `/albums/${album.id}` });

    expect(await heading("Sample Symphonies")).toBeTruthy();
    expect(headerFact("年份")).toBe("1998");
    expect(headerFact("曲目")).toBe("3 首");
    expect(screen.getByText("正在加载曲目")).toBeTruthy();
    expect(screen.queryByText("流派")).toBeNull();
    expect(screen.queryByText("总时长")).toBeNull();
    release();
  });

  it("没有额外发行信息的版本：侧栏说明暂无，不写“未知”；没有制作人员区块", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(
      album.representative.id,
      releaseDetailOf(album.representative, { genre: null, catalog_number: null, edition: null, label: null, barcode: null, credits: [] }),
    );
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.getByText("暂无更多发行信息。")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "制作人员" })).toBeNull();
    expect(screen.queryByText("流派")).toBeNull();
    expect(screen.queryByText("未知")).toBeNull();
  });

  it("年份未知写“未知”，不伪造", async () => {
    const album = symphonies({ year: null });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(headerFact("年份")).toBe("未知");
  });

  it("标题超过 80 个字符：完整显示并降一级字号；短标题不降", async () => {
    const long = "长".repeat(90);
    const album = symphonies({ title: long });
    setup({ groups: [album] });
    const { view } = renderApp({ path: `/albums/${album.id}` });
    const title = await heading(long);
    expect(title.parentElement?.hasAttribute("data-compact")).toBe(true);
    view.unmount();

    const shortAlbum = symphonies({ title: "短标题" });
    setup({ groups: [shortAlbum] });
    renderApp({ path: `/albums/${shortAlbum.id}` });
    expect((await heading("短标题")).parentElement?.hasAttribute("data-compact")).toBe(false);
  });
});

describe("专辑详情：介质与曲目", () => {
  const original = Element.prototype.scrollIntoView;
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });
  afterEach(() => {
    Element.prototype.scrollIntoView = original;
  });

  it("多张介质逐段列出：每段一个标题与曲目列表，顶部有跳到每一张的链接", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");

    const first = within(screen.getByRole("region", { name: /^第 1 张/ }));
    const second = within(screen.getByRole("region", { name: /^第 2 张/ }));
    expect(first.getAllByRole("listitem")).toHaveLength(2);
    expect(second.getAllByRole("listitem")).toHaveLength(1);
    expect(first.getByRole("heading", { level: 3 }).textContent).toBe("第 1 张，2 首");
    // 序号、标题、署名与时长；曲目艺人与专辑艺人相同时不重复。
    const row = first.getAllByRole("listitem")[0];
    expect(rowFacts(row)).toBe("1Track 1-01作曲：Sample Composer；指挥：Sample Conductor1:51");
    // 播放动作在行末单独的插槽里，不混进曲目事实。
    if (row === undefined) throw new Error("缺少曲目行");
    expect(within(row).getByRole("button", { name: "从这首开始播放" })).toBeTruthy();
    expect(first.getAllByRole("listitem")[1]?.textContent).toContain("2:01");
    expect(second.getByText("1:52")).toBeTruthy();
    // 不显示来源文件名。
    expect(screen.queryByText(/\.flac/)).toBeNull();

    const jump = within(screen.getByRole("navigation", { name: "介质" }));
    expect(jump.getAllByRole("link").map((link) => link.textContent)).toEqual(["第 1 张", "第 2 张"]);
  });

  it("点“第 2 张”：滚到该段标题并把焦点放上去，地址与历史不变", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    const { history } = renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    const length = history.length;
    const location = history.location.href;

    await userEvent.click(within(screen.getByRole("navigation", { name: "介质" })).getByRole("link", { name: "第 2 张" }));
    const target = screen.getByRole("heading", { level: 3, name: /^第 2 张/ });
    expect(document.activeElement).toBe(target);
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
    expect(history.length).toBe(length);
    expect(history.location.href).toBe(location);
  });

  it("只有一张介质：直接是曲目列表，没有分段标题与跳转链接", async () => {
    const album = symphonies({ medium_count: 1, track_count: 3 });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");
    expect(trackRows()).toHaveLength(3);
    expect(screen.queryByRole("navigation", { name: "介质" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 3 })).toBeNull();
  });

  it("曲目艺人与专辑艺人不同时才显示；没有署名的曲目不留空行；未知时长写“—”并给读屏“时长未知”", async () => {
    const album = symphonies({ medium_count: 1, track_count: 3 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(
      album.representative.id,
      releaseDetailOf(album.representative, {
        media: mediaOf(album.representative, (_medium, track) =>
          track === 2 ? { artist: "Sample Soloist", credits: [], duration_seconds: null } : track === 3 ? { title: "", credits: [{ role: "mixer", name: "Someone" }] } : {},
        ),
      }),
    );
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");

    const rows = trackRows();
    expect(rowFacts(rows[0])).toBe("1Track 01作曲：Sample Composer；指挥：Sample Conductor1:51");
    expect(rowFacts(rows[1])).toBe("2Track 02Sample Soloist—时长未知");
    // 标题缺失写“（无标题）”；未知角色原样显示。
    expect(rowFacts(rows[2])).toBe("3（无标题）mixer：Someone2:11");
    // 有一首的时长未知，就不写总时长。
    expect(screen.queryByText("总时长")).toBeNull();
  });

  it("CUE 分轨带标记：取自真实的 CUE 样本", async () => {
    const cue = decodeReleaseDetail(sampleBody("releases/get-cue"));
    const album = albumGroup(1, { title: cue.title, artist: cue.artist, album_artist: cue.album_artist ?? "Sample Cue Artist", year: cue.year, medium_count: 1, track_count: 2 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, { ...cue, id: album.representative.id });
    renderApp({ path: `/albums/${album.id}` });

    await screen.findByText("Cue One");
    expect(screen.getByText("Cue Two")).toBeTruthy();
    expect(screen.getAllByText("CUE 分轨")).toHaveLength(2);
    expect(trackRows().map((row) => rowFacts(row))).toEqual(["1Cue OneCUE 分轨2:00", "2Cue TwoCUE 分轨2:00"]);
    expect(headerFact("总时长")).toBe("4:00");
  });

  it("一千首曲目的版本可以渲染，计数带千位分隔符", async () => {
    const album = symphonies({ medium_count: 1, track_count: 1000 });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    expect(await screen.findByText("Track 1000", {}, { timeout: 8000 })).toBeTruthy();
    expect(trackRows()).toHaveLength(1000);
    expect(headerFact("曲目")).toBe("1,000 首");
  }, 15_000);

  it("十二张介质：跳转链接与分段都按顺序，介质标题缺省不重复", async () => {
    const album = symphonies({ medium_count: 12, track_count: 24 });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 12-02");
    const jump = within(screen.getByRole("navigation", { name: "介质" }));
    expect(jump.getAllByRole("link")).toHaveLength(12);
    expect(screen.getAllByRole("heading", { level: 3 }).map((item) => item.textContent?.split("，")[0])).toEqual(Array.from({ length: 12 }, (_, index) => `第 ${index + 1} 张`));
  });

  it("没有任何介质或曲目：说明没有可显示的曲目", async () => {
    const album = symphonies({ medium_count: 1, track_count: 0 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, releaseDetailOf(album.representative, { media: [] }));
    renderApp({ path: `/albums/${album.id}` });
    expect(await screen.findByText("这个版本没有可显示的曲目。")).toBeTruthy();
  });
});

describe("专辑详情：封面", () => {
  it("有封面：详情页的图片有替代文字并立即加载；失败后显示说明，重新加载换一张新图片", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    const image = await screen.findByRole("img", { name: "《Sample Symphonies》封面" });
    expect(image.getAttribute("src")).toBe("/api/v1/artworks/cover-001.png");
    expect(image.getAttribute("loading")).toBe("eager");
    fireEvent.load(image);
    expect(document.querySelector('[data-state="loaded"]')).not.toBeNull();

    fireEvent.error(image);
    expect(await screen.findByText("封面无法加载")).toBeTruthy();
    expect(screen.queryByRole("img", { name: "《Sample Symphonies》封面" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "重新加载封面" }));
    const retried = await screen.findByRole("img", { name: "《Sample Symphonies》封面" });
    expect(retried).not.toBe(image);
    expect(retried.getAttribute("src")).toBe("/api/v1/artworks/cover-001.png");
    expect(screen.queryByText("封面无法加载")).toBeNull();
  });

  it("没有封面：文字版面，名称说明没有封面，不发图片请求", async () => {
    const album = symphonies({ artwork: false });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    expect(await screen.findByRole("img", { name: "《Sample Symphonies》没有封面" })).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });
});

describe("专辑详情：多版本", () => {
  /** 三个版本：代表版本 1998 年，其余两个晚几年、多几首曲目。 */
  function multiVersion(overrides: Parameters<typeof albumGroup>[1] = {}) {
    const album = symphonies({ release_count: 3, ...overrides });
    const versions = releaseVersions(album, 3, (version) => ({ year: 2000 + version }));
    return { album, versions };
  }

  function setupMulti(overrides: Parameters<typeof albumGroup>[1] = {}, role: "admin" | "user" = "admin") {
    const { album, versions } = multiVersion(overrides);
    const context = setup({ role, groups: [album] });
    context.catalog.groupDetails.set(album.id, groupDetailOf(album, versions));
    return { ...context, album, versions };
  }

  const versionLink = (n: number) => screen.getByRole("link", { name: new RegExp(`^版本 ${n} `) });

  it("版本导航：每个版本一个链接，说明年份、介质与曲目数；代表版本是当前版本", async () => {
    const { album } = setupMulti();
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");

    const nav = screen.getByRole("navigation", { name: "版本" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual([
      "版本 1 1998 · 2 张介质 · 3 首曲目",
      "版本 2 2002 · 2 张介质 · 4 首曲目",
      "版本 3 2003 · 2 张介质 · 5 首曲目",
    ]);
    expect(versionLink(1).getAttribute("aria-current")).toBe("page");
    expect(versionLink(2).getAttribute("aria-current")).toBeNull();
    // 有多个版本时，侧栏的区块说明的是“此版本”。
    expect(screen.getByRole("region", { name: "此版本" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "发行信息" })).toBeNull();
    // 代表版本的链接不带 release 参数，其余的带。
    expect(versionLink(1).getAttribute("href")).toBe(`/albums/${album.id}`);
    expect(versionLink(2).getAttribute("href")).toMatch(/^\/albums\/[0-9a-f-]{36}\?release=[0-9a-f-]{36}$/);
  });

  it("各版本标题不同时，版本链接里附上标题；相同时不重复", async () => {
    const { album, catalog, versions } = (() => {
      const context = setupMulti();
      return { ...context, catalog: context.catalog };
    })();
    const renamed = versions.map((version, index) => (index === 1 ? { ...version, title: "Sample Symphonies (Remastered)" } : version));
    catalog.groupDetails.set(album.id, groupDetailOf(album, renamed));
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(versionLink(2).textContent).toBe("版本 2 Sample Symphonies (Remastered) 2002 · 2 张介质 · 4 首曲目");
    expect(versionLink(1).textContent).toBe("版本 1 Sample Symphonies 1998 · 2 张介质 · 3 首曲目");
  });

  it("点另一个版本：地址记录 release，头部、曲目与当前标记换成该版本；点回代表版本，地址不再带 release", async () => {
    const { api, album, versions } = setupMulti();
    const { history } = renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(headerFact("曲目")).toBe("3 首");

    await userEvent.click(versionLink(2));
    await waitFor(() => expect(searchOf(history).get("release")).toBe(versions[1]?.id));
    await waitFor(() => expect(headerFact("曲目")).toBe("4 首"));
    expect(headerFact("年份")).toBe("2002");
    expect(versionLink(2).getAttribute("aria-current")).toBe("page");
    expect(versionLink(1).getAttribute("aria-current")).toBeNull();
    await waitFor(() => expect(trackRows()).toHaveLength(4));
    expect(releaseRequests(api).map((request) => request.path)).toEqual([`/api/v1/releases/${versions[0]?.id}`, `/api/v1/releases/${versions[1]?.id}`]);
    expect(groupRequests(api)).toHaveLength(1); // 切换版本不重新读组

    await userEvent.click(versionLink(1));
    await waitFor(() => expect(history.location.search).toBe(""));
    await waitFor(() => expect(headerFact("曲目")).toBe("3 首"));
    expect(versionLink(1).getAttribute("aria-current")).toBe("page");
  });

  it("直接打开带 release 的地址：选中该版本；刷新后仍是它", async () => {
    const { album, versions } = setupMulti();
    const { history } = renderApp({ path: `/albums/${album.id}?release=${versions[2]?.id}` });
    await waitFor(() => expect(headerFact("曲目")).toBe("5 首"));
    expect(headerFact("年份")).toBe("2003");
    expect(versionLink(3).getAttribute("aria-current")).toBe("page");
    expect(searchOf(history).get("release")).toBe(versions[2]?.id);
  });

  it("release 不在本组成员里、或就是代表版本：用替换历史的方式去掉，不新增历史项", async () => {
    const { album, versions } = setupMulti();
    const stranger = "00000000-0000-4000-8000-0000000000ff";
    const { history } = renderApp({ path: `/albums/${album.id}?release=${stranger}` });
    await waitFor(() => expect(history.location.search).toBe(""));
    expect(versionLink(1).getAttribute("aria-current")).toBe("page");
    expect(history.length).toBe(1);
    expect(headerFact("曲目")).toBe("3 首");

    const again = renderApp({ path: `/albums/${album.id}?release=${versions[0]?.id}` });
    await waitFor(() => expect(again.history.location.search).toBe(""));
    expect(again.history.length).toBe(1);
  });

  it("release 大小写不同的 UUID 也认得（统一成小写）", async () => {
    const { album, versions } = setupMulti();
    const upper = (versions[1]?.id ?? "").toUpperCase();
    expect(upper).not.toBe(versions[1]?.id);
    const { history } = renderApp({ path: `/albums/${album.id}?release=${upper}` });
    await waitFor(() => expect(headerFact("曲目")).toBe("4 首"));
    expect(searchOf(history).get("release")).toBe(versions[1]?.id); // 地址里是统一后的小写
    expect(versionLink(2).getAttribute("aria-current")).toBe("page");
  });

  it("组里只剩一个版本时，带着 release 打开：去掉参数，版本导航不出现", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    const other = "00000000-0000-4000-8000-0000000000fe";
    const { history } = renderApp({ path: `/albums/${album.id}?release=${other}` });
    await waitFor(() => expect(history.location.search).toBe(""));
    await screen.findByText("Track 1-01");
    expect(screen.queryByRole("navigation", { name: "版本" })).toBeNull();
  });

  it("成员多于接口列出的 100 个：说明只列出前 100 个", async () => {
    const album = symphonies({ release_count: 150 });
    const { catalog } = setup({ groups: [album] });
    catalog.groupDetails.set(album.id, groupDetailOf(album, releaseVersions(album, 100)));
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.getByText("共 150 个版本，这里只列出前 100 个。")).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "版本" })).getAllByRole("link")).toHaveLength(100);
  });

  it("切换版本后，“来源与证据”回到折叠状态", async () => {
    const { album } = setupMulti();
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    expect(await screen.findByRole("table", { name: "证据摘要" })).toBeTruthy();

    await userEvent.click(versionLink(2));
    await waitFor(() => expect(headerFact("曲目")).toBe("4 首"));
    await screen.findByText("Track 2-01");
    expect(screen.queryByRole("table", { name: "证据摘要" })).toBeNull();
    expect(document.querySelector("details")?.hasAttribute("open")).toBe(false);
  });
});

describe("专辑详情：返回专辑列表", () => {
  it("从卡片进入：返回链接带回原来的搜索词、筛选与页码；点它回到那一页", async () => {
    // 每个组都需要关注，筛选后仍有两页，第 2 页存在。
    setup({ groups: albumGroups(120, () => ({ attention_count: 1 })) });
    const { history } = renderApp({ path: "/albums?q=Album&attention=required&page=2" });
    await waitFor(() => expect(screen.getAllByRole("link").some((link) => /^\/albums\/[0-9a-f-]{36}$/.test(link.getAttribute("href") ?? ""))).toBe(true));
    const card = screen.getAllByRole("link").find((link) => /^\/albums\/[0-9a-f-]{36}$/.test(link.getAttribute("href") ?? ""));
    if (card === undefined) throw new Error("没有卡片");
    await userEvent.click(card);

    const back = await screen.findByRole("link", { name: "返回专辑列表" });
    expect(back.getAttribute("href")).toBe("/albums?q=Album&attention=required&page=2");
    await userEvent.click(back);
    await waitFor(() => expect(history.location.pathname).toBe("/albums"));
    expect(history.location.search).toBe("?q=Album&attention=required&page=2");
    expect(await screen.findByRole("region", { name: "“Album”中需要关注的专辑" })).toBeTruthy();
  });

  it("切换版本后返回链接仍然有效", async () => {
    const album = symphonies({ release_count: 2, attention_count: 1 });
    const { catalog } = setup({ groups: [album] });
    catalog.groupDetails.set(album.id, groupDetailOf(album, releaseVersions(album, 2)));
    renderApp({ path: "/albums?attention=required" });
    await userEvent.click(await screen.findByRole("link", { name: /^Sample Symphonies/ }));
    await userEvent.click(await screen.findByRole("link", { name: /^版本 2 / }));
    await waitFor(() => expect(headerFact("年份")).toBe("1999"));
    expect(screen.getByRole("link", { name: "返回专辑列表" }).getAttribute("href")).toBe("/albums?attention=required");
  });

  it("规范化地址之后返回链接仍然有效", async () => {
    const album = symphonies({ release_count: 2, attention_count: 1 });
    const { catalog } = setup({ groups: [album] });
    catalog.groupDetails.set(album.id, groupDetailOf(album, releaseVersions(album, 2)));
    const { history } = renderApp({ path: "/albums?q=Sample" });
    await userEvent.click(await screen.findByRole("link", { name: /^Sample Symphonies/ }));
    await screen.findByText("Track 1-01");
    // 手工改成不存在的版本：地址被替换，列表参数随之保留。
    history.replace(`${history.location.pathname}?release=00000000-0000-4000-8000-0000000000ff`, history.location.state);
    await waitFor(() => expect(history.location.search).toBe(""));
    expect(screen.getByRole("link", { name: "返回专辑列表" }).getAttribute("href")).toBe("/albums?q=Sample");
  });

  it("直接打开详情：返回链接指向不带参数的列表", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    expect((await screen.findByRole("link", { name: "返回专辑列表" })).getAttribute("href")).toBe("/albums");
  });
});

describe("专辑详情：不存在与失败", () => {
  it("组 ID 不是 UUID：不发请求，直接说专辑不存在", async () => {
    const { api } = setup();
    renderApp({ path: "/albums/not-a-uuid" });
    expect(await heading("专辑不存在")).toBeTruthy();
    expect(screen.getByText("它可能已被移除，或者地址不正确。")).toBeTruthy();
    expect(screen.getByRole("link", { name: "返回专辑列表" }).getAttribute("href")).toBe("/albums");
    expect(groupRequests(api)).toHaveLength(0);
    expect(document.title).toBe("专辑不存在 · ROOMusic");
  });

  it("组不存在（404）：专辑不存在，不再请求版本详情", async () => {
    const { api } = setup();
    renderApp({ path: "/albums/00000000-0000-4000-8000-0000000000aa" });
    expect(await heading("专辑不存在")).toBeTruthy();
    expect(groupRequests(api)).toHaveLength(1);
    expect(releaseRequests(api)).toHaveLength(0);
  });

  it("选中的版本在组详情里还是成员，读详情却是 404：就地提示已不可见，可以回到代表版本，导航随之刷新", async () => {
    const album = symphonies({ release_count: 2 });
    const versions = releaseVersions(album, 2);
    const { api, catalog } = setup({ groups: [album] });
    catalog.groupDetails.set(album.id, groupDetailOf(album, versions));
    const gone = versions[1]?.id ?? "";
    catalog.hiddenReleases.add(gone);
    const { history } = renderApp({ path: `/albums/${album.id}?release=${gone}` });

    expect(await screen.findByText("这个版本已经找不到了")).toBeTruthy();
    expect(screen.getByText(/可能刚重新扫描过/)).toBeTruthy();
    // 页面其余部分仍在：标题、头部与版本导航。
    expect(screen.getByRole("heading", { level: 1, name: "Sample Symphonies" })).toBeTruthy();
    expect(screen.getByRole("navigation", { name: "版本" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "曲目" })).toBeNull();
    expect(groupRequests(api)).toHaveLength(1);

    await userEvent.click(screen.getByRole("link", { name: "查看代表版本" }));
    await waitFor(() => expect(history.location.search).toBe(""));
    await screen.findByText("Track 1-01");
    expect(screen.queryByText("这个版本已经找不到了")).toBeNull();
    // 点链接让组详情过期并重新读取，版本导航随之刷新。
    await waitFor(() => expect(groupRequests(api)).toHaveLength(2));
  });

  it("代表版本本身不可见：提示刷新，刷新后重新读取", async () => {
    const album = symphonies();
    const { api, catalog } = setup({ groups: [album] });
    catalog.hiddenReleases.add(album.representative.id);
    renderApp({ path: `/albums/${album.id}` });
    expect(await screen.findByText("这个版本已经找不到了")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "查看代表版本" })).toBeNull();

    catalog.hiddenReleases.clear();
    await userEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(await screen.findByText("Track 1-01")).toBeTruthy();
    expect(screen.queryByText("这个版本已经找不到了")).toBeNull();
    expect(groupRequests(api).length).toBeGreaterThanOrEqual(2);
  });

  it("组详情读取失败（503）：带请求 ID 的错误与重试，重试成功后显示专辑", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.groupFailure = sampleResponse("errors/database-unavailable");
    renderApp({ path: `/albums/${album.id}` });
    // 503 会被自动重试（最多 2 次，带退避），耗尽后才显示错误。
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();

    catalog.groupFailure = null;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await heading("Sample Symphonies")).toBeTruthy();
    await screen.findByText("Track 1-01");
    expect(screen.queryByRole("alert")).toBeNull();
  }, 15_000);

  it("版本详情读取失败（503）：头部仍在，曲目区显示错误与重试", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.releaseFailure = sampleResponse("errors/database-unavailable");
    renderApp({ path: `/albums/${album.id}` });
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(screen.getByRole("heading", { level: 1, name: "Sample Symphonies" })).toBeTruthy();
    expect(headerFact("曲目")).toBe("3 首");
    expect(screen.queryByText("Track 1-01")).toBeNull();

    catalog.releaseFailure = null;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByText("Track 1-01")).toBeTruthy();
  }, 15_000);

  it("会话失效（401）：进入会话恢复，回到登录页", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.groupFailure = sampleResponse("errors/unauthorized");
    const { history } = renderApp({ path: `/albums/${album.id}` });
    await waitFor(() => expect(history.location.pathname).toBe("/login"));
  });
});

describe("专辑详情：来源与证据", () => {
  const summaryTable = () => screen.findByRole("table", { name: "证据摘要" });

  it("所有登录用户都有折叠的“来源与证据”：默认折叠且不渲染内容，展开后是证据摘要与“需要关注”的说明", async () => {
    const album = symphonies();
    setup({ role: "user", groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");

    const details = document.querySelector("details");
    expect(details?.hasAttribute("open")).toBe(false);
    expect(screen.getByText("（9 项）")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "证据摘要" })).toBeNull();

    await userEvent.click(screen.getByText("来源与证据"));
    const table = await summaryTable();
    expect(details?.hasAttribute("open")).toBe(true);
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(10); // 表头加九个字段
    expect(within(rows[1] as HTMLElement).getAllByRole("cell").map((cell) => cell.textContent)).toEqual(["音频标签", "高", "直接采用", "majority_v1"]);
    expect(within(rows[1] as HTMLElement).getByRole("rowheader").textContent).toBe("专辑艺人");

    // 再点一次折叠，内容随之移除。
    await userEvent.click(screen.getByText("来源与证据"));
    await waitFor(() => expect(screen.queryByRole("table", { name: "证据摘要" })).toBeNull());
  });

  it("普通用户从不请求完整证据接口，也没有“查看完整证据”按钮", async () => {
    const album = symphonies();
    const { api } = setup({ role: "user", groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await summaryTable();
    expect(screen.queryByRole("button", { name: /完整证据/ })).toBeNull();
    expect(screen.queryByRole("heading", { name: "完整证据" })).toBeNull();
    expect(evidenceRequests(api)).toHaveLength(0);
  });

  it("管理员：展开后才出现“查看完整证据”；点之前没有请求，点之后请求一次并按字段列出取值、候选与原因，分组依据单列", async () => {
    const album = symphonies();
    const { api } = setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.queryByRole("button", { name: "查看完整证据" })).toBeNull();
    await userEvent.click(screen.getByText("来源与证据"));
    const button = await screen.findByRole("button", { name: "查看完整证据" });
    expect(evidenceRequests(api)).toHaveLength(0);

    await userEvent.click(button);
    const fields = await screen.findByRole("table", { name: "字段证据" });
    expect(evidenceRequests(api).map((request) => request.path)).toEqual([`/api/v1/releases/${album.representative.id}/evidence`]);
    const rows = within(fields).getAllByRole("row");
    expect(rows).toHaveLength(10);
    expect(within(rows[1] as HTMLElement).getAllByRole("cell").map((cell) => cell.textContent)).toEqual(["Sample Orchestra", "Sample Orchestra", "—"]);

    expect(screen.getByRole("heading", { level: 4, name: "归组依据" })).toBeTruthy();
    const grouping = screen.getByText("归组方式").closest("dl");
    expect(grouping?.textContent).toContain("普通目录");
    expect(grouping?.textContent).toContain("organizer_v2");
    expect(grouping?.textContent).toContain("double-album/d1-01.flac");
    // 按钮留在原处，现在是“重新加载”。
    await userEvent.click(screen.getByRole("button", { name: "重新加载完整证据" }));
    await waitFor(() => expect(evidenceRequests(api)).toHaveLength(2));
  });

  it("完整证据加载中：按钮忙碌、有占位，焦点不丢", async () => {
    const album = symphonies();
    const { api } = setup({ groups: [album] });
    let finish!: (response: Response) => void;
    const gate = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    api.on("GET", /^\/api\/v1\/releases\/[^/]+\/evidence$/, () => gate);
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));

    const busy = await screen.findByRole("button", { name: "正在加载…" });
    expect(busy.getAttribute("aria-busy")).toBe("true");
    expect(document.activeElement).toBe(busy);
    expect(screen.getByText("正在加载完整证据")).toBeTruthy();
    finish(sampleResponse("errors/not-found-release"));
    expect(await screen.findByRole("alert")).toBeTruthy();
  });

  it("完整证据被截断：提示内容过长，仍显示已有部分", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.evidence.set(album.representative.id, releaseEvidenceOf(album.representative, { truncated: true }));
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));
    expect(await screen.findByText("证据太长，只显示了前面一部分。")).toBeTruthy();
    expect(screen.getByRole("table", { name: "字段证据" })).toBeTruthy();
  });

  it("没有归组证据、没有字段证据：各自说明", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.evidence.set(album.representative.id, releaseEvidenceOf(album.representative, { fields: [], grouping: null }));
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));
    expect(await screen.findByText("没有字段证据。")).toBeTruthy();
    expect(screen.getByText("没有归组证据。")).toBeTruthy();
  });

  it.each([
    ["403", "errors/forbidden", "你没有权限查看完整证据。"],
    ["404", "errors/not-found-release", "这个版本已经找不到了，可能刚重新扫描过。"],
  ])("完整证据 %s：就地提示，页面其他部分不受影响", async (_status, sample, message) => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.evidenceFailure = sampleResponse(sample);
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(message);
    expect(screen.getByRole("table", { name: "证据摘要" })).toBeTruthy();
    expect(screen.getByText("Track 1-01")).toBeTruthy();
    // 仍可重新加载，恢复后显示内容。
    catalog.evidenceFailure = null;
    await userEvent.click(screen.getByRole("button", { name: "重新加载完整证据" }));
    expect(await screen.findByRole("table", { name: "字段证据" })).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("完整证据 503：带请求 ID 的错误，页面其他部分不受影响", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.evidenceFailure = sampleResponse("errors/database-unavailable");
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();
    expect(screen.getByRole("table", { name: "证据摘要" })).toBeTruthy();
  }, 15_000);

  it("需要关注的版本：头部有标签并指向“来源与证据”；没有需要关注的版本没有这个标签", async () => {
    const attention = symphonies({ attention_count: 2 });
    setup({ groups: [attention] });
    const { view } = renderApp({ path: `/albums/${attention.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.getByText("需要关注")).toBeTruthy();
    expect(screen.getByText("详见页面底部的“来源与证据”。")).toBeTruthy();
    view.unmount();

    const calm = symphonies();
    setup({ groups: [calm] });
    renderApp({ path: `/albums/${calm.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.queryByText("需要关注")).toBeNull();
  });

  it("词表之外的取值原样显示，不会让页面出错；已知取值显示中文名", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(
      album.representative.id,
      releaseDetailOf(album.representative, {
        evidence: [
          { field: "year", source: "cue_sheet", confidence: "medium", action: "uncertain_apply", rule_id: "majority_v1" },
          { field: "new_field", source: "new_source", confidence: "unsure", action: "new_action", rule_id: "new_rule_v9" },
        ],
        credits: [{ role: "arranger", name: "Someone Else" }],
      }),
    );
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    const table = await summaryTable();
    const rows = within(table).getAllByRole("row");
    expect(rows.map((row) => row.textContent)).toEqual([
      "字段来源可信度处理方式规则",
      "年份CUE 文件（专辑）中不确定，已暂时采用majority_v1",
      "new_fieldnew_sourceunsurenew_actionnew_rule_v9",
    ]);
    // 未知的署名角色同样原样显示。
    expect(within(screen.getByRole("region", { name: "制作人员" })).getByText("arranger")).toBeTruthy();
  });
});

describe("样本数据", () => {
  it("后端样本里的双碟发行版本可以直接显示", async () => {
    const release = decodeReleaseDetail(sampleBody("releases/get"));
    const album = albumGroup(1, { title: release.title, artist: release.artist, album_artist: release.album_artist ?? release.artist, year: release.year, medium_count: release.medium_count, track_count: release.track_count });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, { ...release, id: album.representative.id });
    renderApp({ path: `/albums/${album.id}` });
    await heading("Sample Symphonies");
    await screen.findByText("Allegro");
    expect(trackRows().map((row) => row.querySelector("p")?.textContent)).toEqual(["Allegro", "Andante", "Finale"]);
    expect(screen.getByRole("region", { name: /^第 1 张/ })).toBeTruthy();
    expect(screen.getByRole("region", { name: /^第 2 张/ })).toBeTruthy();
    // 曲目艺人与专辑艺人不同：第二首单独一行显示 Sample Soloist；第一首的艺人与专辑艺人相同，只有署名里提到演奏者。
    const lines = (row: Element | undefined) => Array.from(row?.querySelectorAll("p") ?? []).map((line) => line.textContent);
    expect(lines(trackRows()[1])).toContain("Sample Soloist");
    expect(lines(trackRows()[0])).not.toContain("Sample Soloist");
    expect(lines(trackRows()[0])).toContain("作曲：Sample Composer；指挥：Sample Conductor；演奏：Sample Soloist");
  });
});
