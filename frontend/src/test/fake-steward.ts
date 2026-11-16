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
  mode: "assistant" | "steward" | "operator";
  model: string;
  status: "idle" | "running";
  created_at: string;
  updated_at: string;
  messages: unknown[];
};

export function conversationOf(id: string, overrides: Partial<FakeConversation> = {}): FakeConversation {
  return { id, title: "合成对话", mode: "assistant", model: "synthetic-model", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z", messages: [], ...overrides };
}

/** 方案项（C6）：形状与运行事件 plan、会话详情的 pending_plan 一致。 */
export type FakePlanItem = {
  id: string;
  call_id: string;
  tool: string;
  target: string;
  changes: { field: string; before: unknown; after: unknown }[];
  revertible: boolean;
  basis?: { source: "local" | "external" | "mixed"; note: string } | null;
};

/** 审查（C7）：形状与运行事件 review_finished、会话详情里方案的 review 一致。 */
export type FakeReviewItem = {
  item_id: string;
  decision: "approved" | "rejected" | "needs_human_confirmation";
  risk: "low" | "medium" | "high";
  reasons: string[];
  source: "model" | "policy";
};

export type FakeReview = {
  review_id: string;
  plan_id: string;
  model: string;
  status: "completed" | "unavailable";
  policy_version: number;
  expires_at: string;
  items: FakeReviewItem[];
};

/** 合成的审查：默认 15 分钟后过期；不可用时没有项。 */
export function reviewOf(planId: string, items: FakeReviewItem[], overrides: Partial<FakeReview> = {}): FakeReview {
  return {
    review_id: `00000000-0000-4000-8000-${planId.slice(-4).padStart(12, "e")}`,
    plan_id: planId,
    model: "synthetic-review-model",
    status: "completed",
    policy_version: 1,
    expires_at: new Date(Date.now() + 15 * 60_000).toISOString(),
    items,
    ...overrides,
  };
}

export type FakePlan = {
  plan_id: string;
  conversation_id: string;
  status: "pending" | "applied" | "declined" | "expired" | "superseded";
  digest: string;
  expires_at: string;
  items: FakePlanItem[];
  /** 确认时接受的项；会话详情的 plans 里据此写每项的 decision。 */
  accepted?: string[];
  /** 最近一次审查（C7）。 */
  review?: FakeReview | null;
  /** 审查已经处理的项（C7）：批准并执行的是 accepted（带 operation_id），被拒绝的是 rejected_by_review；这些项不能再确认。 */
  reviewed?: Record<string, { decision: "accepted" | "rejected_by_review"; operation_id?: string }>;
};

/** 合成的方案：默认 24 小时后过期。 */
export function planOf(planId: string, conversationId: string, items: FakePlanItem[], overrides: Partial<FakePlan> = {}): FakePlan {
  return {
    plan_id: planId,
    conversation_id: conversationId,
    status: "pending",
    digest: `digest-${planId.slice(-4)}`,
    expires_at: new Date(Date.now() + 24 * 3600_000).toISOString(),
    items,
    ...overrides,
  };
}

/** 落库的消息里这次调用的记录（助手消息的 tool_call 块与 tool_calls 按下标对上）。 */
function storedCallOf(messages: readonly unknown[], callId: string): { status?: unknown; output?: unknown } | undefined {
  for (const message of messages as { content?: { type: string; tool_call?: { id: string } }[]; tool_calls?: { status?: unknown; output?: unknown }[] }[]) {
    const calls = (message.content ?? []).filter((block) => block.type === "tool_call");
    const index = calls.findIndex((block) => block.tool_call?.id === callId);
    if (index >= 0) return message.tool_calls?.[index];
  }
  return undefined;
}

/**
 * 方案在运行事件与会话详情里的样子：带状态，已处理的方案每项带取舍；会话详情里，调用已有结果（成功或失败）的项带 result，
 * 与后端一样把结果写在方案项上。
 */
export function planPayload(plan: FakePlan, messages: readonly unknown[] = []) {
  const decided = plan.status === "applied" || plan.status === "declined";
  const items = plan.items.map((item) => {
    const stored = storedCallOf(messages, item.call_id);
    const finished = stored?.status === "succeeded" || stored?.status === "failed";
    const reviewed = plan.reviewed?.[item.id];
    const decision = reviewed?.decision ?? (decided ? (plan.accepted?.includes(item.id) === true ? "accepted" : "declined") : "pending");
    return {
      ...item,
      decision,
      ...(reviewed?.operation_id === undefined ? {} : { operation_id: reviewed.operation_id }),
      ...(finished ? { result: stored?.output ?? null } : {}),
    };
  });
  return { plan_id: plan.plan_id, digest: plan.digest, expires_at: plan.expires_at, status: plan.status, items, review: plan.review ?? null };
}

/** 未处理方案里还等用户决定的项数（会话列表的 pending_item_count）。 */
function pendingItemCount(plan: FakePlan | null): number {
  return plan === null ? 0 : plan.items.filter((item) => plan.reviewed?.[item.id] === undefined).length;
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
  /** 方案（C6）：会话详情带上其中 pending 且没过期的那份；发新消息时作废，确认接口按设计的校验顺序检查。 */
  plans: FakePlan[];
  /** 确认接口收到的请求与对应的流。 */
  decisions: { request: RecordedRequest; planId: string; body: unknown; stream: FakeSse }[];
  /** 重新审查接口（C7）收到的请求与对应的流；nextReviewResponse 预置下一次开流前的拒绝。 */
  reviews: { request: RecordedRequest; planId: string; stream: FakeSse }[];
  nextReviewResponse: Response | null;
  /** 撤销接口收到的操作 ID 与幂等键；nextRevertResponse 预置下一次的响应。 */
  reverts: { operationId: string; key: string | null }[];
  nextRevertResponse: Response | null;
  /** 管理员：成员开关与放行名单。 */
  instance: { members_enabled: boolean };
  allowlist: { host: string; port: number; added_by: string | null; created_at: string }[];
};

const conversationPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)$/;
const runPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/(messages|continue)$/;
const decisionPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/plans\/([^/]+)\/decision$/;
const reviewPath = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/plans\/([^/]+)\/review$/;
const revertPath = /^\/api\/v1\/operations\/([^/]+)\/revert$/;
const selectableModes = ["assistant", "steward", "operator"];
const failureOf = (status: number, code: string, message: string) => jsonResponse(status, { error: { code, message }, request_id: "fake-request" });
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

/**
 * 管家接口的替身：设置、会话的增删改查（含模式）、运行流（可控，记下请求体里的页面上下文）与取消、
 * 方案的确认接口与作废（C6）、方案项的撤销、审查结果与重新审查（C7）。形状按 C2、C5、C6、C7 的接口合同与样本。
 */
export function installStewardApi(api: FakeApi, initial: Partial<StewardFake> = {}): StewardFake {
  const state: StewardFake = {
    settings: settingsOf(),
    disabled: false,
    conversations: [],
    runs: [],
    nextRunResponse: null,
    probeResponse: null,
    plans: [],
    decisions: [],
    reviews: [],
    nextReviewResponse: null,
    reverts: [],
    nextRevertResponse: null,
    instance: { members_enabled: true },
    allowlist: [],
    ...initial,
  };
  const denied = () => (state.disabled ? sampleResponse("steward/disabled") : null);
  const summary = ({ messages: _messages, ...rest }: FakeConversation) => rest;
  const listItem = (conversation: FakeConversation) => ({ ...summary(conversation), pending_item_count: pendingItemCount(pendingPlanOf(conversation.id)) });
  const find = (id: string) => state.conversations.find((item) => item.id === id);
  /** 会话的未处理方案；读到时已过期的标为 expired（后端在读取与确认时判定过期）。 */
  const pendingPlanOf = (conversationId: string) => {
    const plan = state.plans.find((item) => item.conversation_id === conversationId && item.status === "pending");
    if (plan === undefined) return null;
    if (Date.parse(plan.expires_at) <= Date.now()) {
      plan.status = "expired";
      return null;
    }
    return plan;
  };
  const detailOf = (conversation: FakeConversation) => {
    const plan = pendingPlanOf(conversation.id);
    const plans = state.plans.filter((item) => item.conversation_id === conversation.id).map((item) => planPayload(item, conversation.messages));
    return { ...conversation, pending_plan: plan === null ? null : planPayload(plan), plans };
  };

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
    for (const field of ["endpoint", "protocol", "model", "review_model", "display_name", "overrides"]) if (patch[field] !== undefined) next[field] = patch[field];
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
      items: [...state.conversations].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).map(listItem),
      pagination: { page: 1, page_size: 100, total: state.conversations.length },
    }),
  );
  api.on("POST", "/api/v1/me/steward/conversations", (request) => {
    const refused = denied();
    if (refused !== null) return refused;
    // 新建只认 mode（可省略，默认 assistant）：assistant、steward（C7）、operator。
    const body = isRecord(request.body) ? request.body : {};
    const mode = body["mode"] ?? "assistant";
    if (Object.keys(body).some((key) => key !== "mode") || !selectableModes.includes(String(mode))) return failureOf(400, "invalid_request", "mode 不合格");
    created += 1;
    const conversation = conversationOf(`00000000-0000-4000-8000-${String(900 + created).padStart(12, "0")}`, {
      title: "",
      mode: mode as FakeConversation["mode"],
      updated_at: new Date().toISOString(),
      created_at: new Date().toISOString(),
    });
    state.conversations.push(conversation);
    return jsonResponse(201, summary(conversation));
  });
  api.on("GET", conversationPath, (request) => {
    const conversation = find(idOf(request, conversationPath));
    return denied() ?? (conversation === undefined ? sampleResponse("steward/conversation-not-found") : jsonResponse(200, detailOf(conversation)));
  });
  api.on("PATCH", conversationPath, (request) => {
    const conversation = find(idOf(request, conversationPath));
    if (conversation === undefined) return sampleResponse("steward/conversation-not-found");
    const patch = request.body as { title?: string; model?: string; mode?: string };
    if (patch.mode !== undefined) {
      if (!selectableModes.includes(patch.mode)) return failureOf(400, "invalid_request", "mode 不合格");
      // 改模式要求会话空闲且没有未处理的方案。
      if (conversation.status === "running" || pendingPlanOf(conversation.id) !== null) return sampleResponse("steward/conversation-busy");
      conversation.mode = patch.mode as FakeConversation["mode"];
    }
    if (patch.title !== undefined) conversation.title = patch.title;
    if (patch.model !== undefined) conversation.model = patch.model;
    return jsonResponse(200, { title: conversation.title, model: conversation.model, mode: conversation.mode });
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
    // 有未处理的方案时发新消息：方案作废（各项按“未执行”回给模型），再追加用户消息。
    if (kind === "messages") {
      const pending = pendingPlanOf(conversation.id);
      if (pending !== null) pending.status = "superseded";
    }
    const stream = fakeSse();
    // 与浏览器一致：请求被中止时，正在读的响应体随之出错。
    request.signal?.addEventListener("abort", () => stream.fail(), { once: true });
    state.runs.push({ request, kind, context: isRecord(request.body) ? (request.body["context"] ?? null) : null, stream });
    return stream.response;
  });
  // 确认接口（C6）：校验顺序按设计——会话归属、方案属于该会话且 pending、未过期、摘要一致、会话空闲；任何一项不满足都不执行。
  // 会话或方案不属于本人、不在此会话或已处理都是 404 plan_not_found；之后才检查选择的项（400 invalid_input）。
  api.on("POST", decisionPath, (request) => {
    const match = decisionPath.exec(request.path);
    const conversation = find(match?.[1] ?? "");
    const planId = match?.[2] ?? "";
    const plan = state.plans.find((item) => item.plan_id === planId && item.conversation_id === conversation?.id);
    if (conversation === undefined || plan === undefined || plan.status !== "pending") return failureOf(404, "plan_not_found", "方案不存在或已处理");
    if (Date.parse(plan.expires_at) <= Date.now()) {
      plan.status = "expired";
      return failureOf(409, "plan_expired", "方案已过期");
    }
    const body = request.body;
    if (!isRecord(body) || Object.keys(body).some((key) => !["digest", "accepted_item_ids", "auto_approve_tools"].includes(key))) return failureOf(400, "invalid_request", "请求体不合格");
    if (body["digest"] !== plan.digest) return failureOf(409, "plan_digest_mismatch", "方案内容已变化");
    if (conversation.status === "running") return sampleResponse("steward/conversation-busy");
    // 审查已经处理（批准执行或拒绝）的项不能再确认（C7）。
    const itemIds = plan.items.filter((item) => plan.reviewed?.[item.id] === undefined).map((item) => item.id);
    const tools = plan.items.map((item) => item.tool);
    const accepted = body["accepted_item_ids"];
    const autoApprove = body["auto_approve_tools"];
    const distinct = (values: unknown[]) => new Set(values).size === values.length;
    // 免确认的工具：属于方案、方案里这个工具的项全部可以撤销，并且至少有一项被接受。
    const approvable = (tool: unknown) =>
      tools.includes(String(tool)) &&
      plan.items.every((item) => item.tool !== tool || item.revertible) &&
      Array.isArray(accepted) &&
      plan.items.some((item) => item.tool === tool && accepted.includes(item.id));
    // steward 模式的确认不接受免确认工具（C7）：带非空值是 400 invalid_input。
    if (conversation.mode === "steward" && Array.isArray(autoApprove) && autoApprove.length > 0) return failureOf(400, "invalid_input", "steward 模式不接受免确认工具");
    if (
      !Array.isArray(accepted) ||
      !accepted.every((id) => itemIds.includes(String(id))) ||
      !distinct(accepted) ||
      !Array.isArray(autoApprove) ||
      !autoApprove.every(approvable) ||
      !distinct(autoApprove)
    ) {
      return failureOf(400, "invalid_input", "选择的项或工具不合格");
    }
    plan.status = accepted.length === 0 ? "declined" : "applied";
    plan.accepted = accepted.map(String);
    const stream = fakeSse();
    request.signal?.addEventListener("abort", () => stream.fail(), { once: true });
    state.decisions.push({ request, planId, body, stream });
    return stream.response;
  });
  // 重新审查（C7）：校验顺序与确认接口相同——会话与方案归属且 pending、未过期，再是会话模式（不是 steward 时 409 review_not_applicable）、
  // 会话空闲；通过后记下 reviews[i].stream，
  // 审查的开始、结论与批准项的执行由测试推送。
  api.on("POST", reviewPath, (request) => {
    const match = reviewPath.exec(request.path);
    const conversation = find(match?.[1] ?? "");
    const planId = match?.[2] ?? "";
    const plan = state.plans.find((item) => item.plan_id === planId && item.conversation_id === conversation?.id);
    if (conversation === undefined || plan === undefined || plan.status !== "pending") return failureOf(404, "plan_not_found", "方案不存在或已处理");
    if (Date.parse(plan.expires_at) <= Date.now()) {
      plan.status = "expired";
      return failureOf(409, "plan_expired", "方案已过期");
    }
    if (conversation.mode !== "steward") return failureOf(409, "review_not_applicable", "当前模式不使用审查");
    if (conversation.status === "running") return sampleResponse("steward/conversation-busy");
    if (state.nextReviewResponse !== null) {
      const response = state.nextReviewResponse;
      state.nextReviewResponse = null;
      return response;
    }
    if (request.body !== undefined && request.body !== null) return failureOf(400, "invalid_request", "重新审查没有请求体");
    const stream = fakeSse();
    request.signal?.addEventListener("abort", () => stream.fail(), { once: true });
    state.reviews.push({ request, planId, stream });
    return stream.response;
  });
  // 受治理操作的撤销接口（方案卡片里的“撤销”）：需要幂等键。
  api.on("POST", revertPath, (request) => {
    const operationId = revertPath.exec(request.path)?.[1] ?? "";
    const key = request.headers.get("Idempotency-Key");
    state.reverts.push({ operationId, key });
    if (state.nextRevertResponse !== null) {
      const response = state.nextRevertResponse;
      state.nextRevertResponse = null;
      return response;
    }
    return jsonResponse(200, { operation_id: "00000000-0000-4000-8000-00000000f0f0", reverts_operation_id: operationId, target_type: "release", target_id: "00000000-0000-4000-8000-00000000e0e0", revision: 3 });
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
