import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { decodeArtistSplitPreview, decodeOperationPage } from "../features/governance/api/decoders";
import { artistDataOf, artistError, artistTestId, primaryArtistId, respondToArtists, seedArtist, seedArtistCandidates, seedArtistIntegration } from "../test/artist-data";
import { sampleBody } from "../test/api-samples";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse, type RecordedRequest } from "../test/http";
import { renderApp } from "../test/render-app";
import { FakeAudio } from "../test/fake-audio";
import { deferred } from "../test/deferred";
import { catalogStateKeys } from "../features/catalog/query-keys";

beforeEach(() => { vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } }); });

const primary = primaryArtistId();
const otherId = artistTestId(700);
const keyA = artistTestId(701);
const keyB = artistTestId(702);
const keyC = artistTestId(703);
const endpoint = (action: string) => `/api/v1/artists/${primary}/${action}`;
const checked = (element: HTMLElement) => {
  if (!(element instanceof HTMLInputElement)) throw new Error("预期为表单输入");
  return element.checked;
};

function setup(member = false) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: member ? "listener" : "curator" });
  const data = installArtistsApi(api, artistDataOf(), session);
  const detail = data.details.get(primary);
  if (detail === undefined) throw new Error("缺少艺人详情夹具");
  const source = seedArtist(data, { ...detail.artist, id: otherId, name: "待合并艺人", track_count: 2 });
  const reply = (request: RecordedRequest) => {
    const result = respondToArtists(data, request.method, new URL(request.path, "http://localhost"), request.body, request.headers.get("Idempotency-Key"));
    return jsonResponse(result.status, result.body);
  };
  return { api, data, detail, source, reply, user: userEvent.setup() };
}

type Setup = ReturnType<typeof setup>;

function seedSplit(context: Setup, options: { sources?: boolean; emptyKeys?: boolean } = {}) {
  const { data, source } = context;
  data.names.set(primary, [
    { id: artistTestId(710), key_id: keyA, name: "旧艺人甲", is_display_name: false, origin: "scan" },
    { id: artistTestId(711), key_id: keyA, name: "舊藝人甲", is_display_name: false, origin: "scan" },
    { id: artistTestId(712), key_id: keyB, name: "旧艺人乙", is_display_name: false, origin: "scan" },
    { id: artistTestId(713), key_id: keyC, name: "本来就在这里", is_display_name: true, origin: "scan" },
  ]);
  source.artist.name = "旧身份";
  source.redirect_artist_id = primary;
  source.revision = 4;
  seedArtistIntegration(data, primary, { mergedSources: options.sources === false ? [] : [{ artist_id: otherId, name: "旧身份", revision: 4, name_key_ids: options.emptyKeys ? [] : [keyA, keyB], operation_id: artistTestId(720), merged_at: "2026-11-04T12:00:00Z" }] });
}

async function openDialog(context: Setup, name: "拆开" | "合并艺人" | "修改主名") {
  renderApp({ path: `/artists/${primary}` });
  await context.user.click(await screen.findByRole("button", { name: `${name}…` }));
  return within(await screen.findByRole("dialog", { name: name === "拆开" ? "拆开艺人" : name }));
}

async function prepareMerge(context: Setup) {
  const dialog = await openDialog(context, "合并艺人");
  await context.user.type(dialog.getByRole("textbox", { name: "查找艺人" }), "待合并艺人{Enter}");
  await context.user.click(await dialog.findByRole("radio", { name: /待合并艺人/ }));
  await waitFor(() => expect(dialog.getByRole<HTMLButtonElement>("button", { name: "合并" }).disabled).toBe(false));
  return dialog;
}

async function previewSplit(context: Setup, dialog: ReturnType<typeof within>) {
  await context.user.click(dialog.getByRole("button", { name: "预览" }));
  await dialog.findByRole("button", { name: "拆开" });
}

function seedAlbums(context: Setup, count = 21) {
  const original = context.data.albums.get(primary)?.[0];
  if (original === undefined) throw new Error("缺少专辑夹具");
  const albums = Array.from({ length: count }, (_, index) => ({
    ...structuredClone(original),
    group: { ...structuredClone(original.group), id: artistTestId(1000 + index), representative: { ...structuredClone(original.group.representative), title: `参与专辑 ${String(index + 1).padStart(2, "0")}` } },
    roles: ["composer", "performer"] as const,
  })).map((album) => ({ ...album, roles: [...album.roles] }));
  seedArtistIntegration(context.data, primary, { participationAlbums: albums });
  return albums;
}

describe("艺人治理：预览、确认与恢复", () => {
  it("合并先说明方向与不可撤销，确认携带双方版本号且成功没有撤销入口", async () => {
    const context = setup();
    const dialog = await prepareMerge(context);
    expect(dialog.getByRole("status").textContent).toContain(`“待合并艺人”会并入“${context.detail.artist.name}”`);
    expect(dialog.getByRole("status").textContent).toContain("合并不能撤销，需要时可以再拆开。");
    expect(context.api.requestsTo("POST", endpoint("merge"))).toHaveLength(0);
    await context.user.click(dialog.getByRole("button", { name: "合并" }));
    await screen.findByText(/已把“待合并艺人”并入/);
    expect(context.api.requestsTo("POST", endpoint("merge"))[0]?.body).toEqual({ expected_revision: 1, source_artist_id: otherId, source_expected_revision: 1 });
    expect(screen.queryByRole("button", { name: /撤销/ })).toBeNull();
  });

  it.each(["aliases", "albums"] as const)("%s 方式预览后拆给新艺人，确认提交同一个范围与 basis", async (mode) => {
    const context = setup();
    seedSplit(context, { sources: false });
    const albums = seedAlbums(context, 1);
    const dialog = await openDialog(context, "拆开");
    if (mode === "albums") {
      await context.user.click(dialog.getByRole("radio", { name: "按专辑分给另一位同名的人" }));
      await context.user.click(await dialog.findByRole("checkbox", { name: /参与专辑 01/ }));
    } else {
      await context.user.click(await dialog.findByRole("checkbox", { name: "旧艺人甲 / 舊藝人甲" }));
    }
    await context.user.type(dialog.getByRole("textbox", { name: "新艺人的主名（可不填）" }), "新身份");
    await previewSplit(context, dialog);
    expect(dialog.getByRole("status").textContent).toMatch(/个版本、.+首曲目上的署名分给“新身份”/);
    expect(dialog.getByRole("status").textContent).toContain("拆开不能撤销，需要时可以再合并。");
    expect(context.api.requestsTo("POST", endpoint("split"))).toHaveLength(0);
    const scope = { mode, name_key_ids: mode === "aliases" ? [keyA] : [keyA, keyB, keyC], ...(mode === "albums" ? { group_ids: albums.map((item) => item.group.id) } : {}) };
    expect(context.api.requestsTo("POST", endpoint("split-preview"))[0]?.body).toEqual(scope);
    await context.user.click(dialog.getByRole("button", { name: "拆开" }));
    await screen.findByText("已拆出“新身份”。");
    expect(context.api.requestsTo("POST", endpoint("split"))[0]?.body).toEqual({ ...scope, expected_revision: 1, basis: "scope-1-1", target: { kind: "new", display_name: "新身份" } });
    expect(screen.getByRole("link", { name: "打开“新身份”" }).getAttribute("href")).toMatch(/^\/artists\//);
    expect(screen.queryByRole("button", { name: /撤销/ })).toBeNull();
  });

  it("改名确认提示不可撤销；保存后可改回最常见写法，auto 不带旧 name", async () => {
    const context = setup();
    let dialog = await openDialog(context, "修改主名");
    expect(dialog.getByText("改名不能撤销，需要时可以再改。")).toBeTruthy();
    const name = dialog.getByRole("textbox", { name: "主名" });
    await context.user.clear(name);
    await context.user.type(name, "新的主名");
    expect(context.api.requestsTo("PATCH", endpoint("name"))).toHaveLength(0);
    await context.user.click(dialog.getByRole("button", { name: "保存" }));
    await screen.findByText("主名已改为“新的主名”。");
    expect(context.api.requestsTo("PATCH", endpoint("name"))[0]?.body).toEqual({ expected_revision: 1, mode: "set", name: "新的主名" });
    await context.user.click(screen.getByRole("button", { name: "修改主名…" }));
    dialog = within(await screen.findByRole("dialog", { name: "修改主名" }));
    await context.user.click(dialog.getByRole("radio", { name: "改回最常见的写法" }));
    await context.user.click(dialog.getByRole("button", { name: "保存" }));
    await screen.findByText(/主名改回了最常见的写法/);
    expect(context.api.requestsTo("PATCH", endpoint("name"))[1]?.body).toEqual({ expected_revision: 2, mode: "auto" });
  });
});

describe("I1：恢复合并前的身份", () => {
  it("拆回自动选择别名方式、同键的全部写法及 restore 目标，提交旧身份版本号", async () => {
    const context = setup();
    seedSplit(context);
    const dialog = await openDialog(context, "拆开");
    await context.user.click(dialog.getByRole("radio", { name: "按专辑分给另一位同名的人" }));
    await context.user.click(await dialog.findByRole("button", { name: "拆回 旧身份" }));
    expect(checked(dialog.getByRole("radio", { name: "把一些名字拆成另一位艺人" }))).toBe(true);
    expect(checked(dialog.getByRole("checkbox", { name: "旧艺人甲 / 舊藝人甲" }))).toBe(true);
    expect(checked(dialog.getByRole("checkbox", { name: "旧艺人乙" }))).toBe(true);
    expect(checked(dialog.getByRole("checkbox", { name: /本来就在这里/ }))).toBe(false);
    expect(checked(dialog.getByRole("radio", { name: /恢复为“旧身份”/ }))).toBe(true);
    await previewSplit(context, dialog);
    await context.user.click(dialog.getByRole("button", { name: "拆开" }));
    await screen.findByText("已拆出“旧身份”。");
    expect(context.api.requestsTo("POST", endpoint("split"))[0]?.body).toEqual({ mode: "aliases", name_key_ids: [keyA, keyB], expected_revision: 1, basis: "scope-1-1", target: { kind: "restore", artist_id: otherId, expected_revision: 4 } });
  });

  it("手动同源 keys 自动恢复；混入其他 keys 自动新建，去掉后重新恢复", async () => {
    const context = setup();
    seedSplit(context);
    const dialog = await openDialog(context, "拆开");
    await dialog.findByRole("button", { name: "拆回 旧身份" });
    await context.user.click(await dialog.findByRole("checkbox", { name: "旧艺人甲 / 舊藝人甲" }));
    await context.user.click(dialog.getByRole("checkbox", { name: "旧艺人乙" }));
    expect(checked(dialog.getByRole("radio", { name: /恢复为“旧身份”/ }))).toBe(true);
    await context.user.click(dialog.getByRole("checkbox", { name: /本来就在这里/ }));
    expect(checked(dialog.getByRole("radio", { name: "一位新的艺人" }))).toBe(true);
    await context.user.click(dialog.getByRole("checkbox", { name: /本来就在这里/ }));
    expect(checked(dialog.getByRole("radio", { name: /恢复为“旧身份”/ }))).toBe(true);
  });

  it.each(["new", "restore"] as const)("明确选择 %s 后，改变 keys 不覆盖管理员目标", async (target) => {
    const context = setup();
    seedSplit(context);
    const dialog = await openDialog(context, "拆开");
    await dialog.findByRole("button", { name: "拆回 旧身份" });
    await context.user.click(await dialog.findByRole("checkbox", { name: "旧艺人甲 / 舊藝人甲" }));
    if (target === "restore") await context.user.click(dialog.getByRole("checkbox", { name: /本来就在这里/ }));
    const targetName = target === "new" ? "一位新的艺人" : /恢复为“旧身份”/;
    await context.user.click(dialog.getByRole("radio", { name: targetName }));
    await context.user.click(dialog.getByRole("checkbox", { name: "旧艺人乙" }));
    expect(checked(dialog.getByRole("radio", { name: targetName }))).toBe(true);
  });

  it("没有可恢复来源时整段入口不出现", async () => {
    const context = setup();
    seedSplit(context, { sources: false });
    const dialog = await openDialog(context, "拆开");
    await dialog.findByRole("checkbox", { name: "旧艺人乙" });
    await waitFor(() => expect(context.api.requestsTo("GET", endpoint("merged-sources"))).toHaveLength(1));
    expect(dialog.queryByRole("heading", { name: "之前并入这里的艺人" })).toBeNull();
    expect(dialog.queryByRole("button", { name: /拆回/ })).toBeNull();
    expect(dialog.queryByRole("radio", { name: /恢复为/ })).toBeNull();
  });

  it("来源已无 keys 时拆回只恢复目标，不误勾其他名字或提交空范围", async () => {
    const context = setup();
    seedSplit(context, { emptyKeys: true });
    const dialog = await openDialog(context, "拆开");
    await context.user.click(await dialog.findByRole("button", { name: "拆回 旧身份" }));
    expect(dialog.getAllByRole("checkbox").every((input) => !checked(input))).toBe(true);
    expect(checked(dialog.getByRole("radio", { name: /恢复为“旧身份”/ }))).toBe(true);
    expect(dialog.getByRole<HTMLButtonElement>("button", { name: "预览" }).disabled).toBe(true);
    expect(context.api.requestsTo("POST", endpoint("split-preview"))).toHaveLength(0);
    await context.user.click(dialog.getByRole("checkbox", { name: /本来就在这里/ }));
    expect(checked(dialog.getByRole("radio", { name: /恢复为“旧身份”/ }))).toBe(true);
  });
});

describe("I2：任何身份参与的专辑", () => {
  it("显示角色，按名称筛选，跨页和筛选保留选择与已选 N，预览提交所有已选组", async () => {
    const context = setup();
    seedSplit(context);
    const albums = seedAlbums(context);
    const dialog = await openDialog(context, "拆开");
    await context.user.click(dialog.getByRole("radio", { name: "按专辑分给另一位同名的人" }));
    const first = await dialog.findByRole("checkbox", { name: /参与专辑 01.*作曲.*演奏/ });
    await context.user.click(first);
    await context.user.click(dialog.getByRole("button", { name: "下一页" }));
    await context.user.click(await dialog.findByRole("checkbox", { name: /参与专辑 21/ }));
    expect(dialog.getByRole("group", { name: "分出去的专辑（已选 2 张）" })).toBeTruthy();
    await context.user.type(dialog.getByRole("textbox", { name: "按专辑名筛选" }), "参与专辑 01{Enter}");
    await waitFor(() => expect(dialog.queryByRole("checkbox", { name: /参与专辑 21/ })).toBeNull());
    expect(checked(await dialog.findByRole("checkbox", { name: /参与专辑 01/ }))).toBe(true);
    expect(dialog.getByRole("group", { name: "分出去的专辑（已选 2 张）" })).toBeTruthy();
    const request = context.api.requestsTo("GET", endpoint("albums")).at(-1);
    const query = new URL(request?.path ?? "", "http://localhost").searchParams;
    expect(query.get("participation")).toBe("any");
    expect(query.get("q")).toBe("参与专辑 01");
    expect(query.get("page")).toBe("1");
    await previewSplit(context, dialog);
    expect(context.api.requestsTo("POST", endpoint("split-preview")).at(-1)?.body).toEqual({ mode: "albums", name_key_ids: [keyA, keyB, keyC], group_ids: [albums[0]?.group.id, albums[20]?.group.id] });
  });
});

describe("艺人治理：冲突和不确定结果", () => {
  it.each(["revision_conflict", "artist_scope_changed"])("拆开遇到 %s 自动重新预览，再确认使用新依据而不是自动重提", async (code) => {
    const context = setup();
    seedSplit(context, { sources: false });
    const dialog = await openDialog(context, "拆开");
    await context.user.click(await dialog.findByRole("checkbox", { name: "旧艺人乙" }));
    await previewSplit(context, dialog);
    if (code === "revision_conflict") context.detail.revision += 1;
    else context.data.scopeVersion += 1;
    await context.user.click(dialog.getByRole("button", { name: "拆开" }));
    await dialog.findByText(code === "revision_conflict" ? "艺人刚被修改过" : "预览已经过时");
    await waitFor(() => expect(context.api.requestsTo("POST", endpoint("split-preview")).length).toBeGreaterThan(1));
    expect(context.api.requestsTo("POST", endpoint("split"))).toHaveLength(1);
    await context.user.click(dialog.getByRole("button", { name: "拆开" }));
    await screen.findByText("已拆出“旧艺人乙”。");
    expect(context.api.requestsTo("POST", endpoint("split"))[1]?.body).toMatchObject({ expected_revision: context.detail.revision - 1, basis: code === "revision_conflict" ? "scope-2-1" : "scope-1-2" });
  });

  it("改名遇到 revision_conflict 保留草稿，刷新版本后由用户重新确认", async () => {
    const context = setup();
    const dialog = await openDialog(context, "修改主名");
    const name = dialog.getByRole("textbox", { name: "主名" });
    await context.user.clear(name);
    await context.user.type(name, "保留的草稿");
    context.detail.revision = 9;
    await context.user.click(dialog.getByRole("button", { name: "保存" }));
    await dialog.findByText("艺人刚被修改过");
    expect(dialog.getByRole<HTMLInputElement>("textbox", { name: "主名" }).value).toBe("保留的草稿");
    await context.user.click(dialog.getByRole("button", { name: "保存" }));
    await screen.findByText("主名已改为“保留的草稿”。");
    expect(context.api.requestsTo("PATCH", endpoint("name"))[1]?.body).toEqual({ expected_revision: 9, mode: "set", name: "保留的草稿" });
  });

  it("artist_catalog_not_ready 是明确整理提示而非数据库故障或不确定重试", async () => {
    const context = setup();
    const dialog = await prepareMerge(context);
    context.api.on("POST", endpoint("merge"), () => {
      const result = artistError("artist_catalog_not_ready");
      return jsonResponse(result.status, result.body);
    });
    await context.user.click(dialog.getByRole("button", { name: "合并" }));
    await dialog.findByText("艺人信息正在整理，请稍后再试。");
    expect(dialog.queryByText("结果未确认")).toBeNull();
    expect(dialog.queryByRole("button", { name: "重试合并" })).toBeNull();
    expect(context.api.requestsTo("POST", endpoint("merge"))).toHaveLength(1);
  });

  it.each(["合并艺人", "拆开", "修改主名"] as const)("%s 响应丢失后沿用原请求体和幂等键重试，不执行第二次", async (kind) => {
    const context = setup();
    seedSplit(context, { sources: false });
    // 合并的另一方必须仍是活动身份。
    context.source.redirect_artist_id = null;
    context.source.artist.name = "待合并艺人";
    let dialog: ReturnType<typeof within>;
    if (kind === "合并艺人") dialog = await prepareMerge(context);
    else {
      dialog = await openDialog(context, kind);
      if (kind === "拆开") {
        await context.user.click(await dialog.findByRole("checkbox", { name: "旧艺人乙" }));
        await previewSplit(context, dialog);
      } else {
        await context.user.clear(dialog.getByRole("textbox", { name: "主名" }));
        await context.user.type(dialog.getByRole("textbox", { name: "主名" }), "改名结果");
      }
    }
    const action = kind === "合并艺人" ? "merge" : kind === "拆开" ? "split" : "name";
    const method = kind === "修改主名" ? "PATCH" : "POST";
    let first = true;
    context.api.on(method, endpoint(action), (request) => {
      const response = context.reply(request);
      if (first) { first = false; throw new TypeError("模拟响应丢失"); }
      return response;
    });
    const button = kind === "合并艺人" ? "合并" : kind === "拆开" ? "拆开" : "保存";
    await context.user.click(dialog.getByRole("button", { name: button }));
    await dialog.findByText("结果未确认");
    const revision = context.detail.revision;
    await context.user.click(await dialog.findByRole("button", { name: `重试${button}` }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const requests = context.api.requestsTo(method, endpoint(action));
    expect(requests).toHaveLength(2);
    expect(requests[0]?.headers.get("Idempotency-Key")).toMatch(/^[\da-f-]{36}$/);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(requests[1]?.body).toEqual(requests[0]?.body);
    expect(context.detail.revision).toBe(revision);
  });
});

describe("候选和操作记录", () => {
  it("候选合并默认保留参与更多者，可改另一方并携带双方版本号", async () => {
    const context = setup();
    const pair = seedArtistCandidates(context.data)[0];
    if (pair === undefined) throw new Error("缺少候选夹具");
    pair.left.track_count = 1;
    pair.right.track_count = 10;
    renderApp({ path: "/artist-candidates" });
    await context.user.click(await screen.findByRole("button", { name: `合并“${pair.left.name}”和“${pair.right.name}”…` }));
    const dialog = within(await screen.findByRole("dialog", { name: "合并为同一位艺人" }));
    expect(checked(dialog.getByRole("radio", { name: new RegExp(pair.right.name) }))).toBe(true);
    expect(dialog.getByRole("status").textContent).toContain("合并不能撤销，需要时可以再拆开。");
    await context.user.click(dialog.getByRole("radio", { name: new RegExp(pair.left.name) }));
    await waitFor(() => expect(dialog.getByRole<HTMLButtonElement>("button", { name: "合并" }).disabled).toBe(false));
    await context.user.click(dialog.getByRole("button", { name: "合并" }));
    await screen.findByText(`已把“${pair.right.name}”并入“${pair.left.name}”。`);
    expect(context.api.requestsTo("POST", `/api/v1/artists/${pair.left.id}/merge`)[0]?.body).toEqual({ expected_revision: 1, source_artist_id: pair.right.id, source_expected_revision: 1 });
  });

  it("候选显示具体理由，忽略后消失，可用新意图恢复同一组 key_pairs", async () => {
    const context = setup();
    const pair = seedArtistCandidates(context.data)[0];
    if (pair === undefined) throw new Error("缺少候选夹具");
    renderApp({ path: "/artist-candidates" });
    const ignoreName = `忽略“${pair.left.name}”和“${pair.right.name}”`;
    const ignore = await screen.findByRole("button", { name: ignoreName });
    expect(screen.getAllByText(/同一首曲目的.+写了|同一张专辑的专辑艺人写/).length).toBeGreaterThan(0);
    await context.user.click(ignore);
    const restore = await screen.findByRole("button", { name: "恢复提示" });
    expect(screen.queryByRole("button", { name: ignoreName })).toBeNull();
    await context.user.click(restore);
    await screen.findByText(`已恢复“${pair.left.name}”和“${pair.right.name}”的提示。`);
    expect(await screen.findByRole("button", { name: ignoreName })).toBeTruthy();
    const writes = context.api.requestsTo("POST", `/api/v1/artists/${pair.left.id}/candidate-ignore`);
    expect(writes).toHaveLength(2);
    expect(writes[0]?.body).toEqual({ expected_revision: 1, other_artist_id: pair.right.id, other_expected_revision: 1, key_pairs: pair.key_pairs, ignored: true });
    expect(writes[1]?.body).toEqual({ expected_revision: 2, other_artist_id: pair.right.id, other_expected_revision: 2, key_pairs: pair.key_pairs, ignored: false });
    expect(writes[1]?.headers.get("Idempotency-Key")).not.toBe(writes[0]?.headers.get("Idempotency-Key"));
    expect(screen.queryByRole("button", { name: /撤销/ })).toBeNull();
  });

  it("成员艺人页没有治理按钮、操作记录与候选入口，也不读管理员来源接口", async () => {
    const context = setup(true);
    renderApp({ path: `/artists/${primary}` });
    await screen.findByRole("heading", { name: context.detail.artist.name, level: 1 });
    for (const name of ["合并艺人…", "拆开…", "修改主名…"]) expect(screen.queryByRole("button", { name })).toBeNull();
    expect(screen.queryByRole("link", { name: "操作记录" })).toBeNull();
    expect(screen.queryByRole("link", { name: "可能是同一位" })).toBeNull();
    expect(context.api.requestsTo("GET", endpoint("merged-sources"))).toHaveLength(0);
  });

  it("操作记录的 artist 筛选传到服务端，对象链接到艺人，类型和反向办法不落入专辑默认文案", async () => {
    const context = setup();
    const page = decodeOperationPage(sampleBody("operations/artists"));
    context.api.on("GET", "/api/v1/operations", () => jsonResponse(200, page));
    renderApp({ path: `/settings/operations?artist=${primary}` });
    const table = await screen.findByRole("table", { name: "操作记录" });
    expect(within(table).getByRole("columnheader", { name: "对象" })).toBeTruthy();
    expect(await screen.findByRole("heading", { name: "这位艺人的记录" })).toBeTruthy();
    expect(new URL(context.api.requestsTo("GET", "/api/v1/operations")[0]?.path ?? "", "http://localhost").searchParams.get("artist_id")).toBe(primary);
    for (const item of page.items) {
      if (item.artist === null) continue;
      expect(within(table).getAllByRole("link", { name: item.artist.name }).some((link) => link.getAttribute("href") === `/artists/${item.artist?.id}`)).toBe(true);
    }
    expect(within(table).getAllByText("拆开艺人").length).toBeGreaterThan(0);
    expect(within(table).getAllByText("合并艺人").length).toBeGreaterThan(0);
    expect(within(table).getAllByText("修改艺人主名").length).toBeGreaterThan(0);
    expect(within(table).queryByRole("button", { name: /撤销/ })).toBeNull();
    expect(within(table).queryByText("专辑已不可见")).toBeNull();
    expect(within(table).queryByText("之后又有修改")).toBeNull();
  });
});

describe("艺人治理：边界补充", () => {
  it("拆开预览明确列出已分给别人而不会移动的处数", async () => {
    const context = setup();
    seedSplit(context, { sources: false });
    const preview = decodeArtistSplitPreview(sampleBody("governance/artist-split-preview-aliases"));
    context.api.on("POST", endpoint("split-preview"), () => jsonResponse(200, {
      ...preview, artist_id: primary, revision: 1,
      scope: { ...preview.scope, name_key_ids: [keyB], release_count: 3, track_count: 8, excluded_scope_count: 2, excluded_scopes: [{ release_id: artistTestId(3000) }, { release_id: artistTestId(3001) }] },
    }));
    const dialog = await openDialog(context, "拆开");
    await context.user.click(await dialog.findByRole("checkbox", { name: "旧艺人乙" }));
    await previewSplit(context, dialog);
    expect(dialog.getByRole("status").textContent).toContain("3 个版本、8 首曲目");
    expect(dialog.getByRole("status").textContent).toContain("已经分给别人的 2 处不动。");
    expect(context.api.requestsTo("POST", endpoint("split"))).toHaveLength(0);
  });

  it("按专辑也可以恢复旧身份，提交明确所选专辑和旧身份版本号", async () => {
    const context = setup();
    seedSplit(context);
    const albums = seedAlbums(context, 1);
    const dialog = await openDialog(context, "拆开");
    await context.user.click(await dialog.findByRole("button", { name: "拆回 旧身份" }));
    await context.user.click(dialog.getByRole("radio", { name: "按专辑分给另一位同名的人" }));
    await context.user.click(await dialog.findByRole("checkbox", { name: /参与专辑 01/ }));
    await previewSplit(context, dialog);
    await context.user.click(dialog.getByRole("button", { name: "拆开" }));
    await screen.findByText("已拆出“旧身份”。");
    expect(context.api.requestsTo("POST", endpoint("split"))[0]?.body).toEqual({ mode: "albums", name_key_ids: [keyA, keyB], group_ids: albums.map((item) => item.group.id), expected_revision: 1, basis: "scope-1-1", target: { kind: "restore", artist_id: otherId, expected_revision: 4 } });
  });

  it("合并 revision_conflict 后保留选择，重新读取双方版本再确认", async () => {
    const context = setup();
    const dialog = await prepareMerge(context);
    context.detail.revision = 5;
    context.source.revision = 7;
    await context.user.click(dialog.getByRole("button", { name: "合并" }));
    await dialog.findByText("艺人刚被修改过");
    expect(checked(dialog.getByRole("radio", { name: /待合并艺人/ }))).toBe(true);
    expect(context.api.requestsTo("POST", endpoint("merge"))).toHaveLength(1);
    await context.user.click(dialog.getByRole("button", { name: "合并" }));
    await screen.findByText(/已把“待合并艺人”并入/);
    expect(context.api.requestsTo("POST", endpoint("merge"))[1]?.body).toEqual({ expected_revision: 5, source_artist_id: otherId, source_expected_revision: 7 });
  });

  it("忽略请求未到达时明确结果未确认，重试保持同一个幂等键", async () => {
    const context = setup();
    const pair = seedArtistCandidates(context.data)[0];
    if (pair === undefined) throw new Error("缺少候选夹具");
    const path = `/api/v1/artists/${pair.left.id}/candidate-ignore`;
    let first = true;
    context.api.on("POST", path, (request) => {
      if (first) { first = false; throw new TypeError("模拟请求未到达"); }
      return context.reply(request);
    });
    renderApp({ path: "/artist-candidates" });
    await context.user.click(await screen.findByRole("button", { name: `忽略“${pair.left.name}”和“${pair.right.name}”` }));
    await screen.findByText("结果未确认");
    await context.user.click(screen.getByRole("button", { name: "重试" }));
    await screen.findByRole("button", { name: "恢复提示" });
    const requests = context.api.requestsTo("POST", path);
    expect(requests).toHaveLength(2);
    expect(requests[0]?.headers.get("Idempotency-Key")).toMatch(/^[\da-f-]{36}$/);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(requests[1]?.body).toEqual(requests[0]?.body);
  });

  it.each(["/artist-candidates", "/settings/operations"])("成员直接访问 %s 也不能加载治理列表", async (path) => {
    const context = setup(true);
    renderApp({ path });
    await screen.findByRole("heading", { name: "需要管理员权限" });
    expect(context.api.requestsTo("GET", path === "/settings/operations" ? "/api/v1/operations" : "/api/v1/artist-candidates")).toHaveLength(0);
  });

  it("操作记录中不可见艺人只显示名称，已合并艺人仍保留旧身份链接", async () => {
    const context = setup();
    const original = decodeOperationPage(sampleBody("operations/artists")).items[0];
    if (original === undefined || original.artist === null) throw new Error("缺少艺人操作记录");
    const items = [
      { ...original, id: artistTestId(2000), artist: { ...original.artist, id: artistTestId(2001), name: "已不可见的艺人", visible: false, redirect_artist_id: null } },
      { ...original, id: artistTestId(2002), artist: { ...original.artist, id: artistTestId(2003), name: "已经合并的艺人", visible: false, redirect_artist_id: primary } },
    ];
    context.api.on("GET", "/api/v1/operations", () => jsonResponse(200, { items, pagination: { page: 1, page_size: 50, total: 2 } }));
    renderApp({ path: "/settings/operations" });
    const table = within(await screen.findByRole("table", { name: "操作记录" }));
    expect(table.getByText("已不可见的艺人")).toBeTruthy();
    expect(table.queryByRole("link", { name: "已不可见的艺人" })).toBeNull();
    expect(table.getByRole("link", { name: "已经合并的艺人" }).getAttribute("href")).toBe(`/artists/${artistTestId(2003)}`);
  });
});

describe("浏览器已确认的艺人治理回归", () => {
  it("合并后缓存过 redirect 的旧身份拆回后，打开成功链接必须留在恢复的艺人页", async () => {
    const context = setup();
    const { runtime, history } = renderApp({ path: `/artists/${primary}` });
    await context.user.click(await screen.findByRole("button", { name: "合并艺人…" }));
    const merge = within(await screen.findByRole("dialog", { name: "合并艺人" }));
    await context.user.type(merge.getByRole("textbox", { name: "查找艺人" }), "待合并艺人{Enter}");
    await context.user.click(await merge.findByRole("radio", { name: /待合并艺人/ }));
    await waitFor(() => expect(merge.getByRole<HTMLButtonElement>("button", { name: "合并" }).disabled).toBe(false));
    await context.user.click(merge.getByRole("button", { name: "合并" }));
    await screen.findByText(/已把“待合并艺人”并入/);

    // 经真实详情请求缓存旧身份的 redirect，再回到主艺人；不手写 Query 缓存形状。
    await act(async () => { await runtime.router.navigate({ to: "/artists/$artistId", params: { artistId: otherId } }); });
    await waitFor(() => expect(history.location.pathname).toBe(`/artists/${primary}`));
    await context.user.click(await screen.findByRole("button", { name: "拆开…" }));
    const split = within(await screen.findByRole("dialog", { name: "拆开艺人" }));
    await context.user.click(await split.findByRole("button", { name: "拆回 待合并艺人" }));
    await previewSplit(context, split);
    await context.user.click(split.getByRole("button", { name: "拆开" }));
    const openRestored = await screen.findByRole("link", { name: "打开“待合并艺人”" });
    expect(context.source.redirect_artist_id).toBeNull();

    // 挂起恢复身份的重读，让“缓存先渲染，网络随后返回”的次序确定，不靠 sleep 或设备速度。
    const gate = deferred<void>();
    const sourcePath = `/api/v1/artists/${otherId}`;
    const beforeReads = context.api.requestsTo("GET", sourcePath).length;
    let delivered: Response | null = null;
    context.api.on("GET", sourcePath, async (request) => {
      await gate.promise;
      delivered = context.reply(request);
      return delivered;
    });
    await context.user.click(openRestored);
    await waitFor(() => expect(context.api.requestsTo("GET", sourcePath).length).toBeGreaterThan(beforeReads));
    await act(async () => { gate.resolve(); await gate.promise; });
    await waitFor(() => expect(delivered?.status).toBe(200));
    await waitFor(() => expect(history.location.pathname).toBe(`/artists/${otherId}`));
    expect(await screen.findByRole("heading", { name: "待合并艺人", level: 1 })).toBeTruthy();
  });

  it("候选从 preparing 发布为 ready 且 epoch 增大时，同页自动重读并恢复候选", async () => {
    const context = setup();
    const pair = seedArtistCandidates(context.data)[0];
    if (pair === undefined) throw new Error("缺少候选夹具");
    context.data.state = "preparing";
    context.api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, {
      artists_state: context.data.state, epoch: context.data.epoch, search_mode: "normalized",
    }));
    const { runtime, history } = renderApp({ path: "/artist-candidates" });
    // 保留 503 重试次数，只将默认退避缩为零，先让整理态稳定，再验证 epoch 发布。
    runtime.queryClient.setDefaultOptions({ queries: { ...runtime.queryClient.getDefaultOptions().queries, retryDelay: 0 } });
    await screen.findByText("艺人信息正在整理，请稍后再试。");
    const beforeReads = context.api.requestsTo("GET", "/api/v1/artist-candidates").length;
    const statusReads = context.api.requestsTo("GET", "/api/v1/catalog/status").length;
    context.data.state = "ready";
    context.data.epoch += 1;
    // 驱动下一次目录状态请求（同轮询返回），只刷新状态，不替产品刷新 governance 查询。
    await act(async () => {
      await runtime.queryClient.invalidateQueries({ queryKey: catalogStateKeys.current(runtime.session.currentGeneration()) });
    });
    expect(context.api.requestsTo("GET", "/api/v1/catalog/status").length).toBeGreaterThan(statusReads);
    expect(history.location.pathname).toBe("/artist-candidates");
    await waitFor(() => expect(context.api.requestsTo("GET", "/api/v1/artist-candidates").length).toBeGreaterThan(beforeReads));
    expect(screen.queryByText("艺人信息正在整理，请稍后再试。")).toBeNull();
    expect(await screen.findByRole("button", { name: `忽略“${pair.left.name}”和“${pair.right.name}”` })).toBeTruthy();
  });
});
