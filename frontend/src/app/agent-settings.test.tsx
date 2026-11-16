import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { setDockOpen } from "../features/steward";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installStewardApi, settingsOf, type StewardFake } from "../test/fake-steward";
import { sampleResponse } from "../test/http";
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
    expect(result.getByRole("list", { name: "没有通过的步骤" }).textContent).toBe("试调用工具（OpenAI 兼容接口）：认证失败，请检查密钥，接口返回 401：[已隐藏]");
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

  it("不显示联网搜索、审查模型与模式的设置", async () => {
    await openAgent();
    await screen.findByLabelText("接口地址");
    expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual(["名字", "模型", "访问令牌", "成员", "成员可以连接的内网地址"]);
  });
});
