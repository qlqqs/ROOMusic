import { sampleBody } from "./api-samples";
import type { FakeApi } from "./fake-api";
import { jsonResponse, sampleResponse, type RecordedRequest } from "./http";

/** 一条可控的 SSE 响应：测试逐个推送事件，再结束或断开。 */
export type FakeSse = {
  response: Response;
  send: (event: string, data: unknown) => void;
  close: () => void;
  /** 没有 done 就断开。 */
  fail: () => void;
};

export function fakeSse(): FakeSse {
  const encoder = new TextEncoder();
  let controller: ReadableStreamDefaultController<Uint8Array> | null = null;
  let closed = false;
  const body = new ReadableStream<Uint8Array>({
    start(created) {
      controller = created;
      created.enqueue(encoder.encode(": ping\n\n"));
    },
    cancel() {
      closed = true;
    },
  });
  return {
    response: new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } }),
    send: (event, data) => {
      if (!closed) controller?.enqueue(encoder.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
    },
    close: () => {
      if (closed) return;
      closed = true;
      controller?.close();
    },
    fail: () => {
      if (closed) return;
      closed = true;
      controller?.error(new TypeError("network error"));
    },
  };
}

type Settings = ReturnType<typeof settingsOf>;

/** 管家设置：默认是检测通过、可以对话的设置（样本 probe-supported），可以覆盖名字等字段。 */
export function settingsOf(overrides: Record<string, unknown> = {}) {
  return { ...(sampleBody("steward/probe-supported") as Record<string, unknown>), ...overrides };
}

export type FakeConversation = {
  id: string;
  title: string;
  mode: "assistant";
  model: string;
  status: "idle" | "running";
  created_at: string;
  updated_at: string;
  messages: unknown[];
};

export function conversationOf(id: string, overrides: Partial<FakeConversation> = {}): FakeConversation {
  return { id, title: "合成对话", mode: "assistant", model: "synthetic-model", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z", messages: [], ...overrides };
}

export type StewardFake = {
  settings: Settings | null;
  /** 成员开关关闭：设置与会话接口都返回 403 steward_disabled。 */
  disabled: boolean;
  conversations: FakeConversation[];
  /** 发消息与继续运行收到的请求、请求体里的页面上下文（没有时是 null）与对应的流；测试据此推送事件。 */
  runs: { request: RecordedRequest; kind: "messages" | "continue"; context: unknown; stream: FakeSse }[];
  /** 下一次发消息或继续时直接返回的响应（开始流之前的拒绝）。 */
  nextRunResponse: Response | null;
  /** 检测的响应；null 时按提交的地址、模型与密钥合成一次通过的检测并保存。 */
  probeResponse: Response | null;
  /** 管理员：成员开关与放行名单。 */
  instance: { members_enabled: boolean };
  allowlist: { host: string; port: number; added_by: string | null; created_at: string }[];
};

const conversationPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)$/;
const runPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/(messages|continue)$/;
const cancelPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/cancel$/;
const idOf = (request: RecordedRequest, pattern: RegExp) => pattern.exec(request.path.split("?")[0] ?? "")?.[1] ?? "";

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
const pageKinds = ["album", "artist", "playlist", "track", "other"];

/**
 * 请求体里的页面上下文（C5）：按后端的规则检查——发消息只认 text 与 context（DisallowUnknownFields），
 * context 只有 page 与 playback，page.kind 是枚举，路径不超过 512、标题类不超过 300 个字符；继续不接受 context。
 * 不合格时返回说明，合格时是 null。
 */
function contextProblemOf(body: unknown, kind: "messages" | "continue"): string | null {
  if (kind === "continue") return isRecord(body) && "context" in body ? "继续不接受 context" : null;
  if (!isRecord(body)) return "请求体应为对象";
  if (Object.keys(body).some((key) => key !== "text" && key !== "context")) return "请求体有不认识的字段";
  const context = body["context"];
  if (context === undefined) return null;
  if (!isRecord(context) || Object.keys(context).some((key) => key !== "page" && key !== "playback")) return "context 形状不对";
  const tooLong = (value: unknown, max: number) => value !== undefined && (typeof value !== "string" || Array.from(value).length > max);
  const page = context["page"];
  if (page !== undefined && (!isRecord(page) || typeof page["path"] !== "string" || tooLong(page["path"], 512) || !pageKinds.includes(String(page["kind"])) || tooLong(page["title"], 300))) {
    return "page 不合格";
  }
  const playback = context["playback"];
  if (playback !== undefined && (!isRecord(playback) || typeof playback["zone_id"] !== "string" || ["zone_name", "title", "artist"].some((key) => tooLong(playback[key], 300)))) {
    return "playback 不合格";
  }
  return null;
}

let created = 0;

/** 与后端 endpointAuthority 相同：主机名转小写、去掉末尾的点，未写端口时 http 取 80、https 取 443。 */
function authorityOf(endpoint: string): string {
  try {
    const url = new URL(endpoint);
    return `${url.hostname.toLowerCase().replace(/\.$/, "")}:${url.port !== "" ? url.port : url.protocol === "https:" ? "443" : "80"}`;
  } catch {
    return "";
  }
}

/** 管家接口的替身：设置、会话的增删改查、运行流（可控，记下请求体里的页面上下文）与取消。形状按 C2、C5 的接口合同与样本。 */
export function installStewardApi(api: FakeApi, initial: Partial<StewardFake> = {}): StewardFake {
  const state: StewardFake = {
    settings: settingsOf(),
    disabled: false,
    conversations: [],
    runs: [],
    nextRunResponse: null,
    probeResponse: null,
    instance: { members_enabled: true },
    allowlist: [],
    ...initial,
  };
  const denied = () => (state.disabled ? sampleResponse("steward/disabled") : null);
  const summary = ({ messages: _messages, ...rest }: FakeConversation) => rest;
  const find = (id: string) => state.conversations.find((item) => item.id === id);

  api.on("GET", "/api/v1/me/steward/settings", () => denied() ?? (state.settings === null ? sampleResponse("steward/settings-empty") : jsonResponse(200, state.settings)));
  const current = () => state.settings ?? (sampleBody("steward/settings-empty") as Settings);
  // 部分更新，按后端 mergeSettings（steward/httpapi/settings.go，合同 4.2）的语义：
  // 省略的键保留，overrides 给出时整体替换；key 为 null 清除、为字符串时保存（只留末四位）；
  // 规则 1：主机或端口变了、没提交 key、库里有密钥时清除密钥，没提交 protocol 时协议置空；
  // 规则 2：地址、协议、生效协议或密钥变了时清除检测结论，没提交 overrides 时置为 {}；
  // 规则 3：否则模型或生效模型变了时，检测结论的工具能力改为 unconfirmed 并去掉 tools 步骤的失败，去掉 overrides.tool_support（提交了 model 时也去掉 overrides.model）。
  api.on("PUT", "/api/v1/me/steward/settings", (request) => {
    const refused = denied();
    if (refused !== null) return refused;
    const patch = request.body as Record<string, unknown>;
    const before = current() as Record<string, unknown> & { overrides: Record<string, unknown> };
    const next: Record<string, unknown> & { overrides: Record<string, unknown> } = { ...before, updated_at: "2026-11-16T00:00:01Z" };
    for (const field of ["endpoint", "protocol", "model", "display_name", "overrides"]) if (patch[field] !== undefined) next[field] = patch[field];
    let keyChanged = false;
    if (patch["key"] === null) {
      keyChanged = before["key_saved"] === true;
      Object.assign(next, { key_saved: false, key_last_four: "", key_needs_reentry: false });
    } else if (typeof patch["key"] === "string") {
      keyChanged = true;
      Object.assign(next, { key_saved: true, key_last_four: patch["key"].slice(-4), key_needs_reentry: false });
    }
    if (authorityOf(String(before["endpoint"])) !== authorityOf(String(next["endpoint"])) && !("key" in patch) && before["key_saved"] === true) {
      keyChanged = true;
      Object.assign(next, { key_saved: false, key_last_four: "", key_needs_reentry: false });
      if (patch["protocol"] === undefined) next["protocol"] = "";
    }
    const effectiveProtocol = (settings: typeof before) => settings.overrides["protocol"] ?? settings["protocol"];
    const effectiveModel = (settings: typeof before) => settings.overrides["model"] ?? settings["model"];
    if (before["endpoint"] !== next["endpoint"] || before["protocol"] !== next["protocol"] || effectiveProtocol(before) !== effectiveProtocol(next) || keyChanged) {
      Object.assign(next, { probe_result: null, probed_at: null });
      if (patch["overrides"] === undefined) next.overrides = {};
    } else if (before["model"] !== next["model"] || effectiveModel(before) !== effectiveModel(next)) {
      const probe = before["probe_result"] as { tool_support: string; failures?: { step: string }[] } | null;
      if (probe !== null) {
        const failures = (probe.failures ?? []).filter((failure) => failure.step !== "tools");
        next["probe_result"] = { ...probe, tool_support: "unconfirmed", ...(failures.length === 0 ? { failures: undefined } : { failures }) };
      }
      const { tool_support: _tool, ...rest } = next.overrides;
      const { model: _model, ...withoutModel } = rest;
      next.overrides = patch["model"] !== undefined ? withoutModel : rest;
    }
    state.settings = JSON.parse(JSON.stringify(next)) as Settings;
    return jsonResponse(200, state.settings);
  });
  api.on("POST", "/api/v1/me/steward/settings/probe", (request) => {
    const refused = denied();
    if (refused !== null) return refused;
    if (state.probeResponse !== null) return state.probeResponse;
    const input = request.body as { endpoint?: string; key?: string | null; model?: string };
    const keyed = typeof input.key === "string" ? { key_saved: true, key_last_four: input.key.slice(-4), key_needs_reentry: false } : {};
    const supported = sampleBody("steward/probe-supported") as Record<string, unknown>;
    state.settings = settingsOf({
      ...current(),
      ...keyed,
      endpoint: input.endpoint ?? current()["endpoint"],
      model: input.model ?? current()["model"],
      protocol: "openai",
      overrides: {},
      probe_result: supported["probe_result"],
      probed_at: supported["probed_at"],
    });
    return jsonResponse(200, state.settings);
  });
  api.on("GET", "/api/v1/admin/steward/settings", () => jsonResponse(200, state.instance));
  api.on("PUT", "/api/v1/admin/steward/settings", (request) => {
    state.instance = { members_enabled: (request.body as { members_enabled: boolean }).members_enabled };
    return jsonResponse(200, state.instance);
  });
  api.on("GET", "/api/v1/admin/steward/egress-allowlist", () => jsonResponse(200, { items: state.allowlist }));
  api.on("POST", "/api/v1/admin/steward/egress-allowlist", (request) => {
    const { host, port } = request.body as { host: string; port: number };
    const entry = { host: host.toLowerCase(), port, added_by: "00000000-0000-4000-8000-000000000002", created_at: "2026-11-16T00:00:00Z" };
    state.allowlist = [...state.allowlist.filter((item) => item.host !== entry.host || item.port !== port), entry];
    return jsonResponse(200, entry);
  });
  api.on("DELETE", "/api/v1/admin/steward/egress-allowlist", (request) => {
    const { host, port } = request.body as { host: string; port: number };
    // 后端对不存在的放行项同样是 404 not_found。
    if (!state.allowlist.some((item) => item.host === host && item.port === port)) return sampleResponse("steward/conversation-not-found");
    state.allowlist = state.allowlist.filter((item) => item.host !== host || item.port !== port);
    return sampleResponse("steward/allowlist-deleted");
  });
  api.on("GET", "/api/v1/me/steward/conversations", () =>
    denied() ??
    jsonResponse(200, {
      items: [...state.conversations].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).map(summary),
      pagination: { page: 1, page_size: 100, total: state.conversations.length },
    }),
  );
  api.on("POST", "/api/v1/me/steward/conversations", () => {
    const refused = denied();
    if (refused !== null) return refused;
    created += 1;
    const conversation = conversationOf(`00000000-0000-4000-8000-${String(900 + created).padStart(12, "0")}`, { title: "", updated_at: new Date().toISOString(), created_at: new Date().toISOString() });
    state.conversations.push(conversation);
    return jsonResponse(201, summary(conversation));
  });
  api.on("GET", conversationPath, (request) => {
    const conversation = find(idOf(request, conversationPath));
    return denied() ?? (conversation === undefined ? sampleResponse("steward/conversation-not-found") : jsonResponse(200, conversation));
  });
  api.on("PATCH", conversationPath, (request) => {
    const conversation = find(idOf(request, conversationPath));
    if (conversation === undefined) return sampleResponse("steward/conversation-not-found");
    const patch = request.body as { title?: string; model?: string };
    if (patch.title !== undefined) conversation.title = patch.title;
    if (patch.model !== undefined) conversation.model = patch.model;
    return jsonResponse(200, { title: conversation.title, model: conversation.model });
  });
  api.on("DELETE", conversationPath, (request) => {
    const conversation = find(idOf(request, conversationPath));
    if (conversation === undefined) return sampleResponse("steward/conversation-not-found");
    if (conversation.status === "running") return sampleResponse("steward/conversation-busy");
    state.conversations = state.conversations.filter((item) => item !== conversation);
    return sampleResponse("steward/conversation-deleted");
  });
  api.on("POST", runPath, (request) => {
    const match = runPath.exec(request.path);
    const conversation = find(match?.[1] ?? "");
    if (conversation === undefined) return sampleResponse("steward/conversation-not-found");
    if (state.nextRunResponse !== null) {
      const response = state.nextRunResponse;
      state.nextRunResponse = null;
      return response;
    }
    const kind = match?.[2] === "continue" ? "continue" : "messages";
    const problem = contextProblemOf(request.body, kind);
    if (problem !== null) return jsonResponse(400, { error: { code: "invalid_request", message: problem }, request_id: "fake-request" });
    const stream = fakeSse();
    // 与浏览器一致：请求被中止时，正在读的响应体随之出错。
    request.signal?.addEventListener("abort", () => stream.fail(), { once: true });
    state.runs.push({ request, kind, context: isRecord(request.body) ? (request.body["context"] ?? null) : null, stream });
    return stream.response;
  });
  api.on("POST", cancelPath, (request) => {
    const conversation = find(idOf(request, cancelPath));
    if (conversation === undefined) return sampleResponse("steward/conversation-not-found");
    const run = state.runs.at(-1);
    if (run === undefined) return sampleResponse("steward/cancel-idle");
    run.stream.send("done", { type: "done", end: { reason: "cancelled" } });
    run.stream.close();
    return jsonResponse(200, { cancelled: true });
  });
  return state;
}

/** 合成的已落库消息。 */
export function storedMessage(sequence: number, role: "user" | "assistant" | "tool", content: unknown[], toolCalls: unknown[] = []) {
  return {
    id: `00000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`,
    sequence,
    role,
    content,
    input_tokens: null,
    output_tokens: null,
    created_at: "2026-11-16T00:00:00Z",
    tool_calls: toolCalls,
  };
}
