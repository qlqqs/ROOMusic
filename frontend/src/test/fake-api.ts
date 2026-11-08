import { sampleBody } from "./api-samples";
import { installPersonalApi } from "./fake-personal";
import { jsonResponse, sampleResponse, stubFetch, type RecordedRequest } from "./http";
import { respondToSearch, searchDataOf, type SearchData } from "./search-data";
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

export type FakeAccount = { id: string; password: string; role: "admin" | "user" };

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
    // ID 与 users/list 样本里同名成员的 ID 一致：成员页按 ID 标出“你”，替身的查看者必须能对上列表里的那一行。
    accounts: {
      curator: { id: "00000000-0000-4000-8000-000000000002", password: "curator-password", role: "admin" },
      listener: { id: "00000000-0000-4000-8000-000000000003", password: "listener-password", role: "user" },
    },
    current: null,
    ...initial,
  };
  const viewer = () => {
    const account = session.current === null ? undefined : session.accounts[session.current];
    return session.current === null || account === undefined ? null : { id: account.id, username: session.current, role: account.role };
  };

  // 新心形和默认收藏页需要明确的空状态；测试可再次安装带 catalog 的完整替身。
  installPersonalApi(api, { session });

  api.on("GET", "/api/v1/setup/status", () => jsonResponse(200, { setup_required: session.setupRequired }));
  api.on("GET", "/api/v1/auth/me", () => {
    const current = viewer();
    return current === null ? sampleResponse("errors/unauthorized") : jsonResponse(200, current);
  });
  // 默认采用已发布的搜索状态；需要回填或错误状态的用例显式覆盖，不默认登记搜索响应。
  api.on("GET", "/api/v1/catalog/status", () => viewer() === null ? sampleResponse("errors/unauthorized") : sampleResponse("search/catalog-status-ready"));
  // 已登录外壳中的专辑播放动作读取格式表；错误场景可以显式覆盖这个默认响应。
  api.on("GET", "/api/v1/playback/formats", () => viewer() === null ? sampleResponse("errors/unauthorized") : sampleResponse("playback/formats"));
  // 已登录外壳里的播放历史：概览读取最近播放（默认没有记录）、播放器上报一次播放、清除历史。响应取自后端样本，需要数据的测试自行覆盖。
  api.on("GET", "/api/v1/me/plays/recent-releases", () => viewer() === null ? sampleResponse("errors/unauthorized") : sampleResponse("listening/recent-releases-empty"));
  api.on("POST", "/api/v1/me/plays", () => viewer() === null ? sampleResponse("errors/unauthorized") : sampleResponse("listening/play-recorded"));
  api.on("DELETE", "/api/v1/me/plays", () => viewer() === null ? sampleResponse("errors/unauthorized") : sampleResponse("listening/plays-cleared"));
  // 概览的最近新增默认没有记录；具体场景可覆盖。匿名会话仍被拒绝，不用 404 伪装空列表。
  api.on("GET", "/api/v1/release-groups/recently-added", () => viewer() === null ? sampleResponse("errors/unauthorized") : sampleResponse("scan-feedback/recent-empty"));
  // 报告只给管理员；未登记的扫描返回 404，不能以默认成功掩盖遗漏的场景或错误的扫描 ID。
  api.on("GET", /^\/api\/v1\/scans\/[^/]+\/report$/, () => {
    const current = viewer();
    if (current === null) return sampleResponse("scan-feedback/report-unauthorized");
    if (current.role !== "admin") return sampleResponse("scan-feedback/report-forbidden");
    return sampleResponse("scan-feedback/report-not-found");
  });
  api.on("POST", "/api/v1/auth/login", (request) => {
    const { username, password } = request.body as { username: string; password: string };
    const account = session.accounts[username];
    if (account === undefined || account.password !== password) return sampleResponse("errors/invalid-credentials");
    session.current = username;
    return jsonResponse(200, { id: account.id, username, role: account.role });
  });
  api.on("POST", "/api/v1/auth/logout", () => {
    session.current = null;
    return sampleResponse("auth/logout");
  });
  // 修改自己的密码：先校验新密码（400 invalid_input），再核对当前密码（403 current_password_incorrect，会话不变）。
  // 替身只有一个会话，成功后当前会话保留；其他会话被撤销由后端测试覆盖。
  api.on("POST", "/api/v1/auth/password", (request) => {
    const account = session.current === null ? undefined : session.accounts[session.current];
    if (account === undefined) return sampleResponse("errors/unauthorized");
    const { current_password: current, new_password: next } = request.body as { current_password: string; new_password: string };
    if (!/^[\x20-\x7E]{12,72}$/.test(next)) return sampleResponse("errors/invalid-input-password");
    if (account.password !== current) return sampleResponse("errors/current-password-incorrect");
    account.password = next;
    return sampleResponse("auth/password-changed");
  });
  api.on("POST", "/api/v1/setup/admin", (request) => {
    if (!session.setupRequired) return sampleResponse("errors/setup-closed");
    const { username, password } = request.body as { username: string; password: string };
    const id = "00000000-0000-4000-8000-0000000000f0";
    session.accounts[username] = { id, password, role: "admin" };
    session.setupRequired = false;
    return jsonResponse(201, { id, username, role: "admin" });
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
  api.on("GET", "/api/v1/catalog/stats", () => {
    const stats = sampleBody("catalog/stats") as { albums: number };
    return jsonResponse(200, { ...stats, albums });
  });
  api.on("GET", "/api/v1/audio-analysis/progress", () => sampleResponse("audio-analysis/progress"));
  api.on("GET", "/api/v1/scans/active", () => sampleResponse("scans/active-none"));
  api.on("GET", "/api/v1/library-roots", () => sampleResponse("library-roots/list"));
  api.on("GET", "/api/v1/users", () => sampleResponse("users/list"));
}

/** 搜索只响应显式登记的条目；可与浏览器替身共享同一组响应。 */
export function installFakeSearch(api: FakeApi, data: SearchData = searchDataOf()): SearchData {
  for (const path of ["/api/v1/search", "/api/v1/search/suggestions"]) {
    api.on("GET", path, (request) => {
      const url = new URL(request.path, "http://localhost");
      const { status, body } = respondToSearch(data, url.pathname, url.searchParams);
      return jsonResponse(status, body);
    });
  }
  return data;
}

export { installArtistsApi } from "./fake-artists";
