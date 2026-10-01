import { sampleBody } from "./api-samples";
import { jsonResponse, sampleResponse, stubFetch, type RecordedRequest } from "./http";
import { withValue } from "./mutate";

type Handler = (request: RecordedRequest) => Response | Promise<Response>;

/** 测试用的后端替身：按“方法 路径”分派请求；没有注册的接口返回后端真实的“接口不存在”响应。 */
export type FakeApi = {
  readonly requests: RecordedRequest[];
  /**
   * 注册处理器。字符串路径带查询串时只匹配该查询串，否则匹配该路径的任意查询串；
   * 正则匹配不含查询串的路径（用于带 ID 的路径），字符串路径优先于正则。后注册的覆盖先注册的，
   * 测试因此可以在场景里改写某个接口的行为。
   */
  on: (method: string, path: string | RegExp, handler: Handler) => void;
  /** 已发出的、方法与路径（不含查询串）匹配的请求。 */
  requestsTo: (method: string, path: string | RegExp) => RecordedRequest[];
};

const pathOf = (request: RecordedRequest) => request.path.split("?")[0] ?? request.path;

export function installFakeApi(): FakeApi {
  const handlers = new Map<string, Handler>();
  const patterns: { method: string; pattern: RegExp; handler: Handler }[] = [];
  const requests = stubFetch((request) => {
    const exact = handlers.get(`${request.method} ${request.path}`) ?? handlers.get(`${request.method} ${pathOf(request)}`);
    const handler = exact ?? patterns.find((route) => route.method === request.method && route.pattern.test(pathOf(request)))?.handler;
    return handler === undefined ? sampleResponse("errors/not-found-api") : handler(request);
  });
  const matches = (path: string | RegExp, request: RecordedRequest) => (typeof path === "string" ? pathOf(request) === path : path.test(pathOf(request)));
  return {
    requests,
    on: (method, path, handler) => {
      if (typeof path === "string") handlers.set(`${method} ${path}`, handler);
      else patterns.unshift({ method, pattern: path, handler });
    },
    requestsTo: (method, path) => requests.filter((request) => request.method === method && matches(path, request)),
  };
}

export type FakeAccount = { password: string; role: "admin" | "user" };

/** 后端会话的替身状态：测试可以直接改 current 来模拟另一个标签页登录、退出或会话失效。 */
export type FakeSession = {
  setupRequired: boolean;
  accounts: Record<string, FakeAccount>;
  /** 当前会话的用户名；null 表示没有有效会话。 */
  current: string | null;
};

export function installFakeSession(api: FakeApi, initial: Partial<FakeSession> = {}): FakeSession {
  const session: FakeSession = {
    setupRequired: false,
    accounts: { curator: { password: "curator-password", role: "admin" }, listener: { password: "listener-password", role: "user" } },
    current: null,
    ...initial,
  };
  const viewer = () => {
    const account = session.current === null ? undefined : session.accounts[session.current];
    return session.current === null || account === undefined ? null : { username: session.current, role: account.role };
  };

  api.on("GET", "/api/v1/setup/status", () => jsonResponse(200, { setup_required: session.setupRequired }));
  api.on("GET", "/api/v1/auth/me", () => {
    const current = viewer();
    return current === null ? sampleResponse("errors/unauthorized") : jsonResponse(200, current);
  });
  api.on("POST", "/api/v1/auth/login", (request) => {
    const { username, password } = request.body as { username: string; password: string };
    const account = session.accounts[username];
    if (account === undefined || account.password !== password) return sampleResponse("errors/invalid-credentials");
    session.current = username;
    return jsonResponse(200, { username, role: account.role });
  });
  api.on("POST", "/api/v1/auth/logout", () => {
    session.current = null;
    return sampleResponse("auth/logout");
  });
  api.on("POST", "/api/v1/setup/admin", (request) => {
    if (!session.setupRequired) return sampleResponse("errors/setup-closed");
    const { username, password } = request.body as { username: string; password: string };
    session.accounts[username] = { password, role: "admin" };
    session.setupRequired = false;
    return jsonResponse(201, { username, role: "admin" });
  });
  return session;
}

/**
 * 概览用到的接口：专辑数（带 attention=required 时返回需要关注的数量）、活动扫描、目录与成员列表。
 * 数据取自后端真实样本，只改动数量。
 */
export function installOverviewApi(api: FakeApi, options: { albums?: number; attention?: number } = {}): void {
  const { albums = 12, attention = 3 } = options;
  const groups = sampleBody("release-groups/list-one");
  api.on("GET", "/api/v1/release-groups", (request) =>
    jsonResponse(200, withValue(groups, ["pagination", "total"], request.path.includes("attention=required") ? attention : albums)),
  );
  api.on("GET", "/api/v1/scans/active", () => sampleResponse("scans/active-none"));
  api.on("GET", "/api/v1/library-roots", () => sampleResponse("library-roots/list"));
  api.on("GET", "/api/v1/users", () => sampleResponse("users/list"));
}
