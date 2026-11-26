import { fireEvent, isInaccessible, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { decodeReleaseDetail, type ReleaseGroupSummaryDTO } from "../features/catalog/api/decoders";
import type { ReleaseMatchResultDTO } from "../features/curation/api/decoders";
import { formatDateTime } from "../shared/lib/time";
import { sampleBody } from "../test/api-samples";
import { albumGroup, albumGroups, groupDetailOf, mediaOf, releaseDetailOf, releaseEvidenceOf, releaseVersions } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { jsonResponse, sampleResponse } from "../test/http";
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
const trackRows = () => Array.from(document.querySelectorAll<HTMLLIElement>("ol > li"));
/** 页头里“全部曲目”那一行（全部曲目共有的署名）；没有时是 null。 */
const sharedCreditsText = () => Array.from(document.querySelectorAll("header p")).find((line) => line.textContent.startsWith("全部曲目"))?.textContent ?? null;
/** 曲目行自身的文字（序号、标题、艺人与作曲栏、时长、标记），不含播放器放进行末插槽的动作；插槽内容由播放器测试覆盖。 */
function rowFacts(row: Element | undefined): string | null | undefined {
  if (row === undefined) return undefined;
  const copy = row.cloneNode(true) as Element;
  for (const child of Array.from(copy.children)) if (child.querySelector("button") !== null) child.remove();
  return copy.textContent;
}
const searchOf = (history: { location: { search: string } }) => new URLSearchParams(history.location.search);

/** 头部概况里的一项（读屏标签 + 值）；概况列表只显示值。 */
function headerFact(label: string): string | null {
  const list = screen.getByRole("list", { name: "概况" });
  const item = Array.from(list.querySelectorAll("li")).find((li) => li.textContent?.startsWith(`${label}：`));
  return item?.textContent?.slice(`${label}：`.length) ?? null;
}

describe("专辑详情：单版本", () => {
  it("标题与专辑艺人、头部概况、侧栏发行信息；只有专辑艺人时不显示制作人员；没有版本导航与“个版本”字样", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });

    const title = await heading("Sample Symphonies");
    expect(title.parentElement?.textContent).toContain("Sample Orchestra");
    expect(document.title).toBe("Sample Symphonies · ROOMusic");
    await screen.findByText("Track 1-01");

    expect(headerFact("年份")).toBe("1998");
    expect(headerFact("流派")).toBe("Classical · Baroque");
    expect(headerFact("介质")).toBe("2 张碟");
    expect(headerFact("曲目")).toBe("3 首");
    expect(headerFact("总时长")).toBe("6 分钟");

    const aside = screen.getByRole("complementary", { name: "发行信息" });
    // 单版本的组：区块叫“发行信息”，页面上没有“此版本”之类的版本字样。
    const facts = within(within(aside).getByRole("region", { name: "发行信息" }));
    expect(facts.getByText("厂牌").nextElementSibling?.textContent).toBe("Sample Label");
    expect(facts.getByText("编号").nextElementSibling?.textContent).toBe("SL-0001");
    expect(facts.getByText("条码").nextElementSibling?.textContent).toBe("0123456789012");
    expect(facts.getByText("版本说明").nextElementSibling?.textContent).toBe("Remastered");
    expect(facts.queryByText("来源类型")).toBeNull(); // 没有值的条目不列出
    expect(within(aside).queryByRole("region", { name: "制作人员" })).toBeNull();

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

  it("没有额外发行信息的版本：没有发行信息区块（不写“暂无”或“未知”），也没有制作人员区块", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(
      album.representative.id,
      releaseDetailOf(album.representative, { genres: [], catalog_number: null, edition: null, label: null, barcode: null, credits: [] }),
    );
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.queryByRole("region", { name: "发行信息" })).toBeNull();
    expect(screen.queryByText("暂无更多发行信息。")).toBeNull();
    expect(screen.queryByRole("region", { name: "制作人员" })).toBeNull();
    expect(screen.queryByText("流派")).toBeNull();
    expect(screen.queryByText("未知")).toBeNull();
  });

  it("年份未知时不显示年份项", async () => {
    const album = symphonies({ year: null });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    expect(headerFact("年份")).toBeNull();
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

  it("多张介质逐段列出：每段一个标题与曲目列表；只有两张时没有跳转链接，表头只在第一张出现", async () => {
    const album = symphonies();
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");

    const first = within(screen.getByRole("region", { name: /^第 1 张/ }));
    const second = within(screen.getByRole("region", { name: /^第 2 张/ }));
    expect(first.getAllByRole("listitem")).toHaveLength(2);
    expect(second.getAllByRole("listitem")).toHaveLength(1);
    expect(first.getByRole("heading", { level: 3 }).textContent).toBe("第 1 张，2 首");
    // 艺人都与专辑艺人相同时没有艺人一栏；全部曲目共同的作曲与指挥只在页头列一次，曲目行不再重复。
    const row = first.getAllByRole("listitem")[0];
    expect(rowFacts(row)).toBe("1Track 1-011:51");
    expect(sharedCreditsText()).toBe("全部曲目：作曲： Sample Composer；指挥： Sample Conductor");
    // 播放动作在行末单独的插槽里，不混进曲目事实。
    if (row === undefined) throw new Error("缺少曲目行");
    expect(within(row).getByRole("button", { name: "从这首开始播放" })).toBeTruthy();
    expect(first.getAllByRole("listitem")[1]?.textContent).toContain("2:01");
    expect(second.getByText("1:52")).toBeTruthy();
    // 不显示来源文件名。
    expect(screen.queryByText(/\.flac/)).toBeNull();

    // 两张碟一眼看得全，不需要页内跳转；表头不在每张碟上重复。
    expect(screen.queryByRole("navigation", { name: "介质" })).toBeNull();
    const columnHeaders = (region: ReturnType<typeof within>) => region.queryAllByText("时长");
    expect(columnHeaders(first)).toHaveLength(1);
    expect(columnHeaders(second)).toHaveLength(0);
  });

  it("三张及以上介质：顶部有跳到每一张的链接；点“第 2 张”滚到该段标题并把焦点放上去，地址与历史不变", async () => {
    const album = symphonies({ medium_count: 3, track_count: 4 });
    setup({ groups: [album] });
    const { history } = renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    const jump = within(screen.getByRole("navigation", { name: "介质" }));
    expect(jump.getAllByRole("link").map((link) => link.textContent)).toEqual(["第 1 张", "第 2 张", "第 3 张"]);
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
    expect(headerFact("介质")).toBeNull();
  });

  it("艺人和作曲分栏，其他署名在曲目信息里显示；未知时长写“—”并给读屏“时长未知”", async () => {
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
    expect(rowFacts(rows[0])).toBe("1Track 01艺人：Sample Orchestra作曲：Sample Composer1:51");
    expect(rowFacts(rows[1])).toBe("2Track 02艺人：Sample Soloist—时长未知");
    await userEvent.click(screen.getByText("Track 01"));
    let info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.queryByText("艺人", { exact: true })).toBeNull();
    expect(info.getByText("作曲").nextElementSibling?.textContent).toBe("Sample Composer");
    expect(info.getByText("指挥").nextElementSibling?.textContent).toBe("Sample Conductor");
    await userEvent.click(screen.getByText("Track 02"));
    info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.getByText("艺人").nextElementSibling?.textContent).toBe("Sample Soloist");
    expect(info.queryByText("作曲", { exact: true })).toBeNull();
    // 标题缺失写“（无标题）”；未知角色在曲目信息里原样显示。
    expect(rowFacts(rows[2])).toBe("3（无标题）艺人：Sample Orchestra2:11");
    await userEvent.click(screen.getByText("（无标题）"));
    info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.getByText("mixer").nextElementSibling?.textContent).toBe("Someone");
    // 有一首的时长未知，就不写总时长。
    expect(screen.queryByText("总时长")).toBeNull();
    // 不是每一首都有的署名不上提到页头。
    expect(sharedCreditsText()).toBeNull();
  });

  it("全部曲目共有的署名只在页头列一次：同一位作曲上提，各首不同的指挥留在曲目信息", async () => {
    const album = symphonies({ medium_count: 1, track_count: 3 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(
      album.representative.id,
      releaseDetailOf(album.representative, {
        media: mediaOf(album.representative, (_medium, track) => ({
          credits: [
            { role: "composer", name: "Sample Composer" },
            { role: "conductor", name: `Conductor ${track}` },
            ...(track === 1 ? [{ role: "performer", name: "Sample Orchestra" }] : []),
          ],
        })),
      }),
    );
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");
    expect(sharedCreditsText()).toBe("全部曲目：作曲： Sample Composer");
    for (const [index, row] of trackRows().entries()) {
      expect(row.textContent).not.toContain("Conductor");
      await userEvent.click(row);
      const info = within(screen.getByRole("region", { name: "曲目信息" }));
      expect(info.queryByText("作曲", { exact: true })).toBeNull();
      expect(info.queryByText("Sample Composer")).toBeNull();
      expect(info.queryByText("Sample Orchestra")).toBeNull();
      expect(info.getByText("指挥").nextElementSibling?.textContent).toBe(`Conductor ${index + 1}`);
    }
  });

  it("只有一首曲目：署名保留在曲目信息，页头没有“全部曲目”", async () => {
    const album = symphonies({ medium_count: 1, track_count: 1 });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");
    expect(sharedCreditsText()).toBeNull();
    await userEvent.click(screen.getByText("Track 01"));
    const info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.getByText("作曲").nextElementSibling?.textContent).toBe("Sample Composer");
    expect(info.getByText("指挥").nextElementSibling?.textContent).toBe("Sample Conductor");
  });

  it("整张都是 CUE 分轨：页头概况写一次“CUE 分轨”，曲目行不再逐行标出（取自真实的 CUE 样本）", async () => {
    const cue = decodeReleaseDetail(sampleBody("releases/get-cue"));
    const album = albumGroup(1, { title: cue.title, artist: cue.artist, album_artist: cue.album_artist ?? "Sample Cue Artist", year: cue.year, medium_count: 1, track_count: 2 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, { ...cue, id: album.representative.id });
    renderApp({ path: `/albums/${album.id}` });

    await screen.findByText("Cue One");
    expect(screen.getByText("Cue Two")).toBeTruthy();
    expect(headerFact("来源")).toBe("CUE 分轨");
    expect(screen.getAllByText("CUE 分轨")).toHaveLength(1);
    expect(trackRows().map((row) => rowFacts(row))).toEqual(["1Cue One2:00", "2Cue Two2:00"]);
    expect(headerFact("总时长")).toBe("4 分钟");
  });

  it("CUE 分轨与普通文件混在一个版本里：只给 CUE 分轨的那几行标出", async () => {
    const cue = decodeReleaseDetail(sampleBody("releases/get-cue"));
    const [medium] = cue.media;
    if (medium === undefined) throw new Error("CUE 样本缺少介质");
    const [cueTrack, plainTrack] = medium.tracks;
    if (cueTrack === undefined || plainTrack === undefined) throw new Error("CUE 样本缺少曲目");
    const mixed = { ...cue, media: [{ ...medium, tracks: [cueTrack, { ...plainTrack, source_kind: "file" as const, cue: null }] }] };
    const album = albumGroup(1, { title: cue.title, artist: cue.artist, album_artist: cue.album_artist ?? "Sample Cue Artist", year: cue.year, medium_count: 1, track_count: 2 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, { ...mixed, id: album.representative.id });
    renderApp({ path: `/albums/${album.id}` });

    await screen.findByText("Cue One");
    expect(headerFact("来源")).toBeNull();
    expect(trackRows().map((row) => rowFacts(row))).toEqual(["1Cue OneCUE 分轨2:00", "2Cue Two2:00"]);
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

describe("专辑详情：曲目选中与信息", () => {
  it("按接口顺序显示曲目，缺号时列表提供读屏文字，曲目信息只显示碟号和时长", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, releaseDetailOf(album.representative, {
      media: mediaOf(album.representative, (medium, track) => (
        medium === 1 ? { position: track === 1 ? 7 : 0 } : {}
      )),
    }));
    renderApp({ path: `/albums/${album.id}` });

    const firstDisc = within(await screen.findByRole("region", { name: /^第 1 张/ }));
    const rows = firstDisc.getAllByRole("listitem");
    expect(rows.map(rowFacts)).toEqual(["7Track 1-011:51", "—曲号未知Track 1-022:01"]);
    const [knownRow, unknownRow] = rows;
    if (knownRow === undefined || unknownRow === undefined) throw new Error("缺少曲目行");
    const unknown = within(unknownRow);
    expect(unknown.getByText("—").getAttribute("aria-hidden")).toBe("true");
    expect(isInaccessible(unknown.getByText("曲号未知"))).toBe(false);
    expect(within(knownRow).queryByText("曲号未知")).toBeNull();

    await userEvent.click(unknown.getByText("Track 1-02"));
    let info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.getByText("第 1 张 · 2:01")).toBeTruthy();
    expect(info.queryByText(/第 \d+ 首/)).toBeNull();

    await userEvent.click(within(knownRow).getByText("Track 1-01"));
    info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.getByText("第 1 张 · 第 7 首 · 1:51")).toBeTruthy();
  });

  it("单击行在发行信息后显示曲目信息，再次单击、Esc 或关闭按钮取消选中", async () => {
    const album = symphonies({ medium_count: 1, track_count: 2 });
    const { catalog } = setup({ groups: [album] });
    catalog.releases.set(album.representative.id, releaseDetailOf(album.representative, {
      credits: [{ role: "album_artist", name: "Sample Orchestra" }, { role: "producer", name: "Sample Producer" }],
    }));
    renderApp({ path: `/albums/${album.id}` });
    const title = await screen.findByText("Track 01");
    const row = title.closest("li");
    if (row === null) throw new Error("缺少曲目行");
    await userEvent.click(title);
    expect(row.hasAttribute("data-selected")).toBe(true);
    expect(within(row).getByText("，已选中")).toBeTruthy();
    const aside = screen.getByRole("complementary", { name: "发行信息" });
    expect(within(aside).getAllByRole("region").map((region) => within(region).getByRole("heading").textContent)).toEqual(["发行信息", "制作人员", "曲目信息"]);
    const credits = within(screen.getByRole("region", { name: "制作人员" }));
    expect(credits.getByText("制作人").nextElementSibling?.textContent).toBe("Sample Producer");
    expect(credits.queryByText("专辑艺人")).toBeNull();
    const info = within(screen.getByRole("region", { name: "曲目信息" }));
    expect(info.getByText("第 1 首 · 1:51")).toBeTruthy();
    expect(info.queryByText("艺人", { exact: true })).toBeNull();
    expect(info.queryByText("Sample Composer")).toBeNull();
    expect(info.queryByText("Sample Conductor")).toBeNull();
    await userEvent.click(title);
    expect(screen.queryByRole("region", { name: "曲目信息" })).toBeNull();
    expect(row.hasAttribute("data-selected")).toBe(false);
    await userEvent.click(title);
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("region", { name: "曲目信息" })).toBeNull();
    await userEvent.click(title);
    await userEvent.click(screen.getByRole("button", { name: "关闭曲目信息" }));
    expect(screen.queryByRole("region", { name: "曲目信息" })).toBeNull();
  });

  it("右键和更多菜单的曲目信息项都选中对应行并聚焦标题", async () => {
    const album = symphonies({ medium_count: 1, track_count: 2 });
    setup({ groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");
    for (const [index, row] of trackRows().entries()) {
      if (index === 0) fireEvent.contextMenu(row, { button: 2, clientX: 100, clientY: 100 });
      else await userEvent.click(within(row).getByRole("button", { name: "更多操作" }));
      const menu = screen.getByRole("menu");
      expect(within(menu).getAllByRole("menuitem").at(-1)?.textContent).toBe("曲目信息");
      await userEvent.click(within(menu).getByRole("menuitem", { name: "曲目信息" }));
      expect(row.hasAttribute("data-selected")).toBe(true);
      const info = screen.getByRole("region", { name: "曲目信息" });
      expect(within(info).getByText(`Track 0${index + 1}`)).toBeTruthy();
      expect(document.activeElement).toBe(within(info).getByRole("heading", { name: "曲目信息" }));
      await userEvent.click(screen.getByRole("button", { name: "关闭曲目信息" }));
    }
  });
});

describe("专辑详情：修正曲目信息（M7 I）", () => {
  it("管理员在曲目菜单里修正曲目标题：只提交改了的字段，带曲目自己的版本号；成员没有这一项", async () => {
    const album = symphonies({ medium_count: 1, track_count: 2 });
    const { api } = setup({ groups: [album] });
    api.on("GET", /^\/api\/v1\/tracks\/[^/]+\/corrections$/, () => sampleResponse("tracks/corrections-get"));
    api.on("PATCH", /^\/api\/v1\/tracks\/[^/]+\/corrections$/, () => sampleResponse("tracks/corrections-patched"));
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");
    const [row] = trackRows();
    if (row === undefined) throw new Error("缺少曲目行");
    await userEvent.click(within(row).getByRole("button", { name: "更多操作" }));
    await userEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "修正曲目信息…" }));
    const dialog = await screen.findByRole("dialog", { name: /修正曲目信息/ });
    const title = await within(dialog).findByRole("group", { name: /曲目标题/ });
    expect(within(title).getByText("扫描结果不确定")).toBeTruthy();
    await userEvent.click(within(title).getByRole("radio", { name: "自定义" }));
    const input = within(title).getByRole("textbox", { name: "曲目标题（自定义）" });
    await userEvent.clear(input);
    await userEvent.click(within(dialog).getByRole("button", { name: "保存修改" }));
    expect(within(dialog).getByText("曲目标题不能留空。")).toBeTruthy();
    await userEvent.type(input, "前奏曲");
    expect(within(dialog).getByText("将修改：曲目标题。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "保存修改" }));
    await waitFor(() => expect(api.requestsTo("PATCH", /\/api\/v1\/tracks\/[^/]+\/corrections$/)[0]?.body).toEqual({ expected_revision: 1, changes: { title: { mode: "set", value: "前奏曲" } } }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("成员的曲目菜单里没有修正曲目信息", async () => {
    const album = symphonies({ medium_count: 1, track_count: 1 });
    setup({ role: "user", groups: [album] });
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 01");
    const [row] = trackRows();
    if (row === undefined) throw new Error("缺少曲目行");
    await userEvent.click(within(row).getByRole("button", { name: "更多操作" }));
    expect(within(screen.getByRole("menu")).queryByRole("menuitem", { name: "修正曲目信息…" })).toBeNull();
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
    expect(document.querySelector('img[src^="/api/v1/artworks/"]')).toBeNull();
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
  it("专辑列表按接口顺序显示代表版本的原样标题，进入详情后返回仍保持顺序", async () => {
    const beach = albumGroup(1, { title: "Beach Boys" });
    const beatles = albumGroup(2, { title: "The Béa!tles" });
    const les = albumGroup(3, { title: "Les amis" });
    const theatre = albumGroup(4, { title: "Theatre" });
    setup({ groups: [beach, beatles, les, theatre] });
    renderApp({ path: "/albums" });

    const cardNames = /^(Beach Boys|The Béa!tles|Les amis|Theatre)/;
    const cardHrefs = () => screen.getAllByRole("link", { name: cardNames }).map((link) => link.getAttribute("href"));
    const expected = [beach, beatles, les, theatre].map((group) => `/albums/${group.id}`);
    await screen.findByRole("link", { name: /^The Béa!tles/ });
    expect(cardHrefs()).toEqual(expected);
    await userEvent.click(screen.getByRole("link", { name: /^The Béa!tles/ }));
    expect(await heading("The Béa!tles")).toBeTruthy();
    await userEvent.click(screen.getByRole("link", { name: "返回专辑列表" }));
    await screen.findByRole("link", { name: /^The Béa!tles/ });
    expect(cardHrefs()).toEqual(expected);
  });

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

  it("组详情读取失败（503）：错误（不显示请求 ID）与重试，重试成功后显示专辑", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.groupFailure = sampleResponse("errors/database-unavailable");
    renderApp({ path: `/albums/${album.id}` });
    // 503 会被自动重试（最多 2 次，带退避），耗尽后才显示错误。
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).queryByText(/请求 ID/)).toBeNull();

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

describe("专辑详情：外部识别", () => {
  const matchPath = (releaseId: string) => `/api/v1/admin/curation/releases/${releaseId}/match-results`;

  it.each(["admin", "user"] as const)("没有外部识别记录时隐藏区块，%s 的读取遵守角色权限", async (role) => {
    const album = symphonies();
    const { api } = setup({ role, groups: [album] });
    const path = matchPath(album.representative.id);
    if (role === "admin") api.on("GET", path, () => sampleResponse("curation/match-results-empty"));
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");

    await waitFor(() => expect(api.requestsTo("GET", path)).toHaveLength(role === "admin" ? 1 : 0));
    expect(screen.queryByText("外部识别")).toBeNull();
    expect(api.requestsTo("POST", /^\/api\/v1\/admin\/curation\/releases\/[^/]+\/recheck$/)).toHaveLength(0);
  });

  it("折叠时显示来源摘要，展开后读出各行详情，普通未匹配可请求重查", async () => {
    const album = symphonies();
    const id = album.representative.id;
    const { api } = setup({ groups: [album] });
    const user = userEvent.setup();
    const checkedAt = "2026-01-05T08:30:00Z";
    const queried = { checked_at: checkedAt, reason: "", conflicts: [] };
    const results: ReleaseMatchResultDTO[] = [
      { ...queried, source: "musicbrainz", status: "unmatched" },
      { ...queried, source: "acoustid", status: "unmatched", reason: "candidates_revoked" },
      { ...queried, source: "coverartarchive", status: "matched", checked_at: null },
      { ...queried, source: "lrclib", status: "conflict", conflicts: ["track_count"] },
    ];
    const recheck = `/api/v1/admin/curation/releases/${id}/recheck`;
    api.on("GET", matchPath(id), () => jsonResponse(200, { results }));
    api.on("POST", recheck, () => new Response(null, { status: 202, headers: { "Content-Length": "0" } }));

    renderApp({ path: `/albums/${album.id}` });
    const title = await screen.findByText("外部识别");
    const disclosure = title.closest("details");
    if (disclosure === null) throw new Error("外部识别缺少折叠区块");
    const block = within(disclosure);
    expect(block.getByText("（未匹配 · MusicBrainz；未匹配 · AcoustID；已匹配 · Cover Art Archive；有冲突 · LRCLIB）")).toBeTruthy();
    expect(block.queryByRole("list")).toBeNull();
    await user.click(title);

    const rows = within(await block.findByRole("list")).getAllByRole("listitem");
    expect(rows).toHaveLength(4);
    const [musicbrainz, revoked, matched, conflict] = rows.map((row) => within(row));
    if (musicbrainz === undefined || revoked === undefined || matched === undefined || conflict === undefined) {
      throw new Error("外部识别缺少来源结果行");
    }
    expect(musicbrainz.getByText("未匹配")).toBeTruthy();
    expect(musicbrainz.getByText("MusicBrainz")).toBeTruthy();
    expect(musicbrainz.getByText(`，${formatDateTime(checkedAt)} 查询`).closest('[aria-hidden="true"]')).toBeNull();
    expect(revoked.getByText("未匹配")).toBeTruthy();
    expect(revoked.getByText("AcoustID")).toBeTruthy();
    expect(revoked.getByText(/候选已撤销/).closest('[aria-hidden="true"]')).toBeNull();
    expect(matched.getByText("已匹配")).toBeTruthy();
    expect(matched.getByText("Cover Art Archive")).toBeTruthy();
    expect(conflict.getByText("有冲突")).toBeTruthy();
    expect(conflict.getByText("LRCLIB")).toBeTruthy();
    expect(conflict.getByText(/曲数不同/).closest('[aria-hidden="true"]')).toBeNull();
    for (const row of [revoked, matched, conflict]) expect(row.queryByRole("button")).toBeNull();

    await user.click(musicbrainz.getByRole("button", { name: "重新查询" }));
    expect(await musicbrainz.findByRole("button", { name: "已排上重查" })).toHaveProperty("disabled", true);
    const requests = api.requestsTo("POST", recheck);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toBeUndefined();
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
    expect(within(rows[1] as HTMLElement).getAllByRole("cell").map((cell) => cell.textContent)).toEqual(["音频标签", "高", "直接采用"]);
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

  it("字段证据显示平票原因、候选值和未选值", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.evidence.set(album.representative.id, releaseEvidenceOf(album.representative, {
      fields: [{
        field: "label",
        source: "tag",
        confidence: "low",
        action: "uncertain_apply",
        rule_id: "majority_v1",
        value: null,
        candidates: ["Label A", "Label B"],
        reason_code: "tied_candidates",
      }],
    }));
    renderApp({ path: `/albums/${album.id}` });
    await userEvent.click(await screen.findByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));

    const fields = within(await screen.findByRole("table", { name: "字段证据" }));
    expect(fields.getByRole("rowheader", { name: "厂牌" })).toBeTruthy();
    expect(fields.getAllByRole("cell").map((cell) => cell.textContent)).toEqual([
      "—", "Label ALabel B", "有冲突：几首的取值票数相同，没有填",
    ]);
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

  it("完整证据 503：错误（不显示请求 ID），页面其他部分不受影响", async () => {
    const album = symphonies();
    const { catalog } = setup({ groups: [album] });
    catalog.evidenceFailure = sampleResponse("errors/database-unavailable");
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    await userEvent.click(await screen.findByRole("button", { name: "查看完整证据" }));
    const alert = await screen.findByRole("alert", {}, { timeout: 8000 });
    expect(within(alert).queryByText(/请求 ID/)).toBeNull();
    expect(screen.getByRole("table", { name: "证据摘要" })).toBeTruthy();
  }, 15_000);

  it("需要关注的版本：头部有标签，“查看来源与证据”展开页面底部的那个区块并把焦点放上去；没有需要关注的版本没有这个标签", async () => {
    const attention = symphonies({ attention_count: 2 });
    setup({ groups: [attention] });
    const { view } = renderApp({ path: `/albums/${attention.id}` });
    await screen.findByText("Track 1-01");
    expect(screen.getByText("需要关注")).toBeTruthy();
    const summary = screen.getByText("来源与证据").closest("summary");
    const details = summary?.parentElement;
    expect(details instanceof HTMLDetailsElement && details.open).toBe(false);
    const scroll = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scroll;
    try {
      await userEvent.click(screen.getByRole("button", { name: "查看来源与证据" }));
    } finally {
      Element.prototype.scrollIntoView = original;
    }
    expect(details instanceof HTMLDetailsElement && details.open).toBe(true);
    expect(scroll).toHaveBeenCalled();
    expect(document.activeElement).toBe(summary);
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
        credits: [
          { role: "unknown_role", name: "Someone Else" },
          { role: "lyricist", name: "Sample Lyricist" },
          { role: "arranger", name: "Sample Arranger" },
        ],
      }),
    );
    renderApp({ path: `/albums/${album.id}` });
    await screen.findByText("Track 1-01");
    await userEvent.click(screen.getByText("来源与证据"));
    const table = await summaryTable();
    const rows = within(table).getAllByRole("row");
    expect(rows.map((row) => row.textContent)).toEqual([
      "字段来源可信度处理方式",
      "年份CUE 文件（专辑）中不确定，已暂时采用",
      "new_fieldnew_sourceunsurenew_action",
    ]);
    // 未知角色原样显示；新增的已知角色显示中文名，并保留对应署名。
    const credits = within(screen.getByRole("region", { name: "制作人员" }));
    expect(credits.queryByText("专辑艺人")).toBeNull();
    expect(credits.getByText("unknown_role").nextElementSibling?.textContent).toBe("Someone Else");
    expect(credits.getByText("作词").nextElementSibling?.textContent).toBe("Sample Lyricist");
    expect(credits.getByText("编曲").nextElementSibling?.textContent).toBe("Sample Arranger");
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
    // 曲目艺人在独立栏目里，第二首与专辑艺人不同。
    expect(trackRows()[1]?.querySelector('p[title="Sample Soloist"]')?.textContent).toBe("艺人：Sample Soloist");
    expect(trackRows()[0]?.querySelector('p[title="Sample Orchestra"]')?.textContent).toBe("艺人：Sample Orchestra");
    // 样本里三首的署名相同：页头列一次，曲目行不再重复。
    expect(sharedCreditsText()).toBe("全部曲目：作词： Sample Lyricist；作曲： Sample Composer；编曲： Sample Arranger；指挥： Sample Conductor；小提琴： Sample Soloist");
    for (const row of trackRows()) expect(row.textContent).not.toContain("Sample Lyricist");
  });
});
