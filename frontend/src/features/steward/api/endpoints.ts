import type { ApiResponse } from "../../../shared/api";
import { apiRequest, apiStream } from "../../../shared/api";
import type { StewardRequestContext } from "../context/context";
import {
  decodeAllowlist,
  decodeAllowlistEntry,
  decodeCancelled,
  decodeConversation,
  decodeConversationDetail,
  decodeConversationPage,
  decodeConversationPatch,
  decodeCreatedToken,
  decodeDeleted,
  decodeInstanceSettings,
  decodeSettings,
  decodeTokenList,
  type AllowlistEntryDTO,
  type ConversationDetailDTO,
  type ConversationDTO,
  type ConversationPageDTO,
  type ConversationPatchDTO,
  type CreatedTokenDTO,
  type InstanceSettingsDTO,
  type Protocol,
  type SettingsDTO,
  type TokenListDTO,
  type TokenScope,
  type ToolSupport,
} from "./decoders";

const base = "/api/v1/me/steward";
const tokensPath = `${base}/tokens`;
const conversationsPath = `${base}/conversations`;

// ---- 设置 ----

export function fetchSettings(signal?: AbortSignal): Promise<ApiResponse<SettingsDTO>> {
  return apiRequest({ method: "GET", path: `${base}/settings`, signal }, decodeSettings);
}

/** 部分更新：省略的键保留原值；key 写 null 清除已存密钥（合同 4.2）。 */
export type SettingsPatch = {
  endpoint?: string;
  key?: string | null;
  protocol?: "" | Protocol;
  model?: string;
  display_name?: string;
  overrides?: { protocol?: Protocol; model?: string; tool_support?: ToolSupport };
};

export function updateSettings(patch: SettingsPatch): Promise<ApiResponse<SettingsDTO>> {
  return apiRequest({ method: "PUT", path: `${base}/settings`, body: patch }, decodeSettings);
}

/** 探测端点，成功即保存（合同 4.3）。失败响应里的探测报告用 decodeProbeFailure 读。 */
export function probeSettings(input: { endpoint?: string; key?: string | null; model?: string }): Promise<ApiResponse<SettingsDTO>> {
  return apiRequest({ method: "POST", path: `${base}/settings/probe`, body: input }, decodeSettings);
}

// ---- 会话 ----

export function fetchConversations(page: number, pageSize: number, signal?: AbortSignal): Promise<ApiResponse<ConversationPageDTO>> {
  return apiRequest({ method: "GET", path: `${conversationsPath}?page=${page}&page_size=${pageSize}`, signal }, decodeConversationPage);
}

/** 新建会话（201）：要求已配置；没有幂等键，不自动重试。 */
export function createConversation(): Promise<ApiResponse<ConversationDTO>> {
  return apiRequest({ method: "POST", path: conversationsPath, body: {} }, decodeConversation);
}

export function fetchConversation(id: string, signal?: AbortSignal): Promise<ApiResponse<ConversationDetailDTO>> {
  return apiRequest({ method: "GET", path: `${conversationsPath}/${id}`, signal }, decodeConversationDetail);
}

/** 改标题或模型；model 为空字符串表示用设置里的模型。 */
export function patchConversation(id: string, patch: { title?: string; model?: string }): Promise<ApiResponse<ConversationPatchDTO>> {
  return apiRequest({ method: "PATCH", path: `${conversationsPath}/${id}`, body: patch }, decodeConversationPatch);
}

/** 删除会话；正在回答时 409 conversation_busy。 */
export function deleteConversation(id: string): Promise<ApiResponse<{ deleted: true }>> {
  return apiRequest({ method: "DELETE", path: `${conversationsPath}/${id}` }, decodeDeleted);
}

/** 取消本进程里这个会话正在进行的运行；没有运行时 cancelled 为 false。 */
export function cancelConversation(id: string): Promise<ApiResponse<{ cancelled: boolean }>> {
  return apiRequest({ method: "POST", path: `${conversationsPath}/${id}/cancel` }, decodeCancelled);
}

/**
 * 开始一次运行，响应是 SSE（合同第 5 节）。message 不为 null 时是发消息（追加用户消息），context 是页面上下文（C5），
 * 为 null 时请求体里没有这个字段；message 为 null 时是从现有历史继续：没有请求体（不追加用户消息，原用户消息的上下文保持发送时的样子；
 * 最后一条不是 user/tool 时 409 nothing_to_continue）。开始流之前的检查失败时按错误信封抛出 ApiError。
 */
export function openRun(id: string, message: { text: string; context: StewardRequestContext | null } | null, signal: AbortSignal): Promise<Response> {
  if (message === null) return apiStream({ path: `${conversationsPath}/${id}/continue`, method: "POST", signal });
  const { text, context } = message;
  return apiStream({ path: `${conversationsPath}/${id}/messages`, method: "POST", body: context === null ? { text } : { text, context }, signal });
}

// ---- 管理员 ----

export function fetchInstanceSettings(signal?: AbortSignal): Promise<ApiResponse<InstanceSettingsDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/admin/steward/settings", signal }, decodeInstanceSettings);
}

export function updateInstanceSettings(input: InstanceSettingsDTO): Promise<ApiResponse<InstanceSettingsDTO>> {
  return apiRequest({ method: "PUT", path: "/api/v1/admin/steward/settings", body: input }, decodeInstanceSettings);
}

const allowlistPath = "/api/v1/admin/steward/egress-allowlist";

export function fetchAllowlist(signal?: AbortSignal): Promise<ApiResponse<{ items: AllowlistEntryDTO[] }>> {
  return apiRequest({ method: "GET", path: allowlistPath, signal }, decodeAllowlist);
}

export function addAllowlistEntry(entry: { host: string; port: number }): Promise<ApiResponse<AllowlistEntryDTO>> {
  return apiRequest({ method: "POST", path: allowlistPath, body: entry }, decodeAllowlistEntry);
}

/** 删除放行项：请求体指定主机与端口；不存在时 404 not_found。 */
export function removeAllowlistEntry(entry: { host: string; port: number }): Promise<ApiResponse<{ deleted: true }>> {
  return apiRequest({ method: "DELETE", path: allowlistPath, body: entry }, decodeDeleted);
}

// ---- 访问令牌（C3）----

export function fetchTokens(signal?: AbortSignal): Promise<ApiResponse<TokenListDTO>> {
  return apiRequest({ method: "GET", path: tokensPath, signal }, decodeTokenList);
}

export type NewToken = { name: string; scope: TokenScope; expires_at: string | null };

/** 签发令牌（201）。没有幂等键：同一个请求发两次就签发两个，所以不自动重试。 */
export function createToken(input: NewToken): Promise<ApiResponse<CreatedTokenDTO>> {
  return apiRequest({ method: "POST", path: tokensPath, body: input }, decodeCreatedToken);
}

/** 吊销令牌（204）；令牌不存在或不是自己的返回 404 not_found。 */
export function revokeToken(id: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: `${tokensPath}/${id}` }, () => null);
}
