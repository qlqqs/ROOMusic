import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { jsonResponse, sampleResponse } from "../test/http";
import { setDockOpen } from "../features/steward";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { conversationOf, installStewardApi, settingsOf, storedMessage, type StewardFake } from "../test/fake-steward";
import { renderApp } from "../test/render-app";

const id = "00000000-0000-4000-8000-000000000001";
const other = "00000000-0000-4000-8000-000000000002";

function setup(options: { role?: "admin" | "user" } & Partial<StewardFake> = {}) {
  const { role = "admin", ...steward } = options;
  const api = installFakeApi();
  installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  const state = installStewardApi(api, { settings: settingsOf({ display_name: "小蓝" }), ...steward });
  return { api, state };
}

// 面板开关记在这个浏览器里，测试之间不能带过去。
afterEach(() => setDockOpen(false));

const sidebarNav = () => within(screen.getAllByRole("navigation", { name: "主导航" })[0] as HTMLElement);
const findSidebarNav = async () => within((await screen.findAllByRole("navigation", { name: "主导航" }))[0] as HTMLElement);

async function openPanel() {
  await userEvent.click(await (await findSidebarNav()).findByRole("button", { name: "小蓝" }));
  return within(await screen.findByRole("complementary", { name: "小蓝" }));
}

/** 一次已落库的回答：用户问题、一次工具调用与结果、最终回答。 */
function persistedRun(question: string, answer: string) {
  return [
    storedMessage(1, "user", [{ type: "text", text: question }]),
    storedMessage(
      2,
      "assistant",
      [{ type: "text", text: "我查一下。" }, { type: "tool_call", tool_call: { id: "call-1", name: "list_albums", arguments: { q: "贝九" } } }],
      [{ id: "00000000-0000-4000-8000-0000000000a1", name: "list_albums", input: { q: "贝九" }, output: { items: [], pagination: { page: 1, page_size: 20, total: 3 } }, truncated: false, untrusted: true, status: "succeeded", error_code: null, duration_ms: 21_000 }],
    ),
    storedMessage(3, "tool", [{ type: "tool_result", tool_result: { call_id: "call-1", content: "{}", is_error: false } }]),
    storedMessage(4, "assistant", [{ type: "text", text: answer }]),
  ];
}

describe("管家的入口与名字", () => {
  it("侧栏“聆听”里是起的名字；点它在当前页面打开或收起面板，面板标题与输入框提示用同一个名字", async () => {
    setup();
    renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    const entry = await sidebarNav().findByRole("button", { name: "小蓝" });
    expect(entry.getAttribute("aria-expanded")).toBe("false");
    const panel = await openPanel();
    expect(entry.getAttribute("aria-expanded")).toBe("true");
    expect(panel.getByRole("heading", { level: 2, name: "小蓝" })).toBeTruthy();
    expect(await panel.findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
    // 当前页面没有变化。
    expect(screen.getByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    await userEvent.click(entry);
    expect(screen.queryByRole("complementary", { name: "小蓝" })).toBeNull();
  });

  it("没起名字时叫“管家”", async () => {
    setup({ settings: settingsOf({ display_name: "" }) });
    renderApp({ path: "/" });
    expect(await (await findSidebarNav()).findByRole("button", { name: "管家" })).toBeTruthy();
  });

  it("成员开关关闭时成员看不到入口", async () => {
    const { api } = setup({ role: "user", disabled: true });
    renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/me/steward/settings").length).toBeGreaterThan(0));
    await waitFor(() => expect(sidebarNav().getByRole("link", { name: "艺人" })).toBeTruthy());
    expect(sidebarNav().queryByRole("button", { name: /管家|小蓝/ })).toBeNull();
  });

  it("成员开关关闭时直接打开地址看到说明", async () => {
    setup({ role: "user", disabled: true });
    renderApp({ path: "/steward" });
    expect(await screen.findByText("管理员暂时关闭了这个功能")).toBeTruthy();
  });

  it("未配置模型时面板里是配置引导，没有输入框", async () => {
    setup({ settings: settingsOf({ display_name: "小蓝", endpoint: "", protocol: "", model: "", probe_result: null, probed_at: null }) });
    renderApp({ path: "/" });
    const panel = await openPanel();
    expect(await panel.findByRole("heading", { name: "先接上一个模型" })).toBeTruthy();
    expect(panel.getByRole("link", { name: "去设置" }).getAttribute("href")).toBe("/settings/agent");
    expect(panel.queryByRole("textbox")).toBeNull();
  });
});

describe("对话", () => {
  it("新对话：发第一条消息时创建会话；工具条目逐条出现、回答流式显示；结束后过程收成摘要", async () => {
    const { api, state } = setup();
    renderApp({ path: "/" });
    const panel = await openPanel();
    const input = await panel.findByRole("textbox", { name: "问问小蓝" });
    await userEvent.type(input, "贝九有几版？{Enter}");

    await waitFor(() => expect(state.runs).toHaveLength(1));
    expect(api.requestsTo("POST", "/api/v1/me/steward/conversations")).toHaveLength(1);
    const created = state.conversations[0];
    if (created === undefined) throw new Error("没有创建会话");
    expect(state.runs[0]?.request.body).toEqual({ text: "贝九有几版？" });
    expect(await panel.findByText("贝九有几版？")).toBeTruthy();
    // 有了第一句话，提示换成“回复”。
    expect((panel.getByRole("textbox", { name: "回复小蓝" }) as HTMLTextAreaElement).value).toBe("");

    const stream = state.runs[0]?.stream;
    stream?.send("text", { type: "text", step: 1, text: "我查一下。" });
    stream?.send("tool_started", { type: "tool_started", step: 1, tool_started: { call_id: "call-1", name: "list_albums", input: { q: "贝九" } } });
    const step = (await panel.findByText("浏览专辑")).closest("li") as HTMLElement;
    expect(within(step).getByText("贝九")).toBeTruthy();
    expect(within(step).getByText("进行中")).toBeTruthy();
    expect(panel.getByRole("button", { name: "停止回答" })).toBeTruthy();

    stream?.send("tool_finished", { type: "tool_finished", step: 1, tool_finished: { call_id: "call-1", status: "succeeded", output: { items: [], pagination: { page: 1, page_size: 20, total: 3 } }, truncated: false, untrusted: true } });
    expect(await within(step).findByText("3 张")).toBeTruthy();
    stream?.send("text", { type: "text", step: 2, text: "一共 **3 版**。" });
    expect(await panel.findByText("3 版")).toBeTruthy();

    created.messages = persistedRun("贝九有几版？", "一共 **3 版**。");
    created.title = "贝九有几版？";
    stream?.send("done", { type: "done", step: 2, end: { reason: "completed" } });
    stream?.close();
    expect(await panel.findByText("查了 1 处资料，用时 21 秒")).toBeTruthy();
    expect(panel.getByText("3 版").tagName).toBe("STRONG");
    expect(panel.getByRole("button", { name: "发送" })).toBeTruthy();
    expect(await panel.findByText("贝九有几版？", { selector: "span" })).toBeTruthy();
  });

  it("运行中切到别的页面再回来、展开到整页，回答仍在继续且内容一致", async () => {
    const { state } = setup({ conversations: [conversationOf(id)] });
    const { history } = renderApp({ path: "/" });
    const panel = await openPanel();
    await userEvent.click(await sidebarNav().findByRole("link", { name: "专辑" }));
    await waitFor(() => expect(history.location.pathname).toBe("/albums"));
    await userEvent.type(await panel.findByRole("textbox", { name: "问问小蓝" }), "你好{Enter}");
    await waitFor(() => expect(state.runs).toHaveLength(1));
    state.runs[0]?.stream.send("text", { type: "text", step: 1, text: "正在看曲库" });
    expect(await panel.findByText("正在看曲库")).toBeTruthy();

    await userEvent.click(sidebarNav().getByRole("link", { name: "概览" }));
    await screen.findByRole("heading", { level: 1, name: "概览" });
    const again = within(screen.getByRole("complementary", { name: "小蓝" }));
    state.runs[0]?.stream.send("text", { type: "text", step: 1, text: "，马上好" });
    expect(await again.findByText("正在看曲库，马上好")).toBeTruthy();

    await userEvent.click(again.getByRole("button", { name: "展开到整页" }));
    await waitFor(() => expect(history.location.pathname).toMatch(/^\/steward\/[0-9a-f-]{36}$/));
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    expect(await conversation.findByText("正在看曲库，马上好")).toBeTruthy();
    expect(screen.queryByRole("complementary", { name: "小蓝" })).toBeNull();
    state.runs[0]?.stream.send("text", { type: "text", step: 1, text: "。" });
    expect(await conversation.findByText("正在看曲库，马上好。")).toBeTruthy();
  });

  it("停止：运行结束，对话里显示已停止，可以接着发下一条", async () => {
    const { api, state } = setup({ conversations: [conversationOf(id, { messages: persistedRun("上一个问题", "上一个回答") })] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    await userEvent.type(await conversation.findByRole("textbox", { name: "回复小蓝" }), "再查一次{Enter}");
    await waitFor(() => expect(state.runs).toHaveLength(1));
    expect(state.runs[0]?.request.path).toBe(`/api/v1/me/steward/conversations/${id}/messages`);
    await userEvent.click(await conversation.findByRole("button", { name: "停止回答" }));
    expect(await conversation.findByText("已停止")).toBeTruthy();
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/cancel`)).toHaveLength(1);
    await userEvent.type(conversation.getByRole("textbox", { name: "回复小蓝" }), "下一条{Enter}");
    await waitFor(() => expect(state.runs).toHaveLength(2));
  });

  it("模型接口出错：显示原因与接口返回的状态；重试从已有历史继续，不重复追加用户消息", async () => {
    const { api, state } = setup({ conversations: [conversationOf(id)] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    await userEvent.type(await conversation.findByRole("textbox", { name: "问问小蓝" }), "1942 那版的来源？{Enter}");
    await waitFor(() => expect(state.runs).toHaveLength(1));
    const target = state.conversations[0];
    if (target === undefined) throw new Error("没有会话");
    target.messages = [storedMessage(1, "user", [{ type: "text", text: "1942 那版的来源？" }])];
    state.runs[0]?.stream.send("done", { type: "done", step: 1, end: { reason: "provider_error", error_kind: "other", error_code: "provider_unreachable", status_code: 529, message: "Overloaded" } });
    state.runs[0]?.stream.close();

    expect(await conversation.findByText("模型接口出错了，这次回答没有完成")).toBeTruthy();
    expect(conversation.getByText("接口返回 529：Overloaded。")).toBeTruthy();
    expect(conversation.getByRole("link", { name: "检查模型设置" }).getAttribute("href")).toBe("/settings/agent");
    await userEvent.click(conversation.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(state.runs).toHaveLength(2));
    expect(state.runs[1]?.kind).toBe("continue");
    expect(state.runs[1]?.request.body).toBeUndefined();
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/messages`)).toHaveLength(1);
    // 重试期间问题只出现一次。
    expect(conversation.getAllByText("1942 那版的来源？")).toHaveLength(1);
  });

  it("刷新后最后一条是用户消息：显示没有完成与“继续”", async () => {
    setup({ conversations: [conversationOf(id, { messages: [storedMessage(1, "user", [{ type: "text", text: "问题" }])] })] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    expect(await conversation.findByText("这次回答没有完成")).toBeTruthy();
    expect(conversation.getByRole("button", { name: "继续" })).toBeTruthy();
  });

  it("正在别处回答的会话：显示说明，不能发送", async () => {
    setup({ conversations: [conversationOf(id, { status: "running", messages: [storedMessage(1, "user", [{ type: "text", text: "问题" }])] })] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    expect(await conversation.findByText("正在别处回答，结束后才能继续发消息。")).toBeTruthy();
    await userEvent.type(conversation.getByRole("textbox", { name: "回复小蓝" }), "插一句");
    expect((conversation.getByRole("button", { name: "发送" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("回答里的 HTML 标签按文字显示，javascript: 链接不可点", async () => {
    setup({ conversations: [conversationOf(id, { messages: [storedMessage(1, "user", [{ type: "text", text: "问" }]), storedMessage(2, "assistant", [{ type: "text", text: "<b>粗</b> [点我](javascript:alert(1))" }])] })] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    expect(await conversation.findByText(/<b>粗<\/b>/)).toBeTruthy();
    expect(conversation.queryByRole("link", { name: "点我" })).toBeNull();
  });

  it("切换模型只改当前会话（PATCH model），下一条消息用它", async () => {
    const { api } = setup({
      conversations: [conversationOf(id)],
      settings: settingsOf({ display_name: "小蓝", probe_result: { models: [{ id: "synthetic-model" }, { id: "bigger-model" }], protocol: "openai", tool_support: "supported" } }),
    });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    await userEvent.click(await conversation.findByRole("button", { name: "对话模型：synthetic-model" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "bigger-model" }));
    await waitFor(() => expect(api.requestsTo("PATCH", `/api/v1/me/steward/conversations/${id}`).map((request) => request.body)).toEqual([{ model: "bigger-model" }]));
    expect(await conversation.findByRole("button", { name: "对话模型：bigger-model" })).toBeTruthy();
  });
});

describe("会话列表", () => {
  it("按今天、本周、更早分组；正在回答的有标记；可以切换、改名与删除", async () => {
    const now = new Date();
    const { api } = setup({
      conversations: [
        conversationOf(id, { title: "富特文格勒的贝九", updated_at: now.toISOString(), status: "running" }),
        conversationOf(other, { title: "夜间扫描为什么慢了", updated_at: "2020-01-01T00:00:00Z" }),
      ],
    });
    const { history } = renderApp({ path: `/steward/${other}` });
    const list = within(await screen.findByRole("navigation", { name: "对话" }));
    const today = within(await list.findByRole("region", { name: "今天" }));
    expect(today.getByText("正在回答")).toBeTruthy();
    expect(within(list.getByRole("region", { name: "更早" })).getByRole("link", { name: "夜间扫描为什么慢了" }).getAttribute("aria-current")).toBe("page");
    expect(await screen.findByRole("heading", { level: 1, name: "夜间扫描为什么慢了" })).toBeTruthy();

    await userEvent.click(list.getByRole("button", { name: "“夜间扫描为什么慢了”的操作" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "改名" }));
    const rename = within(screen.getByRole("dialog", { name: "改名" }));
    await userEvent.clear(rename.getByLabelText("标题"));
    await userEvent.type(rename.getByLabelText("标题"), "扫描变慢");
    await userEvent.click(rename.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PATCH", `/api/v1/me/steward/conversations/${other}`).map((request) => request.body)).toEqual([{ title: "扫描变慢" }]));
    expect(await screen.findByRole("heading", { level: 1, name: "扫描变慢" })).toBeTruthy();

    // 正在回答的会话删除前要先停止。
    await userEvent.click(list.getByRole("button", { name: "“富特文格勒的贝九”的操作" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "删除" }));
    const busy = within(screen.getByRole("dialog", { name: "这个对话正在回答" }));
    expect(busy.getByRole("button", { name: "停止回答" })).toBeTruthy();
    await userEvent.click(busy.getByRole("button", { name: "取消" }));

    await userEvent.click(await list.findByRole("button", { name: "“扫描变慢”的操作" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "删除" }));
    await userEvent.click(within(screen.getByRole("dialog", { name: "删除这个对话？" })).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(history.location.pathname).toBe("/steward"));
    expect(api.requestsTo("DELETE", `/api/v1/me/steward/conversations/${other}`)).toHaveLength(1);
    // 换了路由，列表重新挂载。
    const after = within(await screen.findByRole("navigation", { name: "对话" }));
    await waitFor(() => expect(after.queryByRole("link", { name: "扫描变慢" })).toBeNull());

    await userEvent.click(after.getByRole("link", { name: /富特文格勒的贝九/ }));
    await waitFor(() => expect(history.location.pathname).toBe(`/steward/${id}`));
  });

  it("新对话按钮回到空白对话", async () => {
    setup({ conversations: [conversationOf(id)] });
    const { history } = renderApp({ path: `/steward/${id}` });
    await userEvent.click(await screen.findByRole("button", { name: "新对话" }));
    await waitFor(() => expect(history.location.pathname).toBe("/steward"));
    expect(await screen.findByRole("heading", { level: 1, name: "新对话" })).toBeTruthy();
    expect(await screen.findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
  });
});

describe("审查后的修正", () => {
  it("新对话的第一条消息在开流前被拒绝：文字回到输入框，说明原因", async () => {
    const { state } = setup();
    state.nextRunResponse = sampleResponse("steward/egress-denied-settings");
    renderApp({ path: "/" });
    const panel = await openPanel();
    await userEvent.type(await panel.findByRole("textbox", { name: "问问小蓝" }), "贝九有几版？{Enter}");
    expect(await panel.findByText("没有发出去")).toBeTruthy();
    await waitFor(() => expect((panel.getByRole("textbox") as HTMLTextAreaElement).value).toBe("贝九有几版？"));
    expect(state.conversations).toHaveLength(1);
  });

  it("新对话预选的模型没能写进去：仍换到建好的会话，提示模型没有切换成功，消息不发、文字留着", async () => {
    const { api, state } = setup({ settings: settingsOf({ display_name: "小蓝", probe_result: { models: [{ id: "synthetic-model" }, { id: "bigger-model" }], protocol: "openai", tool_support: "supported" } }) });
    api.on("PATCH", /^\/api\/v1\/me\/steward\/conversations\/[^/]+$/, () => sampleResponse("errors/database-unavailable"));
    renderApp({ path: "/steward" });
    const conversation = () => within(screen.getByRole("region", { name: "对话" }));
    await userEvent.click(await screen.findByRole("button", { name: "对话模型：synthetic-model" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "bigger-model" }));
    await userEvent.type(conversation().getByRole("textbox", { name: "问问小蓝" }), "你好{Enter}");
    expect(await screen.findByText("对话已经建好，但模型没有切换成功，消息还没有发出")).toBeTruthy();
    const created = state.conversations[0];
    expect(created).toBeDefined();
    expect(screen.getByRole("heading", { level: 1, name: "新对话" })).toBeTruthy();
    expect((conversation().getByRole("textbox") as HTMLTextAreaElement).value).toBe("你好");
    expect(state.runs).toHaveLength(0);
    expect(await screen.findByRole("link", { name: "新对话" })).toBeTruthy();
  });

  it.each([
    ["最后一条是助手消息（已经完成）", "assistant", false],
    ["最后一条是工具消息（没有完成）", "tool", true],
  ] as const)("用量上限结束、%s时“继续”%s", async (_label, lastRole, offered) => {
    const { state } = setup({ conversations: [conversationOf(id)] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    await userEvent.type(await conversation.findByRole("textbox", { name: "问问小蓝" }), "长一点{Enter}");
    await waitFor(() => expect(state.runs).toHaveLength(1));
    const target = state.conversations[0];
    if (target === undefined) throw new Error("没有会话");
    target.messages = [
      storedMessage(1, "user", [{ type: "text", text: "长一点" }]),
      lastRole === "assistant"
        ? storedMessage(2, "assistant", [{ type: "text", text: "说到一半" }])
        : storedMessage(2, "tool", [{ type: "tool_result", tool_result: { call_id: "c", content: "{}", is_error: false } }]),
    ];
    state.runs[0]?.stream.send("done", { type: "done", step: 1, end: { reason: "token_limit" } });
    state.runs[0]?.stream.close();
    expect(await conversation.findByText("用量到了上限，回答还没有完成")).toBeTruthy();
    await waitFor(() => expect(conversation.queryByRole("button", { name: "继续" }) !== null).toBe(offered));
    // 说明是 live region，按钮在它外面。
    const status = conversation.getByText("用量到了上限，回答还没有完成").closest("[role='status']") as HTMLElement;
    expect(within(status).queryByRole("button")).toBeNull();
  });

  it("没执行的工具调用显示“没有执行”；摘要只数执行过的", async () => {
    setup({
      conversations: [
        conversationOf(id, {
          messages: [
            storedMessage(1, "user", [{ type: "text", text: "问" }]),
            storedMessage(
              2,
              "assistant",
              [{ type: "tool_call", tool_call: { id: "a", name: "list_albums", arguments: { q: "贝九" } } }, { type: "tool_call", tool_call: { id: "b", name: "list_artists", arguments: { q: "卡拉扬" } } }],
              [
                { id: "00000000-0000-4000-8000-0000000000a1", name: "list_albums", input: { q: "贝九" }, output: { items: [1] }, truncated: false, untrusted: true, status: "succeeded", error_code: null, duration_ms: 1_000 },
                { id: "00000000-0000-4000-8000-0000000000a2", name: "list_artists", input: { q: "卡拉扬" }, output: { code: "token_limit" }, truncated: false, untrusted: false, status: "failed", error_code: "token_limit", duration_ms: null },
              ],
            ),
          ],
        }),
      ],
    });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    expect(await conversation.findByText("查了 1 处资料，用时 1 秒")).toBeTruthy();
    const row = conversation.getByText("浏览艺人").closest("li") as HTMLElement;
    expect(within(row).getByText("没有执行")).toBeTruthy();
  });

  it("工具都执行完、等模型下一段输出时显示“正在想…”；刚结束还没重读时摘要不写用时", async () => {
    const { api, state } = setup({ conversations: [conversationOf(id)] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    await userEvent.type(await conversation.findByRole("textbox", { name: "问问小蓝" }), "贝九{Enter}");
    await waitFor(() => expect(state.runs).toHaveLength(1));
    const stream = state.runs[0]?.stream;
    stream?.send("tool_started", { type: "tool_started", step: 1, tool_started: { call_id: "call-1", name: "list_albums", input: { q: "贝九" } } });
    expect(await conversation.findByText("进行中")).toBeTruthy();
    expect(conversation.queryByText("正在想…")).toBeNull();
    stream?.send("tool_finished", { type: "tool_finished", step: 1, tool_finished: { call_id: "call-1", status: "succeeded", output: { items: [1, 2] }, truncated: false, untrusted: true } });
    expect(await conversation.findByText("正在想…")).toBeTruthy();

    // 结束后的重读先挂住：此时没有耗时数据。
    let release: () => void = () => undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const target = state.conversations[0];
    if (target === undefined) throw new Error("没有会话");
    api.on("GET", `/api/v1/me/steward/conversations/${id}`, async () => {
      await held;
      return jsonResponse(200, { ...target, messages: persistedRun("贝九", "两张。") });
    });
    stream?.send("text", { type: "text", step: 2, text: "两张。" });
    stream?.send("done", { type: "done", step: 2, end: { reason: "completed" } });
    stream?.close();
    expect(await conversation.findByText("查了 1 处资料")).toBeTruthy();
    release();
    expect(await conversation.findByText("查了 1 处资料，用时 21 秒")).toBeTruthy();
  });

  it("发消息得到 steward_disabled：设置重读，整页换成“已关闭”的说明（旧的设置数据还在也一样）", async () => {
    const { state } = setup({ role: "user", conversations: [conversationOf(id)] });
    renderApp({ path: `/steward/${id}` });
    const conversation = within(await screen.findByRole("region", { name: "对话" }));
    state.disabled = true;
    state.nextRunResponse = sampleResponse("steward/disabled");
    await userEvent.type(await conversation.findByRole("textbox", { name: "问问小蓝" }), "你好{Enter}");
    expect(await screen.findByText("管理员暂时关闭了这个功能")).toBeTruthy();
  });

  it("用户打开面板时焦点进输入框；收起后回到侧栏入口", async () => {
    setup();
    renderApp({ path: "/" });
    const panel = await openPanel();
    const input = await panel.findByRole("textbox", { name: "问问小蓝" });
    await waitFor(() => expect(document.activeElement).toBe(input));
    await userEvent.click(panel.getByRole("button", { name: "收起" }));
    expect(document.activeElement).toBe(sidebarNav().getByRole("button", { name: "小蓝" }));
  });

  it("未配置时焦点移到面板本身", async () => {
    setup({ settings: settingsOf({ display_name: "小蓝", endpoint: "", protocol: "", model: "", probe_result: null, probed_at: null }) });
    renderApp({ path: "/" });
    await openPanel();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("complementary", { name: "小蓝" })));
  });

  it("面板浮在内容上（768–1099px）时按 Esc 收起，焦点回到入口", async () => {
    const original = window.matchMedia;
    window.matchMedia = (query: string) => ({ ...original(query), matches: query === "(min-width: 768px) and (max-width: 1099px)" });
    try {
      setup();
      renderApp({ path: "/" });
      const panel = await openPanel();
      const input = await panel.findByRole("textbox", { name: "问问小蓝" });
      await waitFor(() => expect(document.activeElement).toBe(input));
      await userEvent.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByRole("complementary", { name: "小蓝" })).toBeNull());
      expect(document.activeElement).toBe(sidebarNav().getByRole("button", { name: "小蓝" }));
    } finally {
      window.matchMedia = original;
    }
  });

  it("浮层状态下焦点在面板外时 Esc 归页面自己，不收起面板；输入法组合中的 Esc 也不收起", async () => {
    const original = window.matchMedia;
    window.matchMedia = (query: string) => ({ ...original(query), matches: query === "(min-width: 768px) and (max-width: 1099px)" });
    try {
      setup();
      renderApp({ path: "/" });
      const panel = await openPanel();
      const input = await panel.findByRole("textbox", { name: "问问小蓝" });
      await waitFor(() => expect(document.activeElement).toBe(input));
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, isComposing: true }));
      expect(screen.getByRole("complementary", { name: "小蓝" })).toBeTruthy();
      const search = screen.getAllByRole("combobox")[0] as HTMLElement;
      search.focus();
      await userEvent.keyboard("{Escape}");
      expect(screen.getByRole("complementary", { name: "小蓝" })).toBeTruthy();
    } finally {
      window.matchMedia = original;
    }
  });

  it("面板内容加载期间焦点被挪去别处时，就绪后不抢焦点", async () => {
    const { api } = setup({ conversations: [conversationOf(id, { title: "记着的对话" })] });
    const { runtime, history } = renderApp({ path: `/steward/${id}` });
    expect(await screen.findByRole("heading", { level: 1, name: "记着的对话" })).toBeTruthy();
    await userEvent.click(sidebarNav().getByRole("link", { name: "概览" }));
    await waitFor(() => expect(history.location.pathname).toBe("/"));
    // 面板记着的会话读取挂住：面板内容一时就绪不了。
    let release: () => void = () => undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    api.on("GET", `/api/v1/me/steward/conversations/${id}`, async () => {
      await held;
      return jsonResponse(200, { ...conversationOf(id, { title: "记着的对话" }) });
    });
    runtime.queryClient.removeQueries({ queryKey: ["viewer"], predicate: (query) => query.queryKey.includes("conversation") });
    await userEvent.click(sidebarNav().getByRole("button", { name: "小蓝" }));
    await screen.findByRole("complementary", { name: "小蓝" });
    const search = screen.getAllByRole("combobox")[0] as HTMLElement;
    search.focus();
    release();
    expect(await within(screen.getByRole("complementary", { name: "小蓝" })).findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
    expect(document.activeElement).toBe(search);
  });

  it("发送时会话过期（401）：重新登录后看到的草稿与提示都是空的", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    installStewardApi(api, { settings: settingsOf({ display_name: "小蓝" }) });
    api.on("POST", "/api/v1/me/steward/conversations", () => {
      session.current = null;
      return sampleResponse("steward/unauthorized");
    });
    renderApp({ path: "/" });
    const panel = await openPanel();
    await userEvent.type(await panel.findByRole("textbox", { name: "问问小蓝" }), "不该留下的草稿{Enter}");
    await userEvent.type(await screen.findByLabelText("用户名"), "listener");
    await userEvent.type(screen.getByLabelText("密码"), "listener-password");
    await userEvent.click(screen.getByRole("button", { name: "登录" }));
    // 面板开关是这个浏览器的偏好：重新登录后面板仍开着。
    await screen.findByRole("heading", { level: 1, name: "概览" });
    const again = within(await screen.findByRole("complementary", { name: "小蓝" }));
    expect((await again.findByRole("textbox", { name: "问问小蓝" }) as HTMLTextAreaElement).value).toBe("");
    expect(again.queryByText("没能开始新对话")).toBeNull();
    expect(screen.queryByText("不该留下的草稿")).toBeNull();
  });

  it("宽屏挤占内容时 Esc 不收起面板", async () => {
    setup();
    renderApp({ path: "/" });
    await openPanel();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("complementary", { name: "小蓝" })).toBeTruthy();
  });

  it("刷新后进 /steward（面板没有记着会话）：转到列表里最近的会话", async () => {
    setup({
      conversations: [
        conversationOf(id, { title: "早一点的", updated_at: "2026-11-10T00:00:00Z" }),
        conversationOf(other, { title: "最近的", updated_at: "2026-11-15T00:00:00Z" }),
      ],
    });
    const { history } = renderApp({ path: "/steward" });
    await waitFor(() => expect(history.location.pathname).toBe(`/steward/${other}`));
    expect(await screen.findByRole("heading", { level: 1, name: "最近的" })).toBeTruthy();
  });

  it("没有会话时 /steward 是空白新对话", async () => {
    setup();
    const { history } = renderApp({ path: "/steward" });
    expect(await screen.findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
    expect(history.location.pathname).toBe("/steward");
  });

  it("记着的会话已经不存在：清掉，回到空白新对话（不再转到最近的会话）", async () => {
    const { state } = setup({ conversations: [conversationOf(id, { title: "会被删的" }), conversationOf(other, { title: "还在的", updated_at: "2026-11-01T00:00:00Z" })] });
    const { history, runtime } = renderApp({ path: `/steward/${id}` });
    expect(await screen.findByRole("heading", { level: 1, name: "会被删的" })).toBeTruthy();
    state.conversations = state.conversations.filter((item) => item.id !== id);
    runtime.queryClient.removeQueries();
    history.push("/steward");
    await waitFor(() => expect(history.location.pathname).toBe(`/steward/${id}`));
    await waitFor(() => expect(history.location.pathname).toBe("/steward"));
    expect(await screen.findByRole("heading", { level: 1, name: "新对话" })).toBeTruthy();
    expect(history.location.pathname).toBe("/steward");
  });

  it("地址里不存在的 ID 不记进面板状态", async () => {
    setup();
    const { history } = renderApp({ path: "/steward/not-a-uuid" });
    expect(await screen.findByText("这个对话不存在或已经删除。")).toBeTruthy();
    history.push("/steward");
    expect(await screen.findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
    expect(history.location.pathname).toBe("/steward");
  });
});
