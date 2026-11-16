import { decode as d, decodeErrorEnvelope } from "../../../shared/api";

// ---- 设置与探测（合同 4.2、4.3）----

export const protocols = ["openai", "messages"] as const;
export type Protocol = (typeof protocols)[number];
export const toolSupports = ["supported", "unsupported", "unconfirmed"] as const;
export type ToolSupport = (typeof toolSupports)[number];

/** 名字最多这么多个字符（后端的上限）。 */
export const MAX_DISPLAY_NAME = 20;

/** 后端已有的 JSON 值（工具输入输出、调用参数）：原样保留，不在这里解释。 */
const json: d.Decoder<unknown> = (input) => input;

const probeFailureDecoder = d.object({
  step: d.oneOf(["endpoint", "models", "tools"] as const),
  protocol: d.optional(d.oneOf(protocols)),
  error: d.object({ kind: d.string({ min: 1 }), status_code: d.optional(d.integer()), message: d.string() }),
});

// 模型列表没有后端上限（聚合服务商可能有几百个），这里取一个宽松的界。
const probeResultDecoder = d.object({
  provider_name: d.optional(d.string()),
  protocol: d.optional(d.oneOf(protocols)),
  models: d.array(d.object({ id: d.string({ min: 1 }), display_name: d.optional(d.string()) }), { max: 5000 }),
  tool_support: d.oneOf(toolSupports),
  failures: d.optional(d.array(probeFailureDecoder, { max: 20 })),
});

const settingsDecoder = d.object({
  endpoint: d.string(),
  key_last_four: d.string({ max: 4 }),
  key_saved: d.boolean(),
  key_needs_reentry: d.boolean(),
  protocol: d.oneOf(["", ...protocols] as const),
  model: d.string(),
  /** 审查模型（C7）；空字符串表示与对话用同一个模型。 */
  review_model: d.optional(d.string()),
  display_name: d.string({ max: MAX_DISPLAY_NAME }),
  probe_result: d.nullable(probeResultDecoder),
  probed_at: d.nullable(d.timestamp()),
  overrides: d.object({
    protocol: d.optional(d.oneOf(protocols)),
    model: d.optional(d.string({ min: 1 })),
    tool_support: d.optional(d.oneOf(toolSupports)),
  }),
  updated_at: d.nullable(d.timestamp()),
});

export const decodeSettings = d.defineDecoder(settingsDecoder);
export type SettingsDTO = d.Infer<typeof settingsDecoder>;
export type ProbeResultDTO = d.Infer<typeof probeResultDecoder>;
export type ProbeFailureDTO = d.Infer<typeof probeFailureDecoder>;

/** 探测失败的响应：错误信封旁边带本次的探测报告（没有存进设置）。 */
const decodeProbeReport = d.defineDecoder(d.object({ probe_result: probeResultDecoder, probed_at: d.timestamp() }));
export function decodeProbeFailure(input: unknown) {
  return { ...decodeErrorEnvelope(input), ...decodeProbeReport(input) };
}
export type ProbeFailureResponseDTO = ReturnType<typeof decodeProbeFailure>;

// ---- 会话与消息（合同 4.4）----

/** 会话的模式：assistant 修改曲库前给用户确认（C6），steward 交给独立的审查调用、通过的自动执行（C7），operator 直接执行（C6）。 */
export const stewardModes = ["assistant", "steward", "operator"] as const;
export type StewardMode = (typeof stewardModes)[number];

const conversationShape = {
  id: d.uuid(),
  title: d.string(),
  mode: d.oneOf(stewardModes),
  /** 会话自己的模型；空字符串表示发消息时用设置里当时的模型。 */
  model: d.string(),
  status: d.oneOf(["idle", "running"] as const),
  created_at: d.timestamp(),
  updated_at: d.timestamp(),
};

// 会话列表的每一项另带未处理方案里等用户确认的项数（C7，列表标出“N 项等你确认”）；没有时省略或为 0。
const conversationDecoder = d.object(conversationShape);
const conversationListItemDecoder = d.object({ ...conversationShape, pending_item_count: d.optional(d.integer({ min: 0 })) });
export const decodeConversation = d.defineDecoder(conversationDecoder);
export type ConversationDTO = d.Infer<typeof conversationDecoder>;
export type ConversationListItemDTO = d.Infer<typeof conversationListItemDecoder>;

/** 会话列表一页最多这么多个（后端的上限）。 */
export const MAX_CONVERSATION_PAGE_SIZE = 100;

const conversationPageDecoder = d.object({
  items: d.array(conversationListItemDecoder, { max: MAX_CONVERSATION_PAGE_SIZE }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_CONVERSATION_PAGE_SIZE }),
    total: d.integer({ min: 0 }),
  }),
});
export const decodeConversationPage = d.defineDecoder(conversationPageDecoder);
export type ConversationPageDTO = d.Infer<typeof conversationPageDecoder>;

const toolCallBlockDecoder = d.object({ id: d.string(), name: d.string(), arguments: json });
const toolResultBlockDecoder = d.object({ call_id: d.string(), content: d.string({ max: d.unbounded }), is_error: d.boolean() });

export type BlockDTO =
  | { type: "text"; text: string }
  | { type: "tool_call"; tool_call: d.Infer<typeof toolCallBlockDecoder> }
  | { type: "tool_result"; tool_result: d.Infer<typeof toolResultBlockDecoder> };

const textBlockDecoder = d.object({ type: d.literal("text"), text: d.optional(d.string({ max: d.unbounded })) });
const toolCallBlockTypeDecoder = d.object({ type: d.literal("tool_call"), tool_call: toolCallBlockDecoder });
const toolResultBlockTypeDecoder = d.object({ type: d.literal("tool_result"), tool_result: toolResultBlockDecoder });

/** 读取接口只投影三种块；空文本的 text 字段被省略，补成空字符串。 */
const blockDecoder: d.Decoder<BlockDTO> = (input, path) => {
  const type = typeof input === "object" && input !== null && "type" in input ? input.type : undefined;
  switch (type) {
    case "text":
      return { type: "text", text: textBlockDecoder(input, path).text ?? "" };
    case "tool_call":
      return toolCallBlockTypeDecoder(input, path);
    case "tool_result":
      return toolResultBlockTypeDecoder(input, path);
    default:
      throw new d.DecodeError(`${path}.type`, "不是已知的消息块");
  }
};

// awaiting_confirmation（C6）：方案里等用户确认、还没执行的调用。
const toolCallStatuses = ["running", "succeeded", "failed", "awaiting_confirmation"] as const;

const toolCallRecordDecoder = d.object({
  id: d.uuid(),
  name: d.string(),
  input: json,
  output: json,
  truncated: d.boolean(),
  untrusted: d.boolean(),
  status: d.oneOf(toolCallStatuses),
  error_code: d.nullable(d.string()),
  duration_ms: d.nullable(d.integer({ min: 0 })),
});

// 用户消息上存的页面上下文（C5）：只为保持形状，对话里不显示；字段都按可缺失处理，没有时后端给 null 或省略。
const optionalText = d.optional(d.string());
const messageContextDecoder = d.object({
  page: d.optional(d.nullable(d.object({ path: d.string(), kind: d.string(), id: optionalText, title: optionalText }))),
  playback: d.optional(
    d.nullable(
      d.object({ zone_id: d.string(), zone_name: optionalText, track_id: optionalText, title: optionalText, artist: optionalText, status: optionalText }),
    ),
  ),
});

const messageDecoder = d.object({
  id: d.uuid(),
  sequence: d.integer({ min: 1 }),
  role: d.oneOf(["user", "assistant", "tool"] as const),
  content: d.array(blockDecoder, { max: d.unbounded }),
  input_tokens: d.nullable(d.integer({ min: 0 })),
  output_tokens: d.nullable(d.integer({ min: 0 })),
  created_at: d.timestamp(),
  tool_calls: d.array(toolCallRecordDecoder, { max: d.unbounded }),
  context: d.optional(d.nullable(messageContextDecoder)),
});
export type MessageDTO = d.Infer<typeof messageDecoder>;
export type ToolCallDTO = d.Infer<typeof toolCallRecordDecoder>;

// ---- 修改方案（C6）----
// 与后端形状有关的假设集中在这里：SSE 的 plan 事件是 {type, step, plan}；会话详情的未处理方案在 pending_plan，
// 全部方案（含已处理的）在 plans；方案项带模型给的调用标识 call_id，界面据此把方案放进对应的回合、把执行结果对到每一项。

const planBasisDecoder = d.object({
  source: d.oneOf(["local", "external", "mixed"] as const),
  note: d.string({ max: d.unbounded }),
});

const planChangeDecoder = d.object({ field: d.string(), before: json, after: json });

// ---- 审查（C7）----
// steward 模式下方案交给一次独立的模型调用审查，按项给结论：approved 自动执行，rejected 不执行，needs_human_confirmation 转给用户确认。
// source 是结论的来源：policy 是后端的策略规则（不可撤销的修改、一次超过 20 项、高风险），不是审查模型的判断。
// status 为 unavailable 时审查调用失败或返回不合格，items 为空，整份方案没有执行。

export const reviewDecisions = ["approved", "rejected", "needs_human_confirmation"] as const;
export type ReviewDecision = (typeof reviewDecisions)[number];

const reviewItemDecoder = d.object({
  item_id: d.uuid(),
  decision: d.oneOf(reviewDecisions),
  risk: d.oneOf(["low", "medium", "high"] as const),
  /** 理由来自审查模型或策略，只按文字显示，不走 Markdown。 */
  reasons: d.array(d.string({ max: d.unbounded }), { max: d.unbounded }),
  source: d.oneOf(["model", "policy"] as const),
});

const reviewDecoder = d.object({
  review_id: d.uuid(),
  plan_id: d.uuid(),
  model: d.string(),
  status: d.oneOf(["completed", "unavailable"] as const),
  policy_version: d.integer({ min: 1 }),
  expires_at: d.timestamp(),
  items: d.array(reviewItemDecoder, { max: d.unbounded }),
});
export type ReviewDTO = d.Infer<typeof reviewDecoder>;
export type ReviewItemDTO = d.Infer<typeof reviewItemDecoder>;

const planItemDecoder = d.object({
  id: d.uuid(),
  call_id: d.string({ min: 1 }),
  tool: d.string({ min: 1 }),
  /** 目标的显示名（专辑标题、艺人名），来自曲库，只按文字显示。 */
  target: d.string({ max: d.unbounded }),
  changes: d.array(planChangeDecoder, { max: d.unbounded }),
  revertible: d.boolean(),
  basis: d.optional(d.nullable(planBasisDecoder)),
  /** 这一项的取舍：用户确认（accepted、declined），或被审查拒绝（rejected_by_review，C7）；审查批准并执行的项也是 accepted。 */
  decision: d.optional(d.oneOf(["pending", "accepted", "declined", "rejected_by_review"] as const)),
  /** 执行结果（会话详情的 plans 里，有结果才有）；这里只看有没有，不解释内容。 */
  result: d.optional(json),
  /** 执行留下的操作记录（会话详情的 plans 里）。 */
  operation_id: d.optional(d.nullable(d.uuid())),
});

const planDecoder = d.object({
  plan_id: d.uuid(),
  status: d.optional(d.oneOf(["pending", "applied", "declined", "expired", "superseded"] as const)),
  digest: d.string({ min: 1 }),
  expires_at: d.timestamp(),
  items: d.array(planItemDecoder, { min: 1, max: d.unbounded }),
  /** 最近一次审查（C7，会话详情里）；没有审查过时是 null 或省略。 */
  review: d.optional(d.nullable(reviewDecoder)),
});
export type PlanDTO = d.Infer<typeof planDecoder>;
export type PlanItemDTO = d.Infer<typeof planItemDecoder>;
export type PlanBasisDTO = d.Infer<typeof planBasisDecoder>;

/** 已执行的方案项留下的操作记录：取自工具输出的 operation_id（与对应 HTTP 写接口的响应一致）；没有时是 null。 */
export function operationIdOf(output: unknown): string | null {
  if (typeof output !== "object" || output === null || !("operation_id" in output)) return null;
  const id = output.operation_id;
  return typeof id === "string" && id !== "" ? id : null;
}

// 会话的消息没有后端上限（历史长度控制另行安排）。pending_plan 是还没处理的方案，plans 是全部方案（按创建顺序，含已处理的）；
// 刷新后据此恢复方案卡片与结果。
const conversationDetailDecoder = d.object({
  ...conversationShape,
  messages: d.array(messageDecoder, { max: d.unbounded }),
  pending_plan: d.optional(d.nullable(planDecoder)),
  plans: d.optional(d.array(planDecoder, { max: d.unbounded })),
});
export const decodeConversationDetail = d.defineDecoder(conversationDetailDecoder);
export type ConversationDetailDTO = d.Infer<typeof conversationDetailDecoder>;

const conversationPatchDecoder = d.object({ title: d.string(), model: d.string(), mode: d.optional(d.oneOf(stewardModes)) });
export const decodeConversationPatch = d.defineDecoder(conversationPatchDecoder);
export type ConversationPatchDTO = d.Infer<typeof conversationPatchDecoder>;

export const decodeDeleted = d.defineDecoder(d.object({ deleted: d.literal(true) }));
/** 撤销一条操作的响应（受治理操作合同）：这里只用到新的撤销记录。 */
export const decodeReverted = d.defineDecoder(d.object({ operation_id: d.uuid() }));
export const decodeCancelled = d.defineDecoder(d.object({ cancelled: d.boolean() }));

// ---- 运行流的事件（合同第 5 节）----

// awaiting_confirmation（C6）：这一步有等用户确认的方案，运行停在这里；review_unavailable（C7）：审查不可用，方案没有执行、仍在等待。
export const endReasons = ["completed", "awaiting_confirmation", "review_unavailable", "step_limit", "token_limit", "cancelled", "provider_error", "internal_error"] as const;
export type EndReason = (typeof endReasons)[number];

const runEndDecoder = d.object({
  reason: d.optional(d.oneOf(endReasons)),
  error_code: d.optional(d.string()),
  error_kind: d.optional(d.string()),
  status_code: d.optional(d.integer()),
  message: d.optional(d.string()),
});
export type RunEndDTO = d.Infer<typeof runEndDecoder>;

const step = d.integer({ min: 1 });

const runEventDecoders = {
  text: d.object({ type: d.literal("text"), step, text: d.string({ max: d.unbounded }) }),
  // 确认方案之后执行方案项时发的 tool_started、tool_finished 发生在模型继续之前，没有 step（C6）。
  tool_started: d.object({
    type: d.literal("tool_started"),
    step: d.optional(step),
    tool_started: d.object({ call_id: d.string(), name: d.string(), input: json }),
  }),
  tool_finished: d.object({
    type: d.literal("tool_finished"),
    step: d.optional(step),
    tool_finished: d.object({
      call_id: d.string(),
      status: d.oneOf(["succeeded", "failed"] as const),
      output: json,
      truncated: d.boolean(),
      untrusted: d.boolean(),
      error_code: d.optional(d.string()),
    }),
  }),
  usage: d.object({
    type: d.literal("usage"),
    step,
    usage: d.object({ input_tokens: d.optional(d.integer({ min: 0 })), output_tokens: d.optional(d.integer({ min: 0 })) }),
  }),
  plan: d.object({ type: d.literal("plan"), step: d.optional(step), plan: planDecoder }),
  // 审查（C7）：开始时只有方案与审查模型，结束时是每项的结论；重新审查的流里没有 step。
  review_started: d.object({ type: d.literal("review_started"), step: d.optional(step), plan_id: d.uuid(), model: d.string() }),
  review_finished: d.object({ type: d.literal("review_finished"), step: d.optional(step), review: reviewDecoder }),
  done: d.object({ type: d.literal("done"), step: d.optional(step), end: runEndDecoder }),
};

type RunEventDecoders = typeof runEventDecoders;
export type RunEvent = { [K in keyof RunEventDecoders]: d.Infer<RunEventDecoders[K]> }[keyof RunEventDecoders];

/** 解一条运行流的消息；不认识的事件名返回 null（丢掉），data 不合法时抛出 DecodeError。 */
export function decodeRunEvent(event: string, data: string): RunEvent | null {
  if (!Object.hasOwn(runEventDecoders, event)) return null;
  const decoder = runEventDecoders[event as keyof RunEventDecoders];
  return decoder(JSON.parse(data) as unknown, "$");
}

// ---- 管理员：成员开关与出站放行名单（合同 4.1、第 8 节）----

const instanceSettingsDecoder = d.object({ members_enabled: d.boolean() });
export const decodeInstanceSettings = d.defineDecoder(instanceSettingsDecoder);
export type InstanceSettingsDTO = d.Infer<typeof instanceSettingsDecoder>;

const allowlistEntryDecoder = d.object({
  host: d.string({ min: 1 }),
  port: d.integer({ min: 1, max: 65535 }),
  added_by: d.nullable(d.uuid()),
  created_at: d.timestamp(),
});
export const decodeAllowlistEntry = d.defineDecoder(allowlistEntryDecoder);
export type AllowlistEntryDTO = d.Infer<typeof allowlistEntryDecoder>;
// 放行名单没有后端上限。
export const decodeAllowlist = d.defineDecoder(d.object({ items: d.array(allowlistEntryDecoder, { max: d.unbounded }) }));

// ---- 访问令牌（C3）----

export const tokenScopes = ["read", "read_write"] as const;
export type TokenScope = (typeof tokenScopes)[number];

/** 每个用户最多这么多个未吊销的令牌（后端的上限）。 */
export const MAX_TOKENS = 20;

const tokenShape = {
  id: d.uuid(),
  name: d.string({ min: 1 }),
  scope: d.oneOf(tokenScopes),
  /** 明文的末四位，用来在列表里认出是哪一个。 */
  tail: d.string({ min: 1 }),
  created_at: d.timestamp(),
  expires_at: d.nullable(d.timestamp()),
  last_used_at: d.nullable(d.timestamp()),
};

const tokenDecoder = d.object(tokenShape);

// available 为 false：管理员关闭了成员使用管家，成员的令牌暂时不能用，也不能新建。
const tokenListDecoder = d.object({ available: d.boolean(), tokens: d.array(tokenDecoder, { max: MAX_TOKENS }) });

export const decodeTokenList = d.defineDecoder(tokenListDecoder);
export type TokenDTO = d.Infer<typeof tokenDecoder>;
export type TokenListDTO = d.Infer<typeof tokenListDecoder>;

// 签发的响应：明文只在这里出现一次，之后任何接口都不再返回。
const createdTokenDecoder = d.object({ ...tokenShape, token: d.string({ min: 1 }) });

export const decodeCreatedToken = d.defineDecoder(createdTokenDecoder);
export type CreatedTokenDTO = d.Infer<typeof createdTokenDecoder>;
