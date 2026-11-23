import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { OperationChangesDTO } from "../features/governance/api/decoders";
import { albumGroup } from "../test/catalog-data";
import { enableCuration, refreshSources, seedBatch, seedSuppression, type CurationData } from "../test/curation-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installCurationApi } from "../test/fake-curation";
import { installGovernanceApi } from "../test/fake-governance";
import { jsonResponse } from "../test/http";
import { seedAutoOperation, type GovernanceData } from "../test/governance-data";
import { renderApp } from "../test/render-app";

const albumA = () => albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, artwork: true });
const albumB = () => albumGroup(2, { title: "Beta Quartets", artist: "Beta Quartet", year: 1990 });
const releaseOf = (group: ReturnType<typeof albumA>) => group.representative.id;
const BATCH = "00000000-0000-4000-8000-0000000000b1";

function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: [albumA(), albumB()] });
  const governance = installGovernanceApi(api, catalog);
  const curation = installCurationApi(api, governance);
  return { api, catalog, governance, curation };
}

const linkChanges = (title: string): OperationChangesDTO => ({
  kind: "external_link",
  source: "musicbrainz",
  method: "barcode",
  confidence: "certain",
  external: { title, artist: "Sample Artist", date: "2015", country: "JP" },
  links: [{ object_type: "release", object_id: releaseOf(albumA()), external_id: "11111111-1111-4111-8111-111111111111" }],
});

/** 一批两条：A 的外部对应与 B 的外部对应，都来自 MusicBrainz。 */
function seedBatchOperations(governance: GovernanceData, curation: CurationData) {
  seedBatch(curation, { id: BATCH, openedAt: "2026-01-05T00:00:00Z" });
  seedAutoOperation(governance, { type: "external_link", releaseId: releaseOf(albumA()), changes: linkChanges("Sample Symphonies"), provider: "musicbrainz", basis: "MusicBrainz 发行，依据条码", batchId: BATCH });
  seedAutoOperation(governance, { type: "external_link", releaseId: releaseOf(albumB()), changes: linkChanges("Beta Quartets"), provider: "musicbrainz", batchId: BATCH });
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const group = (name: string) => screen.findByRole("region", { name });

describe("自动整理设置：入口与权限", () => {
  it("管理员在设置分栏看到“自动整理”，点进去是设置页的五组", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/settings/account" });
    const nav = await screen.findByRole("navigation", { name: "设置" });
    await user.click(within(nav).getByRole("link", { name: "自动整理" }));
    expect(await heading("自动整理")).toBeTruthy();
    expect(history.location.pathname).toBe("/settings/curation");
    for (const name of ["自动整理", "进度", "外部源", "批次", "不再提出"]) expect(await group(name)).toBeTruthy();
  });

  it("成员打开地址看到需要管理员权限，不请求自动整理接口", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: "/settings/curation" });
    expect(await screen.findByRole("heading", { level: 1, name: "需要管理员权限" })).toBeTruthy();
    expect(api.requests.filter((request) => request.path.startsWith("/api/v1/admin/curation"))).toHaveLength(0);
  });
});

describe("总开关与暂停", () => {
  it("从关到开先确认，确认框列出开着的源会收到什么；取消不发请求，确认后暂停开关出现", async () => {
    const { api, curation } = setup();
    curation.sources = curation.sources.map((source) => (source.id === "lrclib" ? { ...source, enabled: false } : source));
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const toggle = await screen.findByRole("switch", { name: "自动整理" });
    expect(screen.queryByRole("switch", { name: "暂停" })).toBeNull();

    await user.click(toggle);
    const dialog = await screen.findByRole("dialog", { name: "打开自动整理？" });
    expect(within(dialog).getByText("MusicBrainz")).toBeTruthy();
    expect(within(dialog).getByText("Cover Art Archive")).toBeTruthy();
    expect(within(dialog).queryByText("LRCLIB")).toBeNull();
    expect(within(dialog).getByText(/不发送音频、文件路径/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")).toHaveLength(0);
    expect((toggle as HTMLInputElement).checked).toBe(false);

    await user.click(toggle);
    await user.click(within(await screen.findByRole("dialog", { name: "打开自动整理？" })).getByRole("button", { name: "打开" }));
    await waitFor(() => expect((screen.getByRole("switch", { name: "自动整理" }) as HTMLInputElement).checked).toBe(true));
    expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")[0]?.body).toEqual({ enabled: true });
    expect(await screen.findByRole("switch", { name: "暂停" })).toBeTruthy();
    expect(await screen.findByText(/由 curator 于 .* 打开/)).toBeTruthy();
  });

  it("关闭不确认；暂停与继续直接保存", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    await user.click(await screen.findByRole("switch", { name: "暂停" }));
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")[0]?.body).toEqual({ paused: true }));
    await user.click(screen.getByRole("switch", { name: "自动整理" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")[1]?.body).toEqual({ enabled: false }));
    await waitFor(() => expect(screen.queryByRole("switch", { name: "暂停" })).toBeNull());
  });

  it("保存失败时回到服务器的值并就近提示", async () => {
    const { api } = setup();
    api.on("PUT", "/api/v1/admin/curation/settings", () => new Response(JSON.stringify({ error: { code: "database_unavailable", message: "数据库暂时不可用" }, request_id: "r" }), { status: 503, headers: { "Content-Type": "application/json" } }));
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    await user.click(await screen.findByRole("switch", { name: "自动整理" }));
    await user.click(within(await screen.findByRole("dialog", { name: "打开自动整理？" })).getByRole("button", { name: "打开" }));
    expect(await screen.findByText("设置没有改成")).toBeTruthy();
    expect((screen.getByRole("switch", { name: "自动整理" }) as HTMLInputElement).checked).toBe(false);
  });
});

describe("进度", () => {
  it("从未打开过时收成一行；打开后写出状态与计数", async () => {
    const { curation } = setup();
    curation.releases = { total: 12, processed: 4, matched: 3, uncertain: 1, no_match: 0, try_later: 0 };
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByText("未打开 · 曲库共 12 个发行")).toBeTruthy();
  });

  it("工作中显示已处理比例、结果、待定项与当前批次", async () => {
    const { curation, governance } = setup();
    enableCuration(curation);
    seedBatch(curation, { id: BATCH, openedAt: "2026-01-05T00:00:00Z", closedAt: null });
    seedAutoOperation(governance, { type: "external_link", releaseId: releaseOf(albumA()), changes: linkChanges("Sample Symphonies"), provider: "musicbrainz", batchId: BATCH });
    curation.state = "running";
    curation.releases = { total: 10, processed: 4, matched: 3, uncertain: 1, no_match: 0, try_later: 2 };
    curation.waitingModel = 1;
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByText("正在整理")).toBeTruthy();
    expect(within(progress).getByRole("progressbar", { name: "已处理的发行" }).getAttribute("aria-valuenow")).toBe("4");
    expect(within(progress).getByText("对上 3 · 拿不准 1 · 没对上 0 · 稍后再试 2")).toBeTruthy();
    expect(within(progress).getByText("1 项")).toBeTruthy();
    expect(within(progress).getByRole("link", { name: "查看改动" }).getAttribute("href")).toBe(`/settings/operations?batch=${BATCH}`);
  });
});

describe("外部源", () => {
  it("总开关关着时在分组层面说明；开关即改即存，关着的源写出原因", async () => {
    const { api, curation } = setup();
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("自动整理没有打开，后台不使用任何外部源。")).toBeTruthy();
    enableCuration(curation);
    await user.click(await within(sources).findByRole("switch", { name: "LRCLIB" }));
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/sources/lrclib")[0]?.body).toEqual({ enabled: false }));
    expect(await within(sources).findByText("歌词；已关闭，后台不使用它。")).toBeTruthy();
  });

  it("按来源撤销：确认后分段撤销直到做完，结果写出撤了多少；查看改动进入按来源筛选的操作记录", async () => {
    const { api, governance, curation } = setup();
    seedBatchOperations(governance, curation);
    curation.revertPageSize = 1;
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    await user.click(await within(sources).findByRole("button", { name: "MusicBrainz 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "撤销来自 MusicBrainz 的全部自动修改" }));
    const dialog = await screen.findByRole("dialog", { name: "撤销来自 MusicBrainz 的全部自动修改？" });
    expect(await within(dialog).findByText(/撤销来自 MusicBrainz 的 2 条自动修改/)).toBeTruthy();
    expect(within(dialog).getByText(/这个源还开着/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    expect(await within(sources).findByText(/撤销了 2 条。/)).toBeTruthy();
    const calls = api.requestsTo("POST", "/api/v1/admin/curation/reverts");
    expect(calls).toHaveLength(2);
    expect(calls[0]?.body).toEqual({ scope: { kind: "provider", provider: "musicbrainz" } });
    expect((calls[1]?.body as { cursor?: string }).cursor).toBeTypeOf("string");
    expect(calls[0]?.headers.get("Idempotency-Key")).toBe(calls[1]?.headers.get("Idempotency-Key"));

    await user.click(within(sources).getByRole("button", { name: "MusicBrainz 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "查看来自 MusicBrainz 的改动" }));
    await waitFor(() => expect(history.location.pathname).toBe("/settings/operations"));
    expect(history.location.search).toContain("provider=musicbrainz");
    expect(await screen.findByText("只显示来自 MusicBrainz 的自动整理的记录。")).toBeTruthy();
  });
});

describe("AcoustID 与两把密钥", () => {
  const acoustid = (curation: CurationData) => curation.sources.find((source) => source.id === "acoustid");
  const keyRow = (sources: HTMLElement, label: string) => within(sources).getByRole("form", { name: `AcoustID 的${label}` });

  it("确认框写明 AcoustID 收到什么，密钥没填全时注明“填入密钥后”", async () => {
    setup();
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    await user.click(await screen.findByRole("switch", { name: "自动整理" }));
    const dialog = await screen.findByRole("dialog", { name: "打开自动整理？" });
    const item = within(dialog).getByText("AcoustID").closest("li");
    expect(item?.textContent).toBe("AcoustID（填入密钥后）：由音频算出的声学指纹（不能还原成音频）与时长，以及你填入的应用密钥与用户密钥");
  });

  it("两把密钥分别填写、更换与清除：只显示末四位；都填了才可用；AcoustID 没有“来自它的改动”的菜单", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("声学指纹；对上的结果记在 MusicBrainz 名下；填入密钥后可用。")).toBeTruthy();
    expect(within(sources).queryByRole("button", { name: "AcoustID 的更多操作" })).toBeNull();
    expect(within(sources).getByRole("link", { name: "注册一个应用" }).getAttribute("href")).toBe("https://acoustid.org/new-application");
    expect(within(sources).getByRole("link", { name: "这个页面" }).getAttribute("href")).toBe("https://acoustid.org/api-key");

    const application = keyRow(sources, "应用密钥");
    const save = within(application).getByRole("button", { name: "保存" });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    await user.type(within(application).getByLabelText("应用密钥"), "  abcdEFGH1234  ");
    await user.click(save);
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/sources/acoustid/keys/application")[0]?.body).toEqual({ key: "abcdEFGH1234" }));
    expect(await within(sources).findByText("已保存，末四位 1234")).toBeTruthy();
    // 只填了一把仍不可用。
    expect(within(sources).getByText("声学指纹；对上的结果记在 MusicBrainz 名下；填入密钥后可用。")).toBeTruthy();

    await user.type(within(keyRow(sources, "用户密钥")).getByLabelText("用户密钥"), "userKEY9876");
    await user.click(within(keyRow(sources, "用户密钥")).getByRole("button", { name: "保存" }));
    expect(await within(sources).findByText("已保存，末四位 9876")).toBeTruthy();
    expect(within(sources).getByText("声学指纹；对上的结果记在 MusicBrainz 名下")).toBeTruthy();
    expect(within(sources).queryByRole("form")).toBeNull();

    const [replaceApplication] = within(sources).getAllByRole("button", { name: "更换" });
    if (replaceApplication === undefined) throw new Error("缺少更换按钮");
    await user.click(replaceApplication);
    expect(keyRow(sources, "应用密钥")).toBeTruthy();
    await user.click(within(sources).getByRole("button", { name: "取消" }));
    expect(within(sources).getByText("已保存，末四位 1234")).toBeTruthy();

    const [, clearUser] = within(sources).getAllByRole("button", { name: "清除" });
    if (clearUser === undefined) throw new Error("缺少清除按钮");
    await user.click(clearUser);
    await waitFor(() => expect(api.requestsTo("DELETE", "/api/v1/admin/curation/sources/acoustid/keys/user")).toHaveLength(1));
    expect(await within(sources).findByLabelText("用户密钥")).toBeTruthy();
    expect(within(sources).getByText("已保存，末四位 1234")).toBeTruthy();
    expect(within(sources).getByText("声学指纹；对上的结果记在 MusicBrainz 名下；填入密钥后可用。")).toBeTruthy();
  });

  it("某一把无效、指纹工具不可用时写出原因；关掉 AcoustID 后密钥的行不出现", async () => {
    const { curation } = setup();
    enableCuration(curation);
    const source = acoustid(curation);
    if (source === undefined) throw new Error("缺少 AcoustID");
    source.keys = [
      { configured: true, last_four: "wxyz", name: "application", status: "valid" },
      { configured: true, last_four: "u123", name: "user", status: "invalid" },
    ];
    refreshSources(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("声学指纹；对上的结果记在 MusicBrainz 名下；密钥无效，请重新填写。")).toBeTruthy();
    expect(within(sources).getAllByText(/AcoustID 说这把密钥无效/)).toHaveLength(1);
    expect(within(sources).getByText("已保存，末四位 u123")).toBeTruthy();

    await user.click(within(sources).getByRole("switch", { name: "AcoustID" }));
    await waitFor(() => expect(within(sources).queryByText("已保存，末四位 wxyz")).toBeNull());
    expect(await within(sources).findByText("声学指纹；对上的结果记在 MusicBrainz 名下；已关闭，后台不使用它。")).toBeTruthy();
  });

  it("服务器缺少指纹工具时说明不能使用", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.fingerprintAvailable = false;
    refreshSources(curation);
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("声学指纹；对上的结果记在 MusicBrainz 名下；这台服务器缺少指纹工具（fpcalc），暂时不能使用。")).toBeTruthy();
  });

  it("保存失败时就近提示是哪一把，输入框保留", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    api.on("PUT", "/api/v1/admin/curation/sources/acoustid/keys/user", () => jsonResponse(400, { error: { code: "invalid_input", message: "密钥无效" }, request_id: "r1" }));
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    await user.type(await within(sources).findByLabelText("用户密钥"), "bad key");
    await user.click(within(keyRow(sources, "用户密钥")).getByRole("button", { name: "保存" }));
    expect(await within(sources).findByText("用户密钥没有保存")).toBeTruthy();
    expect((within(sources).getByLabelText("用户密钥") as HTMLInputElement).value).toBe("bad key");
  });
});

describe("批次", () => {
  it("列出批次；撤销这一批后读到权威总数", async () => {
    const { api, governance, curation } = setup();
    seedBatchOperations(governance, curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const batches = await group("批次");
    const table = await within(batches).findByRole("table", { name: "自动整理的批次" });
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    await user.click(within(table).getByRole("button", { name: /开始的批次的操作/ }));
    await user.click(await screen.findByRole("menuitem", { name: "撤销这一批" }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这一批？" });
    expect(within(dialog).getByText(/还能撤销的 2 条自动修改/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "撤销" }));
    expect(await within(batches).findByText(/这一批共 2 条，已撤销 2 条/)).toBeTruthy();
    expect(api.requestsTo("GET", `/api/v1/admin/curation/batches/${BATCH}`)).toHaveLength(1);
    expect(within(batches).getByRole("link", { name: "查看全部自动修改" }).getAttribute("href")).toBe("/settings/operations?source=curation");
  });

  it("没有批次时写空态", async () => {
    setup();
    renderApp({ path: "/settings/curation" });
    expect(await within(await group("批次")).findByText("还没有自动整理的记录。")).toBeTruthy();
  });
});

describe("不再提出", () => {
  it("列出标记（封面的一对只显示一行），清除前确认，清除后列表刷新", async () => {
    const { api, curation } = setup();
    const operation = "00000000-0000-4000-8000-0000000000c9";
    seedSuppression(curation, { id: "00000000-0000-4000-8000-0000000000d1", target_type: "release", target_id: releaseOf(albumA()), subject: "year", value: { mode: "set", value: 1999 }, operation_id: operation, created_at: "2026-01-05T00:00:00Z" });
    seedSuppression(curation, { id: "00000000-0000-4000-8000-0000000000d2", target_type: "release", target_id: releaseOf(albumB()), subject: "artwork", value: { source: "coverartarchive", external_id: "42" }, operation_id: operation, created_at: "2026-01-05T00:00:01Z" });
    seedSuppression(curation, { id: "00000000-0000-4000-8000-0000000000d3", target_type: "release", target_id: releaseOf(albumB()), subject: "artwork_source", value: { source: "coverartarchive", external_id: "42" }, operation_id: operation, created_at: "2026-01-05T00:00:01Z" });
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const markers = await group("不再提出");
    const table = await within(markers).findByRole("table", { name: "不再提出的标记" });
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(within(table).getByText("1999")).toBeTruthy();
    expect(within(table).getByText("Cover Art Archive · 42")).toBeTruthy();
    await user.click(within(table).getByRole("button", { name: /的封面标记/ }));
    await user.click(within(await screen.findByRole("dialog", { name: "清除这条标记？" })).getByRole("button", { name: "清除" }));
    await waitFor(() => expect(within(markers).getAllByRole("row")).toHaveLength(2));
    expect(api.requestsTo("DELETE", /\/suppressions\//)).toHaveLength(1);
    expect(curation.suppressions.map((item) => item.subject)).toEqual(["year"]);
  });
});

describe("操作记录的自动整理筛选", () => {
  it("按批次筛选，显示依据；查看全部清掉筛选", async () => {
    const { governance, curation } = setup();
    seedBatchOperations(governance, curation);
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/settings/operations?batch=${BATCH}` });
    expect(await screen.findByText("只显示这一批自动整理的记录。")).toBeTruthy();
    expect(await screen.findByText("依据：MusicBrainz 发行，依据条码")).toBeTruthy();
    expect(screen.getAllByText("自动整理 · MusicBrainz").length).toBe(2);
    await user.click(screen.getByRole("link", { name: "查看全部记录" }));
    await waitFor(() => expect(history.location.search).toBe(""));
  });
});

describe("概览", () => {
  it("曲库里有发行而从未打开过：管理员看到提示，不再提示之后不再出现", async () => {
    const { curation } = setup();
    curation.releases = { total: 3, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0 };
    const user = userEvent.setup();
    renderApp({ path: "/" });
    expect(await screen.findByText("曲库扫描好了")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "不再提示" }));
    await waitFor(() => expect(screen.queryByText("曲库扫描好了")).toBeNull());
    expect(window.localStorage.getItem("roomusic.curation.first-scan-hint.00000000-0000-4000-8000-000000000002")).toBe("dismissed");
    window.localStorage.clear();
  });

  it("打开过之后不提示；成员不提示也不请求", async () => {
    const { curation } = setup();
    curation.releases = { total: 3, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0 };
    enableCuration(curation);
    renderApp({ path: "/" });
    expect(await screen.findByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    await waitFor(() => expect(screen.getAllByText("自动整理").length).toBeGreaterThan(0));
    expect(screen.queryByText("曲库扫描好了")).toBeNull();
  });
});
