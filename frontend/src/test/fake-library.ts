import { decodeRootList, decodeRootOperationList, type RootDTO, type RootOperationDTO, type ScanDTO } from "../features/library/api/decoders";
import type { FakeApi } from "./fake-api";
import { sampleBody } from "./api-samples";
import { jsonResponse, sampleResponse, type RecordedRequest } from "./http";

type DiagnosticsBody = { items: { id: string; kind: string; path: string | null; message: string }[]; summary: ScanDTO["diagnostics"] };

/** 测试用的目录与扫描后端替身的状态：测试可以直接读写，用来模拟“别处改了数据”或预置扫描。 */
export type FakeLibrary = {
  roots: RootDTO[];
  /** 最新的在前，与后端一致。 */
  operations: RootOperationDTO[];
  scans: Map<string, ScanDTO>;
  /** 当前运行中的扫描 ID；POST /scans 在它存在时返回 200 与它。 */
  activeScanId: string | null;
  diagnostics: Map<string, DiagnosticsBody>;
  /** 设置后，POST /scans 总是返回它（例如 scan_coordination_unavailable 的 503）。 */
  startScanFailure: Response | null;
  /** 允许登记的路径前缀；不在其内的路径返回 400 invalid_library_path。 */
  allowedPrefix: string;
  /** 接下来这么多次目录写请求在到达服务端之前就中断（服务端没有执行）。 */
  loseRequests: number;
  /** 接下来这么多次目录写请求服务端已经执行，但响应丢失（客户端得到网络失败）。 */
  loseResponses: number;
};

const operationRecords = new Map<string, { fingerprint: string; status: number; body: unknown }>();

function errorResponse(status: number, code: string, message: string): Response {
  return jsonResponse(status, { error: { code, message }, request_id: "sample-request" });
}

const uuidOf = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;

/**
 * 目录与扫描的接口替身，行为依据后端实现：幂等键（同键同请求重放，同键不同请求 409）、expected_revision 校验、
 * 新增已登记路径返回已有目录、扫描开始 202/200、取消 202/200。种子数据取自后端真实样本（经 decoder 解码）。
 */
export function installLibraryApi(api: FakeApi, overrides: Partial<FakeLibrary> = {}): FakeLibrary {
  operationRecords.clear();
  const library: FakeLibrary = {
    roots: decodeRootList(sampleBody("library-roots/list")).items,
    operations: decodeRootOperationList(sampleBody("library-root-operations/list")).items,
    scans: new Map(),
    activeScanId: null,
    diagnostics: new Map(),
    startScanFailure: null,
    allowedPrefix: "/music/",
    loseRequests: 0,
    loseResponses: 0,
    ...overrides,
  };
  let clock = 100;
  const now = () => new Date(Date.UTC(2026, 0, 2, 0, 0, clock++)).toISOString();
  let nextRoot = 10;
  let nextScan = 1;
  let nextOperation = 100;

  const recordOperation = (item: Omit<RootOperationDTO, "id" | "created_at">) => {
    library.operations.unshift({ id: uuidOf("b", nextOperation++), created_at: now(), ...item });
  };

  /** 目录写请求的网络故障注入：先丢请求（没执行），或执行后丢响应（已执行），都表现为 fetch 失败。 */
  const unreliable = (execute: () => Response): Response => {
    if (library.loseRequests > 0) {
      library.loseRequests -= 1;
      throw new TypeError("Failed to fetch");
    }
    const response = execute();
    if (library.loseResponses > 0) {
      library.loseResponses -= 1;
      throw new TypeError("Failed to fetch");
    }
    return response;
  };

  /** 同一个操作者的幂等流程：没有键 400；同键同请求重放；同键不同请求 409。返回 null 表示可以继续执行。 */
  const idempotent = (kind: string, request: RecordedRequest, fingerprint: object): Response | null => {
    const key = request.headers.get("Idempotency-Key");
    if (key === null || key === "") return sampleResponse("errors/invalid-input-missing-idempotency-key");
    const recorded = operationRecords.get(`${kind}:${key}`);
    if (recorded === undefined) return null;
    return JSON.stringify(fingerprint) === recorded.fingerprint ? jsonResponse(recorded.status, recorded.body) : sampleResponse("errors/idempotency-conflict");
  };
  const remember = (kind: string, request: RecordedRequest, fingerprint: object, status: number, body: unknown) => {
    operationRecords.set(`${kind}:${request.headers.get("Idempotency-Key") ?? ""}`, { fingerprint: JSON.stringify(fingerprint), status, body });
  };

  api.on("GET", "/api/v1/library-folders", (request) => {
    const path = new URL(request.path, "http://localhost").searchParams.get("path");
    if (path === null || path === "/music") return sampleResponse("library-folders/start");
    if (path === "/music/classical-a") return sampleResponse("library-folders/child");
    return sampleResponse("errors/invalid-library-folder");
  });
  api.on("GET", "/api/v1/library-roots", () => jsonResponse(200, { items: library.roots }));
  api.on("GET", "/api/v1/library-root-operations", () => jsonResponse(200, { items: library.operations }));

  api.on("POST", "/api/v1/library-roots", (request) =>
    unreliable(() => {
      const { path } = request.body as { path: string };
      if (!path.startsWith(library.allowedPrefix)) return sampleResponse("errors/invalid-library-path");
      const fingerprint = { path };
      const replay = idempotent("create", request, fingerprint);
      if (replay !== null) return replay;
      const name = path.split("/").filter(Boolean).pop() ?? path;
      let root = library.roots.find((candidate) => candidate.name === name);
      if (root === undefined) {
        root = { id: uuidOf("a", nextRoot++), path: name, name, status: "active", revision: 1, created_at: now(), updated_at: now() };
        library.roots.push(root);
      }
      const body = { id: root.id, name: root.name, status: root.status, revision: root.revision };
      recordOperation({ operation: "create", status: "succeeded", root_id: root.id, revision: root.revision });
      remember("create", request, fingerprint, 201, body);
      return jsonResponse(201, body);
    }),
  );

  const change = (kind: "disable" | "restore", request: RecordedRequest, id: string, expectedRevision: number): Response => {
    const fingerprint = { root_id: id, kind, expected_revision: expectedRevision };
    const replay = idempotent(kind, request, fingerprint);
    if (replay !== null) return replay;
    const index = library.roots.findIndex((candidate) => candidate.id === id);
    const root = library.roots[index];
    if (root === undefined) return errorResponse(404, "not_found", "目录不存在");
    const expectedStatus = kind === "disable" ? "active" : "disabled";
    if (root.revision !== expectedRevision || root.status !== expectedStatus) return sampleResponse("errors/revision-conflict");
    const updated: RootDTO = { ...root, status: kind === "disable" ? "disabled" : "active", revision: root.revision + 1, updated_at: now() };
    library.roots[index] = updated;
    const body = { id, path: updated.name, status: updated.status, revision: updated.revision };
    recordOperation({ operation: kind, status: "succeeded", root_id: id, revision: updated.revision });
    remember(kind, request, fingerprint, 200, body);
    return jsonResponse(200, body);
  };

  api.on("PATCH", /^\/api\/v1\/library-roots\/([^/]+)$/, (request) => {
    const id = /\/library-roots\/([^/?]+)/.exec(request.path)?.[1] ?? "";
    const { expected_revision: expectedRevision } = request.body as { status: string; expected_revision: number };
    return unreliable(() => change("disable", request, id, expectedRevision));
  });
  api.on("POST", /^\/api\/v1\/library-roots\/([^/]+)\/restore$/, (request) => {
    const id = /\/library-roots\/([^/?]+)/.exec(request.path)?.[1] ?? "";
    const { expected_revision: expectedRevision } = request.body as { expected_revision: number };
    return unreliable(() => change("restore", request, id, expectedRevision));
  });

  const activeScan = () => (library.activeScanId === null ? null : (library.scans.get(library.activeScanId) ?? null));
  api.on("GET", "/api/v1/scans/active", () => jsonResponse(200, { scan: activeScan() }));
  // 扫描记录：开始时间倒序，同一时间按 ID 倒序；page 默认 1、page_size 默认 20 且最大 100，非法值 400 invalid_pagination。
  api.on("GET", "/api/v1/scans", (request) => {
    const query = new URL(request.path, "http://localhost").searchParams;
    const page = Number(query.get("page") ?? "1");
    const pageSize = Number(query.get("page_size") ?? "20");
    if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) {
      return sampleResponse("errors/invalid-pagination");
    }
    const ordered = [...library.scans.values()].sort((left, right) =>
      left.started_at === right.started_at ? (left.id < right.id ? 1 : -1) : left.started_at < right.started_at ? 1 : -1,
    );
    return jsonResponse(200, {
      items: ordered.slice((page - 1) * pageSize, page * pageSize),
      pagination: { page, page_size: pageSize, total: ordered.length },
    });
  });
  api.on("POST", "/api/v1/scans", () => {
    if (library.startScanFailure !== null) return library.startScanFailure.clone();
    const existing = activeScan();
    if (existing !== null) return jsonResponse(200, existing);
    const scan: ScanDTO = {
      id: uuidOf("c", nextScan),
      scan_run_id: uuidOf("c", nextScan),
      status: "running",
      started_at: now(),
      finished_at: null,
      cancel_requested_at: null,
      diagnostics: { total: 0, counts: [] },
    };
    nextScan += 1;
    library.scans.set(scan.id, scan);
    library.activeScanId = scan.id;
    return jsonResponse(202, scan);
  });
  api.on("GET", /^\/api\/v1\/scans\/([^/]+)$/, (request) => {
    const id = /\/scans\/([^/?]+)/.exec(request.path)?.[1] ?? "";
    const scan = library.scans.get(id);
    return scan === undefined ? sampleResponse("errors/not-found-scan") : jsonResponse(200, scan);
  });
  api.on("POST", /^\/api\/v1\/scans\/([^/]+)\/cancel$/, (request) => {
    const id = /\/scans\/([^/?]+)/.exec(request.path)?.[1] ?? "";
    const scan = library.scans.get(id);
    if (scan === undefined) return sampleResponse("errors/not-found-scan");
    if (scan.status !== "running") return jsonResponse(200, scan);
    const requested: ScanDTO = { ...scan, cancel_requested_at: scan.cancel_requested_at ?? now() };
    library.scans.set(id, requested);
    return jsonResponse(202, requested);
  });
  api.on("GET", /^\/api\/v1\/scans\/([^/]+)\/diagnostics$/, (request) => {
    const id = /\/scans\/([^/?]+)/.exec(request.path)?.[1] ?? "";
    if (!library.scans.has(id)) return sampleResponse("errors/not-found-scan");
    return jsonResponse(200, library.diagnostics.get(id) ?? { items: [], summary: { total: 0, counts: [] } });
  });

  return library;
}

/** 预置一个扫描并返回它；status 缺省为 running，withActive 为 true 时同时设为活动扫描。 */
export function seedScan(library: FakeLibrary, overrides: Partial<ScanDTO> = {}, withActive = false): ScanDTO {
  const index = library.scans.size + 1;
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
  library.scans.set(scan.id, scan);
  if (withActive) library.activeScanId = scan.id;
  return scan;
}
