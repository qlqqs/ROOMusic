import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeCreatedToken, decodeTokenList, type CreatedTokenDTO, type TokenListDTO, type TokenScope } from "./decoders";

const tokensPath = "/api/v1/me/steward/tokens";

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
