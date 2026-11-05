import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { channelPair } from "../test/channel";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi, type FakeApi, type FakeSession } from "../test/fake-api";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

function setup(session: Partial<FakeSession> = {}) {
  const api = installFakeApi();
  const fakeSession = installFakeSession(api, session);
  installOverviewApi(api);
  return { api, session: fakeSession };
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });

async function signIn(username: string, password: string) {
  await userEvent.type(screen.getByLabelText("用户名"), username);
  await userEvent.type(screen.getByLabelText("密码"), password);
  await userEvent.click(screen.getByRole("button", { name: "登录" }));
}

function requestsTo(api: FakeApi, path: string): number {
  return api.requestsTo("GET", path).length;
}

describe("启动与路由守卫", () => {
  it("确认会话期间显示占位，没有会话时进入登录页", async () => {
    setup();
    renderApp();
    expect(screen.getByRole("status").textContent).toContain("正在加载");
    expect(await heading("登录")).toBeTruthy();
    expect(document.title).toBe("登录 · ROOMusic");
  });

  it("已有会话时直接进入概览", async () => {
    setup({ current: "curator" });
    renderApp();
    expect(await heading("概览")).toBeTruthy();
    expect(document.title).toBe("概览 · ROOMusic");
  });

  it("需要初始化时进入初始化页，访问登录页也被送到初始化页", async () => {
    setup({ setupRequired: true, accounts: {} });
    const { history } = renderApp({ path: "/login" });
    expect(await heading("创建管理员")).toBeTruthy();
    expect(history.location.pathname).toBe("/setup");
  });

  it("未登录访问深层地址：去登录页并带回跳地址，登录后回到原地址", async () => {
    setup();
    const { history } = renderApp({ path: "/some/unknown/page?x=1" });
    await heading("登录");
    expect(history.location.pathname).toBe("/login");
    expect(new URLSearchParams(history.location.search).get("redirect")).toBe("/some/unknown/page?x=1");

    await signIn("curator", "curator-password");
    expect(await heading("页面不存在")).toBeTruthy();
    expect(history.location.pathname).toBe("/some/unknown/page");
    expect(history.location.search).toBe("?x=1");
  });

  it("不合法的回跳地址被丢弃，登录后进入概览", async () => {
    setup();
    const { history } = renderApp({ path: "/login?redirect=//evil.example" });
    await heading("登录");
    await signIn("curator", "curator-password");
    expect(await heading("概览")).toBeTruthy();
    expect(history.location.pathname).toBe("/");
  });

  it("已登录访问登录页时回到概览", async () => {
    setup({ current: "curator" });
    const { history } = renderApp({ path: "/login" });
    expect(await heading("概览")).toBeTruthy();
    expect(history.location.pathname).toBe("/");
  });

  it("已登录访问不存在的路径：在外壳里显示“页面不存在”", async () => {
    setup({ current: "curator" });
    renderApp({ path: "/nowhere" });
    expect(await heading("页面不存在")).toBeTruthy();
    expect(screen.getByRole("navigation", { name: "主导航" })).toBeTruthy();
  });

  it("启动失败（503）显示可重试的整页错误，重试成功后进入应用", async () => {
    const { api } = setup({ current: "curator" });
    let failing = true;
    api.on("GET", "/api/v1/setup/status", () => (failing ? sampleResponse("errors/database-unavailable") : jsonResponse(200, { setup_required: false })));
    renderApp();
    expect(await heading("暂时无法使用")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("服务暂时不可用");
    failing = false;
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await heading("概览")).toBeTruthy();
  });
});

describe("登录与初始化", () => {
  it("登录成功进入概览，外壳显示当前用户与角色", async () => {
    setup();
    renderApp();
    await heading("登录");
    await signIn("curator", "curator-password");
    expect(await heading("概览")).toBeTruthy();
    const identity = screen.getByRole("region", { name: "当前用户" });
    expect(within(identity).getByText("curator")).toBeTruthy();
    expect(within(identity).getByText("管理员")).toBeTruthy();
    const nav = screen.getByRole("navigation", { name: "主导航" });
    expect(within(nav).getByRole("link", { name: "概览" }).getAttribute("aria-current")).toBe("page");
  });

  it("用户名或密码错误：提示错误并留在登录页", async () => {
    setup();
    const { history } = renderApp();
    await heading("登录");
    await signIn("curator", "wrong-password");
    expect((await screen.findByRole("alert")).textContent).toContain("用户名或密码错误");
    expect(history.location.pathname).toBe("/login");
  });

  it("空输入时提示字段错误并把焦点移到第一个出错字段，不发请求", async () => {
    const { api } = setup();
    renderApp();
    await heading("登录");
    await userEvent.click(screen.getByRole("button", { name: "登录" }));
    expect(screen.getByText("请输入用户名。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByLabelText("用户名"));
    expect(api.requestsTo("POST", "/api/v1/auth/login")).toHaveLength(0);
  });

  it("初始化管理员后用同一凭据自动登录，进入概览", async () => {
    const { api } = setup({ setupRequired: true, accounts: {} });
    renderApp();
    await heading("创建管理员");
    await userEvent.type(screen.getByLabelText("用户名"), "owner");
    await userEvent.type(screen.getByLabelText("密码"), "a-long-enough-password");
    await userEvent.type(screen.getByLabelText("确认密码"), "a-long-enough-password");
    await userEvent.click(screen.getByRole("button", { name: "创建管理员并登录" }));
    expect(await heading("概览")).toBeTruthy();
    expect(api.requestsTo("POST", "/api/v1/setup/admin")).toHaveLength(1);
    expect(api.requestsTo("POST", "/api/v1/auth/login")).toHaveLength(1);
  });

  it("初始化时密码太短或两次不一致：字段提示，不发请求", async () => {
    const { api } = setup({ setupRequired: true, accounts: {} });
    renderApp();
    await heading("创建管理员");
    await userEvent.type(screen.getByLabelText("用户名"), "owner");
    await userEvent.type(screen.getByLabelText("密码"), "short");
    await userEvent.type(screen.getByLabelText("确认密码"), "short");
    await userEvent.click(screen.getByRole("button", { name: "创建管理员并登录" }));
    expect(screen.getByText(/密码至少要 12 位/)).toBeTruthy();
    expect(api.requestsTo("POST", "/api/v1/setup/admin")).toHaveLength(0);
  });

  it("初始化已被他人完成：回到登录页并说明", async () => {
    const { session } = setup({ setupRequired: true, accounts: {} });
    renderApp();
    await heading("创建管理员");
    session.setupRequired = false;
    await userEvent.type(screen.getByLabelText("用户名"), "owner");
    await userEvent.type(screen.getByLabelText("密码"), "a-long-enough-password");
    await userEvent.type(screen.getByLabelText("确认密码"), "a-long-enough-password");
    await userEvent.click(screen.getByRole("button", { name: "创建管理员并登录" }));
    expect(await heading("登录")).toBeTruthy();
    expect(screen.getByText("初始化已经完成，请登录。")).toBeTruthy();
  });
});

describe("退出与会话失效", () => {
  it("退出要等后端确认；成功后回到登录页（不带回跳地址）并清空全部缓存", async () => {
    const { api } = setup({ current: "curator" });
    const { history, runtime } = renderApp({ path: "/nowhere" });
    await heading("页面不存在");
    await userEvent.click(screen.getByRole("button", { name: "退出登录" }));
    expect(await heading("登录")).toBeTruthy();
    expect(screen.getByText("已退出登录。")).toBeTruthy();
    expect(history.location.pathname).toBe("/login");
    expect(history.location.search).toBe("");
    expect(api.requestsTo("POST", "/api/v1/auth/logout")).toHaveLength(1);
    expect(runtime.queryClient.getQueryCache().getAll()).toHaveLength(0);
  });

  it("退出请求失败：留在当前页并提示重试，不假装已退出", async () => {
    const { api } = setup({ current: "curator" });
    renderApp();
    await heading("概览");
    api.on("POST", "/api/v1/auth/logout", () => {
      throw new TypeError("Failed to fetch");
    });
    await userEvent.click(screen.getByRole("button", { name: "退出登录" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("退出失败");
    expect(screen.getByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
  });

  it("受保护请求得到 401：结束会话，去登录页并带回跳地址，登录后回来", async () => {
    const { session, api } = setup({ current: "curator" });
    const { runtime, history } = renderApp({ path: "/nowhere" });
    await heading("页面不存在");

    session.current = null;
    act(() => runtime.session.handleUnauthorized(runtime.session.currentGeneration()));
    expect(await heading("登录")).toBeTruthy();
    expect(screen.getByText("登录已失效，请重新登录。")).toBeTruthy();
    expect(new URLSearchParams(history.location.search).get("redirect")).toBe("/nowhere");
    expect(runtime.queryClient.getQueryCache().getAll()).toHaveLength(0);

    await signIn("curator", "curator-password");
    expect(await heading("页面不存在")).toBeTruthy();
    expect(history.location.pathname).toBe("/nowhere");
    expect(api.requestsTo("POST", "/api/v1/auth/login")).toHaveLength(1);
  });

  it("概览里的受保护请求得到 401 时同样结束会话", async () => {
    const { api } = setup({ current: "curator" });
    api.on("GET", "/api/v1/scans/active", () => sampleResponse("errors/unauthorized"));
    renderApp();
    expect(await heading("登录")).toBeTruthy();
    expect(screen.getByText("登录已失效，请重新登录。")).toBeTruthy();
  });
});

describe("切换用户时的数据隔离", () => {
  it("旧用户迟到的响应不会出现在新用户的界面里", async () => {
    const { api } = setup();
    renderApp();
    await heading("登录");

    // 管理员的第一次“专辑数”请求停在进行中，退出并换成成员登录后才释放。
    const gate = deferred<Response>();
    let albumRequests = 0;
    api.on("GET", "/api/v1/release-groups", async (request) => {
      albumRequests += 1;
      if (albumRequests <= 2) return gate.promise; // 管理员的两个并行请求（全部 / 需要关注）
      const total = request.path.includes("attention=required") ? 1 : 7;
      return jsonResponse(200, { items: [], pagination: { page: 1, page_size: 1, total } });
    });

    await signIn("curator", "curator-password");
    await heading("概览");
    await userEvent.click(screen.getByRole("button", { name: "退出登录" }));
    await heading("登录");
    await signIn("listener", "listener-password");
    await heading("概览");
    const library = await screen.findByRole("region", { name: "资料库" });
    await within(library).findByText("7");

    // 旧请求现在才回来，带着 99。
    gate.resolve(jsonResponse(200, { items: [], pagination: { page: 1, page_size: 1, total: 99 } }));
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(screen.queryByText("99")).toBeNull();
    expect(within(library).getByText("7")).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "当前用户" })).getByText("listener")).toBeTruthy();
  });

  it("成员不请求任何管理接口，也看不到管理员区块", async () => {
    const { api } = setup({ current: "listener" });
    renderApp();
    await heading("概览");
    await screen.findByRole("region", { name: "资料库" });
    expect(screen.queryByRole("region", { name: "音乐目录" })).toBeNull();
    expect(screen.queryByRole("region", { name: "成员" })).toBeNull();
    expect(requestsTo(api, "/api/v1/library-roots")).toBe(0);
    expect(requestsTo(api, "/api/v1/users")).toBe(0);
    const identity = screen.getByRole("region", { name: "当前用户" });
    expect(within(identity).getByText("成员")).toBeTruthy();
  });
});

describe("跨标签页与重新可见", () => {
  it("收到其他标签页的会话变化通知后核对：对方已退出则本页结束会话", async () => {
    const { session } = setup({ current: "curator" });
    const [ours, theirs] = channelPair();
    renderApp({ channel: ours });
    await heading("概览");
    session.current = null; // 另一个标签页退出了
    act(() => theirs.notifyChanged());
    expect(await heading("登录")).toBeTruthy();
    expect(screen.getByText("登录已失效，请重新登录。")).toBeTruthy();
  });

  it("另一个标签页换了用户：本页换成新用户，不再显示旧用户", async () => {
    const { session } = setup({ current: "curator" });
    const [ours, theirs] = channelPair();
    renderApp({ channel: ours });
    await heading("概览");
    expect(within(screen.getByRole("region", { name: "当前用户" })).getByText("curator")).toBeTruthy();
    session.current = "listener";
    act(() => theirs.notifyChanged());
    await waitFor(() => expect(within(screen.getByRole("region", { name: "当前用户" })).getByText("listener")).toBeTruthy());
    expect(screen.queryByText("curator")).toBeNull();
    expect(screen.queryByRole("region", { name: "成员" })).toBeNull();
  });

  it("没有登录时，另一个标签页登录后本页进入概览", async () => {
    const { session } = setup();
    const [ours, theirs] = channelPair();
    renderApp({ channel: ours });
    await heading("登录");
    session.current = "curator";
    act(() => theirs.notifyChanged());
    expect(await heading("概览")).toBeTruthy();
  });

  it("页面重新可见且距上次核对超过 30 秒才核对会话", async () => {
    const { session, api } = setup({ current: "curator" });
    let clock = 1_000_000;
    renderApp({ now: () => clock });
    await heading("概览");
    const me = () => api.requestsTo("GET", "/api/v1/auth/me").length;
    const before = me();

    clock += 10_000;
    act(() => void document.dispatchEvent(new Event("visibilitychange")));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(me()).toBe(before);

    clock += 25_000;
    session.current = null;
    act(() => void document.dispatchEvent(new Event("visibilitychange")));
    expect(await heading("登录")).toBeTruthy();
    expect(me()).toBe(before + 1);
  });
});

describe("外壳", () => {
  it("概览以外的导航项按角色显示；跳转链接把焦点带到主内容", async () => {
    setup({ current: "curator" });
    renderApp();
    await heading("概览");
    const main = screen.getByRole("main");
    await userEvent.click(screen.getByRole("link", { name: "跳到主要内容" }));
    expect(document.activeElement).toBe(main);
  });

  it("窄屏的“菜单”按钮打开带名称的模态导航，关闭后焦点回到按钮", async () => {
    setup({ current: "curator" });
    renderApp();
    await heading("概览");
    const menuButton = screen.getByRole("button", { name: "菜单" });
    await userEvent.click(menuButton);
    const dialog = screen.getByRole("dialog", { name: "菜单" });
    expect(within(dialog).getByRole("navigation", { name: "主导航" })).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "退出登录" })).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(menuButton);
  });

  it("在导航对话框里按 Esc 关闭", async () => {
    setup({ current: "curator" });
    renderApp();
    await heading("概览");
    await userEvent.click(screen.getByRole("button", { name: "菜单" }));
    fireEvent(screen.getByRole("dialog"), new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
