import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { decodeCurationSources } from "../features/curation/api/decoders";
import type { OperationChangesDTO } from "../features/governance/api/decoders";
import { sampleBody } from "../test/api-samples";
import { albumGroup, failure } from "../test/catalog-data";
import { enableCuration, refreshSources, seedBatch, seedException, seedSuppression, type CurationData } from "../test/curation-data";
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

const httpFailure = (status: number, code: string, message: string) => {
  const result = failure(status, code, message);
  return jsonResponse(result.status, result.body);
};

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });
const group = (name: string) => screen.findByRole("region", { name });

describe("自动整理设置：入口与权限", () => {
  it("管理员在设置分栏看到“自动整理”，点进去是设置页的七组", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/settings/account" });
    const nav = await screen.findByRole("navigation", { name: "设置" });
    await user.click(within(nav).getByRole("link", { name: "自动整理" }));
    expect(await heading("自动整理")).toBeTruthy();
    expect(history.location.pathname).toBe("/settings/curation");
    for (const name of ["自动整理", "进度", "模型判断", "未采用的建议", "外部源", "批次", "不再提出"]) expect(await group(name)).toBeTruthy();
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

  it("同时运行：越界提示，合法值离开时保存；状态写出同时在跑的项数", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    curation.model = { ...curation.model, state: "running", items: { ...curation.model.items, in_progress: 3 }, running: 3 };
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = await group("模型判断");
    expect(await within(model).findByText(/同时 3\/4 项/)).toBeTruthy();
    const input = within(model).getByRole("spinbutton", { name: "同时运行" });
    await user.clear(input);
    await user.type(input, "20");
    await user.tab();
    expect(within(model).getByText("填 1 到 16 之间的整数。")).toBeTruthy();
    expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")).toHaveLength(0);
    await user.clear(input);
    await user.type(input, "8");
    await user.tab();
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings").at(-1)?.body).toEqual({ model_concurrency: 8 }));
  });

  it("重新巡检：图标按钮，确认后提交，写明已开始", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = await group("模型判断");
    await user.click(await within(model).findByRole("button", { name: "重新巡检全部名字" }));
    const dialog = within(await screen.findByRole("dialog", { name: "重新巡检全部名字？" }));
    expect(dialog.getByText(/会消耗模型用量/)).toBeTruthy();
    await user.click(dialog.getByRole("button", { name: "重新巡检" }));
    await waitFor(() => expect(api.requestsTo("POST", "/api/v1/admin/curation/repatrol")).toHaveLength(1));
    expect(curation.repatrols).toBe(1);
    expect(await within(model).findByText("已开始重新巡检。")).toBeTruthy();
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
    curation.releases = { total: 12, processed: 4, matched: 3, uncertain: 1, no_match: 0, try_later: 0, unlisted: 0, no_usable_candidates: 0 };
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByText("未打开 · 曲库共 12 个发行")).toBeTruthy();
  });

  it("工作中显示已处理比例、结果与当前批次；待定项写在“模型判断”里", async () => {
    const { curation, governance } = setup();
    enableCuration(curation);
    seedBatch(curation, { id: BATCH, openedAt: "2026-01-05T00:00:00Z", closedAt: null });
    seedAutoOperation(governance, { type: "external_link", releaseId: releaseOf(albumA()), changes: linkChanges("Sample Symphonies"), provider: "musicbrainz", batchId: BATCH });
    curation.state = "running";
    curation.releases = { total: 10, processed: 4, matched: 3, uncertain: 1, no_match: 0, try_later: 2, unlisted: 0, no_usable_candidates: 0 };
    curation.model.items = { ...curation.model.items, waiting_model: 1 };
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByText("正在整理")).toBeTruthy();
    expect(within(progress).getByRole("progressbar", { name: "已处理的发行" }).getAttribute("aria-valuenow")).toBe("4");
    expect(within(progress).getByText("对上 3 · 拿不准 1 · 没对上 0 · 稍后再试 2")).toBeTruthy();
    expect(within(await group("模型判断")).getByText("等模型 1 · 处理中 0 · 搁置 0")).toBeTruthy();
    expect(within(progress).getByRole("link", { name: "查看改动" }).getAttribute("href")).toBe(`/settings/operations?batch=${BATCH}`);
  });
});

describe("进度：队列、分类与佐证来源（整理链 G）", () => {
  const queue = (id: "release" | "artwork" | "lyrics" | "artist_media" | "fingerprint") => ({
    id,
    state: "idle" as const,
    remaining: 0,
    ready: 0,
    successful_requests_per_second: null,
    completed_items_per_second: null,
    eta_seconds: 0,
    pause_reason: null,
    source: null,
    retry_at: null,
  });

  it("每条队列一行：剩余、每分钟多少项与预计时间；被限流时写来源与恢复时刻；排空写已完成", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.state = "running";
    curation.queues = [
      { ...queue("release"), state: "paused", remaining: 120, eta_seconds: null, pause_reason: "rate_limited", source: "musicbrainz", retry_at: "2026-01-05T08:30:00Z" },
      { ...queue("artwork"), state: "running", remaining: 30, ready: 30, completed_items_per_second: 0.5, eta_seconds: 60 },
      { ...queue("lyrics"), state: "running", remaining: 900, ready: 900, completed_items_per_second: 0.25, eta_seconds: 3600 },
      queue("artist_media"),
      { ...queue("fingerprint"), remaining: 4, eta_seconds: null, pause_reason: "waiting_for_stability" },
    ];
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByLabelText(/^识别队列：MusicBrainz 限流，约 .+ 恢复 · 剩 120$/)).toBeTruthy();
    expect(within(progress).getByLabelText("封面队列：剩 30 · 每分钟 30 项 · 约 1 分钟")).toBeTruthy();
    expect(within(progress).getByLabelText("歌词队列：剩 900 · 每分钟 15 项 · 约 1 小时")).toBeTruthy();
    expect(within(progress).getByLabelText("艺人资料队列：已完成")).toBeTruthy();
    expect(within(progress).getByLabelText("声纹队列：等文件拷完 · 剩 4")).toBeTruthy();
  });

  it("结果写出未收录与无可用候选；本地分类写四类计数", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.releases = { total: 10, processed: 10, matched: 6, uncertain: 0, no_match: 0, try_later: 0, unlisted: 3, no_usable_candidates: 1 };
    curation.classifications = { total: 10, tagged_id: 2, has_gaps: 5, local_complete: 2, insufficient: 1 };
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByText("对上 6 · 拿不准 0 · 没对上 0 · 外部库未收录 3 · 无可用候选 1")).toBeTruthy();
    expect(await within(progress).findByText("带编号 2 · 有缺口 5 · 本地完整 2 · 信息不足 1")).toBeTruthy();
  });

  it("依据库内或网页写入：各写条数，菜单里可以查看与整批撤销", async () => {
    const { curation, governance } = setup();
    enableCuration(curation);
    seedAutoOperation(governance, { type: "external_link", releaseId: releaseOf(albumA()), changes: linkChanges("Sample Symphonies"), provider: "library_evidence" });
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const progress = await group("进度");
    expect(await within(progress).findByLabelText("库内佐证 1 条")).toBeTruthy();
    expect(await within(progress).findByLabelText("网络搜索 0 条")).toBeTruthy();
    await user.click(within(progress).getByRole("button", { name: "依据库内或网页写入的更多操作" }));
    expect(screen.getByRole("menuitem", { name: "查看依据库内佐证的改动" })).toBeTruthy();
    await user.click(screen.getByRole("menuitem", { name: "撤销依据库内佐证的全部自动修改" }));
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });
});

describe("外部源", () => {
  it("总开关关着时在分组层面说明；开关即改即存，关着的源不另写原因", async () => {
    const { api, curation } = setup();
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("自动整理没有打开，后台不使用任何外部源。")).toBeTruthy();
    enableCuration(curation);
    await user.click(await within(sources).findByRole("switch", { name: "LRCLIB" }));
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/sources/lrclib")[0]?.body).toEqual({ enabled: false }));
    const lrclib = within(sources).getByRole<HTMLInputElement>("switch", { name: "LRCLIB" });
    await waitFor(() => expect(lrclib.checked).toBe(false));
    expect(document.getElementById(lrclib.getAttribute("aria-describedby") ?? "")?.textContent).toBe("歌词");
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
    expect(item?.textContent).toBe("AcoustID（填入密钥后）：由音频算出的声学指纹（不能还原成音频）与时长，以及应用密钥与你填入的用户密钥");
  });

  it("两把密钥分别填写、更换与清除：只显示末四位；都填了才可用；AcoustID 没有“来自它的改动”的菜单", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("填好下面的密钥后才能用。")).toBeTruthy();
    expect(within(sources).queryByRole("button", { name: "AcoustID 的更多操作" })).toBeNull();
    // 没填的密钥收成“未填写”与“填写”，点了才有输入框，焦点进输入框。
    expect(within(sources).queryByRole("form")).toBeNull();
    expect(within(sources).getAllByText("未填写")).toHaveLength(2);
    expect(within(sources).getByRole("link", { name: "注册一个应用" }).getAttribute("href")).toBe("https://acoustid.org/new-application");
    expect(within(sources).getByRole("link", { name: "这个页面" }).getAttribute("href")).toBe("https://acoustid.org/api-key");

    await user.click(within(sources).getByRole("button", { name: "填写 AcoustID 的应用密钥" }));
    const application = keyRow(sources, "应用密钥");
    expect(document.activeElement).toBe(within(application).getByLabelText("应用密钥"));
    const save = within(application).getByRole("button", { name: "保存" });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    await user.type(within(application).getByLabelText("应用密钥"), "  abcdEFGH1234  ");
    await user.click(save);
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/sources/acoustid/keys/application")[0]?.body).toEqual({ key: "abcdEFGH1234" }));
    expect(await within(sources).findByText("已保存，末四位 1234")).toBeTruthy();
    // 只填了一把仍不可用；填好的那把不再显示去哪里申请。
    expect(within(sources).getByText("填好下面的密钥后才能用。")).toBeTruthy();
    expect(within(sources).queryByRole("link", { name: "注册一个应用" })).toBeNull();

    await user.click(within(sources).getByRole("button", { name: "填写 AcoustID 的用户密钥" }));
    await user.type(within(keyRow(sources, "用户密钥")).getByLabelText("用户密钥"), "userKEY9876");
    await user.click(within(keyRow(sources, "用户密钥")).getByRole("button", { name: "保存" }));
    expect(await within(sources).findByText("已保存，末四位 9876")).toBeTruthy();
    expect(within(sources).queryByText("填好下面的密钥后才能用。")).toBeNull();
    expect(within(sources).queryByRole("form")).toBeNull();

    const [replaceApplication] = within(sources).getAllByRole("button", { name: "更换" });
    if (replaceApplication === undefined) throw new Error("缺少更换按钮");
    await user.click(replaceApplication);
    expect(keyRow(sources, "应用密钥")).toBeTruthy();
    await user.click(within(sources).getByRole("button", { name: "取消" }));
    expect(within(sources).getByText("已保存，末四位 1234")).toBeTruthy();
    // 取消后焦点回到这一行的“更换”。
    await waitFor(() => expect(document.activeElement).toBe(within(sources).getAllByRole("button", { name: "更换" })[0]));

    const [, clearUser] = within(sources).getAllByRole("button", { name: "清除" });
    if (clearUser === undefined) throw new Error("缺少清除按钮");
    await user.click(clearUser);
    await waitFor(() => expect(api.requestsTo("DELETE", "/api/v1/admin/curation/sources/acoustid/keys/user")).toHaveLength(1));
    expect(await within(sources).findByRole("button", { name: "填写 AcoustID 的用户密钥" })).toBeTruthy();
    expect(within(sources).getByText("已保存，末四位 1234")).toBeTruthy();
    expect(within(sources).getByText("填好下面的密钥后才能用。")).toBeTruthy();
  });

  it("镜像内置应用密钥时只有用户密钥一行，填上就可用", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const source = acoustid(curation);
    if (source?.keys === undefined) throw new Error("缺少 AcoustID 的密钥");
    source.keys = source.keys.filter((key) => key.name === "user");
    refreshSources(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("填好下面的密钥后才能用。")).toBeTruthy();
    expect(within(sources).queryByRole("button", { name: "填写 AcoustID 的应用密钥" })).toBeNull();
    expect(within(sources).queryByRole("link", { name: "注册一个应用" })).toBeNull();

    await user.click(within(sources).getByRole("button", { name: "填写 AcoustID 的用户密钥" }));
    await user.type(within(keyRow(sources, "用户密钥")).getByLabelText("用户密钥"), "userKEY9876");
    await user.click(within(keyRow(sources, "用户密钥")).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/sources/acoustid/keys/user")[0]?.body).toEqual({ key: "userKEY9876" }));
    expect(await within(sources).findByText("已保存，末四位 9876")).toBeTruthy();
    expect(within(sources).queryByText("填好下面的密钥后才能用。")).toBeNull();
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
    expect(await within(sources).findByText("密钥无效，请重新填写。")).toBeTruthy();
    expect(within(sources).getAllByText(/AcoustID 说这把密钥无效/)).toHaveLength(1);
    expect(within(sources).getByText("已保存，末四位 u123")).toBeTruthy();

    await user.click(within(sources).getByRole("switch", { name: "AcoustID" }));
    await waitFor(() => expect(within(sources).queryByText("已保存，末四位 wxyz")).toBeNull());
    await waitFor(() => expect(within(sources).queryByText("密钥无效，请重新填写。")).toBeNull());
    expect(document.getElementById(within(sources).getByRole("switch", { name: "AcoustID" }).getAttribute("aria-describedby") ?? "")?.textContent).toBe("声学指纹");
  });

  it("服务器缺少指纹工具时说明不能使用", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.fingerprintAvailable = false;
    refreshSources(curation);
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("这台服务器缺少指纹工具（fpcalc），暂时不能使用。")).toBeTruthy();
  });

  it("内置的应用密钥被拒时说明升级后恢复", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.bundledKeyRejected = true;
    refreshSources(curation);
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    expect(await within(sources).findByText("ROOMusic 内置的应用密钥被 AcoustID 拒绝了，升级到新版本后恢复。")).toBeTruthy();
  });

  it("保存失败时就近提示是哪一把，输入框保留", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    api.on("PUT", "/api/v1/admin/curation/sources/acoustid/keys/user", () => jsonResponse(400, { error: { code: "invalid_input", message: "密钥无效" }, request_id: "r1" }));
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const sources = await group("外部源");
    await user.click(await within(sources).findByRole("button", { name: "填写 AcoustID 的用户密钥" }));
    await user.type(within(sources).getByLabelText("用户密钥"), "bad key");
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
    curation.releases = { total: 3, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0, unlisted: 0, no_usable_candidates: 0 };
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
    curation.releases = { total: 3, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0, unlisted: 0, no_usable_candidates: 0 };
    enableCuration(curation);
    renderApp({ path: "/" });
    expect(await screen.findByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    await waitFor(() => expect(screen.getAllByText("自动整理").length).toBeGreaterThan(0));
    expect(screen.queryByText("曲库扫描好了")).toBeNull();
  });
});

describe("模型判断（E2）", () => {
  it("从未打开过时只有一句说明，不显示控件", async () => {
    setup();
    renderApp({ path: "/settings/curation" });
    const model = await group("模型判断");
    expect(await within(model).findByText(/打开总开关、并在 Agent 设置里配置好模型接口之后/)).toBeTruthy();
    expect(within(model).queryByRole("textbox")).toBeNull();
  });

  it("状态与原因：开启者是自己时链接 Agent 设置；用量条按上限；最近的会话", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.model.state = "unavailable";
    curation.model.unavailableReason = "not_configured";
    curation.model.today = { input_tokens: 1_500_000, output_tokens: 500_000 };
    curation.model.items = { in_progress: 2, shelved: 1, waiting_human: 0, waiting_model: 3 };
    curation.conversations = [
      {
        created_at: "2026-01-05T08:00:00Z",
        id: "00000000-0000-4000-9000-000000000099",
        item_counts: { in_progress: 2 },
        kind: "curation",
        last_read_at: null,
        last_run_finished_at: null,
        mode: "steward",
        model: "",
        pending_item_count: 0,
        run_status: "idle",
        status: "idle",
        title: "自动整理 01-05 08:00",
        unread: false,
        updated_at: "2026-01-05T08:10:00Z",
      },
    ];
    renderApp({ path: "/settings/curation" });
    const model = await group("模型判断");
    expect(await within(model).findByText(/暂时不能判断：你还没有配置管家的模型接口。/)).toBeTruthy();
    expect(within(model).queryByRole("button", { name: "测试模型连接" })).toBeNull();
    expect(within(model).getByRole("link", { name: "去 Agent 设置" }).getAttribute("href")).toBe("/settings/agent");
    const meter = within(model).getByRole("progressbar", { name: "今天的用量" });
    expect(meter.getAttribute("aria-valuenow")).toBe("2000000");
    expect(meter.getAttribute("aria-valuemax")).toBe("10000000");
    expect(within(model).getByText("等模型 3 · 处理中 2 · 搁置 1")).toBeTruthy();
    expect((await within(model).findByRole("link", { name: "自动整理 01-05 08:00" })).getAttribute("href")).toBe("/steward/00000000-0000-4000-9000-000000000099");
  });

  const retryAt = new Date(2026, 0, 5, 8, 30).toISOString();

  it.each([
    {
      kind: "configuration",
      detail: "合成密钥已失效",
      retryAt: null,
      reason: "模型接口拒绝了请求（合成密钥已失效），改好配置或测试连接成功后继续",
    },
    {
      kind: "provider_quota",
      detail: null,
      retryAt,
      reason: "服务商的额度或余额不足，2026-01-05 08:30 自动再试一次",
    },
    {
      kind: "temporary",
      detail: null,
      retryAt,
      reason: "服务商暂时出错，2026-01-05 08:30 再试",
    },
    {
      kind: "empty_replies",
      detail: "建议换一个模型",
      retryAt: null,
      reason: "模型连续没有给出回答（建议换一个模型），测试连接成功后继续",
    },
  ] as const)("按 $kind 显示模型暂停的原因与恢复方式", async ({ reason, ...pause }) => {
    const { curation } = setup();
    enableCuration(curation);
    curation.model.state = "unavailable";
    curation.model.unavailableReason = "provider_error";
    curation.model.pause = pause;
    curation.model.canRetry = false;
    renderApp({ path: "/settings/curation" });
    const model = within(await group("模型判断"));
    expect(await model.findByText(`暂时不能判断：${reason}。`)).toBeTruthy();
    expect(model.queryByText(/模型接口最近出错了/)).toBeNull();
    expect(model.queryByRole("button", { name: "测试模型连接" })).toBeNull();
  });

  it("达到本地日限时在状态行只显示一句用量上限提示", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.model.state = "limit_reached";
    curation.model.pause = { kind: "daily_limit", detail: null, retryAt };
    curation.model.today = { input_tokens: 9_000_000, output_tokens: 1_000_000 };
    renderApp({ path: "/settings/curation" });
    const model = within(await group("模型判断"));
    expect(await model.findByText("今天的用量到了上限，明天继续")).toBeTruthy();
    expect(model.queryByText(/到了每日上限/)).toBeNull();
  });

  it("测试模型连接发出无请求体的 POST，成功后收起按钮和暂停原因", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    curation.model.state = "unavailable";
    curation.model.pause = { kind: "configuration", detail: "合成接口拒绝认证", retryAt: null };
    curation.model.canRetry = true;
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = within(await group("模型判断"));
    const pause = /合成接口拒绝认证/;
    expect(await model.findByText(pause)).toBeTruthy();
    await user.click(model.getByRole("button", { name: "测试模型连接" }));
    await waitFor(() => expect(model.queryByRole("button", { name: "测试模型连接" })).toBeNull());
    expect(model.queryByText(pause)).toBeNull();
    const requests = api.requestsTo("POST", "/api/v1/admin/curation/model/retry");
    expect(requests).toHaveLength(1);
    expect(requests[0]?.init.body).toBeUndefined();
    expect(curation.model.retries).toBe(1);
  });

  it("单次最多发送显示默认值和本机提示，拒绝零、超过上限和小数", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = within(await group("模型判断"));
    const input = await model.findByRole("spinbutton", {
      name: "单次最多发送",
      description: /使用本机模型时，请按模型服务实际加载的上下文长度设置单次最多发送。/,
    });
    expect((input as HTMLInputElement).value).toBe("128000");
    for (const invalid of ["0", "1000000000001", "1.5"]) {
      await user.clear(input);
      await user.type(input, invalid);
      expect((input as HTMLInputElement).value).toBe(invalid);
      await user.tab();
      expect(model.getByText("填 1 到 1,000,000,000,000 之间的整数。")).toBeTruthy();
      expect(input.getAttribute("aria-invalid")).toBe("true");
    }
    expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")).toHaveLength(0);
  });

  it.each([
    { action: "离开", tokens: 1, press: "{Tab}" },
    { action: "回车", tokens: 1_000_000_000_000, press: "{Enter}" },
  ])("单次最多发送在$action时保存合法值 $tokens", async ({ tokens, press }) => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = within(await group("模型判断"));
    const input = await model.findByRole("spinbutton", { name: "单次最多发送" });
    await user.clear(input);
    await user.type(input, String(tokens));
    expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings")).toHaveLength(0);
    await user.keyboard(press);
    expect(await model.findByText("已保存。")).toBeTruthy();
    expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings").map((request) => request.body)).toEqual([{ max_request_tokens: tokens }]);
    expect((input as HTMLInputElement).value).toBe(String(tokens));
  });

  it("整理用的模型与每日上限即改即存：回车保存模型，填万 token 离开时保存，勾“不限”立即保存；超出范围提示", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = await group("模型判断");
    const name = await within(model).findByRole("textbox", { name: "整理用的模型" });
    expect(within(model).getByText("整理与艺人联网每天共用 1,000 万 token，达到后当天不再发起新的请求。")).toBeTruthy();
    expect(within(model).queryByText("联网每日上限")).toBeNull();
    await user.type(name, "  curation-model  {Enter}");
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings").at(-1)?.body).toEqual({ model: "curation-model" }));
    expect(await within(model).findByText("已保存。")).toBeTruthy();

    const limit = within(model).getByRole("spinbutton", { name: "每日上限" });
    expect((limit as HTMLInputElement).value).toBe("1000");
    await user.clear(limit);
    await user.type(limit, "0");
    await user.tab();
    expect(await within(model).findByText("填 1 到 100,000,000 之间的整数。")).toBeTruthy();
    await user.clear(limit);
    await user.type(limit, "500");
    await user.tab();
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings").at(-1)?.body).toEqual({ daily_token_limit: 5_000_000 }));
    expect(await within(model).findByText("整理与艺人联网每天共用 500 万 token，达到后当天不再发起新的请求。")).toBeTruthy();

    await user.click(within(within(model).getByRole("group", { name: "每日上限" })).getByRole("checkbox", { name: "不限" }));
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/curation/settings").at(-1)?.body).toEqual({ daily_token_limit: null }));
    expect(await within(model).findByText("不限：整理与艺人联网不按天停下。")).toBeTruthy();
    expect(within(model).queryByRole("progressbar", { name: "今天的用量" })).toBeNull();
  });

  it("保存失败时回到服务器的值并就近提示", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    api.on("PUT", "/api/v1/admin/curation/settings", () => httpFailure(503, "database_unavailable", "数据库不可用"));
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const model = await group("模型判断");
    const name = await within(model).findByRole("textbox", { name: "整理用的模型" });
    await user.type(name, "x{Enter}");
    await waitFor(() => expect((name as HTMLInputElement).value).toBe(""));
    expect(await within(model).findByRole("alert")).toBeTruthy();
  });
});

describe("未采用的建议（E2、整理链 G）", () => {
  it("判断为不需要改的建议只能不采纳，没有采纳按钮", async () => {
    const { curation } = setup();
    enableCuration(curation);
    seedException(curation, { can_accept: false, reason_code: "verification_declined", review: null, reasons: ["只是繁简写法不同"] });
    renderApp({ path: "/settings/curation" });
    const table = await within(await group("未采用的建议")).findByRole("table", { name: "未采用的建议" });
    expect(within(table).getByText("核对后判断不需要改")).toBeTruthy();
    expect(within(table).getByText("只是繁简写法不同")).toBeTruthy();
    expect(within(table).queryByRole("button", { name: /^采纳/ })).toBeNull();
    expect(within(table).getByRole("button", { name: /^不采纳/ })).toBeTruthy();
  });
});

describe("例外（E2）", () => {
  it("没有例外时只写一句；有时列出发行、前后值、审查结论与理由", async () => {
    const { curation } = setup();
    enableCuration(curation);
    renderApp({ path: "/settings/curation" });
    expect(await within(await group("未采用的建议")).findByText("没有未采用的建议。")).toBeTruthy();
  });

  it("采纳：确认框列出改动，成功后行消失、等你处理减一；“查看过程”链接到会话", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const exception = seedException(curation);
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const section = await group("未采用的建议");
    const table = await within(section).findByRole("table", { name: "未采用的建议" });
    expect(within(table).getByText("标题：旧标题 → 新标题")).toBeTruthy();
    expect(within(table).getByText("审查拿不准（风险中）")).toBeTruthy();
    expect(within(table).getByText("标题需要人工核对")).toBeTruthy();
    expect(within(table).getByRole("link", { name: /查看过程/ }).getAttribute("href")).toBe(`/steward/${exception.conversation_id ?? ""}`);
    await user.click(within(table).getByRole("button", { name: /^采纳/ }));
    const dialog = await screen.findByRole("dialog", { name: "采纳这项修改？" });
    expect(within(dialog).getByText(/字段写进人工修正/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "采纳" }));
    await waitFor(() => expect(api.requestsTo("POST", `/api/v1/admin/curation/exceptions/${exception.id}/accept`)).toHaveLength(1));
    expect(await within(section).findByText("没有未采用的建议。")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("匹配的例外写出 MusicBrainz 的发行链接、曲目对应与归组；不采纳写“不再提出”", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const exception = seedException(curation, {
      tool: "resolve_release_match",
      input: { item_id: "00000000-0000-4000-c000-000000000001", external_id: "11111111-1111-4111-8111-111111111111", basis: { note: "条码相符" } },
      preview: {
        changes: [
          { field: "external_link", before: null, after: "11111111-1111-4111-8111-111111111111" },
          { field: "track_links", before: 0, after: 12 },
          { field: "year", before: null, after: 1998 },
          { field: "group", before: "a", after: "b" },
        ],
      },
      review: { decision: "rejected", id: "00000000-0000-4000-d000-000000000009", model: "review-model", reasons: ["曲目数对不上"], risk: null, source: "model" },
    });
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const table = await within(await group("未采用的建议")).findByRole("table", { name: "未采用的建议" });
    const link = within(table).getByRole("link", { name: "11111111-1111-4111-8111-111111111111" });
    expect(link.getAttribute("href")).toBe("https://musicbrainz.org/release/11111111-1111-4111-8111-111111111111");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(within(table).getByText("曲目对应：0 → 12 首")).toBeTruthy();
    expect(within(table).getByText("年份：（空） → 1998")).toBeTruthy();
    expect(within(table).getByText("归入已有的专辑")).toBeTruthy();
    expect(within(table).getByText("审查没通过")).toBeTruthy();
    await user.click(within(table).getByRole("button", { name: /^不采纳/ }));
    const dialog = await screen.findByRole("dialog", { name: "不采纳这项修改？" });
    expect(within(dialog).getByText(/以后程序与模型都不再提出这个值/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "不采纳" }));
    await waitFor(() => expect(api.requestsTo("POST", `/api/v1/admin/curation/exceptions/${exception.id}/decline`)).toHaveLength(1));
    expect(await within(await group("未采用的建议")).findByText("没有未采用的建议。")).toBeTruthy();
  });

  it("采纳失败：内容变了写原因并刷新列表；外部源暂时不可用时框留着，可以重试", async () => {
    const { api, curation } = setup();
    enableCuration(curation);
    const exception = seedException(curation);
    const path = `/api/v1/admin/curation/exceptions/${exception.id}/accept`;
    api.on("POST", path, () => httpFailure(503, "source_unavailable", "外部源暂时不可用"));
    const user = userEvent.setup();
    renderApp({ path: "/settings/curation" });
    const table = await within(await group("未采用的建议")).findByRole("table", { name: "未采用的建议" });
    await user.click(within(table).getByRole("button", { name: /^采纳/ }));
    const dialog = await screen.findByRole("dialog", { name: "采纳这项修改？" });
    await user.click(within(dialog).getByRole("button", { name: "采纳" }));
    expect(await within(dialog).findByText(/外部资料暂时取不到，没有完成/)).toBeTruthy();
    const listRequests = api.requestsTo("GET", "/api/v1/admin/curation/exceptions").length;
    api.on("POST", path, () => httpFailure(409, "exception_stale", "例外的预览已变更"));
    await user.click(within(dialog).getByRole("button", { name: "重试" }));
    expect(await within(dialog).findByText(/提出之后内容变了，这一项没有执行/)).toBeTruthy();
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/admin/curation/exceptions").length).toBeGreaterThan(listRequests));
  });

  it("开启者不是自己时不给“查看过程”", async () => {
    const { curation } = setup();
    enableCuration(curation);
    curation.settings = { ...curation.settings, enabled_by: { id: "00000000-0000-4000-8000-0000000000ff", username: "other" } };
    seedException(curation);
    renderApp({ path: "/settings/curation" });
    const table = await within(await group("未采用的建议")).findByRole("table", { name: "未采用的建议" });
    expect(within(table).queryByRole("link", { name: /查看过程/ })).toBeNull();
  });
});

describe("M8 艺人资料外部源", () => {
  function setupArtistSources() {
    const { curation } = setup();
    curation.sources = decodeCurationSources(sampleBody("curation/sources")).items;
    return curation;
  }

  it("显示五个来源的能力、三把密钥的标签和说明，Wikidata 没有改动菜单", async () => {
    setupArtistSources();
    renderApp({ path: "/settings/curation" });
    const sources = within(await group("外部源"));
    for (const [name, capability] of [
      ["Wikidata", /艺人/],
      ["Wikimedia Commons", /^艺人图片$/],
      ["Wikipedia", /^艺人简介$/],
      ["Fanart.tv", /^艺人图片$/],
      ["TheAudioDB", /^艺人图片、艺人简介$/],
    ] as const) {
      expect(await sources.findByRole("switch", { name, description: capability })).toBeTruthy();
    }
    expect(sources.queryByRole("button", { name: "Wikidata 的更多操作" })).toBeNull();
    await userEvent.click(sources.getByRole("button", { name: "Wikimedia Commons 的更多操作" }));
    expect(await screen.findByRole("menuitem", { name: "查看来自 Wikimedia Commons 的改动" })).toBeTruthy();

    for (const [provider, name, guide, link, address] of [
      ["Fanart.tv", "项目密钥", "在 Fanart.tv 的申请页面免费申请。", "申请页面", "https://fanart.tv/get-an-api-key/"],
      ["Fanart.tv", "个人密钥（可选）", "登录 Fanart.tv 后在这个页面取得；填了以后新上传的图片能更早用上。", "这个页面", "https://fanart.tv/get-an-api-key/"],
      ["TheAudioDB", "API 密钥", "在这个页面查看：免费的测试密钥是 123，订阅后可以换成自己的密钥。", "这个页面", "https://www.theaudiodb.com/free_music_api"],
    ] as const) {
      // 没填时是“填写”按钮，说明（去哪里申请）挂在它上面。
      const fill = sources.getByRole("button", { name: `填写 ${provider} 的${name}` });
      const descriptionId = fill.getAttribute("aria-describedby");
      const description = descriptionId === null ? null : document.getElementById(descriptionId);
      if (description === null) throw new Error(`找不到${name}的说明`);
      expect(description.textContent).toBe(guide);
      expect(within(description).getByRole("link", { name: link }).getAttribute("href")).toBe(address);
    }
  });

  it("打开整理前说明五个艺人资料源会收到哪些数据", async () => {
    setupArtistSources();
    renderApp({ path: "/settings/curation" });
    await userEvent.click(await screen.findByRole("switch", { name: "自动整理" }));
    const dialog = within(await screen.findByRole("dialog", { name: "打开自动整理？" }));
    for (const [name, data] of [
      ["Wikidata", "MusicBrainz 编号与 Wikidata 条目编号"],
      ["Wikimedia Commons", "图片文件名"],
      ["Wikipedia", "条目名"],
      ["Fanart.tv", "项目密钥与你填入的个人密钥"],
      ["TheAudioDB", "MusicBrainz 编号与你填入的密钥"],
    ] as const) {
      expect(dialog.getByText(name, { exact: true }).closest("li")?.textContent).toContain(data);
    }
  });

  it("Wikidata 的来源说明不把查得的结果归入 MusicBrainz", async () => {
    setupArtistSources();
    renderApp({ path: "/settings/curation" });
    const sources = within(await group("外部源"));
    const wikidata = await sources.findByRole("switch", { name: "Wikidata", description: /艺人/ });
    const descriptionId = wikidata.getAttribute("aria-describedby");
    expect(document.getElementById(descriptionId ?? "")?.textContent).not.toContain("记在 MusicBrainz 名下");
  });

  it("内置 Fanart.tv 项目密钥可用时，设置页标明项目密钥可选", async () => {
    const curation = setupArtistSources();
    const fanart = curation.sources.find(({ id }) => id === "fanarttv");
    const project = fanart?.keys?.find(({ name }) => name === "project");
    if (fanart === undefined || project === undefined) throw new Error("找不到 Fanart.tv 项目密钥样本");
    project.optional = true;
    enableCuration(curation);
    curation.sources = curation.sources.map((source) => source.id === fanart.id ? { ...source, active: true, inactive_reason: null } : source);
    renderApp({ path: "/settings/curation" });
    const sources = within(await group("外部源"));
    await userEvent.click(await sources.findByRole("button", { name: /^填写 Fanart\.tv 的项目密钥/ }));
    const form = sources.getByRole("form", { name: /^Fanart\.tv 的项目密钥/ });
    const input = within(form).getByLabelText<HTMLInputElement>(/^项目密钥/, { selector: "input" });
    const label = input.labels?.[0]?.textContent;
    const description = document.getElementById(input.getAttribute("aria-describedby") ?? "")?.textContent;
    expect(`${label} ${description}`).toMatch(/可选|内置|不填/);
  });
});
