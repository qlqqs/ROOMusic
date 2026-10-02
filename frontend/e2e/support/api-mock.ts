import type { BrowserContext } from "@playwright/test";
import type { DiagnosticsPageDTO, RootDTO, RootOperationDTO, ScanDTO } from "../../src/features/library/api/decoders";
import type { MemberDTO } from "../../src/features/members/api/decoders";
import { loadApiSample, sampleBody } from "../../src/test/api-samples";
import { withValue } from "../../src/test/mutate";

// 受控后端：用后端集成测试产出的真实响应样本回答 /api/v1/*，并保持必要的状态（会话、目录、扫描、成员），
// 让页面能走完整的流程。这里只实现冒烟需要的行为；幂等重放、请求丢失等细节由 Vitest 的页面级测试覆盖。
// 注意：e2e 运行在 Node 里，不能导入会连带导入 *.module.css 的应用代码，所以只做类型导入，种子数据取自样本。

type Role = "admin" | "user";
type Handled = { status: number; body: unknown } | "abort";
type Account = { id: string; password: string; role: Role };

export type MockOptions = {
  /** 初始是否需要初始化（全新库）。 */
  setupRequired?: boolean;
  /** 初始会话的用户名；缺省为未登录。curator 是管理员，listener 是成员。 */
  signedInAs?: "curator" | "listener" | null;
  /** 前几次读取初始化状态返回 503（用来测试整页错误与重试）。 */
  failSetupStatusTimes?: number;
};

export type RecordedCall = { method: string; path: string; body: unknown; idempotencyKey: string | null };

export type ApiMock = {
  setupRequired: boolean;
  /** 当前会话的用户名；null 表示没有有效会话。 */
  session: string | null;
  accounts: Map<string, Account>;
  roots: RootDTO[];
  operations: RootOperationDTO[];
  users: MemberDTO[];
  scans: Map<string, ScanDTO>;
  activeScanId: string | null;
  diagnostics: Map<string, DiagnosticsPageDTO>;
  /** 运行中的扫描被读取这么多次之后变为终态（不依赖真实时间）；null 表示永远运行。 */
  scanFinishesAfterReads: number | null;
  /** 新建的扫描结束时的终态。incomplete 会带一条诊断。 */
  scanOutcome: "succeeded" | "incomplete";
  /** 对这些“方法 路径”返回 403（路径不含查询串）。 */
  forbid: Set<string>;
  /** 已收到的接口调用，按先后顺序。 */
  calls: RecordedCall[];
  /** 会话在服务端失效（过期或被撤销）：之后的受保护请求得到 401。 */
  expireSession: () => void;
  /** 预置一个扫描；缺省是运行中，withActive 为 true 时同时作为活动扫描。 */
  seedScan: (overrides?: Partial<ScanDTO>, withActive?: boolean) => ScanDTO;
  callsTo: (method: string, path: string | RegExp) => RecordedCall[];
};

const uuidOf = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;
const sample = (name: string): Handled => {
  const { status, body } = loadApiSample(name);
  return { status, body };
};
const json = (body: unknown, status = 200): Handled => ({ status, body });
const notFound = (message: string): Handled => json({ error: { code: "not_found", message }, request_id: "sample-request" }, 404);
const idIn = (path: string, segment: string): string => new RegExp(`/${segment}/([^/?]+)`).exec(path)?.[1] ?? "";

function seedRoots(): RootDTO[] {
  return (sampleBody("library-roots/list") as { items: RootDTO[] }).items;
}

export async function installApiMock(context: BrowserContext, options: MockOptions = {}): Promise<ApiMock> {
  const { setupRequired = false, signedInAs = null, failSetupStatusTimes = 0 } = options;
  const groups = sampleBody("release-groups/list-one");
  const mock: ApiMock = {
    setupRequired,
    session: signedInAs,
    accounts: setupRequired
      ? new Map()
      : new Map<string, Account>([
          // ID 与 users/list 样本里同名成员的 ID 一致，成员页才能按 ID 标出“你”。
          ["curator", { id: "00000000-0000-4000-8000-000000000002", password: "curator-password", role: "admin" }],
          ["listener", { id: "00000000-0000-4000-8000-000000000003", password: "listener-password", role: "user" }],
        ]),
    roots: seedRoots(),
    operations: (sampleBody("library-root-operations/list") as { items: RootOperationDTO[] }).items,
    users: (sampleBody("users/list") as { items: MemberDTO[] }).items,
    scans: new Map(),
    activeScanId: null,
    diagnostics: new Map(),
    scanFinishesAfterReads: 2,
    scanOutcome: "succeeded",
    forbid: new Set(),
    calls: [],
    expireSession: () => {
      mock.session = null;
    },
    seedScan: (overrides = {}, withActive = false) => {
      const index = mock.scans.size + 1;
      const scan: ScanDTO = {
        id: uuidOf("d", index),
        scan_run_id: uuidOf("d", index),
        status: "running",
        started_at: "2026-01-02T00:00:00Z",
        finished_at: null,
        cancel_requested_at: null,
        diagnostics: { total: 0, counts: [] },
        ...overrides,
      };
      mock.scans.set(scan.id, scan);
      if (withActive) mock.activeScanId = scan.id;
      return scan;
    },
    callsTo: (method, path) =>
      mock.calls.filter((call) => call.method === method && (typeof path === "string" ? call.path === path : path.test(call.path))),
  };

  let setupStatusFailures = failSetupStatusTimes;
  let clock = 100;
  let counter = 10;
  const now = () => new Date(Date.UTC(2026, 0, 2, 0, 0, clock++)).toISOString();
  const scanReads = new Map<string, number>();
  const cancelReads = new Map<string, number>();

  const viewer = () => {
    const account = mock.session === null ? undefined : mock.accounts.get(mock.session);
    return mock.session === null || account === undefined ? null : { id: account.id, username: mock.session, role: account.role };
  };
  /** 受保护接口的门：未登录 401；被指定禁止或角色不够 403；否则交给处理器。 */
  const guarded = (method: string, path: string, requireAdmin: boolean, handler: () => Handled): Handled => {
    const current = viewer();
    if (current === null) return sample("errors/unauthorized");
    if (mock.forbid.has(`${method} ${path}`) || (requireAdmin && current.role !== "admin")) return sample("errors/forbidden");
    return handler();
  };

  const finishScan = (scan: ScanDTO, status: ScanDTO["status"]): ScanDTO => {
    const incomplete = status === "incomplete";
    const diagnostics = incomplete ? (sampleBody("scans/diagnostics") as DiagnosticsPageDTO) : null;
    const finished: ScanDTO = {
      ...scan,
      status,
      finished_at: now(),
      diagnostics: diagnostics === null ? scan.diagnostics : diagnostics.summary,
    };
    mock.scans.set(scan.id, finished);
    if (diagnostics !== null) mock.diagnostics.set(scan.id, diagnostics);
    if (mock.activeScanId === scan.id) mock.activeScanId = null;
    return finished;
  };

  const recordOperation = (item: Omit<RootOperationDTO, "id" | "created_at">) => {
    mock.operations.unshift({ id: uuidOf("b", counter++), created_at: now(), ...item });
  };

  const handle = (method: string, path: string, headers: Record<string, string>, body: unknown): Handled => {
    const key = `${method} ${path}`;
    switch (key) {
      case "GET /api/v1/setup/status":
        if (setupStatusFailures > 0) {
          setupStatusFailures -= 1;
          return sample("errors/database-unavailable");
        }
        return json({ setup_required: mock.setupRequired });
      case "POST /api/v1/setup/admin": {
        if (!mock.setupRequired) return sample("errors/setup-closed");
        const { username, password } = body as { username: string; password: string };
        const id = uuidOf("e", counter++);
        mock.accounts.set(username, { id, password, role: "admin" });
        mock.users = [...mock.users, { id, username, role: "admin", disabled: false, created_at: now() }];
        mock.setupRequired = false;
        return json({ id, username, role: "admin" }, 201);
      }
      case "GET /api/v1/auth/me": {
        const current = viewer();
        return current === null ? sample("errors/unauthorized") : json(current);
      }
      case "POST /api/v1/auth/login": {
        const { username, password } = body as { username: string; password: string };
        const account = mock.accounts.get(username);
        const disabled = mock.users.find((user) => user.username === username)?.disabled === true;
        if (account === undefined || account.password !== password || disabled) return sample("errors/invalid-credentials");
        mock.session = username;
        return json({ id: account.id, username, role: account.role });
      }
      case "POST /api/v1/auth/logout":
        mock.session = null;
        return sample("auth/logout");
      case "GET /api/v1/release-groups":
        return guarded(method, path, false, () => json(withValue(groups, ["pagination", "total"], 12)));
      case "GET /api/v1/library-roots":
        return guarded(method, path, true, () => json({ items: mock.roots }));
      case "GET /api/v1/library-root-operations":
        return guarded(method, path, true, () => json({ items: mock.operations }));
      case "POST /api/v1/library-roots":
        return guarded(method, path, true, () => {
          if (!headers["idempotency-key"]) return sample("errors/invalid-input-missing-idempotency-key");
          const { path: requested } = body as { path: string };
          if (!requested.startsWith("/music/")) return sample("errors/invalid-library-path");
          const name = requested.split("/").filter(Boolean).pop() ?? requested;
          let root = mock.roots.find((candidate) => candidate.name === name);
          if (root === undefined) {
            root = { id: uuidOf("a", counter++), path: name, name, status: "active", revision: 1, created_at: now(), updated_at: now() };
            mock.roots = [...mock.roots, root];
          }
          recordOperation({ operation: "create", status: "succeeded", root_id: root.id, revision: root.revision });
          return json({ id: root.id, name: root.name, status: root.status, revision: root.revision }, 201);
        });
      case "GET /api/v1/scans/active":
        return guarded(method, path, false, () => json({ scan: mock.activeScanId === null ? null : (mock.scans.get(mock.activeScanId) ?? null) }));
      case "POST /api/v1/scans":
        return guarded(method, path, true, () => {
          const existing = mock.activeScanId === null ? undefined : mock.scans.get(mock.activeScanId);
          if (existing !== undefined) return json(existing, 200);
          return json(mock.seedScan({ started_at: now() }, true), 202);
        });
      case "GET /api/v1/users":
        return guarded(method, path, true, () => json({ items: mock.users }));
      case "POST /api/v1/users":
        return guarded(method, path, true, () => {
          const { username, password } = body as { username: string; password: string };
          const passwordBytes = new TextEncoder().encode(password).length;
          if (username.trim() === "" || passwordBytes < 12 || passwordBytes > 72) return sample("errors/invalid-input");
          if (mock.users.some((user) => user.username === username)) return sample("errors/username-taken");
          const created: MemberDTO = { id: uuidOf("e", counter++), username, role: "user", disabled: false, created_at: now() };
          mock.users = [...mock.users, created].sort((left, right) => (left.username < right.username ? -1 : 1));
          mock.accounts.set(username, { id: created.id, password, role: "user" });
          return json({ id: created.id, username, role: "user" }, 201);
        });
    }

    const rootChange = /^\/api\/v1\/library-roots\/[^/]+(\/restore)?$/.exec(path);
    if (rootChange !== null && (method === "PATCH" || method === "POST")) {
      return guarded(method, path, true, () => {
        if (!headers["idempotency-key"]) return sample("errors/invalid-input-missing-idempotency-key");
        const restoring = rootChange[1] !== undefined;
        const index = mock.roots.findIndex((candidate) => candidate.id === idIn(path, "library-roots"));
        const root = mock.roots[index];
        if (root === undefined) return notFound("目录不存在");
        const { expected_revision: expected } = body as { expected_revision: number };
        if (root.revision !== expected || root.status !== (restoring ? "disabled" : "active")) return sample("errors/revision-conflict");
        const updated: RootDTO = { ...root, status: restoring ? "active" : "disabled", revision: root.revision + 1, updated_at: now() };
        mock.roots = mock.roots.map((candidate, position) => (position === index ? updated : candidate));
        recordOperation({ operation: restoring ? "restore" : "disable", status: "succeeded", root_id: root.id, revision: updated.revision });
        return json({ id: root.id, path: root.name, status: updated.status, revision: updated.revision });
      });
    }

    const scanPath = /^\/api\/v1\/scans\/([^/]+)(\/cancel|\/diagnostics)?$/.exec(path);
    if (scanPath !== null) {
      const id = scanPath[1] ?? "";
      const suffix = scanPath[2];
      return guarded(method, path, suffix !== undefined, () => {
        const scan = mock.scans.get(id);
        if (scan === undefined) return sample("errors/not-found-scan");
        if (suffix === "/diagnostics" && method === "GET") return json(mock.diagnostics.get(id) ?? { items: [], summary: { total: 0, counts: [] } });
        if (suffix === "/cancel" && method === "POST") {
          if (scan.status !== "running") return json(scan, 200);
          const requested: ScanDTO = { ...scan, cancel_requested_at: scan.cancel_requested_at ?? now() };
          mock.scans.set(id, requested);
          return json(requested, 202);
        }
        if (suffix === undefined && method === "GET") {
          if (scan.status !== "running") return json(scan);
          // 取消请求被接受后，扫描还要走完当前步骤：第一次读取仍是“取消请求中”，下一次读取它已经停下。
          if (scan.cancel_requested_at !== null) {
            const afterCancel = (cancelReads.get(id) ?? 0) + 1;
            cancelReads.set(id, afterCancel);
            return json(afterCancel === 1 ? scan : finishScan(scan, "canceled"));
          }
          const reads = (scanReads.get(id) ?? 0) + 1;
          scanReads.set(id, reads);
          if (mock.scanFinishesAfterReads !== null && reads >= mock.scanFinishesAfterReads) return json(finishScan(scan, mock.scanOutcome));
          return json(scan);
        }
        return sample("errors/method-not-allowed");
      });
    }

    const userChange = /^\/api\/v1\/users\/[^/]+(\/sessions\/revoke)?$/.exec(path);
    if (userChange !== null && (method === "PATCH" || method === "POST")) {
      return guarded(method, path, true, () => {
        const revoking = userChange[1] !== undefined;
        const index = mock.users.findIndex((user) => user.id === idIn(path, "users"));
        const target = mock.users[index];
        if (target === undefined) return sample("errors/not-found-user");
        if (revoking) {
          if (target.username === mock.session) mock.session = null;
          return json({ revoked: true });
        }
        const { disabled } = body as { disabled: boolean };
        const enabledAdmins = mock.users.filter((user) => user.role === "admin" && !user.disabled).length;
        if (disabled && !target.disabled && target.role === "admin" && enabledAdmins <= 1) return sample("errors/last-admin");
        mock.users = mock.users.map((user, position) => (position === index ? { ...user, disabled } : user));
        if (disabled && target.username === mock.session) mock.session = null;
        return json({ disabled });
      });
    }

    return sample("errors/not-found-api");
  };

  // 在浏览器上下文上拦截：同一上下文里的多个页面（例如跨标签页退出）共享同一个后端。
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    let body: unknown;
    try {
      body = request.postDataJSON();
    } catch {
      body = undefined;
    }
    mock.calls.push({ method: request.method(), path: url.pathname, body, idempotencyKey: request.headers()["idempotency-key"] ?? null });
    const handled = handle(request.method(), url.pathname, request.headers(), body);
    if (handled === "abort") {
      await route.abort("failed");
      return;
    }
    await route.fulfill({ status: handled.status, contentType: "application/json", body: JSON.stringify(handled.body) });
  });

  return mock;
}
