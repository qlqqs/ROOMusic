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

const conversationShape = {
  id: d.uuid(),
  title: d.string(),
  mode: d.literal("assistant"),
  /** 会话自己的模型；空字符串表示发消息时用设置里当时的模型。 */
  model: d.string(),
  status: d.oneOf(["idle", "running"] as const),
  created_at: d.timestamp(),
  updated_at: d.timestamp(),
};

const conversationDecoder = d.object(conversationShape);
export const decodeConversation = d.defineDecoder(conversationDecoder);
export type ConversationDTO = d.Infer<typeof conversationDecoder>;

/** 会话列表一页最多这么多个（后端的上限）。 */
export const MAX_CONVERSATION_PAGE_SIZE = 100;

const conversationPageDecoder = d.object({
  items: d.array(conversationDecoder, { max: MAX_CONVERSATION_PAGE_SIZE }),
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

const toolCallStatuses = ["running", "succeeded", "failed"] as const;

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

// 会话的消息没有后端上限（历史长度控制另行安排）。
const conversationDetailDecoder = d.object({ ...conversationShape, messages: d.array(messageDecoder, { max: d.unbounded }) });
export const decodeConversationDetail = d.defineDecoder(conversationDetailDecoder);
export type ConversationDetailDTO = d.Infer<typeof conversationDetailDecoder>;

const conversationPatchDecoder = d.object({ title: d.string(), model: d.string() });
export const decodeConversationPatch = d.defineDecoder(conversationPatchDecoder);
export type ConversationPatchDTO = d.Infer<typeof conversationPatchDecoder>;

export const decodeDeleted = d.defineDecoder(d.object({ deleted: d.literal(true) }));
export const decodeCancelled = d.defineDecoder(d.object({ cancelled: d.boolean() }));

// ---- 运行流的事件（合同第 5 节）----

export const endReasons = ["completed", "step_limit", "token_limit", "cancelled", "provider_error", "internal_error"] as const;
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
  tool_started: d.object({
    type: d.literal("tool_started"),
    step,
    tool_started: d.object({ call_id: d.string(), name: d.string(), input: json }),
  }),
  tool_finished: d.object({
    type: d.literal("tool_finished"),
    step,
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
