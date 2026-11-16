import type { Page, Route } from "@playwright/test";
import { loadApiSample, sampleBody } from "../../src/test/api-samples";

// 管家接口的受控实现（steward.spec.ts、steward-plan.spec.ts、steward-review.spec.ts）：设置、会话的增删改查（含模式）、发消息（一次性返回整段 SSE）、取消、
// 修改方案的确认（C6，同样一次性返回整段 SSE）与方案项的撤销、审查结果与重新审查（C7，一次性返回整段 SSE）、令牌与管理员接口。
// 用 page.route 注册，优先于 api-mock 在浏览器上下文上的拦截；形状取自后端样本与 C2、C6 的接口合同。不调用任何真实模型。

type Mode = "assistant" | "steward" | "operator";
type PlanItem = { id: string; call_id: string; tool: string; target: string; changes: { field: string; before: unknown; after: unknown }[]; revertible: boolean; basis: { source: string; note: string } | null };
type ReviewItem = { item_id: string; decision: "approved" | "rejected" | "needs_human_confirmation"; risk: "low" | "medium" | "high"; reasons: string[]; source: "model" | "policy" };
type Review = { review_id: string; plan_id: string; model: string; status: "completed" | "unavailable"; policy_version: number; expires_at: string; items: ReviewItem[] };
type Plan = {
  plan_id: string;
  digest: string;
  expires_at: string;
  items: PlanItem[];
  /** 最近一次审查（C7）。 */
  review?: Review | null;
  /** 审查已经处理的项（C7）：批准并执行的是 accepted，被拒绝的是 rejected_by_review；这些项不再等用户确认。 */
  reviewed?: Record<string, "accepted" | "rejected_by_review">;
};
type Conversation = {
  id: string;
  title: string;
  mode: Mode;
  model: string;
  status: "idle";
  created_at: string;
  updated_at: string;
  messages: unknown[];
  /** 未处理的方案；确认或作废后清掉，记进 history。 */
  plan: Plan | null;
  /** 已处理的方案（会话详情的 plans）：状态、接受的项，以及已执行项的操作记录。 */
  history: { plan: Plan; status: "applied" | "declined" | "superseded"; accepted: string[] }[];
};

export type StewardMock = { conversations: Conversation[]; requests: { method: string; path: string; body: unknown }[] };

const BASE = "/api/v1/me/steward";
const json = (route: Route, status: number, body: unknown) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
const sample = (route: Route, name: string) => {
  const { status, body } = loadApiSample(name);
  return json(route, status, body);
};

/** 回答里的表格（Markdown），冒烟断言它按表格渲染。 */
export const ANSWER_TABLE = ["一共 **2 版**：", "", "| 版本 | 年份 |", "| --- | --- |", "| 拜罗伊特 | 1951 |", "| 柏林 | 1942 |"].join("\n");

let counter = 0;
const idOf = (index: number) => `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`;

function message(conversation: Conversation, role: "user" | "assistant" | "tool", content: unknown[], toolCalls: unknown[] = []) {
  counter += 1;
  return {
    id: idOf(5000 + counter),
    sequence: conversation.messages.length + 1,
    role,
    content,
    input_tokens: null,
    output_tokens: null,
    created_at: "2026-11-16T00:00:00Z",
    tool_calls: toolCalls,
  };
}

/** 一次完整回答落库后的样子：用户问题、一次“浏览专辑”、工具结果、带表格的回答。 */
function persistRun(conversation: Conversation, question: string) {
  conversation.messages.push(message(conversation, "user", [{ type: "text", text: question }]));
  conversation.messages.push(
    message(conversation, "assistant", [{ type: "text", text: "我查一下。" }, { type: "tool_call", tool_call: { id: "call-1", name: "list_albums", arguments: { q: "贝九" } } }], [
      { id: idOf(6000 + counter), name: "list_albums", input: { q: "贝九" }, output: { items: [], pagination: { page: 1, page_size: 20, total: 2 } }, truncated: false, untrusted: true, status: "succeeded", error_code: null, duration_ms: 1_200 },
    ]),
  );
  conversation.messages.push(message(conversation, "tool", [{ type: "tool_result", tool_result: { call_id: "call-1", content: "{}", is_error: false } }]));
  conversation.messages.push(message(conversation, "assistant", [{ type: "text", text: ANSWER_TABLE }]));
  if (conversation.title === "") conversation.title = question.slice(0, 32);
  conversation.updated_at = new Date().toISOString();
}

/** 与 persistRun 对应的事件流：一次性返回整段。 */
function runEvents(): string {
  const events: [string, unknown][] = [
    ["text", { type: "text", step: 1, text: "我查一下。" }],
    ["usage", { type: "usage", step: 1, usage: { input_tokens: 10, output_tokens: 5 } }],
    ["tool_started", { type: "tool_started", step: 1, tool_started: { call_id: "call-1", name: "list_albums", input: { q: "贝九" } } }],
    ["tool_finished", { type: "tool_finished", step: 1, tool_finished: { call_id: "call-1", status: "succeeded", output: { items: [], pagination: { page: 1, page_size: 20, total: 2 } }, truncated: false, untrusted: true } }],
    ["text", { type: "text", step: 2, text: ANSWER_TABLE }],
    ["done", { type: "done", step: 2, end: { reason: "completed" } }],
  ];
  return `: ping\n\n${events.map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`).join("")}`;
}

/** 预置一个已经有一次回答的会话。 */
export function seedConversation(mock: StewardMock, title: string, question: string, updatedAt = "2026-11-10T00:00:00Z"): Conversation {
  counter += 1;
  const conversation: Conversation = { id: idOf(4000 + counter), title, mode: "assistant", model: "synthetic-model", status: "idle", created_at: updatedAt, updated_at: updatedAt, messages: [], plan: null, history: [] };
  persistRun(conversation, question);
  conversation.updated_at = updatedAt;
  mock.conversations.push(conversation);
  return conversation;
}

/** 方案里的已执行项留下的操作记录 ID（撤销接口按它响应）。 */
export const PLAN_OPERATION_ID = idOf(7001);

const awaitingRecord = (index: number, name: string, overrides: Record<string, unknown> = {}) => ({
  id: idOf(7100 + index),
  name,
  input: {},
  output: null,
  truncated: false,
  untrusted: true,
  status: "awaiting_confirmation",
  error_code: null,
  duration_ms: null,
  ...overrides,
});

/**
 * 预置一个停在修改方案上的会话（刷新后的样子）：一项修正专辑信息（标题与厂牌，可撤销，依据来自外部资料），
 * 一项合并艺人（不可撤销）。确认后：接受的项执行成功（留下 PLAN_OPERATION_ID），没选的项是 declined_by_user，再给一句回答。
 */
export function seedPlanConversation(mock: StewardMock): Conversation {
  counter += 1;
  const at = "2026-11-15T00:00:00Z";
  const conversation: Conversation = { id: idOf(4000 + counter), title: "整理贝九 1951", mode: "assistant", model: "synthetic-model", status: "idle", created_at: at, updated_at: at, messages: [], plan: null, history: [] };
  conversation.messages.push(message(conversation, "user", [{ type: "text", text: "把这张专辑整理好" }]));
  conversation.messages.push(
    message(
      conversation,
      "assistant",
      [
        { type: "text", text: "标签里缺厂牌，指挥的名字少了变音符号。建议改下面两处。" },
        { type: "tool_call", tool_call: { id: "call-2", name: "correct_release", arguments: { release_id: idOf(7200) } } },
        { type: "tool_call", tool_call: { id: "call-3", name: "merge_artists", arguments: { artist_id: idOf(7201) } } },
      ],
      [awaitingRecord(1, "correct_release"), awaitingRecord(2, "merge_artists")],
    ),
  );
  conversation.plan = {
    plan_id: idOf(7300),
    digest: "synthetic-digest",
    expires_at: new Date(Date.now() + 24 * 3600_000).toISOString(),
    items: [
      {
        id: idOf(7301),
        call_id: "call-2",
        tool: "correct_release",
        target: "Beethoven Symphony No.9 (Bayreuth 1951)",
        changes: [
          { field: "title", before: "Beethoven Symphony No.9 (Bayreuth 1951)", after: "贝多芬：第九交响曲“合唱”" },
          { field: "label", before: null, after: "EMI" },
        ],
        revertible: true,
        basis: { source: "external", note: "来自 MusicBrainz，本地没有可核对的证据" },
      },
      {
        id: idOf(7302),
        call_id: "call-3",
        tool: "merge_artists",
        target: "威廉·富特文格勒",
        changes: [{ field: "artist", before: "Furtwangler", after: "威廉·富特文格勒" }],
        revertible: false,
        basis: { source: "local", note: "曲库里已有这位艺人" },
      },
    ],
  };
  mock.conversations.push(conversation);
  return conversation;
}

/** 确认之后：落库执行结果与回答，返回对应的整段事件流。 */
function applyDecision(conversation: Conversation, accepted: readonly string[]): string {
  const plan = conversation.plan;
  if (plan === null) return "";
  conversation.plan = null;
  conversation.history.push({ plan, status: accepted.length === 0 ? "declined" : "applied", accepted: [...accepted] });
  const assistant = conversation.messages[1] as { tool_calls: unknown[] };
  const events: [string, unknown][] = [];
  assistant.tool_calls = plan.items.map((item, index) => {
    // 审查已经处理的项（C7）不在这次确认里，保持原来的调用记录。
    if (plan.reviewed?.[item.id] !== undefined) return assistant.tool_calls[index];
    const ok = accepted.includes(item.id);
    const output = ok ? { operation_id: PLAN_OPERATION_ID } : { code: "declined_by_user" };
    events.push([
      "tool_finished",
      { type: "tool_finished", step: 1, tool_finished: { call_id: item.call_id, status: ok ? "succeeded" : "failed", output, truncated: false, untrusted: false, ...(ok ? {} : { error_code: "declined_by_user" }) } },
    ]);
    return awaitingRecord(index + 1, item.tool, ok ? { status: "succeeded", output, duration_ms: 200 } : { status: "failed", output, error_code: "declined_by_user" });
  });
  const answer = accepted.length === 0 ? "好的，这次不改。" : "标题和厂牌改好了，艺人没有动。";
  conversation.messages.push(
    message(
      conversation,
      "tool",
      plan.items.filter((item) => plan.reviewed?.[item.id] === undefined).map((item) => ({ type: "tool_result", tool_result: { call_id: item.call_id, content: "{}", is_error: !accepted.includes(item.id) } })),
    ),
  );
  conversation.messages.push(message(conversation, "assistant", [{ type: "text", text: answer }]));
  events.push(["text", { type: "text", step: 2, text: answer }], ["done", { type: "done", step: 2, end: { reason: "completed" } }]);
  return `: ping\n\n${events.map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`).join("")}`;
}

// ---- 审查（C7）----

/** 审查模型的名字（与对话模型 synthetic-model 不同）。 */
export const REVIEW_MODEL = "review-model";

const sse = (events: [string, unknown][]) => `: ping\n\n${events.map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`).join("")}`;

/** Steward 模式下送审的三项：修正专辑信息（可撤销）、合并艺人（不可撤销，策略转人工）、另一张专辑的厂牌（可撤销）。 */
function reviewPlanItems(): PlanItem[] {
  return [
    {
      id: idOf(7401),
      call_id: "call-2",
      tool: "correct_release",
      target: "Beethoven Symphony No.9 (Bayreuth 1951)",
      changes: [{ field: "year", before: 1955, after: 1951 }],
      revertible: true,
      basis: { source: "local", note: "同目录的 CUE 文件写的是 1951" },
    },
    {
      id: idOf(7402),
      call_id: "call-3",
      tool: "merge_artists",
      target: "格伦·古尔德",
      changes: [{ field: "artist", before: "Glenn Gould", after: "格伦·古尔德" }],
      revertible: false,
      basis: { source: "local", note: "两个名字下的专辑曲目一致" },
    },
    {
      id: idOf(7403),
      call_id: "call-4",
      tool: "correct_release",
      target: "Karl Richter 1958",
      changes: [{ field: "label", before: null, after: "Archiv" }],
      revertible: true,
      basis: { source: "external", note: "来自 MusicBrainz" },
    },
  ];
}

const reviewAt = () => new Date(Date.now() + 15 * 60_000).toISOString();

/** 预置 Steward 模式下一个送审的会话（刷新后的样子）：用户的话与一条带三个调用的助手消息，调用记录由 calls 给出。 */
function seedSteward(mock: StewardMock, title: string, calls: Record<string, unknown>[]): Conversation {
  counter += 1;
  const at = "2026-11-16T00:00:00Z";
  const conversation: Conversation = { id: idOf(4000 + counter), title, mode: "steward", model: "synthetic-model", status: "idle", created_at: at, updated_at: at, messages: [], plan: null, history: [] };
  const items = reviewPlanItems();
  conversation.messages.push(message(conversation, "user", [{ type: "text", text: "把待关注的问题过一遍，证据充分的直接处理" }]));
  conversation.messages.push(
    message(
      conversation,
      "assistant",
      [{ type: "text", text: "我把能处理的 3 项交给了审查，结果在下面。" }, ...items.map((item) => ({ type: "tool_call", tool_call: { id: item.call_id, name: item.tool, arguments: {} } }))],
      calls,
    ),
  );
  conversation.plan = { plan_id: idOf(7400), digest: "synthetic-review-digest", expires_at: new Date(Date.now() + 24 * 3600_000).toISOString(), items };
  mock.conversations.push(conversation);
  return conversation;
}

/**
 * 审查完成：修正专辑信息批准并执行了（留下 PLAN_OPERATION_ID），合并艺人按规则转给用户确认，另一张专辑的厂牌被审查拒绝。
 * 确认合并后它执行成功。
 */
export function seedReviewedConversation(mock: StewardMock): Conversation {
  const conversation = seedSteward(mock, "清理待关注的问题", [
    awaitingRecord(1, "correct_release", { status: "succeeded", output: { operation_id: PLAN_OPERATION_ID }, duration_ms: 300 }),
    awaitingRecord(2, "merge_artists"),
    awaitingRecord(3, "correct_release", { status: "failed", output: { code: "rejected_by_review" }, error_code: "rejected_by_review" }),
  ]);
  const plan = conversation.plan;
  if (plan === null) return conversation;
  const [title, merge, reject] = plan.items;
  plan.review = {
    review_id: idOf(7500),
    plan_id: plan.plan_id,
    model: REVIEW_MODEL,
    status: "completed",
    policy_version: 1,
    expires_at: reviewAt(),
    items: [
      { item_id: title?.id ?? "", decision: "approved", risk: "low", reasons: ["目标明确，有 CUE 文件佐证，可以单独撤销"], source: "model" },
      { item_id: merge?.id ?? "", decision: "needs_human_confirmation", risk: "high", reasons: ["不可撤销的修改"], source: "policy" },
      { item_id: reject?.id ?? "", decision: "rejected", risk: "medium", reasons: ["曲库里有两位同名的人，证据不足以判断是哪一位"], source: "model" },
    ],
  };
  plan.reviewed = { [title?.id ?? ""]: "accepted", [reject?.id ?? ""]: "rejected_by_review" };
  return conversation;
}

/** 审查不可用：三项都没有执行，等用户重新审查或自己确认。 */
export function seedUnavailableReviewConversation(mock: StewardMock): Conversation {
  const conversation = seedSteward(mock, "审查没能完成的方案", [awaitingRecord(1, "correct_release"), awaitingRecord(2, "merge_artists"), awaitingRecord(3, "correct_release")]);
  const plan = conversation.plan;
  if (plan !== null) plan.review = { review_id: idOf(7501), plan_id: plan.plan_id, model: REVIEW_MODEL, status: "unavailable", policy_version: 1, expires_at: reviewAt(), items: [] };
  return conversation;
}

/**
 * 重新审查：可撤销的项批准并执行，不可撤销的按规则转给用户确认；落库后返回整段事件流（审查开始与结论、批准项的执行、只含待确认项的方案）。
 */
function applyRereview(conversation: Conversation): string {
  const plan = conversation.plan;
  if (plan === null) return "";
  const review: Review = {
    review_id: idOf(7502),
    plan_id: plan.plan_id,
    model: REVIEW_MODEL,
    status: "completed",
    policy_version: 1,
    expires_at: reviewAt(),
    items: plan.items.map((item) =>
      item.revertible
        ? { item_id: item.id, decision: "approved", risk: "low", reasons: ["可以单独撤销"], source: "model" }
        : { item_id: item.id, decision: "needs_human_confirmation", risk: "high", reasons: ["不可撤销的修改"], source: "policy" },
    ),
  };
  plan.review = review;
  plan.reviewed = Object.fromEntries(plan.items.filter((item) => item.revertible).map((item) => [item.id, "accepted" as const]));
  const assistant = conversation.messages[1] as { tool_calls: Record<string, unknown>[] };
  const events: [string, unknown][] = [
    ["review_started", { type: "review_started", plan_id: plan.plan_id, model: REVIEW_MODEL }],
    ["review_finished", { type: "review_finished", review }],
  ];
  plan.items.forEach((item, index) => {
    if (!item.revertible) return;
    const output = { operation_id: PLAN_OPERATION_ID };
    assistant.tool_calls[index] = awaitingRecord(index + 1, item.tool, { status: "succeeded", output, duration_ms: 200 });
    events.push(
      ["tool_started", { type: "tool_started", tool_started: { call_id: item.call_id, name: item.tool, input: {} } }],
      ["tool_finished", { type: "tool_finished", tool_finished: { call_id: item.call_id, status: "succeeded", output, truncated: false, untrusted: false } }],
    );
  });
  const waiting = plan.items.filter((item) => !item.revertible).map((item) => ({ ...item, decision: "pending" }));
  events.push(["plan", { type: "plan", plan: { plan_id: plan.plan_id, digest: plan.digest, expires_at: plan.expires_at, status: "pending", items: waiting } }]);
  events.push(["done", { type: "done", end: { reason: "awaiting_confirmation" } }]);
  return sse(events);
}

/** reviewModel：设置里的审查模型（C7），空字符串表示与对话同一个模型；省略时沿用样本。 */
export async function installStewardMock(page: Page, { displayName = "小蓝", reviewModel }: { displayName?: string; reviewModel?: string } = {}): Promise<StewardMock> {
  const mock: StewardMock = { conversations: [], requests: [] };
  const settings = { ...(sampleBody("steward/probe-supported") as Record<string, unknown>), display_name: displayName, ...(reviewModel === undefined ? {} : { review_model: reviewModel }) };
  const summary = ({ messages: _messages, plan: _plan, history: _history, ...rest }: Conversation) => rest;
  // 会话列表另带未处理方案里等用户确认的项数（C7）。
  const listItem = (conversation: Conversation) => ({
    ...summary(conversation),
    pending_item_count: conversation.plan === null ? 0 : conversation.plan.items.filter((item) => conversation.plan?.reviewed?.[item.id] === undefined).length,
  });
  // 与后端一致：pending_plan 是未处理的方案，plans 是全部方案（按创建顺序），已处理的每项带取舍、结果与操作记录。
  // 审查已经处理的项在两处都按审查的结果写（C7）。
  const reviewedItem = (item: PlanItem, decision: "accepted" | "rejected_by_review") =>
    decision === "accepted"
      ? { ...item, decision, result: { operation_id: PLAN_OPERATION_ID }, operation_id: PLAN_OPERATION_ID }
      : { ...item, decision, result: { code: "rejected_by_review" } };
  const detail = ({ plan, history, ...rest }: Conversation) => {
    const settled = history.map(({ plan: entry, status, accepted }) => ({
      ...entry,
      review: entry.review ?? null,
      status,
      items: entry.items.map((item) => {
        const reviewed = entry.reviewed?.[item.id];
        if (reviewed !== undefined) return reviewedItem(item, reviewed);
        const ok = accepted.includes(item.id);
        if (status === "superseded") return { ...item, decision: "pending", result: { code: "not_executed" } };
        return { ...item, decision: ok ? "accepted" : "declined", result: ok ? { operation_id: PLAN_OPERATION_ID } : { code: "declined_by_user" }, ...(ok ? { operation_id: PLAN_OPERATION_ID } : {}) };
      }),
    }));
    const pending =
      plan === null
        ? []
        : [
            {
              ...plan,
              review: plan.review ?? null,
              status: "pending",
              items: plan.items.map((item) => {
                const reviewed = plan.reviewed?.[item.id];
                return reviewed === undefined ? { ...item, decision: "pending" } : reviewedItem(item, reviewed);
              }),
            },
          ];
    return { ...rest, pending_plan: pending[0] ?? null, plans: [...settled, ...pending] };
  };

  await page.route(`**${BASE}/**`, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    const body: unknown = request.postData() === null ? undefined : request.postDataJSON();
    mock.requests.push({ method, path, body });

    if (path === `${BASE}/settings` && method === "GET") return json(route, 200, settings);
    if (path === `${BASE}/tokens` && method === "GET") return sample(route, "steward/tokens/list");
    if (path === `${BASE}/conversations` && method === "GET") {
      const items = [...mock.conversations].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).map(listItem);
      return json(route, 200, { items, pagination: { page: 1, page_size: 100, total: items.length } });
    }
    if (path === `${BASE}/conversations` && method === "POST") {
      counter += 1;
      const now = new Date().toISOString();
      const mode = ((body as { mode?: Mode } | undefined)?.mode ?? "assistant");
      const conversation: Conversation = { id: idOf(4000 + counter), title: "", mode, model: "synthetic-model", status: "idle", created_at: now, updated_at: now, messages: [], plan: null, history: [] };
      mock.conversations.push(conversation);
      return json(route, 201, summary(conversation));
    }
    const decision = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/plans\/([^/]+)\/decision$/.exec(path);
    if (decision !== null && method === "POST") {
      const conversation = mock.conversations.find((item) => item.id === decision[1]);
      if (conversation === undefined) return sample(route, "steward/conversation-not-found");
      if (conversation.plan === null || conversation.plan.plan_id !== decision[2]) return json(route, 404, { error: { code: "plan_not_found", message: "方案不存在或已处理" }, request_id: "e2e" });
      const { digest, accepted_item_ids: accepted, auto_approve_tools: autoApprove } = body as { digest: string; accepted_item_ids: string[]; auto_approve_tools: string[] };
      // steward 模式的确认不接受免确认工具（C7）。
      if (conversation.mode === "steward" && autoApprove.length > 0) return json(route, 400, { error: { code: "invalid_input", message: "steward 模式不接受免确认工具" }, request_id: "e2e" });
      if (digest !== conversation.plan.digest) return json(route, 409, { error: { code: "plan_digest_mismatch", message: "方案内容已变化" }, request_id: "e2e" });
      return route.fulfill({ status: 200, contentType: "text/event-stream", headers: { "Cache-Control": "no-store" }, body: applyDecision(conversation, accepted) });
    }
    const rereview = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)\/plans\/([^/]+)\/review$/.exec(path);
    if (rereview !== null && method === "POST") {
      const conversation = mock.conversations.find((item) => item.id === rereview[1]);
      if (conversation === undefined) return sample(route, "steward/conversation-not-found");
      if (conversation.plan === null || conversation.plan.plan_id !== rereview[2]) return json(route, 404, { error: { code: "plan_not_found", message: "方案不存在或已处理" }, request_id: "e2e" });
      if (conversation.mode !== "steward") return json(route, 409, { error: { code: "review_not_applicable", message: "当前模式不使用审查" }, request_id: "e2e" });
      return route.fulfill({ status: 200, contentType: "text/event-stream", headers: { "Cache-Control": "no-store" }, body: applyRereview(conversation) });
    }
    const match = /^\/api\/v1\/me\/steward\/conversations\/([^/]+)(?:\/(messages|continue|cancel))?$/.exec(path);
    const conversation = mock.conversations.find((item) => item.id === match?.[1]);
    if (match === null || conversation === undefined) return sample(route, "steward/conversation-not-found");
    const action = match[2];
    if (action === undefined && method === "GET") return json(route, 200, detail(conversation));
    if (action === undefined && method === "PATCH") {
      Object.assign(conversation, body as object);
      return json(route, 200, { title: conversation.title, model: conversation.model, mode: conversation.mode });
    }
    if (action === undefined && method === "DELETE") {
      mock.conversations = mock.conversations.filter((item) => item !== conversation);
      return sample(route, "steward/conversation-deleted");
    }
    if (action === "messages" && method === "POST") {
      // 有未处理的方案时发新消息：方案作废。
      if (conversation.plan !== null) conversation.history.push({ plan: conversation.plan, status: "superseded", accepted: [] });
      conversation.plan = null;
      persistRun(conversation, (body as { text: string }).text);
      return route.fulfill({ status: 200, contentType: "text/event-stream", headers: { "Cache-Control": "no-store" }, body: runEvents() });
    }
    if (action === "cancel") return sample(route, "steward/cancel-idle");
    return sample(route, "steward/continue-nothing");
  });

  // 方案项的撤销（受治理操作的撤销接口）：只认方案里已执行的那条记录。
  await page.route(`**/api/v1/operations/${PLAN_OPERATION_ID}/revert`, async (route) => {
    mock.requests.push({ method: route.request().method(), path: new URL(route.request().url()).pathname, body: undefined });
    return json(route, 200, { operation_id: idOf(7002), reverts_operation_id: PLAN_OPERATION_ID, target_type: "release", target_id: idOf(7200), revision: 3 });
  });

  await page.route("**/api/v1/admin/steward/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/settings")) return sample(route, "steward/instance-settings");
    return json(route, 200, { items: [] });
  });

  return mock;
}
