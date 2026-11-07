import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { decodeArtistCandidatePage } from "../features/governance/api/decoders";
import { ARTIST_CANDIDATE_PAGE_SIZE } from "../features/governance/hooks/use-artist-candidates";
import { governanceKeys } from "../features/governance/query-keys";
import { artistDataOf, artistTestId, seedArtist, seedArtistCandidates } from "../test/artist-data";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { FakeAudio } from "../test/fake-audio";

beforeEach(() => { vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } }); });

const candidatesPath = "/api/v1/artist-candidates";
const queryOf = (path: string) => new URL(path, "http://localhost").searchParams;
const identity = (left: string, right: string) => [left, right].sort().join(":");

function setup(options: { ignored?: boolean; count?: number } = {}) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "curator" });
  const data = installArtistsApi(api, artistDataOf(), session);
  const original = seedArtistCandidates(data)[0];
  if (original === undefined) throw new Error("缺少真实候选样本");
  if (options.count !== undefined) {
    data.candidates = Array.from({ length: options.count }, (_, index) => ({
      ...structuredClone(original),
      left: { ...original.left, id: artistTestId(10_000 + index * 2), name: `候选甲 ${index + 1}` },
      right: { ...original.right, id: artistTestId(10_001 + index * 2), name: `候选乙 ${index + 1}` },
      key_pairs: [{ left_key_id: artistTestId(20_000 + index * 2), right_key_id: artistTestId(20_001 + index * 2) }],
    }));
    for (const item of data.candidates) for (const artist of [item.left, item.right]) seedArtist(data, artist);
  }
  if (options.ignored) for (const item of data.candidates) for (const pair of item.key_pairs) data.ignored.add(identity(pair.left_key_id, pair.right_key_id));
  return { api, data, original, user: userEvent.setup() };
}

const statusNav = () => within(screen.getByRole("navigation", { name: "候选状态" }));

function expectSelected(status: "pending" | "ignored") {
  const nav = statusNav();
  expect(nav.getAllByRole("link").filter((link) => link.getAttribute("aria-current") === "page")).toHaveLength(1);
  expect(nav.getByRole("link", { name: status === "ignored" ? "已忽略" : "待处理" }).getAttribute("aria-current")).toBe("page");
  expect(nav.getByRole("link", { name: status === "ignored" ? "待处理" : "已忽略" }).getAttribute("aria-current")).toBeNull();
}

describe("重复艺人的理由文案", () => {
  it.each([
    {
      name: "bracket_alias inner，带括号的名字在左侧",
      reason: { kind: "bracket_alias", left_name: "杨璐（羊鹿鹿）", right_name: "羊鹿鹿", bracket_side: "left", alias_part: "inner" },
      expected: "“杨璐（羊鹿鹿）”括号里写着“羊鹿鹿”",
    },
    {
      name: "bracket_alias outer，带括号的名字在右侧",
      reason: { kind: "bracket_alias", left_name: "杨璐", right_name: "杨璐（羊鹿鹿）", bracket_side: "right", alias_part: "outer" },
      expected: "“杨璐（羊鹿鹿）”去掉括号就是“杨璐”",
    },
    {
      name: "album_track_one_to_one 保留专辑与曲目艺人的文案",
      reason: { kind: "album_track_one_to_one", left_name: "Jay Chou", right_name: "周杰伦" },
      expected: "同一张专辑的专辑艺人写“Jay Chou”，曲目艺人写“周杰伦”",
    },
  ] as const)("$name", async ({ reason, expected }) => {
    const context = setup();
    context.original.reasons = [{ ...reason, release_id: artistTestId(30_000) }];
    renderApp({ path: "/artist-candidates" });
    expect((await screen.findByText(expected, { exact: true })).textContent).toBe(expected);
  });

  it("未知 kind 保留候选，但不渲染理由子列表", async () => {
    const context = setup();
    const pair = context.original;
    pair.reasons = [{ kind: "unknown_reason", release_id: artistTestId(30_000), left_name: pair.left.name, right_name: pair.right.name }];
    renderApp({ path: "/artist-candidates" });
    const button = await screen.findByRole("button", { name: `忽略“${pair.left.name}”和“${pair.right.name}”` });
    const candidate = button.closest("li");
    if (candidate === null) throw new Error("缺少候选条目");
    expect(within(candidate).getByRole("link", { name: pair.left.name })).toBeTruthy();
    expect(within(candidate).getByRole("link", { name: pair.right.name })).toBeTruthy();
    expect(within(candidate).queryByRole("list")).toBeNull();
  });
});

describe("V4/V5：候选已忽略列表与恢复", () => {
  it.each([
    ["/artist-candidates", false, 1],
    ["/artist-candidates?page=2", false, 2],
    ["/artist-candidates?status=ignored", true, 1],
    ["/artist-candidates?status=ignored&page=2", true, 2],
  ] as const)("%s 数据稳定后只有当前状态具有 aria-current", async (path, ignored, page) => {
    const context = setup({ count: 21, ignored });
    const { runtime, history } = renderApp({ path });
    const index = ignored ? (page === 2 ? 0 : 20) : (page === 2 ? 20 : 0);
    const pair = context.data.candidates[index];
    if (pair === undefined) throw new Error("缺少分页候选夹具");
    const name = ignored ? `恢复“${pair.left.name}”和“${pair.right.name}”的提示` : `忽略“${pair.left.name}”和“${pair.right.name}”`;
    await screen.findByRole("button", { name });
    const key = governanceKeys.artistCandidatePage(runtime.session.currentGeneration(), { page, pageSize: ARTIST_CANDIDATE_PAGE_SIZE, ignored });
    await waitFor(() => expect(runtime.queryClient.getQueryState(key)).toMatchObject({ status: "success", fetchStatus: "idle" }));
    expect(context.api.requestsTo("GET", candidatesPath).some((request) => queryOf(request.path).get("page") === String(page) && queryOf(request.path).has("ignored") === ignored)).toBe(true);
    expect(queryOf(history.location.href).get("page")).toBe(page === 2 ? "2" : null);
    expectSelected(ignored ? "ignored" : "pending");
  });

  it("status=ignored 请求 ignored=true，筛选有 aria-current，条目只有恢复，没有合并或忽略", async () => {
    const context = setup({ ignored: true });
    renderApp({ path: "/artist-candidates?status=ignored" });
    const pair = context.original;
    await screen.findByRole("button", { name: `恢复“${pair.left.name}”和“${pair.right.name}”的提示` });
    expectSelected("ignored");
    expect(screen.getByRole("heading", { level: 2, name: "已忽略" })).toBeTruthy();
    expect(queryOf(context.api.requestsTo("GET", candidatesPath)[0]?.path ?? "").get("ignored")).toBe("true");
    expect(screen.queryByRole("button", { name: /合并|忽略/ })).toBeNull();
    expect(statusNav().getByRole("link", { name: "待处理" }).getAttribute("href")).toBe("/artist-candidates");
    expect(statusNav().getByRole("link", { name: "待处理" }).getAttribute("aria-current")).toBeNull();
  });

  it("忽略后进已忽略，再恢复先读取双方最新详情，提交 ignored:false，两个列表重读且导航返回保持", async () => {
    const context = setup();
    const pair = context.original;
    const { runtime, history } = renderApp({ path: "/artist-candidates" });
    const ignoreName = `忽略“${pair.left.name}”和“${pair.right.name}”`;
    const restoreName = `恢复“${pair.left.name}”和“${pair.right.name}”的提示`;
    await context.user.click(await screen.findByRole("button", { name: ignoreName }));
    await screen.findByText(`以后不再提示“${pair.left.name}”和“${pair.right.name}”重复。`);
    await context.user.click(statusNav().getByRole("link", { name: "已忽略" }));
    await screen.findByRole("button", { name: restoreName });
    expectSelected("ignored");
    const pendingKey = governanceKeys.artistCandidatePage(runtime.session.currentGeneration(), { page: 1, pageSize: ARTIST_CANDIDATE_PAGE_SIZE, ignored: false });
    // 忽略后的待处理列表已成功重读；恢复前它不是已失效的旧缓存。
    expect(runtime.queryClient.getQueryState(pendingKey)?.isInvalidated).toBe(false);
    const left = context.data.details.get(pair.left.id), right = context.data.details.get(pair.right.id);
    if (left === undefined || right === undefined) throw new Error("缺少候选详情");
    // 先前忽略已经读过双方详情；改变服务端版本，证明恢复不能沿用缓存。
    left.revision = 7;
    right.revision = 9;
    const leftGate = deferred<Response>(), rightGate = deferred<Response>();
    const leftPath = `/api/v1/artists/${pair.left.id}`, rightPath = `/api/v1/artists/${pair.right.id}`;
    const writePath = `${leftPath}/candidate-ignore`;
    const reads = context.api.requestsTo("GET", candidatesPath).length;
    const start = context.api.requests.length;
    context.api.on("GET", leftPath, () => leftGate.promise);
    context.api.on("GET", rightPath, () => rightGate.promise);
    await context.user.click(screen.getByRole("button", { name: restoreName }));
    await waitFor(() => {
      expect(context.api.requests.slice(start).filter((request) => request.method === "GET" && request.path === leftPath)).toHaveLength(1);
      expect(context.api.requests.slice(start).filter((request) => request.method === "GET" && request.path === rightPath)).toHaveLength(1);
    });
    expect(context.api.requestsTo("POST", writePath)).toHaveLength(1);
    expect(screen.getByRole("button", { name: restoreName }).getAttribute("aria-busy")).toBe("true");
    await act(async () => { leftGate.resolve(jsonResponse(200, left)); });
    expect(context.api.requestsTo("POST", writePath)).toHaveLength(1);
    await act(async () => { rightGate.resolve(jsonResponse(200, right)); });
    await screen.findByText(`已恢复“${pair.left.name}”和“${pair.right.name}”的提示。`);
    await screen.findByText("没有忽略过的候选。");
    // 切回待处理之前直接证明非当前缓存已失效，不靠 staleTime: 0 的重新挂载请求蒙混通过。
    expect(runtime.queryClient.getQueryState(pendingKey)?.isInvalidated).toBe(true);
    const writes = context.api.requestsTo("POST", writePath);
    expect(writes).toHaveLength(2);
    expect(writes[1]?.body).toEqual({ expected_revision: 7, other_artist_id: pair.right.id, other_expected_revision: 9, key_pairs: pair.key_pairs, ignored: false });
    expect(writes[1]?.headers.get("Idempotency-Key")).toMatch(/^[\da-f-]{36}$/);
    expect(writes[1]?.headers.get("Idempotency-Key")).not.toBe(writes[0]?.headers.get("Idempotency-Key"));
    const afterRestore = context.api.requestsTo("GET", candidatesPath).slice(reads);
    expect(afterRestore.some((request) => queryOf(request.path).get("ignored") === "true")).toBe(true);
    const pendingReads = context.api.requestsTo("GET", candidatesPath).filter((request) => !queryOf(request.path).has("ignored")).length;
    await context.user.click(statusNav().getByRole("link", { name: "待处理" }));
    await screen.findByRole("button", { name: ignoreName });
    await waitFor(() => expect(context.api.requestsTo("GET", candidatesPath).filter((request) => !queryOf(request.path).has("ignored")).length).toBeGreaterThan(pendingReads));
    expectSelected("pending");
    expect(history.location.pathname).toBe("/artist-candidates");
    expect(queryOf(history.location.href).has("status")).toBe(false);
    // 经过别页再回来，重读仍是恢复后的状态，不靠保留的界面节点。
    await act(async () => { await runtime.router.navigate({ to: "/artists" }); });
    await screen.findByRole("heading", { level: 1, name: "艺人" });
    await act(async () => { await runtime.router.navigate({ to: "/artist-candidates" }); });
    await screen.findByRole("button", { name: ignoreName });
  });

  it("已忽略分页和越界纠正保留 status，切换状态回第一页", async () => {
    const context = setup({ ignored: true, count: 21 });
    const { history } = renderApp({ path: "/artist-candidates?status=ignored&page=99" });
    await waitFor(() => expect(queryOf(history.location.href).get("page")).toBe("2"));
    await screen.findByRole("button", { name: "恢复“候选甲 1”和“候选乙 1”的提示" });
    expectSelected("ignored");
    expect(statusNav().getByRole("link", { name: "待处理" }).getAttribute("aria-current")).toBeNull();
    // 已忽略以最近插入的在前：第 21 条排在第一页，最早的第 1 条排在第二页。
    expect(queryOf(history.location.href).get("status")).toBe("ignored");
    const pagination = within(screen.getByRole("navigation", { name: "候选分页" }));
    for (const link of pagination.getAllByRole("link")) expect(queryOf(link.getAttribute("href") ?? "").get("status")).toBe("ignored");
    await context.user.click(pagination.getByRole("link", { name: "上一页" }));
    await screen.findByRole("button", { name: "恢复“候选甲 21”和“候选乙 21”的提示" });
    expect(queryOf(history.location.href).get("status")).toBe("ignored");
    expect(queryOf(history.location.href).has("page")).toBe(false);
    await context.user.click(within(screen.getByRole("navigation", { name: "候选分页" })).getByRole("link", { name: "下一页" }));
    await waitFor(() => expect(queryOf(history.location.href).get("page")).toBe("2"));
    expectSelected("ignored");
    expect(statusNav().getByRole("link", { name: "待处理" }).getAttribute("aria-current")).toBeNull();
    expect(context.api.requestsTo("GET", candidatesPath).some((request) => queryOf(request.path).get("page") === "2" && queryOf(request.path).get("ignored") === "true")).toBe(true);
    await context.user.click(statusNav().getByRole("link", { name: "待处理" }));
    await screen.findByText("现在没有需要确认的候选。");
    expect(queryOf(history.location.href).has("status")).toBe(false);
    expect(queryOf(history.location.href).has("page")).toBe(false);
    expectSelected("pending");
  });

  it("真实空列表样本显示已忽略空态，不显示恢复按钮或分页", async () => {
    const context = setup();
    context.api.on("GET", candidatesPath, () => sampleResponse("governance/artist-candidates-ignored-empty"));
    renderApp({ path: "/artist-candidates?status=ignored" });
    await screen.findByText("没有忽略过的候选。");
    expectSelected("ignored");
    expect(screen.queryByRole("button", { name: /恢复/ })).toBeNull();
    expect(screen.queryByRole("navigation", { name: "候选分页" })).toBeNull();
  });

  it.each(["pending", "false", "IGNORED", "unknown", "true", ""])("validateSearch 丢弃非法 status=%s，不请求 ignored", async (status) => {
    const context = setup();
    const { runtime } = renderApp({ path: `/artist-candidates?status=${status}` });
    const pair = context.original;
    await screen.findByRole("button", { name: `忽略“${pair.left.name}”和“${pair.right.name}”` });
    const search = runtime.router.state.matches.at(-1)?.search;
    expect(search).toBeDefined();
    if (search !== undefined && "status" in search) expect(search.status).toBeUndefined();
    expectSelected("pending");
    expect(context.api.requestsTo("GET", candidatesPath).every((request) => !queryOf(request.path).has("ignored"))).toBe(true);
  });

  it("已忽略的真实非空样本沿用候选 decoder，无字段丢失", () => {
    const body = sampleBody("governance/artist-candidates-ignored");
    expect(decodeArtistCandidatePage(body)).toEqual(body);
    const empty = sampleBody("governance/artist-candidates-ignored-empty");
    expect(decodeArtistCandidatePage(empty)).toEqual(empty);
  });
});
