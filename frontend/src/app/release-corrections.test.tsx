import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { albumGroup } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { markUncertain, respondToCorrect } from "../test/governance-data";
import { jsonResponse, type RecordedRequest } from "../test/http";
import { renderApp } from "../test/render-app";

const symphonies = () => albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, medium_count: 2, track_count: 3, artwork: true });
const releaseId = symphonies().representative.id;
const groupId = symphonies().id;
const correctionsPath = `/api/v1/releases/${releaseId}/corrections`;

function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: [symphonies()] });
  const governance = installGovernanceApi(api, catalog);
  return { api, catalog, governance };
}

const patchRequests = (api: ReturnType<typeof setup>["api"]) => api.requestsTo("PATCH", correctionsPath);
const openEditor = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(await screen.findByRole("button", { name: "编辑发行信息" }));
  return screen.findByRole("dialog", { name: "编辑发行信息" });
};
// 有人工修正的字段，图例里还有“人工修正”标记，可访问名称是“标题 人工修正”，所以按前缀匹配。
const prefixed = (name: string) => new RegExp(`^${name}`);
const field = (dialog: HTMLElement, name: string) => within(within(dialog).getByRole("group", { name: prefixed(name) }));
const saveButton = (dialog: HTMLElement) => within(dialog).getByRole("button", { name: /^(保存修改|重试保存|正在保存…)$/ });

describe("编辑发行信息：入口与读取", () => {
  it("普通成员没有编辑入口，也不会请求修正状态", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: `/albums/${groupId}` });
    await screen.findByRole("heading", { level: 1, name: "Sample Symphonies" });
    await screen.findByText("Track 1-01");
    expect(screen.queryByRole("button", { name: "编辑发行信息" })).toBeNull();
    expect(api.requestsTo("GET", /\/corrections$/)).toHaveLength(0);
  });

  it("管理员打开对话框才读取修正状态：八个字段、扫描值、不确定标记与“确认扫描值”", async () => {
    const { api, governance } = setup();
    markUncertain(governance, releaseId, ["title", "year"]);
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    await screen.findByRole("heading", { level: 1, name: "Sample Symphonies" });
    expect(api.requestsTo("GET", correctionsPath)).toHaveLength(0);

    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    expect(api.requestsTo("GET", correctionsPath)).toHaveLength(1);
    expect(within(dialog).getAllByRole("group").map((group) => group.querySelector("legend")?.textContent)).toEqual(["标题", "专辑艺人", "年份", "流派", "厂牌", "编号", "版本说明", "条码"]);
    expect(field(dialog, "标题").getByText("Sample Symphonies")).toBeTruthy();
    expect(field(dialog, "年份").getByText("1998")).toBeTruthy();
    // 标题与专辑艺人不能留空；其余字段三选一。
    expect(field(dialog, "标题").getAllByRole("radio").map((radio) => radio.parentElement?.textContent)).toEqual(["使用扫描值", "自定义"]);
    expect(field(dialog, "流派").getAllByRole("radio").map((radio) => radio.parentElement?.textContent)).toEqual(["使用扫描值", "自定义", "留空"]);
    // 不确定的字段有提示与“确认扫描值”，确定的没有。
    expect(field(dialog, "标题").getByText("扫描结果不确定")).toBeTruthy();
    expect(field(dialog, "标题").getByRole("button", { name: "确认扫描值" })).toBeTruthy();
    expect(field(dialog, "年份").getByRole("button", { name: "确认扫描值" })).toBeTruthy();
    expect(field(dialog, "流派").queryByText("扫描结果不确定")).toBeNull();
    expect(field(dialog, "流派").queryByRole("button", { name: "确认扫描值" })).toBeNull();
    // 没有改动时不能保存，原因就写在旁边。
    expect(saveButton(dialog).hasAttribute("disabled")).toBe(true);
    expect(within(dialog).getByText("还没有修改任何字段。")).toBeTruthy();
  });

  it("读取失败时在对话框里说明并可以重试；发行已不可见时只能关闭", async () => {
    const { api } = setup();
    // 500 不会被查询自动重试（只有网络失败与 503 会），失败立刻显示。
    api.on("GET", correctionsPath, () => jsonResponse(500, { error: { code: "database_error", message: "无法读取修正状态" }, request_id: "r-1" }));
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "重试" })).toBeTruthy();
    api.on("GET", correctionsPath, () => jsonResponse(404, { error: { code: "not_found", message: "发行版本不存在" }, request_id: "r-2" }));
    await user.click(within(dialog).getByRole("button", { name: "重试" }));
    expect(await within(dialog).findByText("这个版本已经找不到了，可能刚重新扫描过。")).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "重试" })).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "关闭" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "编辑发行信息" })).toBeNull());
  });
});

describe("编辑发行信息：保存", () => {
  it("只提交动过且确有变化的字段；成功后对话框关闭，页面显示新的有效值，提示里有“撤销”，焦点回到入口", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });

    await user.click(field(dialog, "标题").getByRole("radio", { name: "自定义" }));
    const title = field(dialog, "标题").getByRole("textbox", { name: "标题（自定义）" });
    await user.clear(title);
    await user.type(title, "  Sample   Symphonies (corrected) ");
    await user.click(field(dialog, "年份").getByRole("radio", { name: "自定义" }));
    const year = field(dialog, "年份").getByRole("textbox", { name: "年份（自定义）" });
    await user.clear(year);
    await user.type(year, "1975");
    await user.click(field(dialog, "流派").getByRole("radio", { name: "留空" }));
    // 动了又改回来的字段不提交。
    await user.click(field(dialog, "厂牌").getByRole("radio", { name: "留空" }));
    await user.click(field(dialog, "厂牌").getByRole("radio", { name: "使用扫描值" }));
    expect(within(dialog).getByText("将修改：标题、年份、流派。")).toBeTruthy();

    await user.click(saveButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "编辑发行信息" })).toBeNull());
    const [request] = patchRequests(api);
    expect(patchRequests(api)).toHaveLength(1);
    expect(request?.body).toEqual({
      expected_revision: 1,
      changes: {
        title: { mode: "set", value: "Sample Symphonies (corrected)" },
        year: { mode: "set", value: 1975 },
        genre: { mode: "null" },
      },
    });
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

    // 页面显示新的有效值；提示说明保存了什么并给出“撤销”；焦点回到打开对话框的按钮。
    expect(await screen.findByRole("heading", { level: 1, name: "Sample Symphonies (corrected)" })).toBeTruthy();
    expect(screen.getByText("已保存修改：标题、年份、流派。")).toBeTruthy();
    expect(screen.getByRole("button", { name: "撤销" })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "编辑发行信息" }));
    expect(screen.getAllByText("1975").length).toBeGreaterThan(0);
  });

  it("输入有问题时不能保存：年份必须是整数，自定义不能为空，标题不能留空（没有这个选项）", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });

    await user.click(field(dialog, "年份").getByRole("radio", { name: "自定义" }));
    const year = field(dialog, "年份").getByRole("textbox", { name: "年份（自定义）" });
    await user.clear(year);
    await user.type(year, "19x5");
    expect(field(dialog, "年份").getByText("请填写有效的年份。")).toBeTruthy();
    expect(year.getAttribute("aria-invalid")).toBe("true");
    expect(saveButton(dialog).hasAttribute("disabled")).toBe(true);
    expect(within(dialog).getByText("有 1 个字段需要改好才能保存。")).toBeTruthy();

    await user.click(field(dialog, "标题").getByRole("radio", { name: "自定义" }));
    await user.clear(field(dialog, "标题").getByRole("textbox", { name: "标题（自定义）" }));
    expect(field(dialog, "标题").getByText("标题不能为空。")).toBeTruthy();
    expect(within(dialog).getByText("有 2 个字段需要改好才能保存。")).toBeTruthy();

    await user.clear(year);
    await user.type(year, "1975");
    await user.type(field(dialog, "标题").getByRole("textbox", { name: "标题（自定义）" }), "新标题");
    expect(saveButton(dialog).hasAttribute("disabled")).toBe(false);
    expect(patchRequests(api)).toHaveLength(0);
  });

  it("“确认扫描值”：把当前扫描值存为修正，提交的是 set；已有修正的字段不再提供它", async () => {
    const { api, governance } = setup();
    markUncertain(governance, releaseId, ["title", "genre"]);
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    let dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });

    await user.click(field(dialog, "标题").getByRole("button", { name: "确认扫描值" }));
    expect((field(dialog, "标题").getByRole("radio", { name: "自定义" }) as HTMLInputElement).checked).toBe(true);
    expect((field(dialog, "标题").getByRole("textbox", { name: "标题（自定义）" }) as HTMLInputElement).value).toBe("Sample Symphonies");
    expect(field(dialog, "标题").queryByRole("button", { name: "确认扫描值" })).toBeNull();
    await user.click(saveButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "编辑发行信息" })).toBeNull());
    expect(patchRequests(api)[0]?.body).toEqual({ expected_revision: 1, changes: { title: { mode: "set", value: "Sample Symphonies" } } });

    // 再次打开：标题已有修正（“人工修正”），不再提供“确认”；流派仍不确定。
    await user.click(screen.getByRole("button", { name: "编辑发行信息" }));
    dialog = await screen.findByRole("dialog", { name: "编辑发行信息" });
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    expect(field(dialog, "标题").getByText("人工修正")).toBeTruthy();
    expect(field(dialog, "标题").queryByRole("button", { name: "确认扫描值" })).toBeNull();
    expect(field(dialog, "流派").getByRole("button", { name: "确认扫描值" })).toBeTruthy();
  });

  it("取消不提交；再次打开时从最新的状态开始", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    let dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    await user.click(field(dialog, "流派").getByRole("radio", { name: "留空" }));
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "编辑发行信息" })).toBeNull());
    expect(patchRequests(api)).toHaveLength(0);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "编辑发行信息" }));

    dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("流派") });
    expect((field(dialog, "流派").getByRole("radio", { name: "使用扫描值" }) as HTMLInputElement).checked).toBe(true);
    expect(api.requestsTo("GET", correctionsPath)).toHaveLength(2);
  });
});

describe("编辑发行信息：冲突与结果不确定", () => {
  it("409：对话框保持打开、输入保留、重新载入最新状态；别人改过的字段不会被我的提交盖掉", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    await user.click(field(dialog, "标题").getByRole("radio", { name: "自定义" }));
    const title = field(dialog, "标题").getByRole("textbox", { name: "标题（自定义）" });
    await user.clear(title);
    await user.type(title, "我的标题");

    // 这时另一位管理员把流派设成了“Jazz”：版本号变成 2。
    expect(respondToCorrect(governance, releaseId, "00000000-0000-4000-8000-0000000000c1", { expected_revision: 1, changes: { genre: { mode: "set", value: "Jazz" } } }).status).toBe(200);
    await user.click(saveButton(dialog));

    expect(await within(dialog).findByText("这张专辑刚被修改过")).toBeTruthy();
    expect(within(dialog).getByText("已重新载入，你的输入还在，请核对后再保存。")).toBeTruthy();
    expect((title as HTMLInputElement).value).toBe("我的标题");
    // 没有动过的流派显示最新的状态（别人刚设的 Jazz），而不是我打开时的样子。
    await waitFor(() => expect((field(dialog, "流派").getByRole("radio", { name: "自定义" }) as HTMLInputElement).checked).toBe(true));
    expect(field(dialog, "流派").getByRole("textbox", { name: "流派（自定义）" })).toHaveProperty("value", "Jazz");

    await user.click(saveButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "编辑发行信息" })).toBeNull());
    const requests = patchRequests(api);
    expect(requests).toHaveLength(2);
    expect(requests[1]?.body).toEqual({ expected_revision: 2, changes: { title: { mode: "set", value: "我的标题" } } });
    expect(requests[1]?.headers.get("Idempotency-Key")).not.toBe(requests[0]?.headers.get("Idempotency-Key"));
  });

  it("结果不确定：对话框保持打开，“保存”变成“重试保存”，沿用同一个幂等键，不会重复执行", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    await user.click(field(dialog, "条码").getByRole("radio", { name: "留空" }));

    // 第一次请求在路上丢了（服务器没有收到）。
    api.on("PATCH", correctionsPath, () => {
      throw new Error("connection reset");
    });
    await user.click(saveButton(dialog));
    expect(await within(dialog).findByText("结果未确认")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "重试保存" })).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "保存修改" })).toBeNull();

    // 连接恢复：重试沿用同一个幂等键。
    api.on("PATCH", correctionsPath, (request: RecordedRequest) => {
      const result = respondToCorrect(governance, releaseId, request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user.click(within(dialog).getByRole("button", { name: "重试保存" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "编辑发行信息" })).toBeNull());
    const requests = patchRequests(api);
    expect(requests).toHaveLength(2);
    expect(requests[1]?.headers.get("Idempotency-Key")).toBe(requests[0]?.headers.get("Idempotency-Key"));
    expect(requests[1]?.body).toEqual(requests[0]?.body);
    expect(screen.getByText("已保存修改：条码。")).toBeTruthy();
  });

  it("后端拒绝（400 invalid_correction）：留在对话框里显示后端给出的说明，可以改了再存", async () => {
    const { api } = setup();
    api.on("PATCH", correctionsPath, () => jsonResponse(400, { error: { code: "invalid_correction", message: "标题含有不允许的字符" }, request_id: "r-9" }));
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    await user.click(field(dialog, "流派").getByRole("radio", { name: "留空" }));
    await user.click(saveButton(dialog));
    expect(await within(dialog).findByText("标题含有不允许的字符")).toBeTruthy();
    expect(within(dialog).getByText("没有保存")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "保存修改" })).toBeTruthy();
    expect(patchRequests(api)).toHaveLength(1);
  });
});

describe("撤销与人工修正的标记", () => {
  it("提示里的“撤销”撤销刚才的修改：页面回到扫描值，提示改成撤销的结果", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    await user.click(field(dialog, "标题").getByRole("radio", { name: "自定义" }));
    const title = field(dialog, "标题").getByRole("textbox", { name: "标题（自定义）" });
    await user.clear(title);
    await user.type(title, "改过的标题");
    await user.click(saveButton(dialog));
    await screen.findByRole("heading", { level: 1, name: "改过的标题" });

    await user.click(screen.getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeTruthy();
    await screen.findByRole("heading", { level: 1, name: "Sample Symphonies" });
    const reverts = api.requestsTo("POST", /\/revert$/);
    expect(reverts).toHaveLength(1);
    expect(reverts[0]?.path).toMatch(/^\/api\/v1\/operations\/[0-9a-f-]{36}\/revert$/);
    expect(reverts[0]?.body).toBeUndefined();
    // 撤销的结果提示里没有“撤销”（撤销不能再撤销）。
    expect(screen.queryByRole("button", { name: "撤销" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "关闭" }));
    expect(screen.queryByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeNull();
  });

  it("撤销被拒绝（之后又有修改）：说明原因，页面刷新", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    const dialog = await openEditor(user);
    await within(dialog).findByRole("group", { name: prefixed("标题") });
    await user.click(field(dialog, "流派").getByRole("radio", { name: "留空" }));
    await user.click(saveButton(dialog));
    await screen.findByText("已保存修改：流派。");
    // 别人在此期间又改了这张专辑：撤销被拒绝。
    respondToCorrect(governance, releaseId, "00000000-0000-4000-8000-0000000000c2", { expected_revision: 2, changes: { label: { mode: "null" } } });
    await user.click(screen.getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("这张专辑之后又被修改过，不能撤销这一条。只有最近一次修改可以撤销。")).toBeTruthy();
    expect(api.requestsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  it("有人工修正的字段在发行信息、头部与证据里都标出“人工修正”，所有登录成员可见；证据照旧记录扫描结论", async () => {
    const { catalog, governance } = setup({ role: "user" });
    respondToCorrect(governance, releaseId, "00000000-0000-4000-8000-0000000000c3", {
      expected_revision: 1,
      changes: { genre: { mode: "set", value: "Romantic" }, label: { mode: "set", value: "Corrected Label" }, year: { mode: "set", value: 1975 }, title: { mode: "set", value: "Sample Symphonies (corrected)" } },
    });
    expect(catalog.releases.get(releaseId)?.corrected_fields).toEqual(["title", "year", "genre", "label"]);
    const user = userEvent.setup();
    renderApp({ path: `/albums/${groupId}` });
    await screen.findByRole("heading", { level: 1, name: "Sample Symphonies (corrected)" });
    await screen.findByText("Track 1-01");

    const facts = within(screen.getByRole("region", { name: "发行信息" }));
    expect(facts.getByText("厂牌").nextElementSibling?.textContent).toContain("Corrected Label");
    expect(facts.getByText("厂牌").nextElementSibling?.textContent).toContain("人工修正");
    expect(facts.getByText("流派").nextElementSibling?.textContent).toContain("人工修正");
    expect(facts.getByText("条码").nextElementSibling?.textContent).not.toContain("人工修正");
    const yearFact = screen.getByText("年份", { selector: "dt" }).parentElement;
    expect(yearFact?.textContent).toContain("1975");
    expect(yearFact?.textContent).toContain("人工修正");

    await user.click(screen.getByText("来源与证据"));
    const evidence = await screen.findByRole("table", { name: "证据摘要" });
    const rowOf = (name: string) => within(evidence).getByRole("rowheader", { name: prefixed(name) }).closest("tr");
    expect(rowOf("标题")?.textContent).toContain("人工修正");
    expect(rowOf("年份")?.textContent).toContain("人工修正");
    expect(rowOf("专辑艺人")?.textContent).not.toContain("人工修正");
    expect(screen.getByText(/标有“人工修正”的字段，页面上显示的是改过的值/)).toBeTruthy();
    // 普通成员看不到谁改的，也没有任何编辑入口。
    expect(screen.queryByRole("button", { name: "编辑发行信息" })).toBeNull();
    expect(screen.queryByText("curator")).toBeNull();
  });
});
