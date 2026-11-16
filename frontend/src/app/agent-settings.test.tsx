import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { setDockOpen } from "../features/steward";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installStewardApi, settingsOf, type StewardFake } from "../test/fake-steward";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const settingsPath = "/api/v1/me/steward/settings";
const probePath = "/api/v1/me/steward/settings/probe";

/** 没有配置过的设置（样本 settings-empty）。 */
const emptySettings = () => settingsOf({ endpoint: "", key_last_four: "", key_saved: false, model: "", protocol: "", probe_result: null, probed_at: null, display_name: "", updated_at: null });

function setup(options: { role?: "admin" | "user" } & Partial<StewardFake> = {}) {
  const { role = "admin", ...steward } = options;
  const api = installFakeApi();
  installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  api.on("GET", "/api/v1/me/steward/tokens", () => sampleResponse("steward/tokens/list"));
  const state = installStewardApi(api, steward);
  return { api, state };
}

async function openAgent(options: Parameters<typeof setup>[0] = {}) {
  const context = setup(options);
  const view = renderApp({ path: "/settings/agent" });
  await screen.findByRole("heading", { level: 1, name: "Agent" });
  return { ...context, ...view };
}

const group = (name: string) => within(screen.getByRole("region", { name }));
const sidebar = () => within(screen.getAllByRole("navigation", { name: "主导航" })[0] as HTMLElement);

afterEach(() => setDockOpen(false));

describe("设置 › Agent › 名字", () => {
  it("改名只提交 display_name；侧栏、面板标题与输入框提示不刷新就变成新名字", async () => {
    const { api } = await openAgent({ settings: settingsOf({ display_name: "" }) });
    expect(await sidebar().findByRole("button", { name: "管家" })).toBeTruthy();
    const name = await screen.findByRole("textbox", { name: "名字" });
    await userEvent.type(name, "  小蓝 ");
    await userEvent.click(group("名字").getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ display_name: "小蓝" }]));
    expect(await sidebar().findByRole("button", { name: "小蓝" })).toBeTruthy();
    expect(group("名字").getByText("已保存")).toBeTruthy();

    await userEvent.click(sidebar().getByRole("button", { name: "小蓝" }));
    const panel = within(await screen.findByRole("complementary", { name: "小蓝" }));
    expect(panel.getByRole("heading", { level: 2, name: "小蓝" })).toBeTruthy();
    expect(await panel.findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
  });

  it("恢复为“管家”提交空名字；超过 20 个字不能保存", async () => {
    const { api } = await openAgent({ settings: settingsOf({ display_name: "小蓝" }) });
    const name = await screen.findByRole("textbox", { name: "名字" });
    await userEvent.clear(name);
    await userEvent.type(name, "一二三四五六七八九十一二三四五六七八九十一");
    expect(screen.getByText("最多 20 个字。")).toBeTruthy();
    expect((group("名字").getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(group("名字").getByRole("button", { name: "恢复为“管家”" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ display_name: "" }]));
    expect(await sidebar().findByRole("button", { name: "管家" })).toBeTruthy();
  });
});

describe("设置 › Agent › 联网搜索（C8）", () => {
  const searchTestPath = "/api/v1/me/steward/settings/search-test";
  const source = (name: RegExp) => group("联网搜索").getByRole("radio", { name });
  const openSearch = async (options: Parameters<typeof setup>[0] = {}) => {
    const context = await openAgent(options);
    await screen.findByRole("region", { name: "联网搜索" });
    return context;
  };

  it("选 SearXNG 填地址保存，只提交来源与地址；保存后测试搜索显示结果数", async () => {
    const { api } = await openSearch();
    const test = await group("联网搜索").findByRole("button", { name: "测试搜索" });
    expect((test as HTMLButtonElement).disabled).toBe(true);
    expect((source(/^不联网搜索/) as HTMLInputElement).checked).toBe(true);
    await userEvent.click(source(/^自己的 SearXNG/));
    await userEvent.type(screen.getByLabelText("SearXNG 地址"), " http://192.168.1.30:8888 ");
    expect(group("联网搜索").getByText("保存之后才能测试。")).toBeTruthy();
    await userEvent.click(group("联网搜索").getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ search_source: "searxng", searxng_url: "http://192.168.1.30:8888" }]));
    expect(await group("联网搜索").findByText("已保存")).toBeTruthy();
    await waitFor(() => expect((test as HTMLButtonElement).disabled).toBe(false));
    await userEvent.click(test);
    expect(await group("联网搜索").findByText("这次搜到 3 条结果。")).toBeTruthy();
    expect(api.requestsTo("POST", searchTestPath).map((request) => request.body ?? null)).toEqual([null]);
  });

  it("测试没有通过时说明原因：SearXNG 没有开启 JSON、Tavily 额度用完", async () => {
    const { state } = await openSearch({ settings: settingsOf({ search_source: "searxng", searxng_url: "http://192.168.1.30:8888" }) });
    state.searchTestResult = { ok: false, code: "searxng_json_disabled" };
    await userEvent.click(await group("联网搜索").findByRole("button", { name: "测试搜索" }));
    expect(await group("联网搜索").findByText(/这个实例没有开启 JSON 输出/)).toBeTruthy();
    state.searchTestResult = { ok: false, code: "search_quota_exceeded" };
    await userEvent.click(group("联网搜索").getByRole("button", { name: "测试搜索" }));
    expect(await group("联网搜索").findByText("搜索服务的额度用完了，或者超出了套餐的限制。")).toBeTruthy();
  });

  it("Tavily 密钥：保存时作为 search_key 提交，之后只显示末四位，不留在页面与 mutation 缓存里；可以清除", async () => {
    const { api, runtime } = await openSearch();
    await userEvent.click(await group("联网搜索").findByRole("radio", { name: /^Tavily/ }));
    const save = group("联网搜索").getByRole("button", { name: "保存" }) as HTMLButtonElement;
    // 没有密钥时不能保存。
    expect(save.disabled).toBe(true);
    await userEvent.type(screen.getByLabelText("Tavily 密钥"), "tvly-synthetic-secret");
    await userEvent.click(save);
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ search_source: "tavily", search_key: "tvly-synthetic-secret" }]));
    expect(await group("联网搜索").findByText("已保存，末四位 cret")).toBeTruthy();
    expect(screen.queryByDisplayValue("tvly-synthetic-secret")).toBeNull();
    expect(JSON.stringify(runtime.queryClient.getMutationCache().getAll().map((mutation) => mutation.state.variables))).not.toContain("tvly-synthetic-secret");
    await userEvent.click(group("联网搜索").getByRole("button", { name: "清除 Tavily 密钥" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body).at(-1)).toEqual({ search_key: null }));
    expect(await screen.findByLabelText("Tavily 密钥")).toBeTruthy();
  });

  it("Tavily 密钥解不开：提示重新填写，可以清除", async () => {
    const { api } = await openSearch({ settings: settingsOf({ search_source: "tavily", search_key_saved: true, search_key_last_four: "abcd", search_key_needs_reentry: true }) });
    expect(await group("联网搜索").findByText("Tavily 密钥需要重新填写")).toBeTruthy();
    expect((screen.getByLabelText("Tavily 密钥") as HTMLInputElement).type).toBe("password");
    await userEvent.click(group("联网搜索").getByRole("button", { name: "清除 Tavily 密钥" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ search_key: null }]));
  });

  it("模型自带：检测没有确认支持时不能选并说明去哪里改；在检测结果里改成支持之后可以选", async () => {
    const { api } = await openSearch();
    const native = source(/^模型自带的搜索/) as HTMLInputElement;
    expect(native.disabled).toBe(true);
    expect(group("联网搜索").getByText(/检测没能确认这个模型支持/)).toBeTruthy();
    const result = within(screen.getByRole("region", { name: "检测结果" }));
    expect(result.getByText("自带搜索")).toBeTruthy();
    await userEvent.click(result.getByRole("button", { name: "修改自带搜索" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "支持" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ overrides: { native_search: "supported" } }]));
    await waitFor(() => expect((source(/^模型自带的搜索/) as HTMLInputElement).disabled).toBe(false));
    await userEvent.click(source(/^模型自带的搜索/));
    await userEvent.click(group("联网搜索").getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body).at(-1)).toEqual({ search_source: "native" }));
    // 自带搜索不用测试：测试按钮停用。
    expect((group("联网搜索").getByRole("button", { name: "测试搜索" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("检测确认不支持时说明检测的结论", async () => {
    await openSearch({ settings: settingsOf({ probe_result: { models: [], protocol: "messages", tool_support: "supported", native_search: "unsupported" } }) });
    expect(await group("联网搜索").findByText(/检测结果是这个模型不支持/)).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "检测结果" })).getByText("不支持")).toBeTruthy();
  });

  it("成员的 SearXNG 地址被出站策略拒绝：沿用模型端点的说明，提示管理员放行", async () => {
    const { api } = await openSearch({ role: "user" });
    api.on("PUT", settingsPath, () => sampleResponse("steward/egress-denied-settings"));
    await userEvent.click(await group("联网搜索").findByRole("radio", { name: /^自己的 SearXNG/ }));
    await userEvent.type(screen.getByLabelText("SearXNG 地址"), "http://192.168.1.30:8888");
    await userEvent.click(group("联网搜索").getByRole("button", { name: "保存" }));
    expect(await group("联网搜索").findByText(/成员的 SearXNG 地址只能是公网地址/)).toBeTruthy();
    expect(group("联网搜索").getByText("联网搜索没有保存")).toBeTruthy();
  });
});

describe("设置 › Agent › 模型", () => {
  it("未配置时在这一栏填地址、密钥与模型，检测通过后可以直接开始对话；密钥不留在页面、浏览器存储与 mutation 缓存里", async () => {
    const { api, history, runtime } = await openAgent({ settings: emptySettings() });
    await userEvent.type(await screen.findByLabelText("接口地址"), "http://127.0.0.1:12345");
    await userEvent.type(screen.getByLabelText("密钥"), "sk-synthetic-secret");
    await userEvent.type(screen.getByLabelText("对话模型"), "synthetic-model");
    await userEvent.click(screen.getByRole("button", { name: "检测" }));
    await waitFor(() => expect(api.requestsTo("POST", probePath).map((request) => request.body)).toEqual([{ endpoint: "http://127.0.0.1:12345", key: "sk-synthetic-secret", model: "synthetic-model" }]));
    expect(await screen.findByText("已保存，末四位 cret")).toBeTruthy();
    expect(screen.queryByDisplayValue("sk-synthetic-secret")).toBeNull();
    for (const storage of [window.localStorage, window.sessionStorage]) {
      for (let index = 0; index < storage.length; index += 1) expect(storage.getItem(storage.key(index) ?? "")).not.toContain("sk-synthetic-secret");
    }
    expect(JSON.stringify(runtime.queryClient.getMutationCache().getAll().map((mutation) => mutation.state.variables))).not.toContain("sk-synthetic-secret");
    const result = within(screen.getByRole("region", { name: "检测结果" }));
    expect(result.getByText("OpenAI 兼容接口")).toBeTruthy();
    expect(result.getByText("支持")).toBeTruthy();

    history.push("/steward");
    expect(await screen.findByRole("textbox", { name: "问问管家" })).toBeTruthy();
  });

  it("面板里的“去设置”到达这一栏", async () => {
    setup({ settings: emptySettings() });
    const { history } = renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await userEvent.click(await sidebar().findByRole("button", { name: "管家" }));
    await userEvent.click(within(await screen.findByRole("complementary", { name: "管家" })).getByRole("link", { name: "去设置" }));
    await waitFor(() => expect(history.location.pathname).toBe("/settings/agent"));
    expect(await screen.findByRole("heading", { level: 1, name: "Agent" })).toBeTruthy();
  });

  it("检测失败：逐条显示每一步的原因，原设置不变", async () => {
    const { api, state } = await openAgent({ settings: settingsOf({ display_name: "", probe_result: null, probed_at: null }) });
    state.probeResponse = sampleResponse("steward/provider-unreachable");
    await userEvent.click(await screen.findByRole("button", { name: "检测" }));
    expect(await screen.findByText("检测没有通过，原来的设置没有改动")).toBeTruthy();
    expect(screen.getByText("模型端点认证失败，请检查密钥。")).toBeTruthy();
    const result = within(screen.getByRole("region", { name: "检测结果" }));
    expect(result.getByText("这次检测的结果（没有保存）")).toBeTruthy();
    expect(result.getByRole("list", { name: "没有通过的步骤" }).textContent).toBe(
      "试调用工具（OpenAI 兼容接口）：认证失败，请检查密钥，接口返回 401：[已隐藏]检查 Responses 接口（OpenAI Responses 接口）：认证失败，请检查密钥，接口返回 401：[已隐藏]",
    );
    expect(result.queryByRole("button", { name: "修改调用工具" })).toBeNull();
    // 原设置不变：地址与已存密钥一行照旧。
    expect((screen.getByLabelText("接口地址") as HTMLInputElement).value).toBe("http://127.0.0.1:12345");
    expect(screen.getByText("已保存，末四位 -key")).toBeTruthy();
    expect(api.requestsTo("PUT", settingsPath)).toHaveLength(0);
  });

  it("地址被出站策略拒绝：说明成员只能用公网地址、需要管理员放行", async () => {
    const { state } = await openAgent({ role: "user" });
    state.probeResponse = sampleResponse("steward/egress-denied");
    await userEvent.click(await screen.findByRole("button", { name: "检测" }));
    expect(await screen.findByText(/成员的模型接口只能是公网地址/)).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "检测结果" })).getByText(/检查接口地址：地址不允许连接/)).toBeTruthy();
  });

  it("模型不支持调用工具：按原型显示；“修改”写 overrides", async () => {
    const { api } = await openAgent({ settings: settingsOf(sampleBody("steward/probe-unsupported") as Record<string, unknown>) });
    const result = within(await screen.findByRole("region", { name: "检测结果" }));
    expect(result.getByText("这个模型不支持")).toBeTruthy();
    expect(result.getByText("管家要靠调用工具才能查曲库。换一个支持工具调用的模型，或者在确定它支持时点“修改”。")).toBeTruthy();
    await userEvent.click(result.getByRole("button", { name: "修改调用工具" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "支持" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ overrides: { tool_support: "supported" } }]));
    const updated = within(await screen.findByRole("region", { name: "检测结果" }));
    expect(await updated.findByText("手动修改")).toBeTruthy();
    expect(updated.getByText("支持")).toBeTruthy();
  });

  it("对话模型从检测到的模型里选或直接填写，保存只提交 model", async () => {
    const { api } = await openAgent({ settings: settingsOf({ probe_result: { models: [{ id: "synthetic-model" }, { id: "bigger-model" }], protocol: "openai", tool_support: "supported" } }) });
    const input = await screen.findByLabelText("对话模型");
    expect(Array.from(document.querySelectorAll(`#${CSS.escape(input.getAttribute("list") ?? "")} option`)).map((option) => (option as HTMLOptionElement).value)).toEqual(["synthetic-model", "bigger-model"]);
    await userEvent.clear(input);
    await userEvent.type(input, "bigger-model");
    await userEvent.click(within(input.closest("form") as HTMLElement).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ model: "bigger-model" }]));
  });

  it("审查模型（C7）：留空表示与对话模型相同；从检测到的模型里选，保存只提交 review_model，清空后保存空字符串", async () => {
    const { api } = await openAgent({ settings: settingsOf({ review_model: "", probe_result: { models: [{ id: "synthetic-model" }, { id: "bigger-model" }], protocol: "openai", tool_support: "supported" } }) });
    const input = await screen.findByLabelText("审查模型");
    expect((input as HTMLInputElement).value).toBe("");
    expect(input.getAttribute("placeholder")).toBe("与对话模型相同");
    expect(screen.getByText(/留空表示与对话模型相同；两者相同时审查的独立性有限/)).toBeTruthy();
    expect(Array.from(document.querySelectorAll(`#${CSS.escape(input.getAttribute("list") ?? "")} option`)).map((option) => (option as HTMLOptionElement).value)).toEqual(["synthetic-model", "bigger-model"]);
    const save = within(input.closest("form") as HTMLElement).getByRole("button", { name: "保存" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await userEvent.type(input, "bigger-model");
    await userEvent.click(save);
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ review_model: "bigger-model" }]));
    await waitFor(() => expect(save.disabled).toBe(true));
    await userEvent.clear(input);
    await userEvent.click(save);
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ review_model: "bigger-model" }, { review_model: "" }]));
  });

  it("已存密钥只显示末四位，可以清除；清除后换成输入框，检测结论作废并提示重新检测", async () => {
    const { api } = await openAgent();
    expect(await screen.findByText("已保存，末四位 -key")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "清除" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ key: null }]));
    expect(await screen.findByLabelText("密钥")).toBeTruthy();
    const result = within(screen.getByRole("region", { name: "检测结果" }));
    expect(result.getByText("还没有检测结果")).toBeTruthy();
    expect(result.getByText("OpenAI 兼容接口")).toBeTruthy();
    expect(result.getByText(/之前的检测结论已经作废/)).toBeTruthy();
  });

  it("已存密钥解不开：提示重新填写，密钥换成输入框，也可以清除", async () => {
    const { api } = await openAgent({ settings: settingsOf({ key_needs_reentry: true }) });
    expect(await screen.findByText("密钥需要重新填写")).toBeTruthy();
    expect((screen.getByLabelText("密钥") as HTMLInputElement).type).toBe("password");
    await userEvent.click(screen.getByRole("button", { name: "清除" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ key: null }]));
    await waitFor(() => expect(screen.queryByText("密钥需要重新填写")).toBeNull());
  });

  it("修改接口类型：检测结论被清掉后仍有“接口类型”一行，带手动修改标记、提示重新检测；可以改回检测结果", async () => {
    const { api } = await openAgent();
    await userEvent.click(within(await screen.findByRole("region", { name: "检测结果" })).getByRole("button", { name: "修改接口类型" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Anthropic Messages 接口" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ overrides: { protocol: "messages" } }]));
    const changed = within(await screen.findByRole("region", { name: "检测结果" }));
    expect(await changed.findByText("Anthropic Messages 接口")).toBeTruthy();
    expect(changed.getByText("手动修改")).toBeTruthy();
    expect(changed.getByText(/之前的检测结论已经作废/)).toBeTruthy();
    expect(changed.queryByText("调用工具")).toBeNull();

    await userEvent.click(changed.getByRole("button", { name: "修改接口类型" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "改回检测结果" }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ overrides: { protocol: "messages" } }, { overrides: {} }]));
    const restored = within(await screen.findByRole("region", { name: "检测结果" }));
    expect(await restored.findByText("OpenAI 兼容接口")).toBeTruthy();
    expect(restored.queryByText("手动修改")).toBeNull();
  });

  it("接口类型可以手动选 Responses：检测没有确认时菜单与说明里提一句", async () => {
    const { api } = await openAgent({ settings: settingsOf({ probe_result: { models: [], protocol: "openai", tool_support: "supported", responses_available: false } }) });
    await userEvent.click(within(await screen.findByRole("region", { name: "检测结果" })).getByRole("button", { name: "修改接口类型" }));
    await userEvent.click(screen.getByRole("menuitem", { name: /OpenAI Responses 接口.*检测没有确认这个接口支持/ }));
    await waitFor(() => expect(api.requestsTo("PUT", settingsPath).map((request) => request.body)).toEqual([{ overrides: { protocol: "responses" } }]));
    const changed = within(await screen.findByRole("region", { name: "检测结果" }));
    expect(await changed.findByText("OpenAI Responses 接口")).toBeTruthy();
    expect(changed.getByText(/Responses 接口是手动选的，检测没有确认这个接口支持它/)).toBeTruthy();
  });

  it("保存对话模型或清除密钥时，上一次失败检测的报告一起清掉", async () => {
    const { state } = await openAgent();
    state.probeResponse = sampleResponse("steward/provider-unreachable");
    await userEvent.click(await screen.findByRole("button", { name: "检测" }));
    expect(await screen.findByText("这次检测的结果（没有保存）")).toBeTruthy();
    const input = screen.getByLabelText("对话模型");
    await userEvent.clear(input);
    await userEvent.type(input, "bigger-model");
    await userEvent.click(within(input.closest("form") as HTMLElement).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(screen.queryByText("这次检测的结果（没有保存）")).toBeNull());

    state.probeResponse = sampleResponse("steward/provider-unreachable");
    await userEvent.click(screen.getByRole("button", { name: "检测" }));
    expect(await screen.findByText("这次检测的结果（没有保存）")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "清除" }));
    await waitFor(() => expect(screen.queryByText("这次检测的结果（没有保存）")).toBeNull());
  });

  it("已存密钥时把地址换成别的主机：密钥换成输入框并说明要重新填写；留空检测不带密钥", async () => {
    const { api } = await openAgent();
    expect(await screen.findByText("已保存，末四位 -key")).toBeTruthy();
    const endpoint = screen.getByLabelText("接口地址");
    await userEvent.clear(endpoint);
    await userEvent.type(endpoint, "http://127.0.0.1:12346");
    expect(screen.queryByText("已保存，末四位 -key")).toBeNull();
    expect(screen.getByText("换了地址要重新填写密钥：原来的密钥只发给原来的地址。留空表示这个地址不需要密钥。")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "检测" }));
    await waitFor(() => expect(api.requestsTo("POST", probePath).map((request) => request.body)).toEqual([{ endpoint: "http://127.0.0.1:12346", model: "synthetic-model" }]));
  });

  it("只改地址的路径（主机与端口不变）时，已存密钥一行不变", async () => {
    await openAgent();
    const endpoint = await screen.findByLabelText("接口地址");
    await userEvent.type(endpoint, "/v1");
    expect(screen.getByText("已保存，末四位 -key")).toBeTruthy();
  });

  it("检测进行中，改名保存、模型保存、清除与修改都先停用", async () => {
    const { api } = await openAgent({ settings: settingsOf({ display_name: "" }) });
    let release: () => void = () => undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    api.on("POST", probePath, async () => {
      await held;
      return sampleResponse("steward/probe-supported");
    });
    await userEvent.type(await screen.findByRole("textbox", { name: "名字" }), "小蓝");
    await userEvent.click(screen.getByRole("button", { name: "检测" }));
    expect(await screen.findByRole("button", { name: "正在检测…" })).toBeTruthy();
    expect((group("名字").getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "清除" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "修改接口类型" }) as HTMLButtonElement).disabled).toBe(true);
    release();
    await waitFor(() => expect((group("名字").getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(false));
  });

  it("保存时得到 steward_disabled：设置重读，整栏切到说明", async () => {
    const { state } = await openAgent({ role: "user" });
    await userEvent.type(await screen.findByRole("textbox", { name: "名字" }), "小蓝");
    state.disabled = true;
    await userEvent.click(group("名字").getByRole("button", { name: "保存" }));
    expect(await screen.findByText("管理员暂时关闭了这个功能")).toBeTruthy();
    expect(screen.queryByLabelText("接口地址")).toBeNull();
  });
});

describe("设置 › Agent › 成员（管理员）", () => {
  it("关掉成员开关立即提交；添加与移除成员可以连接的内网地址", async () => {
    const { api } = await openAgent();
    const members = group("成员");
    const toggle = await members.findByRole("switch", { name: "允许普通成员使用 Agent" });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    await userEvent.click(toggle);
    await waitFor(() => expect(api.requestsTo("PUT", "/api/v1/admin/steward/settings").map((request) => request.body)).toEqual([{ members_enabled: false }]));
    await waitFor(() => expect((members.getByRole("switch", { name: "允许普通成员使用 Agent" }) as HTMLInputElement).checked).toBe(false));

    const allowlist = group("成员可以连接的内网地址");
    expect(await allowlist.findByText("还没有放行的地址。")).toBeTruthy();
    await userEvent.click(allowlist.getByRole("button", { name: "添加地址" }));
    const dialog = within(screen.getByRole("dialog", { name: "添加成员可以连接的地址" }));
    await userEvent.type(dialog.getByLabelText("主机"), "192.168.1.20:11434");
    await userEvent.type(dialog.getByLabelText("端口"), "11434");
    await userEvent.click(dialog.getByRole("button", { name: "添加" }));
    expect(dialog.getByText("填主机名或 IP，不带端口和方括号，例如 192.168.1.20。")).toBeTruthy();
    await userEvent.clear(dialog.getByLabelText("主机"));
    await userEvent.type(dialog.getByLabelText("主机"), "192.168.1.20");
    await userEvent.click(dialog.getByRole("button", { name: "添加" }));
    await waitFor(() => expect(api.requestsTo("POST", "/api/v1/admin/steward/egress-allowlist").map((request) => request.body)).toEqual([{ host: "192.168.1.20", port: 11434 }]));
    expect(await allowlist.findByRole("rowheader", { name: "192.168.1.20" })).toBeTruthy();

    await userEvent.click(allowlist.getByRole("button", { name: "移除 192.168.1.20:11434" }));
    await userEvent.click(within(screen.getByRole("dialog", { name: "移除 192.168.1.20:11434？" })).getByRole("button", { name: "移除" }));
    await waitFor(() => expect(api.requestsTo("DELETE", "/api/v1/admin/steward/egress-allowlist").map((request) => request.body)).toEqual([{ host: "192.168.1.20", port: 11434 }]));
    expect(await allowlist.findByText("还没有放行的地址。")).toBeTruthy();
  });

  it("成员没有“成员”一段，不请求管理员接口", async () => {
    const { api } = await openAgent({ role: "user" });
    await screen.findByLabelText("接口地址");
    expect(screen.queryByRole("region", { name: "成员" })).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/admin/steward/settings")).toHaveLength(0);
    expect(api.requestsTo("GET", "/api/v1/admin/steward/egress-allowlist")).toHaveLength(0);
  });

  it("成员开关关闭时成员只看到说明（访问令牌一组保留，仍能吊销）", async () => {
    await openAgent({ role: "user", disabled: true });
    expect(await screen.findByText("管理员暂时关闭了这个功能")).toBeTruthy();
    expect(screen.queryByLabelText("接口地址")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "名字" })).toBeNull();
    expect(screen.getByRole("heading", { level: 2, name: "访问令牌" })).toBeTruthy();
  });

  it("各组依次是名字、模型、联网搜索、记忆、访问令牌与管理员的成员段", async () => {
    await openAgent();
    await screen.findByLabelText("接口地址");
    expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual(["名字", "模型", "联网搜索", "记忆", "访问令牌", "成员", "成员可以连接的内网地址"]);
  });
});

describe("设置 › Agent › 记忆（C10）", () => {
  const memoriesPath = "/api/v1/me/steward/memories";
  const memory = (index: number, content: string) => ({
    id: `00000000-0000-4000-8000-${String(700 + index).padStart(12, "0")}`,
    content,
    created_at: "2026-11-15T00:00:00Z",
    updated_at: `2026-11-15T00:00:0${index}Z`,
  });

  it("空状态说明记忆的来源、谁能看到与会发给模型；显示条数与字数的上限", async () => {
    await openAgent();
    const memories = group("记忆");
    expect(await memories.findByText("还没有记忆")).toBeTruthy();
    expect(memories.getByText(/在对话里按你的要求记下的，或你在这里手动添加的偏好.*只有你自己能看到，每次对话都会发给你配置的模型/)).toBeTruthy();
    expect(memories.getByText(/共 0 条，最多 50 条；每条最多 500 个字/)).toBeTruthy();
    expect((memories.getByRole("button", { name: "全部清除…" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("添加：提交去掉首尾空白的内容，列表随之更新；超过字数上限不能保存", async () => {
    const { api } = await openAgent({ memoryMaxLength: 10 });
    const memories = group("记忆");
    await userEvent.click(await memories.findByRole("button", { name: "添加记忆" }));
    const dialog = within(screen.getByRole("dialog", { name: "添加记忆" }));
    const field = dialog.getByRole("textbox", { name: "内容" });
    expect(document.activeElement).toBe(field);
    await userEvent.type(field, "一二三四五六七八九十一");
    expect(dialog.getByText("11 / 10 字，超出了上限")).toBeTruthy();
    expect((dialog.getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.clear(field);
    await userEvent.type(field, "  回答都用繁体 ");
    expect(dialog.getByText("6 / 10 字")).toBeTruthy();
    await userEvent.click(dialog.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("POST", memoriesPath).map((request) => request.body)).toEqual([{ content: "回答都用繁体" }]));
    expect(await memories.findByText("回答都用繁体")).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: "添加记忆" })).toBeNull();
    expect(memories.getByText(/共 1 条/)).toBeTruthy();
  });

  it("修改与删除一条（删除前确认）；全部清除前确认", async () => {
    const { api, state } = await openAgent({ memories: [memory(1, "不喜欢现场录音版本"), memory(2, "回答都用繁体")] });
    const memories = group("记忆");
    const items = within(await memories.findByRole("list", { name: "记忆" }));
    // 按更新时间倒序。
    expect(items.getAllByRole("listitem").map((item) => item.querySelector("p")?.textContent)).toEqual(["回答都用繁体", "不喜欢现场录音版本"]);

    const first = within(items.getAllByRole("listitem")[0] as HTMLElement);
    await userEvent.click(first.getByRole("button", { name: "修改…" }));
    const dialog = within(screen.getByRole("dialog", { name: "修改记忆" }));
    const field = dialog.getByRole("textbox", { name: "内容" });
    expect((field as HTMLTextAreaElement).value).toBe("回答都用繁体");
    await userEvent.clear(field);
    await userEvent.type(field, "回答都用简体");
    await userEvent.click(dialog.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PATCH", `${memoriesPath}/${memory(2, "").id}`).map((request) => request.body)).toEqual([{ content: "回答都用简体" }]));
    expect(await memories.findByText("回答都用简体")).toBeTruthy();

    const second = within(await memories.findByText("不喜欢现场录音版本").then((text) => text.closest("li") as HTMLElement));
    await userEvent.click(second.getByRole("button", { name: "删除…" }));
    await userEvent.click(within(screen.getByRole("dialog", { name: "删除这条记忆？" })).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(api.requestsTo("DELETE", `${memoriesPath}/${memory(1, "").id}`)).toHaveLength(1));
    await waitFor(() => expect(memories.queryByText("不喜欢现场录音版本")).toBeNull());

    await userEvent.click(memories.getByRole("button", { name: "全部清除…" }));
    const confirm = within(screen.getByRole("dialog", { name: "清除全部记忆？" }));
    expect(confirm.getByText(/1 条记忆都会删除，不能恢复/)).toBeTruthy();
    await userEvent.click(confirm.getByRole("button", { name: "全部清除" }));
    await waitFor(() => expect(api.requestsTo("DELETE", memoriesPath)).toHaveLength(1));
    expect(await memories.findByText("还没有记忆")).toBeTruthy();
    expect(state.memories).toEqual([]);
  });

  it("到了条数上限：不能添加，说明要先删掉不用的", async () => {
    await openAgent({ memories: [memory(1, "甲"), memory(2, "乙")], memoryLimit: 2 });
    const memories = group("记忆");
    expect(await memories.findByText("记忆已经到了上限（2 条），删掉不用的才能添加。")).toBeTruthy();
    expect((memories.getByRole("button", { name: "添加记忆" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("添加时后端说到了上限（别处刚加过）：对话框里说明原因", async () => {
    const { api } = await openAgent({ memoryLimit: 1 });
    api.on("POST", memoriesPath, () => jsonResponse(409, { error: { code: "memory_limit_reached", message: "记忆已到上限" }, request_id: "fake-request" }));
    await userEvent.click(await group("记忆").findByRole("button", { name: "添加记忆" }));
    const dialog = within(screen.getByRole("dialog", { name: "添加记忆" }));
    await userEvent.type(dialog.getByRole("textbox", { name: "内容" }), "新的一条");
    await userEvent.click(dialog.getByRole("button", { name: "保存" }));
    expect(await dialog.findByText("记忆已经有 1 条了，删掉不用的再添加。")).toBeTruthy();
  });

  it("成员开关关闭时成员仍能查看与删除，不能添加与修改", async () => {
    const { api } = await openAgent({ role: "user", disabled: true, memories: [memory(1, "回答都用繁体")] });
    const memories = group("记忆");
    expect(await memories.findByText("回答都用繁体")).toBeTruthy();
    expect(memories.getByText("管理员暂时关闭了成员使用管家，记忆只能查看和删除。")).toBeTruthy();
    expect((memories.getByRole("button", { name: "添加记忆" }) as HTMLButtonElement).disabled).toBe(true);
    expect(memories.queryByRole("button", { name: "修改…" })).toBeNull();
    await userEvent.click(memories.getByRole("button", { name: "删除…" }));
    await userEvent.click(within(screen.getByRole("dialog", { name: "删除这条记忆？" })).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(api.requestsTo("DELETE", `${memoriesPath}/${memory(1, "").id}`)).toHaveLength(1));
    expect(await memories.findByText("还没有记忆")).toBeTruthy();
  });
});
