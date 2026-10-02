import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { installFakeApi, installFakeSession } from "../../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../../test/http";
import { channelPair } from "../../../test/channel";
import { createBroadcastSessionChannel } from "./session-channel";
import { createSessionController, type SessionController, type SessionEndReason, type SessionState } from "./session-controller";

const controllers: SessionController[] = [];

function newController(options: Parameters<typeof createSessionController>[0] = {}): SessionController {
  const controller = createSessionController(options);
  controllers.push(controller);
  return controller;
}

afterEach(() => {
  for (const controller of controllers.splice(0)) controller.dispose();
});

function status(controller: SessionController): SessionState["status"] {
  return controller.getSnapshot().status;
}

describe("启动", () => {
  it("需要初始化时进入 setup_required，不再查询会话", async () => {
    const api = installFakeApi();
    installFakeSession(api, { setupRequired: true });
    const controller = newController();
    await controller.start();
    expect(controller.getSnapshot()).toEqual({ status: "setup_required" });
    expect(api.requestsTo("GET", "/api/v1/auth/me")).toHaveLength(0);
  });

  it("没有会话时进入 anonymous，没有说明", async () => {
    installFakeSession(installFakeApi());
    const controller = newController();
    await controller.start();
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: null, username: null });
  });

  it("有会话时进入 authenticated，代次为 1", async () => {
    installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    expect(controller.getSnapshot()).toEqual({
      status: "authenticated",
      viewer: { id: "00000000-0000-4000-8000-000000000002", username: "curator", role: "admin" },
      generation: 1,
    });
    expect(controller.currentGeneration()).toBe(1);
  });

  it.each([
    ["503 服务不可用", () => sampleResponse("errors/database-unavailable"), "http"],
    ["损坏的响应", () => new Response("<html>", { status: 200, headers: { "Content-Type": "text/html" } }), "invalid_response"],
  ])("启动失败（%s）进入 unavailable，并带请求失败的分类", async (_name, respond, kind) => {
    const api = installFakeApi();
    api.on("GET", "/api/v1/setup/status", respond);
    const controller = newController();
    await controller.start();
    const state = controller.getSnapshot();
    expect(state.status === "unavailable" && state.failure.kind).toBe(kind);
  });

  it("网络失败进入 unavailable；retryStart 经 checking 恢复", async () => {
    const api = installFakeApi();
    let online = false;
    const session = installFakeSession(api, { current: "curator" });
    api.on("GET", "/api/v1/setup/status", () => {
      if (!online) throw new TypeError("Failed to fetch");
      return jsonResponse(200, { setup_required: session.setupRequired });
    });
    const controller = newController();
    await controller.start();
    expect(status(controller)).toBe("unavailable");

    online = true;
    const seen: string[] = [];
    controller.subscribe(() => seen.push(status(controller)));
    await controller.retryStart();
    expect(seen).toEqual(["checking", "authenticated"]);
  });
});

describe("登录、退出与代次", () => {
  it("登录成功进入 authenticated，进入时代次加一", async () => {
    installFakeSession(installFakeApi());
    const controller = newController();
    await controller.start();
    expect(controller.currentGeneration()).toBe(0);
    await controller.login({ username: "curator", password: "curator-password" });
    expect(controller.getSnapshot()).toMatchObject({ status: "authenticated", generation: 1, viewer: { username: "curator" } });
  });

  it("登录失败抛出 ApiError，状态与代次不变", async () => {
    installFakeSession(installFakeApi());
    const controller = newController();
    await controller.start();
    const attempt = controller.login({ username: "curator", password: "wrong" });
    await expect(attempt).rejects.toBeInstanceOf(ApiError);
    await expect(attempt).rejects.toMatchObject({ failure: { kind: "http", status: 401, code: "invalid_credentials" } });
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: null, username: null });
    expect(controller.currentGeneration()).toBe(0);
  });

  it("退出要等后端确认：成功后进入 anonymous 并带“已退出”说明，离开时代次再加一", async () => {
    installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    await controller.logout();
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: "signed_out", username: null });
    expect(controller.currentGeneration()).toBe(2);
  });

  it("退出请求失败时保持已登录，抛出 ApiError", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    const controller = newController();
    await controller.start();
    api.on("POST", "/api/v1/auth/logout", () => {
      throw new TypeError("Failed to fetch");
    });
    await expect(controller.logout()).rejects.toMatchObject({ failure: { kind: "network" } });
    expect(status(controller)).toBe("authenticated");
    expect(controller.currentGeneration()).toBe(1);
  });

  it("代次单调递增：登录、退出、再登录各自换代", async () => {
    installFakeSession(installFakeApi());
    const controller = newController();
    await controller.start();
    const generations: number[] = [];
    await controller.login({ username: "curator", password: "curator-password" });
    generations.push(controller.currentGeneration());
    await controller.logout();
    generations.push(controller.currentGeneration());
    await controller.login({ username: "listener", password: "listener-password" });
    generations.push(controller.currentGeneration());
    expect(generations).toEqual([1, 2, 3]);
  });
});

describe("会话结束与清理", () => {
  it("结束时先发布新状态，再通知处理器；处理器看到的已是新代次", async () => {
    installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    const events: string[] = [];
    controller.subscribe(() => events.push(`state:${status(controller)}`));
    controller.onEnded(({ reason }) => events.push(`ended:${reason}:${status(controller)}:${controller.currentGeneration()}`));
    await controller.logout();
    expect(events).toEqual(["state:anonymous", "ended:signed_out:anonymous:2"]);
  });

  it("受保护请求的 401 结束会话，原因为 expired", async () => {
    installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    const reasons: SessionEndReason[] = [];
    controller.onEnded(({ reason }) => reasons.push(reason));
    controller.handleUnauthorized(controller.currentGeneration());
    await vi.waitFor(() => expect(status(controller)).toBe("anonymous"));
    expect(controller.getSnapshot()).toMatchObject({ notice: "expired" });
    expect(reasons).toEqual(["expired"]);
  });

  it("属于旧代次的 401 被忽略，不会结束新登录的会话", async () => {
    installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    const oldGeneration = controller.currentGeneration();
    await controller.logout();
    await controller.login({ username: "listener", password: "listener-password" });
    const newGeneration = controller.currentGeneration();

    controller.handleUnauthorized(oldGeneration);
    await controller.recheck();
    expect(controller.getSnapshot()).toMatchObject({ status: "authenticated", viewer: { username: "listener" } });
    expect(controller.currentGeneration()).toBe(newGeneration);
  });

  it("排队等待期间会话换代，已排队的 401 处理被丢弃", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "curator" });
    const controller = newController();
    await controller.start();
    const generation = controller.currentGeneration();

    let releaseLogout: () => void = () => undefined;
    const logoutGate = new Promise<void>((resolve) => {
      releaseLogout = resolve;
    });
    api.on("POST", "/api/v1/auth/logout", async () => {
      await logoutGate;
      session.current = null;
      return sampleResponse("auth/logout");
    });
    const loggingOut = controller.logout();
    controller.handleUnauthorized(generation); // 排在退出之后
    releaseLogout();
    await loggingOut;
    await vi.waitFor(() => expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: "signed_out", username: null }));
  });

  it("登录与退出串行执行，不交错", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api);
    const order: string[] = [];
    api.on("POST", "/api/v1/auth/login", async () => {
      order.push("login:start");
      await Promise.resolve();
      await new Promise((resolve) => setTimeout(resolve, 10));
      session.current = "curator";
      order.push("login:end");
      return jsonResponse(200, { id: "00000000-0000-4000-8000-000000000002", username: "curator", role: "admin" });
    });
    api.on("POST", "/api/v1/auth/logout", () => {
      order.push("logout");
      session.current = null;
      return sampleResponse("auth/logout");
    });
    const controller = newController();
    await controller.start();
    await Promise.all([controller.login({ username: "curator", password: "curator-password" }), controller.logout()]);
    expect(order).toEqual(["login:start", "login:end", "logout"]);
    expect(controller.getSnapshot()).toMatchObject({ status: "anonymous", notice: "signed_out" });
  });
});

describe("初始化管理员", () => {
  it("创建成功后用同一凭据自动登录", async () => {
    const api = installFakeApi();
    installFakeSession(api, { setupRequired: true, accounts: {} });
    const controller = newController();
    await controller.start();
    await controller.setupAdmin({ username: "owner", password: "a-long-enough-password" });
    expect(controller.getSnapshot()).toMatchObject({ status: "authenticated", viewer: { username: "owner", role: "admin" } });
    expect(api.requestsTo("POST", "/api/v1/auth/login")).toHaveLength(1);
  });

  it("自动登录失败时回到登录页，预填用户名并提示管理员已创建", async () => {
    const api = installFakeApi();
    installFakeSession(api, { setupRequired: true, accounts: {} });
    api.on("POST", "/api/v1/auth/login", () => {
      throw new TypeError("Failed to fetch");
    });
    const controller = newController();
    await controller.start();
    await controller.setupAdmin({ username: "owner", password: "a-long-enough-password" });
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: "setup_complete", username: "owner" });
  });

  it("初始化已被他人完成（409 setup_closed）：进入登录并抛出错误让表单提示", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { setupRequired: true, accounts: {} });
    const controller = newController();
    await controller.start();
    session.setupRequired = false; // 另一个人抢先完成了初始化
    await expect(controller.setupAdmin({ username: "owner", password: "a-long-enough-password" })).rejects.toMatchObject({
      failure: { kind: "http", code: "setup_closed" },
    });
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: "setup_closed", username: null });
  });

  it("创建失败（输入不合法）时状态不变", async () => {
    const api = installFakeApi();
    installFakeSession(api, { setupRequired: true, accounts: {} });
    api.on("POST", "/api/v1/setup/admin", () => sampleResponse("errors/invalid-input"));
    const controller = newController();
    await controller.start();
    await expect(controller.setupAdmin({ username: "owner", password: "short" })).rejects.toMatchObject({
      failure: { code: "invalid_input" },
    });
    expect(status(controller)).toBe("setup_required");
  });
});

describe("核对会话（recheck）", () => {
  it("同一用户：不换代，不通知", async () => {
    installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    const listener = vi.fn();
    controller.subscribe(listener);
    await controller.recheck();
    expect(listener).not.toHaveBeenCalled();
    expect(controller.currentGeneration()).toBe(1);
  });

  it("另一个用户：视为换人，换代两次并以 switched 通知清理", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "curator" });
    const controller = newController();
    await controller.start();
    const reasons: SessionEndReason[] = [];
    controller.onEnded(({ reason }) => reasons.push(reason));
    session.current = "listener";
    await controller.recheck();
    expect(controller.getSnapshot()).toMatchObject({ status: "authenticated", viewer: { username: "listener", role: "user" }, generation: 3 });
    expect(reasons).toEqual(["switched"]);
  });

  it("用户名相同但 ID 不同：视为换人，按 ID 而不是用户名判断", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "curator" });
    const controller = newController();
    await controller.start();
    const reasons: SessionEndReason[] = [];
    controller.onEnded(({ reason }) => reasons.push(reason));
    const curator = session.accounts.curator;
    if (curator === undefined) throw new Error("替身缺少 curator 账号");
    session.accounts.curator = { ...curator, id: "00000000-0000-4000-8000-0000000000ee" };
    await controller.recheck();
    expect(controller.getSnapshot()).toMatchObject({
      status: "authenticated",
      viewer: { id: "00000000-0000-4000-8000-0000000000ee", username: "curator" },
      generation: 3,
    });
    expect(reasons).toEqual(["switched"]);
  });

  it("会话已失效（401）：结束为 expired", async () => {
    const session = installFakeSession(installFakeApi(), { current: "curator" });
    const controller = newController();
    await controller.start();
    session.current = null;
    await controller.recheck();
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: "expired", username: null });
  });

  it("没有登录时发现另一个标签页已登录：进入 authenticated", async () => {
    const session = installFakeSession(installFakeApi());
    const controller = newController();
    await controller.start();
    session.current = "listener";
    await controller.recheck();
    expect(controller.getSnapshot()).toMatchObject({ status: "authenticated", viewer: { username: "listener" } });
  });

  it("等待初始化时发现初始化已完成：进入 anonymous", async () => {
    const session = installFakeSession(installFakeApi(), { setupRequired: true });
    const controller = newController();
    await controller.start();
    session.setupRequired = false;
    await controller.recheck();
    expect(controller.getSnapshot()).toEqual({ status: "anonymous", notice: null, username: null });
  });

  it("瞬时失败时保持原状态，不进入 unavailable", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    const controller = newController();
    await controller.start();
    api.on("GET", "/api/v1/auth/me", () => {
      throw new TypeError("Failed to fetch");
    });
    await controller.recheck();
    expect(status(controller)).toBe("authenticated");
  });

  it("启动尚未完成或不可用时不核对", async () => {
    const api = installFakeApi();
    installFakeSession(api);
    const controller = newController();
    await controller.recheck();
    expect(api.requests).toHaveLength(0);
  });

  it("recheckIfStale 只在距上次核对超过阈值时才发请求", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    let clock = 1_000_000;
    const controller = newController({ now: () => clock });
    await controller.start();
    const meRequests = () => api.requestsTo("GET", "/api/v1/auth/me").length;
    const afterStart = meRequests();

    clock += 29_000;
    await controller.recheckIfStale(30_000);
    expect(meRequests()).toBe(afterStart);

    clock += 2_000;
    await controller.recheckIfStale(30_000);
    expect(meRequests()).toBe(afterStart + 1);

    await controller.recheckIfStale(30_000); // 刚核对过
    expect(meRequests()).toBe(afterStart + 1);
  });
});

describe("跨标签页同步", () => {
  async function twoTabs() {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "curator" });
    const [channelA, channelB] = channelPair();
    const tabA = newController({ channel: channelA });
    const tabB = newController({ channel: channelB });
    await Promise.all([tabA.start(), tabB.start()]);
    return { api, session, tabA, tabB };
  }

  it("一个标签页退出后，另一个标签页收到通知并核对，随之结束会话", async () => {
    const { tabA, tabB } = await twoTabs();
    await tabA.logout();
    await vi.waitFor(() => expect(tabB.getSnapshot()).toMatchObject({ status: "anonymous", notice: "expired" }));
  });

  it("一个标签页换了用户，另一个标签页换代并清理，看不到旧用户", async () => {
    const { session, tabA, tabB } = await twoTabs();
    const reasons: SessionEndReason[] = [];
    tabB.onEnded(({ reason }) => reasons.push(reason));
    await tabA.logout();
    await vi.waitFor(() => expect(status(tabB)).toBe("anonymous"));
    reasons.length = 0;
    await tabA.login({ username: "listener", password: "listener-password" });
    session.current = "listener";
    await vi.waitFor(() => expect(tabB.getSnapshot()).toMatchObject({ status: "authenticated", viewer: { username: "listener" } }));
    expect(tabA.getSnapshot()).toMatchObject({ viewer: { username: "listener" } });
  });

  it("由核对引起的状态变化不再广播，不会在标签页之间来回触发", async () => {
    const { api, tabA, tabB } = await twoTabs();
    await tabA.logout();
    await vi.waitFor(() => expect(status(tabB)).toBe("anonymous"));
    const meRequests = api.requestsTo("GET", "/api/v1/auth/me").length;
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(api.requestsTo("GET", "/api/v1/auth/me").length).toBe(meRequests);
  });

  it("受保护请求的 401 会广播，其他标签页随之核对", async () => {
    const { session, tabA, tabB } = await twoTabs();
    session.current = null;
    tabA.handleUnauthorized(tabA.currentGeneration());
    await vi.waitFor(() => expect(status(tabA)).toBe("anonymous"));
    await vi.waitFor(() => expect(status(tabB)).toBe("anonymous"));
  });
});

describe("BroadcastChannel 适配", () => {
  it("只传递固定的“会话已变化”消息，两个实例之间可以互相通知", async () => {
    const name = `roomusic:test-${Math.random()}`;
    const sender = createBroadcastSessionChannel(name);
    const receiver = createBroadcastSessionChannel(name);
    expect(sender).not.toBeNull();
    expect(receiver).not.toBeNull();
    const received = vi.fn();
    receiver?.onChanged(received);
    sender?.notifyChanged();
    await vi.waitFor(() => expect(received).toHaveBeenCalledTimes(1));
    sender?.close();
    receiver?.close();
  });

  it("忽略其他形状的消息", async () => {
    const name = `roomusic:test-${Math.random()}`;
    const raw = new BroadcastChannel(name);
    const receiver = createBroadcastSessionChannel(name);
    const received = vi.fn();
    receiver?.onChanged(received);
    raw.postMessage({ type: "other" });
    raw.postMessage("changed");
    raw.postMessage({ type: "changed" });
    await vi.waitFor(() => expect(received).toHaveBeenCalledTimes(1));
    raw.close();
    receiver?.close();
  });

  it("浏览器不支持 BroadcastChannel 时返回 null", () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    expect(createBroadcastSessionChannel()).toBeNull();
  });
});
